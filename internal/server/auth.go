package server

import (
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (a *App) currentUser(r *http.Request) (User, error) {
	u := User{}
	c, e := r.Cookie("magisend_session")
	if e != nil {
		return u, e
	}
	e = a.db.QueryRow(`SELECT u.id,u.username,u.role,u.quota,u.email,u.totp_enabled,u.display_name,CASE WHEN u.avatar IS NULL THEN 0 ELSE u.avatar_version END,u.remind_expiry FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.hash=? AND s.expires>? AND u.disabled=0`, hash(c.Value), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.Role, &u.Quota, &u.Email, &u.TwoFactor, &u.DisplayName, &u.Avatar, &u.RemindExpiry)
	if e != nil {
		return u, e
	}
	e = a.db.QueryRow(`SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files f ON f.id=c.file_id JOIN shares s ON f.share_id=s.id WHERE s.owner=?`, u.ID).Scan(&u.Used)
	return u, e
}
func (a *App) require(w http.ResponseWriter, r *http.Request, admin bool) (User, bool) {
	u, e := a.currentUser(r)
	if e != nil {
		fail(w, 401, "Logga in för att fortsätta")
		return u, false
	}
	if admin && u.Role != "admin" {
		fail(w, 403, "Endast administratörer")
		return u, false
	}
	if !a.legacyCapabilities(w, r, u.ID) {
		return u, false
	}
	return u, true
}

// limited counts every call against key: it bounds how often an action
// (registration, link creation) can be performed. Failed credential checks use
// blocked/failed instead, so successful sign-ins never consume the budget.
func (a *App) limited(key string) bool {
	a.failed(key)
	return a.blocked(key, 21)
}
func (a *App) authRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if !decode(w, r, &b) {
			return
		}
		clientKey, accountKey := "login:"+a.clientIP(r), "login-account:"+b.Username
		// A name that cannot exist is only counted per client.
		trackAccount := validUsername(b.Username)
		a.mu.Lock()
		blocked := a.blocked(clientKey, loginFailuresPerClient) ||
			trackAccount && a.blocked(accountKey, loginFailuresPerAccount) && !a.knownDevice(r, b.Username)
		a.mu.Unlock()
		if blocked {
			fail(w, 429, "För många försök. Vänta 15 minuter")
			return
		}
		var id, p string
		var disabled, twoFactor bool
		e := a.db.QueryRow("SELECT id,password,disabled,totp_enabled FROM users WHERE username=?", b.Username).Scan(&id, &p, &disabled, &twoFactor)
		if e != nil {
			// Unknown accounts cost the same bcrypt work as known ones.
			p = "$2a$12$5lS6hrKwD9DVKe9svvMUOO2Hx8mhHFmC3iUFtoAEamPYDUtdHx6U2"
		}
		if bcrypt.CompareHashAndPassword([]byte(p), []byte(b.Password)) != nil || e != nil {
			a.mu.Lock()
			a.failed(clientKey)
			if trackAccount {
				a.failed(accountKey)
			}
			a.mu.Unlock()
			a.audit(r, "", "login.failed", b.Username)
			fail(w, 401, "Fel användarnamn eller lösenord")
			return
		}
		if disabled {
			fail(w, 403, "account_disabled")
			return
		}
		// The password was right; two-step login asks for the code next.
		if twoFactor {
			if strings.TrimSpace(b.Code) == "" {
				fail(w, 401, "two_factor_required")
				return
			}
			a.mu.Lock()
			valid := a.checkSecondFactor(id, b.Code)
			if !valid {
				a.failed(clientKey)
				if trackAccount {
					a.failed(accountKey)
				}
			}
			a.mu.Unlock()
			if !valid {
				a.audit(r, b.Username, "login.code_failed", "")
				fail(w, 401, "invalid_code")
				return
			}
		}
		// The session is only made if the password checked above is still the
		// account's: a password changed meanwhile ended every session, and this
		// one must not outlive that.
		token := randomID()
		res, e := a.db.Exec("INSERT INTO sessions(hash,user_id,expires) SELECT ?,id,? FROM users WHERE id=? AND password=? AND disabled=0", hash(token), time.Now().Add(7*24*time.Hour).Unix(), id, p)
		if internal(w, e) {
			return
		}
		if n, e := res.RowsAffected(); e != nil || n != 1 {
			fail(w, 401, "Fel användarnamn eller lösenord")
			return
		}
		http.SetCookie(w, a.cookie(r, "magisend_session", token, 7*86400, "/"))
		a.rememberDevice(w, r, id)
		a.audit(r, b.Username, "login", "")
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie("magisend_session"); e == nil {
			if internal(w, exec(a, "DELETE FROM sessions WHERE hash=?", hash(c.Value))) {
				return
			}
		}
		http.SetCookie(w, a.cookie(r, "magisend_session", "", -1, "/"))
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		p, e := a.policy()
		if internal(w, e) {
			return
		}
		respond(w, 200, map[string]any{"user": u, "policy": p, "email_enabled": a.emailEnabled()})
	})
	m.HandleFunc("POST /api/register", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Email    string `json:"email"`
		}
		if !decode(w, r, &b) {
			return
		}
		email, valid := optionalEmail(b.Email)
		if !valid {
			fail(w, 400, "invalid_email")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		p, e := a.policy()
		if internal(w, e) {
			return
		}
		if !p.RegistrationOpen || a.countUsers() < 1 {
			fail(w, 403, "Registreringen är stängd")
			return
		}
		if a.limited("register:" + a.clientIP(r)) {
			fail(w, 429, "För många försök")
			return
		}
		u, e := a.addUser(b.Username, b.Password, "user", p.DefaultQuota)
		if e != nil {
			fail(w, 400, "Kunde inte skapa kontot. Kontrollera namn och lösenord")
			return
		}
		if email != "" {
			if internal(w, exec(a, "UPDATE users SET email=? WHERE id=?", email, u.ID)) {
				return
			}
			u.Email = email
		}
		a.audit(r, u.Username, "account.registered", "")
		respond(w, 201, u)
	})
}
func exec(a *App, q string, args ...any) error { _, e := a.db.Exec(q, args...); return e }

package main

import (
	"database/sql"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// Invitations and password reset by email. Links in these emails are built
// only from an address the administrator chose (MAGISEND_PUBLIC_ORIGIN or
// the email settings), never from a visitor's request, so nobody can make
// MagiSend send a reset link that points to their own server.
const (
	inviteLifetime       = 7 * 24 * time.Hour
	resetLifetime        = time.Hour
	resetsPerAccountHour = 3
)

func migrateInvites(db *sql.DB) error {
	if e := addColumn(db, "settings", "site_url", "TEXT NOT NULL DEFAULT ''"); e != nil {
		return e
	}
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS invites(id TEXT PRIMARY KEY, hash TEXT NOT NULL UNIQUE, email TEXT NOT NULL, quota INTEGER NOT NULL, created INTEGER NOT NULL, expires INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0, created_by TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS password_resets(hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, created INTEGER NOT NULL, expires INTEGER NOT NULL);`)
	return e
}

// validSiteURL accepts an http(s) address and returns its origin. An
// address typed without a scheme gets http:// for an IP address or localhost
// and https:// for a name.
func validSiteURL(raw string) (string, bool) {
	// Spaces and invisible characters that come along when an address is
	// copied from a chat or a document are dropped.
	raw = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		return "", true
	}
	if !strings.Contains(raw, "://") {
		host := raw
		if h, _, e := net.SplitHostPort(raw); e == nil {
			host = h
		}
		if net.ParseIP(host) != nil || host == "localhost" {
			raw = "http://" + raw
		} else {
			raw = "https://" + raw
		}
	}
	u, e := url.Parse(raw)
	// Only the origin is kept, so an address copied with a page path works too.
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || !validHost(u.Host) {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// linkOrigin is the trusted address for links in system emails.
func (a *App) linkOrigin() string {
	if a.publicOrigin != "" {
		return a.publicOrigin
	}
	var site string
	_ = a.db.QueryRow("SELECT site_url FROM settings WHERE id=1").Scan(&site)
	return site
}

func (a *App) passwordResetAvailable() bool { return a.emailEnabled() && a.linkOrigin() != "" }

type Invite struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Quota   int64  `json:"quota"`
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
}

func (a *App) inviteRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		rows, e := a.db.Query("SELECT id,email,quota,created,expires FROM invites WHERE used=0 AND expires>? ORDER BY created DESC", time.Now().Unix())
		if internal(w, e) {
			return
		}
		defer rows.Close()
		list := []Invite{}
		for rows.Next() {
			var i Invite
			if internal(w, rows.Scan(&i.ID, &i.Email, &i.Quota, &i.Created, &i.Expires)) {
				return
			}
			list = append(list, i)
		}
		if !internal(w, rows.Err()) {
			respond(w, 200, list)
		}
	})
	m.HandleFunc("POST /api/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Email    string `json:"email"`
			Quota    *int64 `json:"quota"`
			Language string `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		email, valid := validEmail(b.Email)
		if !valid {
			fail(w, 400, "invalid_email")
			return
		}
		if !a.emailEnabled() {
			fail(w, 409, "email_not_configured")
			return
		}
		p, e := a.policy()
		if internal(w, e) {
			return
		}
		quota := p.DefaultQuota
		if b.Quota != nil {
			quota = *b.Quota
		}
		budget, e := a.storageBudget()
		if internal(w, e) {
			return
		}
		if quota < 0 || quota > budget {
			fail(w, 400, "invalid_quota")
			return
		}
		if a.limited("invite:" + admin.ID) {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		origin := a.linkOrigin()
		if origin == "" {
			origin = a.requestOrigin(r)
		}
		token := randomID()
		invite := Invite{ID: randomID(), Email: email, Quota: quota, Created: time.Now().Unix(), Expires: time.Now().Add(inviteLifetime).Unix()}
		if internal(w, exec(a, "INSERT INTO invites(id,hash,email,quota,created,expires,created_by) VALUES(?,?,?,?,?,?,?)", invite.ID, hash(token), email, quota, invite.Created, invite.Expires, admin.ID)) {
			return
		}
		if !a.queueMail(inviteMail(email, admin.Username, origin+"/invite/"+token, b.Language)) {
			_ = exec(a, "DELETE FROM invites WHERE id=?", invite.ID)
			fail(w, 503, "email_queue_full")
			return
		}
		a.audit(r, admin.Username, "invite.sent", email)
		respond(w, 201, invite)
	})
	m.HandleFunc("DELETE /api/admin/invites/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		if !internal(w, exec(a, "DELETE FROM invites WHERE id=?", r.PathValue("id"))) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
	m.HandleFunc("GET /api/invites/{token}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		var email string
		if a.db.QueryRow("SELECT email FROM invites WHERE hash=? AND used=0 AND expires>?", hash(r.PathValue("token")), time.Now().Unix()).Scan(&email) != nil {
			fail(w, 404, "invite_invalid")
			return
		}
		respond(w, 200, map[string]string{"email": email})
	})
	m.HandleFunc("POST /api/invites/{token}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.limited("invite-accept:" + a.clientIP(r)) {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		var id, email string
		var quota int64
		if a.db.QueryRow("SELECT id,email,quota FROM invites WHERE hash=? AND used=0 AND expires>?", hash(r.PathValue("token")), time.Now().Unix()).Scan(&id, &email, &quota) != nil {
			fail(w, 404, "invite_invalid")
			return
		}
		u, e := a.addUser(b.Username, b.Password, "user", quota)
		if e != nil {
			fail(w, 400, "Kunde inte skapa kontot. Kontrollera namn och lösenord")
			return
		}
		if internal(w, exec(a, "UPDATE users SET email=? WHERE id=?", email, u.ID)) {
			return
		}
		if internal(w, exec(a, "UPDATE invites SET used=1 WHERE id=?", id)) {
			return
		}
		u.Email = email
		a.audit(r, u.Username, "invite.accepted", email)
		respond(w, 201, u)
	})

	m.HandleFunc("POST /api/password/forgot", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Login    string `json:"login"`
			Language string `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.passwordResetAvailable() {
			fail(w, 409, "password_reset_unavailable")
			return
		}
		if a.limited("forgot:" + a.clientIP(r)) {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		// The answer is the same whether or not an account matched.
		done := func() { respond(w, 200, map[string]bool{"ok": true}) }
		login := strings.TrimSpace(b.Login)
		var id, name, email string
		if login == "" || a.db.QueryRow("SELECT id,username,email FROM users WHERE (username=? OR lower(email)=lower(?)) AND email<>'' AND disabled=0 LIMIT 1", login, login).Scan(&id, &name, &email) != nil {
			done()
			return
		}
		var recent int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM password_resets WHERE user_id=? AND created>?", id, time.Now().Add(-time.Hour).Unix()).Scan(&recent)) {
			return
		}
		if recent >= resetsPerAccountHour {
			done()
			return
		}
		token := randomID()
		now := time.Now()
		if internal(w, exec(a, "INSERT INTO password_resets(hash,user_id,created,expires) VALUES(?,?,?,?)", hash(token), id, now.Unix(), now.Add(resetLifetime).Unix())) {
			return
		}
		a.queueMail(resetMail(email, name, a.linkOrigin()+"/reset/"+token, b.Language))
		a.audit(r, "", "password.reset_requested", name)
		done()
	})
	m.HandleFunc("GET /api/password/reset/{token}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		var name string
		if a.db.QueryRow("SELECT u.username FROM password_resets p JOIN users u ON u.id=p.user_id WHERE p.hash=? AND p.expires>? AND u.disabled=0", hash(r.PathValue("token")), time.Now().Unix()).Scan(&name) != nil {
			fail(w, 404, "reset_invalid")
			return
		}
		respond(w, 200, map[string]string{"username": name})
	})
	m.HandleFunc("POST /api/password/reset/{token}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		if !validPassword(b.Password) {
			fail(w, 400, "invalid_password")
			return
		}
		hashed, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
		if internal(w, e) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		var user string
		if a.db.QueryRow("SELECT p.user_id FROM password_resets p JOIN users u ON u.id=p.user_id WHERE p.hash=? AND p.expires>? AND u.disabled=0", hash(r.PathValue("token")), time.Now().Unix()).Scan(&user) != nil {
			fail(w, 404, "reset_invalid")
			return
		}
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		// The new password ends every session and every other reset link.
		for _, q := range []string{"UPDATE users SET password=? WHERE id=?", "DELETE FROM sessions WHERE user_id=?", "DELETE FROM password_resets WHERE user_id=?"} {
			args := []any{user}
			if strings.HasPrefix(q, "UPDATE") {
				args = []any{string(hashed), user}
			}
			if _, e = tx.Exec(q, args...); internal(w, e) {
				return
			}
		}
		if !internal(w, tx.Commit()) {
			a.audit(r, a.username(user), "password.reset", "")
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
}

func inviteMail(to, admin, link, language string) outgoingMail {
	t := emailText[emailLanguage(language)]
	line := fmt.Sprintf(t["inviteIntro"], admin)
	text := line + "\n\n" + t["inviteAction"] + ": " + link + "\n\n" + t["inviteExpiry"] + "\n"
	body := `<p style="font-size:16px;margin:0">` + html.EscapeString(line) + `</p>` + emailButton(link, t["inviteAction"]) +
		`<p style="color:#5d5953;margin:0">` + html.EscapeString(t["inviteExpiry"]) + `</p>`
	return outgoingMail{to: to, subject: fmt.Sprintf(t["inviteSubject"], admin), text: text, body: emailFrame(body)}
}

func resetMail(to, username, link, language string) outgoingMail {
	t := emailText[emailLanguage(language)]
	line := fmt.Sprintf(t["resetIntro"], username)
	text := line + "\n\n" + t["resetAction"] + ": " + link + "\n\n" + t["resetExpiry"] + "\n"
	body := `<p style="font-size:16px;margin:0">` + html.EscapeString(line) + `</p>` + emailButton(link, t["resetAction"]) +
		`<p style="color:#5d5953;margin:0">` + html.EscapeString(t["resetExpiry"]) + `</p>`
	return outgoingMail{to: to, subject: t["resetSubject"], text: text, body: emailFrame(body)}
}

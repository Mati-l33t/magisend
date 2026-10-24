package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Failed attempts allowed per window before a key is blocked.
const (
	loginFailuresPerClient  = 20
	loginFailuresPerAccount = 50
	unlockFailuresPerClient = 20
	unlockFailuresPerShare  = 100
	maxTrackedAttempts      = 50000
	maxStorageBudget        = 1 << 50 // 1 PiB
)

func migrateAccounts(db *sql.DB) error {
	if e := addColumn(db, "users", "disabled", "INTEGER NOT NULL DEFAULT 0"); e != nil {
		return e
	}
	if e := addColumn(db, "settings", "storage_budget", fmt.Sprintf("INTEGER NOT NULL DEFAULT %d", defaultStorageBudget)); e != nil {
		return e
	}
	if e := addColumn(db, "settings", "files_per_user", fmt.Sprintf("INTEGER NOT NULL DEFAULT %d", defaultFilesPerUser)); e != nil {
		return e
	}
	return addColumn(db, "settings", "files_total", fmt.Sprintf("INTEGER NOT NULL DEFAULT %d", defaultFilesTotal))
}

// addColumn is an idempotent, additive ALTER TABLE.
func addColumn(db *sql.DB, table, column, definition string) error {
	rows, e := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if e != nil {
		return e
	}
	exists := false
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			rows.Close()
			return e
		}
		exists = exists || name == column
	}
	e = rows.Err()
	rows.Close()
	if e != nil || exists {
		return e
	}
	_, e = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return e
}

// SetTrustedProxies accepts a comma-separated list of IP addresses or CIDR
// ranges whose X-Forwarded-For header is believed. Call before serving.
func (a *App) SetTrustedProxies(list string) error {
	a.trustedProxies = nil
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return fmt.Errorf("invalid trusted proxy %q", item)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			item = fmt.Sprintf("%s/%d", ip, bits)
		}
		_, network, e := net.ParseCIDR(item)
		if e != nil {
			return fmt.Errorf("invalid trusted proxy %q", item)
		}
		a.trustedProxies = append(a.trustedProxies, network)
	}
	return nil
}

func (a *App) trustedProxy(ip net.IP) bool {
	for _, network := range a.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP identifies the visitor for rate limiting. X-Forwarded-For is only
// read when the connection comes from a configured proxy; the right-most
// address that is not itself a trusted proxy is the client. IPv6 clients are
// grouped by /64 because one host usually controls a whole prefix.
func (a *App) clientIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if a.trustedProxy(ip) {
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop := net.ParseIP(strings.TrimSpace(hops[i]))
			if hop == nil {
				break
			}
			ip = hop
			if !a.trustedProxy(hop) {
				break
			}
		}
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func (a *App) secureOrigin() bool { return strings.HasPrefix(a.publicOrigin, "https://") }

// secureRequest reports whether the visitor reached MagiSend over HTTPS:
// directly, or through a configured reverse proxy that says so.
func (a *App) secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && a.trustedProxy(ip) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *App) cookie(r *http.Request, name, value string, age int, path string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: path, MaxAge: age, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secureRequest(r) || a.secureOrigin()}
}

// blocked reports whether key reached max failures in its current window.
// Caller holds mu.
func (a *App) blocked(key string, max int) bool {
	v, ok := a.attempts[key]
	return ok && time.Now().Before(v.until) && v.count >= max
}

// failed records one failed attempt for key. Caller holds mu.
func (a *App) failed(key string) {
	now := time.Now()
	if _, ok := a.attempts[key]; !ok && len(a.attempts) >= maxTrackedAttempts {
		a.pruneAttempts(now)
	}
	v := a.attempts[key]
	if now.After(v.until) {
		v = attempt{until: now.Add(15 * time.Minute)}
	}
	v.count++
	a.attempts[key] = v
}

// pruneAttempts drops expired entries and, if the table is still full,
// arbitrary ones: a flood of new clients must not lock everybody out.
func (a *App) pruneAttempts(now time.Time) {
	for k, v := range a.attempts {
		if now.After(v.until) {
			delete(a.attempts, k)
		}
	}
	for k := range a.attempts {
		if len(a.attempts) < maxTrackedAttempts {
			break
		}
		delete(a.attempts, k)
	}
}

func validPassword(password string) bool { return len(password) >= 12 && len(password) <= 72 }

// suggestedBudget is three quarters of the data disk, in whole GiB, leaving room
// for the system and uploads in progress. Administrators can change it.
func suggestedBudget(root string) int64 {
	var stat syscall.Statfs_t
	if syscall.Statfs(root, &stat) != nil {
		return defaultStorageBudget
	}
	budget := int64(stat.Blocks) * stat.Bsize / 4 * 3 / GiB * GiB
	return min(max(budget, GiB), maxStorageBudget)
}

func (a *App) storageBudget() (int64, error) {
	var budget int64
	e := a.db.QueryRow("SELECT storage_budget FROM settings WHERE id=1").Scan(&budget)
	return budget, e
}

func (a *App) accountRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/account/password", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Current  string `json:"current"`
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		key := "password:" + u.ID
		a.mu.Lock()
		blocked := a.blocked(key, loginFailuresPerClient)
		a.mu.Unlock()
		if blocked {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		if !validPassword(b.Password) {
			fail(w, 400, "invalid_password")
			return
		}
		var stored string
		if internal(w, a.db.QueryRow("SELECT password FROM users WHERE id=?", u.ID).Scan(&stored)) {
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(stored), []byte(b.Current)) != nil {
			a.mu.Lock()
			a.failed(key)
			a.mu.Unlock()
			fail(w, 403, "wrong_password")
			return
		}
		next, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
		if internal(w, e) {
			return
		}
		session, _ := r.Cookie("magisend_session")
		a.mu.Lock()
		defer a.mu.Unlock()
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		// Compare-and-swap: a concurrent change or reset wins and this one fails.
		res, e := tx.Exec("UPDATE users SET password=? WHERE id=? AND password=? AND disabled=0", string(next), u.ID, stored)
		if internal(w, e) {
			return
		}
		if n, e := res.RowsAffected(); internal(w, e) {
			return
		} else if n != 1 {
			fail(w, 409, "wrong_password")
			return
		}
		// Every other session of this account ends; this browser stays signed in.
		// Upload tokens end too: a new password locks out whoever had the old.
		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=? AND hash<>?", u.ID, hash(session.Value)); internal(w, e) {
			return
		}
		if _, e = tx.Exec("DELETE FROM api_tokens WHERE user_id=?", u.ID); internal(w, e) {
			return
		}
		if internal(w, tx.Commit()) {
			return
		}
		a.audit(r, u.Username, "password.changed", "")
		respond(w, 200, map[string]bool{"ok": true})
	})
}

// updateAccount applies an administrator's change to another account. Caller
// holds mu and has verified the administrator.
func (a *App) updateAccount(w http.ResponseWriter, r *http.Request, admin User, id string, quota *int64, password *string, disabled *bool) {
	var exists int
	if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", id).Scan(&exists)) {
		return
	}
	if exists != 1 {
		fail(w, 404, "account_not_found")
		return
	}
	if disabled != nil && id == admin.ID {
		fail(w, 409, "cannot_change_own_account")
		return
	}
	budget, e := a.storageBudget()
	if internal(w, e) {
		return
	}
	if quota != nil && (*quota < 0 || *quota > budget) {
		fail(w, 400, "invalid_quota")
		return
	}
	var hashed []byte
	if password != nil {
		if !validPassword(*password) {
			fail(w, 400, "invalid_password")
			return
		}
		if hashed, e = bcrypt.GenerateFromPassword([]byte(*password), 12); internal(w, e) {
			return
		}
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	if quota != nil {
		if _, e = tx.Exec("UPDATE users SET quota=? WHERE id=?", *quota, id); internal(w, e) {
			return
		}
	}
	if hashed != nil {
		if _, e = tx.Exec("UPDATE users SET password=? WHERE id=?", string(hashed), id); internal(w, e) {
			return
		}
	}
	if disabled != nil {
		if _, e = tx.Exec("UPDATE users SET disabled=? WHERE id=?", *disabled, id); internal(w, e) {
			return
		}
	}
	// A reset password or a disabled account ends every existing session and
	// upload token.
	if hashed != nil || disabled != nil && *disabled {
		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); internal(w, e) {
			return
		}
		if _, e = tx.Exec("DELETE FROM api_tokens WHERE user_id=?", id); internal(w, e) {
			return
		}
	}
	if internal(w, tx.Commit()) {
		return
	}
	name := a.username(id)
	if quota != nil {
		a.audit(r, admin.Username, "account.quota", name)
	}
	if hashed != nil {
		a.audit(r, admin.Username, "account.password_reset", name)
	}
	if disabled != nil {
		a.audit(r, admin.Username, map[bool]string{true: "account.disabled", false: "account.enabled"}[*disabled], name)
	}
	respond(w, 200, map[string]bool{"ok": true})
}

// deleteAccount removes an account with all of its files, links and folders.
// The account is disabled and signed out first, so a partial failure (for
// example a blob that cannot be removed) leaves it locked, keeps the remaining
// rows and quota intact, and can simply be retried. Caller holds mu.
func (a *App) deleteAccount(id string) error {
	if _, e := a.db.Exec("UPDATE users SET disabled=1 WHERE id=?", id); e != nil {
		return e
	}
	if _, e := a.db.Exec("DELETE FROM sessions WHERE user_id=?", id); e != nil {
		return e
	}
	files, e := a.ids("SELECT l.file_id FROM library_files l JOIN files f ON f.id=l.file_id JOIN shares s ON s.id=f.share_id WHERE s.owner=?", id)
	if e != nil {
		return e
	}
	for _, file := range files {
		if e = a.removeLibraryFile(file); e != nil {
			return e
		}
	}
	shares, e := a.ids("SELECT id FROM shares WHERE owner=?", id)
	if e != nil {
		return e
	}
	for _, share := range shares {
		if e = a.removeShare(share); e != nil {
			return e
		}
	}
	if left, e := a.ids("SELECT id FROM shares WHERE owner=?", id); e != nil {
		return e
	} else if len(left) != 0 {
		return errors.New("account still owns shares")
	}
	if _, e = a.db.Exec("DELETE FROM folders WHERE owner=?", id); e != nil {
		return e
	}
	_, e = a.db.Exec("DELETE FROM users WHERE id=?", id)
	return e
}

func (a *App) ids(query string, args ...any) ([]string, error) {
	rows, e := a.db.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

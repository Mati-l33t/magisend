package server

import (
	"database/sql"
	"net/http"
	"time"
)

// A browser that has signed in to an account before gets a long-lived device
// cookie. The account-wide limit on failed logins, which anyone could trip
// to lock the owner out, does not apply to it; its own per-client limit still
// does. The cookie only skips that throttle: the password (and code) are
// always checked.
const (
	deviceCookie   = "magisend_device"
	deviceLifetime = 180 * 24 * time.Hour
	devicesPerUser = 20
)

func migrateDevices(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS known_devices(hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires INTEGER NOT NULL)`)
	return e
}

// knownDevice reports whether the request comes from a browser that signed
// in to username before.
func (a *App) knownDevice(r *http.Request, username string) bool {
	c, e := r.Cookie(deviceCookie)
	if e != nil || !validToken(c.Value) {
		return false
	}
	var n int
	e = a.db.QueryRow("SELECT COUNT(*) FROM known_devices d JOIN users u ON u.id=d.user_id WHERE d.hash=? AND u.username=? AND d.expires>?", hash(c.Value), username, time.Now().Unix()).Scan(&n)
	return e == nil && n > 0
}

// rememberDevice marks this browser as known for user after a sign-in.
func (a *App) rememberDevice(w http.ResponseWriter, r *http.Request, user string) {
	token := ""
	if c, e := r.Cookie(deviceCookie); e == nil && validToken(c.Value) {
		token = c.Value
	} else {
		token = randomID()
	}
	expires := time.Now().Add(deviceLifetime).Unix()
	if _, e := a.db.Exec("INSERT INTO known_devices(hash,user_id,expires) VALUES(?,?,?) ON CONFLICT(hash) DO UPDATE SET user_id=excluded.user_id,expires=excluded.expires", hash(token), user, expires); e != nil {
		return
	}
	_, _ = a.db.Exec("DELETE FROM known_devices WHERE user_id=? AND hash NOT IN (SELECT hash FROM known_devices WHERE user_id=? ORDER BY expires DESC LIMIT ?)", user, user, devicesPerUser)
	http.SetCookie(w, a.cookie(r, deviceCookie, token, int(deviceLifetime/time.Second), "/api/"))
}

package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Two-step login with an authenticator app: time-based one-time codes
// (RFC 6238: HMAC-SHA1, 30 seconds, 6 digits, one step of clock drift either
// way) plus ten single-use recovery codes. A code is accepted at most once.
const (
	totpPeriod        = 30
	totpDigits        = 6
	recoveryCodeCount = 10
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func migrateTwoFactor(db *sql.DB) error {
	for _, c := range [][2]string{
		{"totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"totp_pending", "TEXT NOT NULL DEFAULT ''"},
		{"totp_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"totp_last", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if e := addColumn(db, "users", c[0], c[1]); e != nil {
			return e
		}
	}
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS recovery_codes(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, hash TEXT NOT NULL, PRIMARY KEY(user_id, hash))`)
	return e
}

func totpCode(secret []byte, counter int64) string {
	mac := hmac.New(sha1.New, secret)
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(counter))
	mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000)
}

// totpMatch returns the time step a code belongs to, if it is valid now and
// newer than the last accepted step (so a code cannot be replayed).
func totpMatch(secret string, code string, now time.Time, last int64) (int64, bool) {
	key, e := totpEncoding.DecodeString(secret)
	code = strings.ReplaceAll(code, " ", "")
	if e != nil || len(code) != totpDigits {
		return 0, false
	}
	step := now.Unix() / totpPeriod
	for _, counter := range []int64{step - 1, step, step + 1} {
		if counter > last && subtle.ConstantTimeCompare([]byte(totpCode(key, counter)), []byte(code)) == 1 {
			return counter, true
		}
	}
	return 0, false
}

// normalizeRecovery accepts codes with or without the dash and in any case.
func normalizeRecovery(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
}

func newRecoveryCodes() ([]string, []string) {
	alphabet := "abcdefghjkmnpqrstuvwxyz23456789"
	codes, hashes := []string{}, []string{}
	for len(codes) < recoveryCodeCount {
		raw := make([]byte, 10)
		if _, e := rand.Read(raw); e != nil {
			panic(e)
		}
		var b strings.Builder
		for i, c := range raw {
			if i == 5 {
				b.WriteByte('-')
			}
			b.WriteByte(alphabet[int(c)%len(alphabet)])
		}
		codes = append(codes, b.String())
		hashes = append(hashes, hash(normalizeRecovery(b.String())))
	}
	return codes, hashes
}

// checkSecondFactor verifies a one-time or recovery code for a user with
// two-step login on. A recovery code is used up. Caller holds mu.
func (a *App) checkSecondFactor(user, code string) bool {
	var secret string
	var last int64
	if a.db.QueryRow("SELECT totp_secret,totp_last FROM users WHERE id=? AND totp_enabled=1", user).Scan(&secret, &last) != nil {
		return false
	}
	if counter, ok := totpMatch(secret, code, time.Now(), last); ok {
		res, e := a.db.Exec("UPDATE users SET totp_last=? WHERE id=? AND totp_last<?", counter, user, counter)
		if e != nil {
			return false
		}
		n, _ := res.RowsAffected()
		return n == 1
	}
	res, e := a.db.Exec("DELETE FROM recovery_codes WHERE user_id=? AND hash=?", user, hash(normalizeRecovery(code)))
	if e != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// confirmPassword re-checks the signed-in user's password before a security
// change, with the same failure limit as a password change.
func (a *App) confirmPassword(w http.ResponseWriter, user, password string) bool {
	key := "password:" + user
	a.mu.Lock()
	blocked := a.blocked(key, loginFailuresPerClient)
	a.mu.Unlock()
	if blocked {
		fail(w, 429, "too_many_attempts_wait")
		return false
	}
	var stored string
	if internal(w, a.db.QueryRow("SELECT password FROM users WHERE id=?", user).Scan(&stored)) {
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) != nil {
		a.mu.Lock()
		a.failed(key)
		a.mu.Unlock()
		fail(w, 403, "wrong_password")
		return false
	}
	return true
}

func (a *App) storeRecoveryCodes(user string) ([]string, error) {
	codes, hashes := newRecoveryCodes()
	tx, e := a.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM recovery_codes WHERE user_id=?", user); e != nil {
		return nil, e
	}
	for _, h := range hashes {
		if _, e = tx.Exec("INSERT INTO recovery_codes(user_id,hash) VALUES(?,?)", user, h); e != nil {
			return nil, e
		}
	}
	return codes, tx.Commit()
}

func (a *App) twoFactorRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/account/2fa", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		var enabled bool
		var left int
		if internal(w, a.db.QueryRow("SELECT totp_enabled,(SELECT COUNT(*) FROM recovery_codes WHERE user_id=users.id) FROM users WHERE id=?", u.ID).Scan(&enabled, &left)) {
			return
		}
		respond(w, 200, map[string]any{"enabled": enabled, "recovery_left": left})
	})
	m.HandleFunc("POST /api/account/2fa/setup", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		u, ok := a.require(w, r, false)
		if !ok || !a.confirmPassword(w, u.ID, b.Password) {
			return
		}
		key := make([]byte, 20)
		if _, e := rand.Read(key); internal(w, e) {
			return
		}
		secret := totpEncoding.EncodeToString(key)
		a.mu.Lock()
		defer a.mu.Unlock()
		// With two-step login on, a new authenticator would replace the current
		// one on the password alone; turning it off first needs a code.
		var enabled bool
		if internal(w, a.db.QueryRow("SELECT totp_enabled FROM users WHERE id=?", u.ID).Scan(&enabled)) {
			return
		}
		if enabled {
			fail(w, 409, "two_factor_on")
			return
		}
		if internal(w, exec(a, "UPDATE users SET totp_pending=? WHERE id=?", secret, u.ID)) {
			return
		}
		issuer := a.brandName()
		label := url.PathEscape(issuer + ":" + u.Username)
		uri := "otpauth://totp/" + label + "?" + url.Values{"secret": {secret}, "issuer": {issuer}, "digits": {"6"}, "period": {"30"}}.Encode()
		respond(w, 200, map[string]string{"secret": secret, "uri": uri})
	})
	m.HandleFunc("POST /api/account/2fa/enable", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Code string `json:"code"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		key := "2fa-setup:" + u.ID
		if a.blocked(key, loginFailuresPerClient) {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		var pending string
		var enabled bool
		if internal(w, a.db.QueryRow("SELECT totp_pending,totp_enabled FROM users WHERE id=?", u.ID).Scan(&pending, &enabled)) {
			return
		}
		if enabled {
			fail(w, 409, "two_factor_on")
			return
		}
		counter, valid := totpMatch(pending, b.Code, time.Now(), 0)
		if pending == "" || !valid {
			a.failed(key)
			fail(w, 400, "invalid_code")
			return
		}
		if internal(w, exec(a, "UPDATE users SET totp_secret=?,totp_pending='',totp_enabled=1,totp_last=? WHERE id=?", pending, counter, u.ID)) {
			return
		}
		codes, e := a.storeRecoveryCodes(u.ID)
		if internal(w, e) {
			return
		}
		a.audit(r, u.Username, "twofactor.on", "")
		respond(w, 200, map[string]any{"recovery_codes": codes})
	})
	m.HandleFunc("POST /api/account/2fa/disable", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if !decode(w, r, &b) {
			return
		}
		u, ok := a.require(w, r, false)
		if !ok || !a.confirmPassword(w, u.ID, b.Password) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.checkSecondFactor(u.ID, b.Code) {
			a.failed("password:" + u.ID)
			fail(w, 400, "invalid_code")
			return
		}
		if internal(w, a.disableTwoFactor(u.ID)) {
			return
		}
		a.audit(r, u.Username, "twofactor.off", "")
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/account/2fa/recovery", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if !decode(w, r, &b) {
			return
		}
		u, ok := a.require(w, r, false)
		if !ok || !a.confirmPassword(w, u.ID, b.Password) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		var enabled bool
		if internal(w, a.db.QueryRow("SELECT totp_enabled FROM users WHERE id=?", u.ID).Scan(&enabled)) {
			return
		}
		if !enabled {
			fail(w, 409, "two_factor_off")
			return
		}
		// New codes replace the old ones, so they need the current factor too.
		if !a.checkSecondFactor(u.ID, b.Code) {
			a.failed("password:" + u.ID)
			fail(w, 400, "invalid_code")
			return
		}
		codes, e := a.storeRecoveryCodes(u.ID)
		if !internal(w, e) {
			respond(w, 200, map[string]any{"recovery_codes": codes})
		}
	})
}

func (a *App) disableTwoFactor(user string) error {
	if e := exec(a, "UPDATE users SET totp_secret='',totp_pending='',totp_enabled=0,totp_last=0 WHERE id=?", user); e != nil {
		return e
	}
	return exec(a, "DELETE FROM recovery_codes WHERE user_id=?", user)
}

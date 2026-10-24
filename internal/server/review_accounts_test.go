package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The rate limiter never keeps a request's huge "username" around.
func TestLimiterKeysStayShort(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	huge := strings.Repeat("x", 60000)
	for range 3 {
		call(a, "POST", "/api/login", `{"username":"`+huge+`","password":"wrong password here"}`, "")
	}
	for key := range a.attempts {
		if len(key) > 200 {
			t.Fatal("a long key was kept", len(key))
		}
	}
}

// An administrator's own password and two-step login change only in their
// account settings, which ask for the current password and code.
func TestAdminCannotResetThemselves(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	id := decodeObject(t, call(a, "GET", "/api/me", "", admin))["user"].(map[string]any)["id"].(string)
	for _, body := range []string{`{"reset_two_factor":true}`, `{"password":"a takeover passphrase"}`} {
		if w := call(a, "PATCH", "/api/admin/users/"+id, body, admin); w.Code != 403 || !strings.Contains(w.Body.String(), "use_account_settings") {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	if w := call(a, "POST", "/api/login", fmt.Sprintf(`{"username":"mati","password":%q}`, testPassword), ""); w.Code != 200 {
		t.Fatal("the password changed", w.Code)
	}
}

// With two-step login on, the factor cannot be replaced, nor recovery codes
// renewed, with the password alone.
func TestSecondFactorNeedsTheCurrentFactor(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	secret := decodeObject(t, call(a, "POST", "/api/account/2fa/setup", fmt.Sprintf(`{"password":%q}`, testPassword), token))["secret"].(string)
	var enabled struct {
		Codes []string `json:"recovery_codes"`
	}
	json.Unmarshal(call(a, "POST", "/api/account/2fa/enable", fmt.Sprintf(`{"code":%q}`, currentCode(t, secret, 0)), token).Body.Bytes(), &enabled)
	if len(enabled.Codes) == 0 {
		t.Fatal("two-step login not on")
	}
	if w := call(a, "POST", "/api/account/2fa/setup", fmt.Sprintf(`{"password":%q}`, testPassword), token); w.Code != 409 {
		t.Fatal("a new authenticator with the password alone", w.Code)
	}
	if w := call(a, "POST", "/api/account/2fa/recovery", fmt.Sprintf(`{"password":%q}`, testPassword), token); w.Code != 400 {
		t.Fatal("new recovery codes with the password alone", w.Code)
	}
	if w := call(a, "POST", "/api/account/2fa/recovery", fmt.Sprintf(`{"password":%q,"code":%q}`, testPassword, enabled.Codes[0]), token); w.Code != 200 {
		t.Fatal("new recovery codes with a recovery code", w.Code, w.Body)
	}
}

// A new account email takes the password, and the old address is told.
func TestEmailChangeNeedsThePassword(t *testing.T) {
	a, admin := emailAdmin(t)
	configureEmail(t, a, admin)
	call(a, "PUT", "/api/account/email", `{"email":"old@example.com","password":"`+testPassword+`"}`, admin)
	queued(a)
	if w := call(a, "PUT", "/api/account/email", `{"email":"thief@example.com"}`, admin); w.Code != 403 {
		t.Fatal("email changed without the password", w.Code)
	}
	if w := call(a, "PUT", "/api/account/email", `{"email":"new@example.com","password":"`+testPassword+`"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	mails := queued(a)
	if len(mails) != 1 || mails[0].to != "old@example.com" || !strings.Contains(mails[0].text, "new@example.com") {
		t.Fatal("the old address was not told", mails)
	}
	// The reminder setting alone needs nothing more.
	if w := call(a, "PUT", "/api/account/email", `{"email":"new@example.com","remind_expiry":false}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}

// Others failing to sign in to an account do not lock out a browser that has
// signed in to it before; strangers stay limited.
func TestKnownDeviceIsNotLockedOut(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	first := call(a, "POST", "/api/login", fmt.Sprintf(`{"username":"mati","password":%q}`, testPassword), "")
	var device string
	for _, c := range first.Result().Cookies() {
		if c.Name == deviceCookie {
			device = c.Value
		}
	}
	if device == "" {
		t.Fatal("no device cookie")
	}
	for i := 0; i < loginFailuresPerAccount; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"mati","password":"wrong password here"}`))
		r.Header.Set("X-MagiSend", "1")
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1", i%250+1)
		a.Handler().ServeHTTP(httptest.NewRecorder(), r)
	}
	signIn := func(withDevice bool) int {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(fmt.Sprintf(`{"username":"mati","password":%q}`, testPassword)))
		r.Header.Set("X-MagiSend", "1")
		r.RemoteAddr = "203.0.113.9:1"
		if withDevice {
			r.AddCookie(&http.Cookie{Name: deviceCookie, Value: device})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if signIn(false) != 429 {
		t.Fatal("a new browser was not limited")
	}
	if signIn(true) != 200 {
		t.Fatal("the owner's known browser was locked out")
	}
}

// The right password always opens a protected link, however many wrong ones
// others sent.
func TestRightSharePasswordIsNeverLockedOut(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	sh := createShare(t, a, owner, "share-password", 3)
	chunk(a, owner, sh.Files[0].ID, 0, "abc")
	call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner)
	unlock := func(client, password string) int {
		r := httptest.NewRequest("POST", "/api/public/"+sh.ID+"/unlock", strings.NewReader(`{"password":"`+password+`"}`))
		r.Header.Set("X-MagiSend", "1")
		r.RemoteAddr = client + ":1"
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < unlockFailuresPerShare+5; i++ {
		unlock(fmt.Sprintf("198.51.100.%d", i%250+1), "wrong")
	}
	if got := unlock("198.51.100.200", "wrong"); got != 429 && got != 401 {
		t.Fatal(got)
	}
	if got := unlock("203.0.113.7", "share-password"); got != 200 {
		t.Fatal("the right password was refused", got)
	}
}

// Recipients cannot write into the links of a disabled account.
func TestNoWritesIntoADisabledOwnersLinks(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	u, e := a.addUser("collab", testPassword, "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	collab := login(t, a, "collab")
	fid := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Shared"}`, 201)["id"].(string)
	libraryUpload(t, a, owner, fid, "first.txt", "first")
	sid := folderCall(t, a, owner, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/users/"+u.ID, `{"add_shared":true}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+sid, `{"recipients":["collab"],"add":true}`, 200)
	upload := "/api/public/" + sid + "/files?name=added.txt"
	folderCall(t, a, collab, "POST", upload, "new", 201)
	a.db.Exec("UPDATE users SET disabled=1 WHERE username='mati'")
	folderCall(t, a, collab, "POST", "/api/public/"+sid+"/files?name=more.txt", "x", 403)
}

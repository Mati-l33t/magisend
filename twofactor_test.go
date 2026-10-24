package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTOTPMatchesRFC6238(t *testing.T) {
	key := []byte("12345678901234567890")
	for seconds, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpCode(key, seconds/30); got != want {
			t.Fatal(seconds, got, want)
		}
	}
	secret := totpEncoding.EncodeToString(key)
	now := time.Unix(1234567890, 0)
	if _, ok := totpMatch(secret, "005924", now, 0); !ok {
		t.Fatal("current code rejected")
	}
	if _, ok := totpMatch(secret, "005 924", now.Add(30*time.Second), 0); !ok {
		t.Fatal("one step of drift must be accepted")
	}
	if _, ok := totpMatch(secret, "005924", now.Add(90*time.Second), 0); ok {
		t.Fatal("old code accepted")
	}
	if _, ok := totpMatch(secret, "005924", now, now.Unix()/30); ok {
		t.Fatal("code replayed")
	}
}

func currentCode(t *testing.T, secret string, offset int64) string {
	key, e := totpEncoding.DecodeString(secret)
	if e != nil {
		t.Fatal(e)
	}
	return totpCode(key, time.Now().Unix()/30+offset)
}

func TestTwoFactorLogin(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	pw := "correct horse battery staple"
	if w := call(a, "POST", "/api/account/2fa/setup", `{"password":"wrong password here"}`, token); w.Code != 403 {
		t.Fatal("setup without the right password", w.Code)
	}
	setup := decodeObject(t, call(a, "POST", "/api/account/2fa/setup", fmt.Sprintf(`{"password":%q}`, pw), token))
	secret := setup["secret"].(string)
	if !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/MagiSend:mati?") {
		t.Fatal(setup)
	}
	if w := call(a, "POST", "/api/account/2fa/enable", `{"code":"000000"}`, token); w.Code != 400 {
		t.Fatal("wrong code enabled two-step login", w.Code)
	}
	w := call(a, "POST", "/api/account/2fa/enable", fmt.Sprintf(`{"code":%q}`, currentCode(t, secret, 0)), token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var enabled struct {
		Codes []string `json:"recovery_codes"`
	}
	json.Unmarshal(w.Body.Bytes(), &enabled)
	if len(enabled.Codes) != recoveryCodeCount {
		t.Fatal(enabled)
	}
	if me := decodeObject(t, call(a, "GET", "/api/me", "", token))["user"].(map[string]any); me["two_factor"] != true {
		t.Fatal(me)
	}
	loginWith := func(code string) int {
		return call(a, "POST", "/api/login", fmt.Sprintf(`{"username":"mati","password":%q,"code":%q}`, pw, code), "").Code
	}
	if w = call(a, "POST", "/api/login", fmt.Sprintf(`{"username":"mati","password":%q}`, pw), ""); w.Code != 401 || !strings.Contains(w.Body.String(), "two_factor_required") || len(w.Result().Cookies()) != 0 {
		t.Fatal("signed in without the code", w.Code, w.Body)
	}
	if w = call(a, "POST", "/api/login", `{"username":"mati","password":"wrong password here","code":"123456"}`, ""); strings.Contains(w.Body.String(), "two_factor") {
		t.Fatal("a wrong password must not reveal two-step login", w.Body)
	}
	if loginWith("123456") != 401 {
		t.Fatal("wrong code accepted")
	}
	// The code used to enable is spent; the next one works once.
	next := currentCode(t, secret, 1)
	if loginWith(next) != 200 || loginWith(next) != 401 {
		t.Fatal("code not single-use")
	}
	recovery := strings.ToUpper(strings.ReplaceAll(enabled.Codes[0], "-", ""))
	if loginWith(recovery) != 200 || loginWith(recovery) != 401 {
		t.Fatal("recovery code not single-use")
	}
	if left := decodeObject(t, call(a, "GET", "/api/account/2fa", "", token))["recovery_left"]; left != float64(recoveryCodeCount-1) {
		t.Fatal(left)
	}
	if w = call(a, "POST", "/api/account/2fa/disable", fmt.Sprintf(`{"password":%q,"code":"999999"}`, pw), token); w.Code != 400 {
		t.Fatal("disabled with a wrong code", w.Code)
	}
	if w = call(a, "POST", "/api/account/2fa/disable", fmt.Sprintf(`{"password":%q,"code":%q}`, pw, enabled.Codes[1]), token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if loginWith("") != 200 {
		t.Fatal("two-step login still required after turning it off")
	}
}

func TestAdminResetsTwoFactor(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), admin)
	bob := login(t, a, "bob")
	secret := decodeObject(t, call(a, "POST", "/api/account/2fa/setup", fmt.Sprintf(`{"password":%q}`, testPassword), bob))["secret"].(string)
	call(a, "POST", "/api/account/2fa/enable", fmt.Sprintf(`{"code":%q}`, currentCode(t, secret, 0)), bob)
	var id string
	a.db.QueryRow("SELECT id FROM users WHERE username='bob'").Scan(&id)
	if w := call(a, "PATCH", "/api/admin/users/"+id, `{"reset_two_factor":true}`, bob); w.Code != 403 {
		t.Fatal("a user reset two-step login", w.Code)
	}
	var users []User
	json.Unmarshal(call(a, "GET", "/api/admin/users", "", admin).Body.Bytes(), &users)
	for _, u := range users {
		if u.Username == "bob" && !u.TwoFactor {
			t.Fatal("admin list must show two-step login")
		}
	}
	if w := call(a, "PATCH", "/api/admin/users/"+id, `{"reset_two_factor":true}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/login", fmt.Sprintf(`{"username":"bob","password":%q}`, testPassword), ""); w.Code != 200 {
		t.Fatal("still asks for a code after the reset", w.Code)
	}
}

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPassword = "correct horse battery staple"

func callFrom(a *App, remote, forwarded, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = remote
	r.Header.Set("X-MagiSend", "1")
	if forwarded != "" {
		r.Header.Set("X-Forwarded-For", forwarded)
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "magisend_session", Value: token})
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestClientIPTrustsOnlyConfiguredProxies(t *testing.T) {
	a := testApp(t)
	request := func(remote, forwarded string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r
	}
	if got := a.clientIP(request("203.0.113.7:4000", "198.51.100.1")); got != "203.0.113.7" {
		t.Fatal("untrusted X-Forwarded-For was used:", got)
	}
	if e := a.SetTrustedProxies("10.0.0.1, 172.16.0.0/12"); e != nil {
		t.Fatal(e)
	}
	cases := map[string][2]string{
		"single proxy":           {"10.0.0.1:443", "198.51.100.1"},
		"spoofed left entries":   {"10.0.0.1:443", "1.2.3.4, 198.51.100.1"},
		"chain of trusted hops":  {"10.0.0.1:443", "198.51.100.1, 172.16.5.5"},
		"garbage stops the walk": {"10.0.0.1:443", "nonsense, 198.51.100.1"},
	}
	for name, c := range cases {
		if got := a.clientIP(request(c[0], c[1])); got != "198.51.100.1" {
			t.Fatal(name, got)
		}
	}
	if got := a.clientIP(request("10.0.0.1:443", "")); got != "10.0.0.1" {
		t.Fatal("proxy without header:", got)
	}
	if got := a.clientIP(request("[2001:db8:1:2:3:4:5:6]:443", "")); got != "2001:db8:1:2::/64" {
		t.Fatal("IPv6 clients are grouped by /64:", got)
	}
	if e := a.SetTrustedProxies("not-an-ip"); e == nil {
		t.Fatal("invalid proxy accepted")
	}
}

func TestLoginLimitCountsFailuresPerClientOnly(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	good := `{"username":"mati","password":"` + testPassword + `"}`
	bad := `{"username":"mati","password":"wrong password guess"}`
	for i := 0; i < 25; i++ {
		if w := callFrom(a, "192.0.2.1:1", "", "POST", "/api/login", good, ""); w.Code != 200 {
			t.Fatal("successful sign-ins must not be limited", i, w.Code)
		}
	}
	for i := 0; i < loginFailuresPerClient; i++ {
		if w := callFrom(a, "192.0.2.2:1", "", "POST", "/api/login", bad, ""); w.Code != 401 {
			t.Fatal(i, w.Code)
		}
	}
	if w := callFrom(a, "192.0.2.2:1", "", "POST", "/api/login", good, ""); w.Code != 429 {
		t.Fatal("blocked client must wait even with the right password", w.Code)
	}
	if w := callFrom(a, "192.0.2.3:1", "", "POST", "/api/login", good, ""); w.Code != 200 {
		t.Fatal("other clients are unaffected", w.Code)
	}
}

func TestSecureCookiesAndHSTSForHTTPSOrigin(t *testing.T) {
	a, e := NewApp(t.TempDir(), "https://share.example.com")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.db.Close() })
	setupAdmin(t, a)
	w := call(a, "POST", "/api/login", `{"username":"mati","password":"`+testPassword+`"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if c := w.Result().Cookies(); len(c) != 1 || !c[0].Secure {
		t.Fatal("session cookie must be Secure behind an HTTPS origin")
	}
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("missing HSTS")
	}
	if csp := w.Header().Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") {
		t.Fatal("CSP allows inline code:", csp)
	}
}

func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	current, other := login(t, a, "mati"), login(t, a, "mati")
	next := "a brand new passphrase"
	if w := call(a, "POST", "/api/account/password", `{"current":"wrong password guess","password":"`+next+`"}`, current); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/account/password", `{"current":"`+testPassword+`","password":"short"}`, current); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/account/password", `{"current":"`+testPassword+`","password":"`+next+`"}`, current); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/me", "", current); w.Code != 200 {
		t.Fatal("this browser stays signed in", w.Code)
	}
	if w := call(a, "GET", "/api/me", "", other); w.Code != 401 {
		t.Fatal("other sessions must end", w.Code)
	}
	if w := call(a, "POST", "/api/login", `{"username":"mati","password":"`+testPassword+`"}`, ""); w.Code != 401 {
		t.Fatal("old password still works", w.Code)
	}
	if w := call(a, "POST", "/api/login", `{"username":"mati","password":"`+next+`"}`, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestAdminDisableResetAndDeleteAccount(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	body := fmt.Sprintf(`{"username":"bob","password":%q,"quota":%d}`, testPassword, GiB)
	w := call(a, "POST", "/api/admin/users", body, admin)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	bobID := decodeObject(t, w)["id"].(string)
	bob := login(t, a, "bob")

	if w = call(a, "PATCH", "/api/admin/users/"+bobID, `{"disabled":true}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/me", "", bob); w.Code != 401 {
		t.Fatal("disabled account keeps its session", w.Code)
	}
	if w = call(a, "POST", "/api/login", `{"username":"bob","password":"`+testPassword+`"}`, ""); w.Code != 403 || !strings.Contains(w.Body.String(), "account_disabled") {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "PATCH", "/api/admin/users/"+bobID, `{"disabled":false}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	bob = login(t, a, "bob")
	adminID := decodeObject(t, call(a, "GET", "/api/me", "", admin))["user"].(map[string]any)["id"].(string)
	for _, request := range [][2]string{{"PATCH", `{"disabled":true}`}, {"DELETE", ""}} {
		if w = call(a, request[0], "/api/admin/users/"+adminID, request[1], admin); w.Code != 409 {
			t.Fatal("administrator locked themselves out", request[0], w.Code)
		}
	}

	next := "reset by the admin 1"
	if w = call(a, "PATCH", "/api/admin/users/"+bobID, `{"password":"`+next+`"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/me", "", bob); w.Code != 401 {
		t.Fatal("password reset keeps old sessions", w.Code)
	}
	w = call(a, "POST", "/api/login", `{"username":"bob","password":"`+next+`"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	bob = w.Result().Cookies()[0].Value

	libraryUpload(t, a, bob, "", "notes.txt", "hello")
	shared := createShare(t, a, bob, "", 3)
	if w = chunk(a, bob, shared.Files[0].ID, 0, "abc"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "POST", "/api/folders", `{"name":"Projects"}`, bob); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "DELETE", "/api/admin/users/"+bobID, "", admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for _, table := range []string{"users WHERE id=?", "shares WHERE owner=?", "folders WHERE owner=?", "sessions WHERE user_id=?"} {
		var n int
		if e := a.db.QueryRow("SELECT COUNT(*) FROM "+table, bobID).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	blobs, e := os.ReadDir(filepath.Join(a.root, "blobs"))
	if e != nil || len(blobs) != 0 {
		t.Fatal("stored files were not removed", len(blobs), e)
	}
	if w = call(a, "DELETE", "/api/admin/users/"+bobID, "", admin); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestStorageBudgetIsAnAdminSetting(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	if p := decodeObject(t, call(a, "GET", "/api/admin/policy", "", admin)); int64(p["storage_budget"].(float64)) != suggestedBudget(a.root) {
		t.Fatal(p)
	}
	policy := func(budget, quota int64) string {
		return fmt.Sprintf(`{"registration_open":false,"default_quota":%d,"max_file":%d,"max_share":%d,"default_days":7,"storage_budget":%d}`, quota, GiB, 2*GiB, budget)
	}
	if w := call(a, "PUT", "/api/admin/policy", policy(10*GiB, 8*GiB), admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "PUT", "/api/admin/policy", policy(10*GiB, 11*GiB), admin); w.Code != 400 {
		t.Fatal("quota above the budget accepted", w.Code)
	}
	if w := call(a, "PUT", "/api/admin/policy", policy(GiB/2, 0), admin); w.Code != 400 {
		t.Fatal("budget below 1 GiB accepted", w.Code)
	}
	legacy := fmt.Sprintf(`{"registration_open":false,"default_quota":%d,"max_file":%d,"max_share":%d,"default_days":7}`, GiB, GiB, 2*GiB)
	if w := call(a, "PUT", "/api/admin/policy", legacy, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if s := decodeObject(t, call(a, "GET", "/api/admin/storage", "", admin)); int64(s["capacity_bytes"].(float64)) != 10*GiB || s["path"] != nil {
		t.Fatal(s)
	}
	body := fmt.Sprintf(`{"username":"bob","password":%q,"quota":%d}`, testPassword, 9*GiB)
	if w := call(a, "POST", "/api/admin/users", body, admin); w.Code != 201 {
		t.Fatal("quota within a raised budget refused", w.Code, w.Body)
	}
}

func TestTrustedProxyHTTPSWithoutPublicOrigin(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	if e := a.SetTrustedProxies("10.0.0.5"); e != nil {
		t.Fatal(e)
	}
	login := func(remote, proto string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"mati","password":"`+testPassword+`"}`))
		r.RemoteAddr, r.Host = remote, "share.example.com"
		r.Header.Set("X-MagiSend", "1")
		r.Header.Set("Origin", "https://share.example.com")
		r.Header.Set("X-Forwarded-Proto", proto)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	w := login("10.0.0.5:4000", "https")
	if w.Code != 200 || !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS through the trusted proxy was refused or cookie not Secure", w.Code, w.Body)
	}
	if w = login("203.0.113.9:4000", "https"); w.Code != 403 {
		t.Fatal("an untrusted client claimed HTTPS", w.Code)
	}
}

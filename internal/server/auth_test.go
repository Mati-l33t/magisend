package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupAdmin(t *testing.T, a *App) {
	t.Helper()
	w := call(a, "POST", "/api/setup", `{"setup_code":"TEST-SETUP-CODE","username":"mati","password":"correct horse battery staple"}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
}
func login(t *testing.T, a *App, name string) string {
	t.Helper()
	w := call(a, "POST", "/api/login", `{"username":"`+name+`","password":"correct horse battery staple"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "magisend_session" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatal("unsafe cookie")
			}
			return c.Value
		}
	}
	t.Fatal("no session")
	return ""
}
func TestAuthenticationAndClosedRegistration(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	if w := call(a, "GET", "/api/me", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(a, "POST", "/api/login", `{"username":"mati","password":"wrong"}`, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	s := login(t, a, "mati")
	if w := call(a, "GET", "/api/me", "", s); w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"admin"`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/register", `{"username":"bob","password":"correct horse battery staple"}`, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := call(a, "POST", "/api/logout", "{}", s)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call(a, "GET", "/api/me", "", s); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestWriteRequestsRejectCrossOriginAndMissingHeader(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("POST", "http://magisend:8080/api/setup", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "http://magisend:8080/api/setup", strings.NewReader(`{}`))
	r.Header.Set("X-MagiSend", "1")
	r.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

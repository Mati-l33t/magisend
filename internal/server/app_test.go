package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, e := NewApp(t.TempDir(), "")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.db.Close() })
	return a
}
func call(a *App, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-MagiSend", "1")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "magisend_session", Value: token})
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestSetupIsOnceOnlyAndClosesOrdinaryRegistration(t *testing.T) {
	a := testApp(t)
	w := call(a, "GET", "/api/health", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	body := `{"setup_code":"TEST-SETUP-CODE","username":"mati","password":"correct horse battery staple"}`
	if w = call(a, "POST", "/api/setup", body, ""); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "POST", "/api/setup", body, ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "GET", "/api/status", "", "")
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if status["setup_required"] != false || status["registration_open"] != false {
		t.Fatal(status)
	}
}

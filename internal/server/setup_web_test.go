package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestWebSetupWithoutConsoleToken(t *testing.T) {
	a := testApp(t)
	w := call(a, "POST", "/api/setup", `{"username":"firstadmin","password":"correct horse battery staple"}`, "")
	if w.Code != 201 {
		t.Fatalf("web-only setup must create first admin, got %d: %s", w.Code, w.Body)
	}
	var user User
	if e := json.Unmarshal(w.Body.Bytes(), &user); e != nil || user.Role != "admin" {
		t.Fatalf("not an admin: %+v %v", user, e)
	}
	if w = call(a, "POST", "/api/setup", `{"username":"secondadmin","password":"correct horse battery staple"}`, ""); w.Code != 409 {
		t.Fatalf("setup must close: %d", w.Code)
	}
	a.db.Exec("UPDATE settings SET registration_open=1")
	if w = call(a, "POST", "/api/register", `{"username":"ordinary","password":"correct horse battery staple"}`, ""); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "POST", "/api/register", `{"username":"imposter","password":"correct horse battery staple","role":"admin"}`, ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "POST", "/api/setup", `{"username":"thirdadmin","password":"correct horse battery staple"}`, ""); w.Code != 409 {
		t.Fatalf("opening registration reopened setup: %d", w.Code)
	}
}

func TestTokenlessSetupRaceStillCreatesExactlyOneAdmin(t *testing.T) {
	a := testApp(t)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]string{"username": name, "password": "correct horse battery staple"})
			codes <- call(a, "POST", "/api/setup", string(b), "").Code
		}(name)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
}

func TestWebSetupRetainsCSRFSecurity(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct{ header, origin string }{{"", ""}, {"1", "http://attacker.example"}} {
		r := httptest.NewRequest("POST", "http://magisend.lan/api/setup", strings.NewReader(`{"username":"first","password":"correct horse battery staple"}`))
		r.Header.Set("X-MagiSend", tc.header)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if n := a.countUsers(); n != 0 {
		t.Fatalf("CSRF created %d users", n)
	}
}

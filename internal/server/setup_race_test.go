package server

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestOnlyFirstSetupCanCreateAdmin(t *testing.T) {
	a := testApp(t)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]string{"setup_code": "TEST-SETUP-CODE", "username": n, "password": "correct horse battery staple"})
			codes <- call(a, "POST", "/api/setup", string(b), "").Code
		}(name)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	var admins int
	a.db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&admins)
	if admins != 1 {
		t.Fatal(admins)
	}
	a.db.Exec("UPDATE settings SET registration_open=1")
	w := call(a, "POST", "/api/register", `{"username":"imposter","password":"correct horse battery staple","role":"admin"}`, "")
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "POST", "/api/register", `{"username":"ordinary","password":"correct horse battery staple"}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var role string
	a.db.QueryRow("SELECT role FROM users WHERE username='ordinary'").Scan(&role)
	if role != "user" {
		t.Fatal(role)
	}
}

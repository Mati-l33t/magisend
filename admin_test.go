package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestAdminUsersPoliciesAndRoleBoundary(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	w := call(a, "POST", "/api/admin/users", `{"username":"bob","password":"correct horse battery staple","quota":100}`, s)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var u User
	json.Unmarshal(w.Body.Bytes(), &u)
	bob := login(t, a, "bob")
	if w = call(a, "GET", "/api/admin/users", "", bob); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "PATCH", "/api/admin/users/"+u.ID, `{"quota":50}`, s); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/me", "", bob); w.Code != 200 || !strings.Contains(w.Body.String(), `"quota":50`) {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "PUT", "/api/admin/policy", `{"registration_open":true,"default_quota":100,"max_file":100,"max_share":200,"default_days":3}`, s)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "POST", "/api/register", `{"username":"alice","password":"correct horse battery staple"}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "PUT", "/api/admin/policy", `{"registration_open":false,"default_quota":100,"max_file":100,"max_share":200,"default_days":3}`, s)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "POST", "/api/register", `{"username":"other","password":"correct horse battery staple"}`, "")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestConcurrentQuotaReservationAndIDOR(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	var uid string
	a.db.QueryRow("SELECT id FROM users WHERE username='mati'").Scan(&uid)
	a.db.Exec("UPDATE users SET quota=10 WHERE id=?", uid)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call(a, "POST", "/api/shares", `{"title":"quota","days":7,"password":"","files":[{"name":"file.bin","size":6}]}`, s).Code
		}()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[201] != 1 || counts[413] != 1 {
		t.Fatal(counts)
	}
	a.addUser("bob", "correct horse battery staple", "user", 100)
	bob := login(t, a, "bob")
	var sh Share
	w := call(a, "GET", "/api/shares", "", s)
	var shares []Share
	json.Unmarshal(w.Body.Bytes(), &shares)
	sh = shares[0]
	id := sh.Files[0].ID
	if w := chunk(a, bob, id, 0, "secret"); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", bob); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/uploads/"+id, "", bob); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/public/"+sh.ID, "", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if w := chunk(a, s, id, 0, "toolong"); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestFilenameTraversalAndShareLimits(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	for _, name := range []string{"../escape", "a/b", "a\\b", ".."} {
		b := fmt.Sprintf(`{"files":[{"name":%q,"size":1}]}`, name)
		if w := call(a, "POST", "/api/shares", b, s); w.Code != 400 {
			t.Fatal(name, w.Code, w.Body)
		}
	}
	for _, size := range []int64{-1, GiB + 1} {
		b := fmt.Sprintf(`{"files":[{"name":"valid.txt","size":%d}]}`, size)
		if w := call(a, "POST", "/api/shares", b, s); w.Code != 400 {
			t.Fatal(size, w.Code, w.Body)
		}
	}
}

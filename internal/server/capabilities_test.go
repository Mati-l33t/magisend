package server

import (
	"encoding/json"
	"testing"
)

func TestCapabilitiesDefaultsInheritanceAndLegacyRoutes(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	var caps map[string]bool
	json.Unmarshal(call(a, "GET", "/api/capabilities", "", admin).Body.Bytes(), &caps)
	for name := range capabilityDefaults {
		if !caps[name] {
			t.Fatalf("administrators must have %s", name)
		}
	}
	if _, e := a.addUser("bob", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	token := login(t, a, "bob")
	w := call(a, "GET", "/api/capabilities", "", token)
	if w.Code != 200 {
		t.Fatalf("capabilities: want 200 got %d %s", w.Code, w.Body)
	}
	caps = nil
	if e := json.Unmarshal(w.Body.Bytes(), &caps); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"edit_own", "grant_edit", "edit_shared", "add_shared", "delete_shared"} {
		if caps[key] {
			t.Fatalf("%s must default OFF", key)
		}
	}
	w = call(a, "PUT", "/api/admin/capabilities/defaults", `{"upload":false,"share":false,"edit_own":true}`, admin)
	if w.Code != 200 {
		t.Fatalf("defaults: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/shares", "/api/shares/missing/publish"} {
		w = call(a, "POST", path, `{}`, token)
		if w.Code != 403 {
			t.Fatalf("legacy bypass %s: %d %s", path, w.Code, w.Body)
		}
	}
	w = call(a, "PATCH", "/api/uploads/missing", ``, token)
	if w.Code != 403 {
		t.Fatalf("resume bypass: %d", w.Code)
	}
	w = call(a, "GET", "/api/uploads/missing", ``, token)
	if w.Code != 403 {
		t.Fatalf("resume GET bypass: %d", w.Code)
	}
	var id string
	if e := a.db.QueryRow("SELECT id FROM users WHERE username='bob'").Scan(&id); e != nil {
		t.Fatal(e)
	}
	w = call(a, "PUT", "/api/admin/capabilities/users/"+id, `{"upload":true,"edit_own":null}`, admin)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "GET", "/api/capabilities", "", token)
	json.Unmarshal(w.Body.Bytes(), &caps)
	if !caps["upload"] || !caps["edit_own"] || caps["share"] {
		t.Fatal(caps)
	}
	w = call(a, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":false}`, admin)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = call(a, "GET", "/api/capabilities", "", token)
	json.Unmarshal(w.Body.Bytes(), &caps)
	if caps["edit_own"] {
		t.Fatal("null override must inherit dynamically")
	}
	if w = call(a, "GET", "/api/admin/users", "", admin); w.Code != 200 {
		t.Fatal("administration locked out", w.Code)
	}
}

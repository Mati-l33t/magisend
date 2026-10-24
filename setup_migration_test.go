package main

import "testing"

func TestLegacyDatabaseMigratesToClosedSetup(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	u, e := a.addUser("existing", "correct horse battery staple", "admin", GiB)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.db.Exec("DROP TABLE installation"); e != nil {
		t.Fatal(e)
	}
	a.db.Close()
	a, e = NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer a.db.Close()
	required, e := a.setupRequired()
	if e != nil || required {
		t.Fatal(required, e)
	}
	var id, role string
	if e = a.db.QueryRow("SELECT id,role FROM users WHERE username='existing'").Scan(&id, &role); e != nil || id != u.ID || role != "admin" {
		t.Fatal(id, role, e)
	}
	if w := call(a, "POST", "/api/setup", `{"username":"imposter","password":"correct horse battery staple"}`, ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestSetupTransactionFailureDoesNotLeaveAnAccountOrCloseSetup(t *testing.T) {
	a := testApp(t)
	if _, e := a.db.Exec(`CREATE TRIGGER refuse_setup BEFORE UPDATE OF completed ON installation BEGIN SELECT RAISE(ABORT,'test failure'); END`); e != nil {
		t.Fatal(e)
	}
	body := `{"username":"first","password":"correct horse battery staple"}`
	w := call(a, "POST", "/api/setup", body, "")
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
	required, e := a.setupRequired()
	if e != nil || !required || a.countUsers() != 0 {
		t.Fatal(required, e, a.countUsers())
	}
	if _, e = a.db.Exec("DROP TRIGGER refuse_setup"); e != nil {
		t.Fatal(e)
	}
	w = call(a, "POST", "/api/setup", body, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
}

package main

import "testing"

func TestAtomicShareGrants(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	if _, e := a.addUser("sender", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	token := login(t, a, "sender")
	_, e := a.addUser("collab", "correct horse battery staple", "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	file := libraryUpload(t, a, token, "", "notes.txt", "abc").Files[0].ID
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	// Invalid recipient must not leave any public link behind.
	folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+file+`"],"recipients":["missing"],"edit":true}`, 400)
	var n int
	a.db.QueryRow("SELECT COUNT(*) FROM library_shares").Scan(&n)
	if n != 0 {
		t.Fatal("orphan link created")
	}
	// All creation settings and grants are committed by a single request.
	sid := folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+file+`"],"recipients":["collab"],"edit":true}`, 201)["id"].(string)
	p, e := a.linkPermissions(sid)
	if e != nil || !p.Edit || len(p.Recipients) != 1 || p.Recipients[0] != "collab" {
		t.Fatalf("missing atomic grants %+v %v", p, e)
	}
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":false}`, 200)
	folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+file+`"],"recipients":["collab"],"edit":true}`, 403)
	a.db.QueryRow("SELECT COUNT(*) FROM library_shares").Scan(&n)
	if n != 1 {
		t.Fatal("revoked creator left extra link", n)
	}
}

package main

import "testing"

func TestNamedCollaborationRequiresExplicitGrantAndCurrentCapabilities(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	for _, name := range []string{"owner", "collab", "excluded"} {
		if _, e := a.addUser(name, "correct horse battery staple", "user", GiB); e != nil {
			t.Fatal(e)
		}
	}
	owner := login(t, a, "owner")
	collab := login(t, a, "collab")
	excluded := login(t, a, "excluded")
	file := libraryUpload(t, a, owner, "", "shared.txt", "before").Files[0].ID
	share := folderCall(t, a, owner, "POST", "/api/library/shares", `{"files":["`+file+`"]}`, 201)["id"].(string)
	path := "/api/public/" + share + "/files/" + file + "/text"
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+share, `{"recipients":["collab"],"edit":true}`, 403)
	folderCall(t, a, "", "PUT", path, textBody(1, "no"), 401)
	folderCall(t, a, collab, "PUT", path, textBody(1, "no"), 403)
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+share, `{"recipients":["collab"],"edit":true}`, 200)
	folderCall(t, a, collab, "PUT", path, textBody(1, "no capability"), 403)
	var cid string
	if e := a.db.QueryRow("SELECT id FROM users WHERE username='collab'").Scan(&cid); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/users/"+cid, `{"edit_shared":true}`, 200)
	folderCall(t, a, excluded, "PUT", path, textBody(1, "excluded"), 403)
	folderCall(t, a, "", "GET", path, "", 200)
	folderCall(t, a, collab, "PUT", path, textBody(1, "by recipient"), 200)
	folderCall(t, a, collab, "PUT", path, textBody(1, "conflict"), 409)
	folderCall(t, a, collab, "GET", "/api/public/"+share+"/files/"+file+"/versions", "", 200)
	folderCall(t, a, collab, "POST", "/api/public/"+share+"/files/"+file+"/restore", `{"version":2,"restore":1}`, 200)
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":false}`, 200)
	folderCall(t, a, collab, "PUT", path, textBody(3, "revoked sender"), 403)
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/users/"+cid, `{"edit_shared":false}`, 200)
	folderCall(t, a, collab, "PUT", path, textBody(3, "revoked recipient"), 403)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+share, `{"recipients":["nobody"],"edit":true}`, 400)
	old := createShare(t, a, owner, "", 0)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+old.ID, `{"recipients":["collab"],"edit":true}`, 404)
}

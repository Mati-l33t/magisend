package server

import "testing"

func TestPublicRightsReflectNamedRecipientAndFileIDOR(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	u, e := a.addUser("collab", "correct horse battery staple", "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	collab := login(t, a, "collab")
	fid := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Rights"}`, 201)["id"].(string)
	file := libraryUpload(t, a, owner, fid, "one.txt", "one").Files[0].ID
	unshared := libraryUpload(t, a, owner, "", "private.txt", "private").Files[0].ID
	sid := folderCall(t, a, owner, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	rights := folderCall(t, a, "", "GET", "/api/public/"+sid+"/permissions", "", 200)
	if rights["edit"] != false || rights["add"] != false || rights["delete"] != false {
		t.Fatal(rights)
	}
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/users/"+u.ID, `{"edit_shared":true,"delete_shared":true}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+sid, `{"recipients":["collab"],"edit":true,"delete":true}`, 200)
	rights = folderCall(t, a, collab, "GET", "/api/public/"+sid+"/permissions", "", 200)
	if rights["edit"] != true || rights["add"] != false || rights["delete"] != true {
		t.Fatal(rights)
	}
	folderCall(t, a, collab, "DELETE", "/api/public/"+sid+"/files/"+unshared, `{}`, 404)
	folderCall(t, a, collab, "PUT", "/api/public/"+sid+"/files/"+unshared+"/text", textBody(1, "stolen"), 404)
	folderCall(t, a, owner, "PATCH", "/api/library/files/"+file, `{"folder_id":""}`, 200)
	folderCall(t, a, collab, "PUT", "/api/public/"+sid+"/files/"+file+"/text", textBody(1, "moved"), 404)
}

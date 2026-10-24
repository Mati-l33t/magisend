package main

import (
	"testing"
	"time"
)

func TestCollaboratorAddAndDeleteAreIndependent(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	u, e := a.addUser("collab", "correct horse battery staple", "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	collab := login(t, a, "collab")
	fid := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Collaborate"}`, 201)["id"].(string)
	file := libraryUpload(t, a, owner, fid, "first.txt", "first").Files[0].ID
	sid := folderCall(t, a, owner, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+sid, `{"recipients":["collab"],"add":true}`, 200)
	upload := "/api/public/" + sid + "/files?name=added.txt"
	del := "/api/public/" + sid + "/files/" + file
	folderCall(t, a, "", "POST", upload, "new", 401)
	folderCall(t, a, collab, "POST", upload, "new", 403)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/users/"+u.ID, `{"add_shared":true}`, 200)
	added := folderCall(t, a, collab, "POST", upload, "new", 201)["id"].(string)
	folderCall(t, a, collab, "DELETE", del, `{}`, 403)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+sid, `{"delete":true}`, 200)
	folderCall(t, a, collab, "DELETE", del, `{}`, 403)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/users/"+u.ID, `{"delete_shared":true}`, 200)
	folderCall(t, a, collab, "DELETE", del, `{}`, 200)
	folderCall(t, a, "", "GET", "/api/public/"+sid+"/files/"+file, "", 404)
	folderCall(t, a, owner, "GET", "/api/library/files/"+file+"/download", "", 404)
	folderCall(t, a, collab, "POST", "/api/public/"+sid+"/files?name=..%2Fescape.txt", "x", 400)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/defaults", `{"upload":false}`, 200)
	folderCall(t, a, collab, "POST", upload, "no", 403)
	if w := call(a, "GET", "/api/public/"+sid+"/files/"+added, "", ""); w.Code != 200 || w.Body.String() != "new" {
		t.Fatal(w.Code, w.Body)
	}
}
func TestLibraryLinksDefaultExpiryAndPassword(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	file := libraryUpload(t, a, owner, "", "one.txt", "one").Files[0].ID
	sh := folderCall(t, a, owner, "POST", "/api/library/shares", `{"files":["`+file+`"],"password":"secret","days":1}`, 201)
	if sh["expires"].(float64) < float64(time.Now().Add(23*time.Hour).Unix()) {
		t.Fatal(sh)
	}
	id := sh["id"].(string)
	folderCall(t, a, "", "GET", "/api/public/"+id, "", 401)
	w := call(a, "POST", "/api/public/"+id+"/unlock", `{"password":"secret"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	defaultLink := folderCall(t, a, owner, "POST", "/api/library/shares", `{"files":["`+file+`"]}`, 201)
	if defaultLink["expires"].(float64) == 0 {
		t.Fatal("must use policy default, not silently permanent")
	}
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+id, `{"days":3,"password":"changed"}`, 200)
	folderCall(t, a, "", "POST", "/api/public/"+id+"/unlock", `{"password":"secret"}`, 401)
	folderCall(t, a, "", "POST", "/api/public/"+id+"/unlock", `{"password":"changed"}`, 200)
}

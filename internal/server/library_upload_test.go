package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPrivateLibraryUploadAndManagedRetention(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	folder := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Keep"}`, 201)
	id := folder["id"].(string)
	folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"share":false}`, 200)
	w := call(a, "POST", "/api/library/uploads", `{"files":[{"name":"private.txt","size":4}],"folder_id":"`+id+`"}`, token)
	if w.Code != 201 {
		t.Fatalf("private upload with sharing disabled: %d %s", w.Code, w.Body)
	}
	var sh Share
	if e := json.Unmarshal(w.Body.Bytes(), &sh); e != nil || len(sh.Files) != 1 {
		t.Fatal(e, w.Body)
	}
	file := sh.Files[0].ID
	if w = chunk(a, token, file, 0, "keep"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
	folderCall(t, a, "", "GET", "/api/public/"+sh.ID, ``, 404)
	if e := a.Cleanup(time.Now().Add(48 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if w = call(a, "GET", "/api/library/files/"+file+"/download", "", token); w.Code != 200 || w.Body.String() != "keep" {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"share":true}`, 200)
	legacy := createShare(t, a, token, "", 4)
	oldFile := legacy.Files[0].ID
	if w = chunk(a, token, oldFile, 0, "stay"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+legacy.ID+"/publish", `{}`, 200)
	folderCall(t, a, token, "PATCH", "/api/library/files/"+oldFile, `{"folder_id":"`+id+`"}`, 200)
	folderCall(t, a, token, "DELETE", "/api/shares/"+legacy.ID, `{}`, 200)
	if w = call(a, "GET", "/api/library/files/"+oldFile+"/download", "", token); w.Code != 200 || w.Body.String() != "stay" {
		t.Fatal("managed original lost when link revoked", w.Code, w.Body)
	}
}

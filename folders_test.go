package main

import (
	"encoding/json"
	"testing"
)

func folderCall(t *testing.T, a *App, token, method, path, body string, status int) map[string]any {
	t.Helper()
	w := call(a, method, path, body, token)
	if w.Code != status {
		t.Fatalf("%s %s want %d got %d %s", method, path, status, w.Code, w.Body)
	}
	var value map[string]any
	if status != 204 {
		_ = json.Unmarshal(w.Body.Bytes(), &value)
	}
	return value
}
func TestFoldersLibraryOwnershipAndMove(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	other := login(t, a, "other")
	folder := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Project"}`, 201)
	id, ok := folder["id"].(string)
	if !ok {
		t.Fatal(folder)
	}
	folderCall(t, a, other, "PATCH", "/api/folders/"+id, `{"name":"Stolen"}`, 404)
	folderCall(t, a, other, "DELETE", "/api/folders/"+id, `{}`, 404)
	folderCall(t, a, owner, "PATCH", "/api/folders/"+id, `{"name":"Renamed"}`, 200)
	folderCall(t, a, owner, "POST", "/api/folders", `{"name":"../escape"}`, 400)
	sh := createShare(t, a, owner, "", 5)
	file := sh.Files[0].ID
	if w := chunk(a, owner, file, 0, "hello"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, owner, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
	w := call(a, "GET", "/api/library/files", "", owner)
	var files []map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &files) != nil || len(files) != 1 || files[0]["id"] != file {
		t.Fatal("old uploads must be in library", w.Code, w.Body)
	}
	folderCall(t, a, other, "PATCH", "/api/library/files/"+file, `{"name":"stolen.txt","folder_id":"`+id+`"}`, 404)
	folderCall(t, a, owner, "PATCH", "/api/library/files/"+file, `{"name":"notes.md","folder_id":"`+id+`"}`, 200)
	folderCall(t, a, owner, "DELETE", "/api/folders/"+id, `{}`, 409)
	if w = call(a, "GET", "/api/public/"+sh.ID+"/files/"+file, "", ""); w.Code != 200 || w.Body.String() != "hello" {
		t.Fatal("old link broken", w.Code, w.Body)
	}
	folderCall(t, a, owner, "PATCH", "/api/library/files/"+file, `{"name":"../bad.txt"}`, 400)
	folderCall(t, a, owner, "PATCH", "/api/library/files/"+file, `{"folder_id":""}`, 200)
	folderCall(t, a, owner, "DELETE", "/api/folders/"+id, `{}`, 200)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/defaults", `{"folders":false}`, 200)
	folderCall(t, a, other, "POST", "/api/folders", `{"name":"Denied"}`, 403)
}

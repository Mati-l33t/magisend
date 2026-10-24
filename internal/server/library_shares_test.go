package server

import (
	"encoding/json"
	"testing"
)

func libraryUpload(t *testing.T, a *App, token, folder, name, data string) Share {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"files": []map[string]any{{"name": name, "size": len(data)}}, "folder_id": folder})
	w := call(a, "POST", "/api/library/uploads", string(body), token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var sh Share
	if e := json.Unmarshal(w.Body.Bytes(), &sh); e != nil {
		t.Fatal(e)
	}
	if w = chunk(a, token, sh.Files[0].ID, 0, data); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
	return sh
}
func TestLiveFolderAndSelectionShares(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	folder := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Live"}`, 201)
	fid := folder["id"].(string)
	first := libraryUpload(t, a, token, fid, "one.txt", "one").Files[0].ID
	live := folderCall(t, a, token, "POST", "/api/library/shares", `{"title":"Live folder","folder_id":"`+fid+`"}`, 201)
	lid := live["id"].(string)
	selection := folderCall(t, a, token, "POST", "/api/library/shares", `{"title":"Selection","files":["`+first+`"]}`, 201)
	sid := selection["id"].(string)
	second := libraryUpload(t, a, token, fid, "two.txt", "two").Files[0].ID
	read := func(id string, want int) {
		t.Helper()
		w := call(a, "GET", "/api/public/"+id, "", "")
		var sh Share
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sh) != nil || len(sh.Files) != want {
			t.Fatalf("membership %s want%d: %d %s", id, want, w.Code, w.Body)
		}
	}
	read(lid, 2)
	read(sid, 1)
	folderCall(t, a, token, "PATCH", "/api/library/files/"+first, `{"folder_id":""}`, 200)
	read(lid, 1)
	read(sid, 1)
	folderCall(t, a, "", "GET", "/api/public/"+lid+"/files/"+first, "", 404)
	folderCall(t, a, "", "GET", "/api/public/"+sid+"/files/"+second, "", 404)
	folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+fid+`","files":["`+first+`"]}`, 400)
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	other := login(t, a, "other")
	folderCall(t, a, other, "POST", "/api/library/shares", `{"files":["`+first+`"]}`, 404)
	folderCall(t, a, other, "PATCH", "/api/library/shares/"+sid, `{"title":"stolen"}`, 404)
	folderCall(t, a, token, "PATCH", "/api/library/shares/"+sid, `{"title":"Updated"}`, 200)
	folderCall(t, a, token, "DELETE", "/api/shares/"+sid, `{}`, 200)
	folderCall(t, a, "", "GET", "/api/public/"+sid, "", 404)
	if w := call(a, "GET", "/api/library/files/"+first+"/download", "", token); w.Code != 200 || w.Body.String() != "one" {
		t.Fatal("revoke deleted original", w.Code, w.Body)
	}
}

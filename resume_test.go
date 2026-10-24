package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestUnfinishedLibraryUploadsCanBeListedAndResumed(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	fid := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Photos"}`, 201)["id"].(string)
	up := folderCall(t, a, owner, "POST", "/api/library/uploads", `{"folder_id":"`+fid+`","files":[{"name":"a.jpg","size":6},{"name":"b.jpg","size":3}]}`, 201)
	files := up["files"].([]any)
	first := files[0].(map[string]any)["id"].(string)
	w := call(a, "GET", "/api/uploads/"+first, "", owner)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	send := func(id string, offset int64, data string) {
		t.Helper()
		r := chunk(a, owner, id, offset, data)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body)
		}
	}
	send(first, 0, "abc")

	var list []pendingUpload
	w = call(a, "GET", "/api/library/uploads", "", owner)
	if json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || len(list[0].Files) != 2 {
		t.Fatal(w.Code, w.Body)
	}
	if f := list[0].Files[0]; f.Offset != 3 || f.Size != 6 || f.FolderID != fid {
		t.Fatal(f)
	}
	// Another user sees nothing of it.
	if _, e := a.addUser("alex", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	if w = call(a, "GET", "/api/library/uploads", "", login(t, a, "alex")); w.Body.String() != "[]\n" && w.Body.String() != "[]" {
		t.Fatal(w.Body)
	}
	send(first, 3, "def")
	send(files[1].(map[string]any)["id"].(string), 0, "ghi")
	folderCall(t, a, owner, "POST", "/api/shares/"+up["id"].(string)+"/publish", `{}`, 200)
	if w = call(a, "GET", "/api/library/uploads", "", owner); w.Body.String() != "[]\n" && w.Body.String() != "[]" {
		t.Fatal("finished upload still listed", w.Body)
	}
}

func TestUnfinishedRequestUploadCanBeCheckedAndDiscarded(t *testing.T) {
	a, admin := emailAdmin(t)
	id := folderCall(t, a, admin, "POST", "/api/requests", `{"title":"Docs","days":7}`, 201)["id"].(string)
	w := visitor(a, "POST", "/api/requests/"+id+"/uploads", `{"files":[{"name":"x.txt","size":4}]}`, nil)
	var up startedUpload
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &up) != nil {
		t.Fatal(w.Code, w.Body)
	}
	batch := "/api/requests/" + id + "/uploads/" + up.Upload
	token := map[string]string{"X-Upload-Token": up.Token}
	if w = visitor(a, "PATCH", batch+"/files/"+up.Files[0].ID, "ab", map[string]string{"Upload-Offset": "0", "X-Upload-Token": up.Token}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = visitor(a, "GET", batch, "", map[string]string{"X-Upload-Token": "wrong"}); w.Code != 404 {
		t.Fatal("status without the token", w.Code)
	}
	var got pendingUpload
	w = visitor(a, "GET", batch, "", token)
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Files) != 1 || got.Files[0].Offset != 2 || got.Files[0].FolderID != "" {
		t.Fatal(w.Code, w.Body)
	}
	if w = visitor(a, "DELETE", batch, "", token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = visitor(a, "GET", batch, "", token); w.Code != 404 {
		t.Fatal("discarded batch still there", w.Code)
	}
	var count int
	a.db.QueryRow("SELECT COUNT(*) FROM files WHERE share_id=?", up.Upload).Scan(&count)
	if count != 0 {
		t.Fatal(fmt.Sprint("files left: ", count))
	}
}

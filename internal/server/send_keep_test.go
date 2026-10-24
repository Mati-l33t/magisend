package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSentFilesKeptInChosenFolderAfterExpiry(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	folder := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Sent"}`, 201)["id"].(string)
	send := func(body string) Share {
		t.Helper()
		w := call(a, "POST", "/api/shares", body, token)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		var sh Share
		if e := json.Unmarshal(w.Body.Bytes(), &sh); e != nil {
			t.Fatal(e)
		}
		if w = chunk(a, token, sh.Files[0].ID, 0, "data"); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		folderCall(t, a, token, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
		return sh
	}
	kept := send(`{"days":1,"keep":true,"folder_id":"` + folder + `","password":"secret-words","files":[{"name":"kept.txt","size":4}]}`)
	root := send(`{"days":1,"keep":true,"files":[{"name":"root.txt","size":4}]}`)
	gone := send(`{"days":1,"files":[{"name":"gone.txt","size":4}]}`)
	// The link keeps its own settings; only the files are filed away.
	if !kept.Protected || kept.Expires == 0 {
		t.Fatal("keep changed the link", kept)
	}
	// A folder still needs keep, and must be the sender's own.
	folderCall(t, a, token, "POST", "/api/shares", `{"days":1,"folder_id":"`+folder+`","files":[{"name":"x.txt","size":1}]}`, 400)
	folderCall(t, a, token, "POST", "/api/shares", `{"days":1,"keep":true,"folder_id":"nope","files":[{"name":"x.txt","size":1}]}`, 404)
	if e := a.Cleanup(time.Now().Add(48 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, "", "GET", "/api/public/"+kept.ID, ``, 404)
	for _, id := range []string{kept.Files[0].ID, root.Files[0].ID} {
		if w := call(a, "GET", "/api/library/files/"+id+"/download", "", token); w.Code != 200 || w.Body.String() != "data" {
			t.Fatal("kept file lost at expiry", w.Code, w.Body)
		}
	}
	if w := call(a, "GET", "/api/library/files/"+gone.Files[0].ID+"/download", "", token); w.Code != 404 {
		t.Fatal("unkept file survived expiry", w.Code)
	}
	var where string
	if e := a.db.QueryRow("SELECT COALESCE(folder_id,'') FROM library_files WHERE file_id=?", kept.Files[0].ID).Scan(&where); e != nil || where != folder {
		t.Fatal("kept file not in chosen folder", where, e)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFolderFileLimitIncludesReservations(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	fid := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Full"}`, 201)["id"].(string)
	for batch := 0; batch < folderFileLimit/100; batch++ {
		files := []map[string]any{}
		for i := 0; i < 100; i++ {
			files = append(files, map[string]any{"name": fmt.Sprintf("%d.txt", i), "size": 0})
		}
		body, _ := json.Marshal(map[string]any{"folder_id": fid, "files": files})
		w := call(a, "POST", "/api/library/uploads", string(body), token)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
	}
	folderCall(t, a, token, "POST", "/api/library/uploads", `{"folder_id":"`+fid+`","files":[{"name":"overflow.txt","size":0}]}`, 409)
	file := libraryUpload(t, a, token, "", "unfiled.txt", "").Files[0].ID
	folderCall(t, a, token, "PATCH", "/api/library/files/"+file, `{"folder_id":"`+fid+`"}`, 409)
}
func TestTextBlobCleanupRestartAndFailedDeleteReservation(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	file := libraryUpload(t, a, token, "", "keep.txt", "old").Files[0].ID
	folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":true}`, 200)
	folderCall(t, a, token, "PUT", "/api/library/files/"+file+"/text", textBody(1, "newer"), 200)
	orphan := randomID()
	if e := os.WriteFile(a.blob(orphan), []byte("orphan"), 0600); e != nil {
		t.Fatal(e)
	}
	root := a.root
	if e := a.db.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.db.Close()
	a = reopened
	if _, e = os.Stat(a.blob(orphan)); !os.IsNotExist(e) {
		t.Fatal("orphan was not collected", e)
	}
	w := call(a, "GET", "/api/library/files/"+file+"/download", "", token)
	if w.Code != 200 || w.Body.String() != "newer" {
		t.Fatal(w.Code, w.Body)
	}
	var blob string
	if e = a.db.QueryRow("SELECT blob FROM text_versions WHERE file_id=? AND number=2", file).Scan(&blob); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(a.blob(blob)); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(a.blob(blob), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(a.blob(blob), "block"), []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, token, "DELETE", "/api/library/files/"+file, `{}`, 200)
	folderCall(t, a, token, "DELETE", "/api/trash/"+file, `{}`, 503)
	me := folderCall(t, a, token, "GET", "/api/me", "", 200)
	if me["user"].(map[string]any)["used"] != float64(8) {
		t.Fatal("quota released before destructive delete succeeded", me)
	}
	if e = os.Remove(filepath.Join(a.blob(blob), "block")); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(a.blob(blob)); e != nil {
		t.Fatal(e)
	}
	if e = a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(a.blob(""))
	if e != nil || len(entries) != 0 {
		t.Fatal("version/original leak", entries, e)
	}
	me = folderCall(t, a, token, "GET", "/api/me", "", 200)
	if me["user"].(map[string]any)["used"] != float64(0) {
		t.Fatal(me)
	}
}

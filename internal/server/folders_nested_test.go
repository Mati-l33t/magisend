package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"sort"
	"testing"
)

// An installation from before nesting keeps its folders, files, links and
// requests; folder names then only need to be unique within their parent.
func TestFlatFoldersMigrateToNested(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	setupAdmin(t, a)
	token := login(t, a, "mati")
	fid := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	file := libraryUpload(t, a, token, fid, "plan.txt", "plan").Files[0].ID
	sid := folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	// Put the folders table back the way it was before nesting.
	for _, q := range []string{
		"PRAGMA foreign_keys=OFF",
		"CREATE TABLE folders_flat(id TEXT PRIMARY KEY,owner TEXT NOT NULL REFERENCES users(id),name TEXT NOT NULL,UNIQUE(owner,name))",
		"INSERT INTO folders_flat(id,owner,name) SELECT id,owner,name FROM folders",
		"DROP TABLE folders",
		"ALTER TABLE folders_flat RENAME TO folders",
		"PRAGMA foreign_keys=ON",
	} {
		if _, e = a.db.Exec(q); e != nil {
			t.Fatal(q, e)
		}
	}
	a.db.Close()
	a, e = NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer a.db.Close()
	var folders []Folder
	if e = json.Unmarshal(call(a, "GET", "/api/folders", "", token).Body.Bytes(), &folders); e != nil || len(folders) != 1 || folders[0].ID != fid || folders[0].ParentID != "" {
		t.Fatal("folders after migration:", folders, e)
	}
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", token); w.Body.String() != "plan" {
		t.Fatal("file lost:", w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/public/"+sid, "", ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("plan.txt")) {
		t.Fatal("link lost:", w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work","parent_id":"`+fid+`"}`, 201)
	folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 409)
}

func TestNestedFoldersAndSharedSubfolders(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	work := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	trip := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Trip","parent_id":"`+work+`"}`, 201)["id"].(string)
	day := folderCall(t, a, token, "POST", "/api/folders", `{"name":"day2","parent_id":"`+trip+`"}`, 201)["id"].(string)
	folderCall(t, a, token, "POST", "/api/folders", `{"name":"x","parent_id":"missing"}`, 404)
	libraryUpload(t, a, token, work, "notes.txt", "top")
	libraryUpload(t, a, token, trip, "map.txt", "route")
	libraryUpload(t, a, token, day, "map.txt", "second")

	// A shared folder includes its subfolders, with their paths.
	sid := folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+work+`"}`, 201)["id"].(string)
	var s Share
	if e := json.Unmarshal(call(a, "GET", "/api/public/"+sid, "", "").Body.Bytes(), &s); e != nil {
		t.Fatal(e)
	}
	got := []string{}
	for _, f := range s.Files {
		got = append(got, f.Folder+"|"+f.Name)
	}
	want := []string{"|notes.txt", "Trip|map.txt", "Trip/day2|map.txt"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatal("shared files:", got)
	}
	w := call(a, "GET", "/api/public/"+sid+"/zip", "", "")
	z, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil {
		t.Fatal(w.Code, e)
	}
	names := []string{}
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	if len(names) != 3 || names[0] != "Trip/day2/map.txt" || names[1] != "Trip/map.txt" || names[2] != "notes.txt" {
		t.Fatal("zip entries:", names)
	}

	// No loops, and a folder with contents or subfolders stays.
	folderCall(t, a, token, "PATCH", "/api/folders/"+work, `{"parent_id":"`+day+`"}`, 400)
	folderCall(t, a, token, "PATCH", "/api/folders/"+work, `{"parent_id":"`+work+`"}`, 400)
	folderCall(t, a, token, "DELETE", "/api/folders/"+trip, `{}`, 409)
	// Moving day2 to the top takes it out of the shared folder.
	folderCall(t, a, token, "PATCH", "/api/folders/"+day, `{"parent_id":""}`, 200)
	if e := json.Unmarshal(call(a, "GET", "/api/public/"+sid, "", "").Body.Bytes(), &s); e != nil || len(s.Files) != 2 {
		t.Fatal("after moving out:", len(s.Files), e)
	}
	moved := folderCall(t, a, token, "PATCH", "/api/folders/"+day, `{"name":"Day two","parent_id":"`+work+`"}`, 200)
	if moved["name"] != "Day two" || moved["parent_id"] != work {
		t.Fatal(moved)
	}

	// A move that would make a shared folder larger than a share may be is refused.
	p := folderCall(t, a, token, "GET", "/api/admin/policy", "", 200)
	p["max_share"], p["max_file"] = float64(12), float64(12)
	body, _ := json.Marshal(p)
	folderCall(t, a, token, "PUT", "/api/admin/policy", string(body), 200)
	other := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Other"}`, 201)["id"].(string)
	libraryUpload(t, a, token, other, "big.txt", "0123456789")
	folderCall(t, a, token, "PATCH", "/api/folders/"+other, `{"parent_id":"`+trip+`"}`, 413)
}

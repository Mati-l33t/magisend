package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) string {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte(data))
	}
	z.Close()
	return b.String()
}

func TestExtractZipIntoNewFolder(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	parent := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	archive := zipOf(t, map[string]string{
		"readme.txt":            "top",
		"photos/2024/a.txt":     "deep",
		"photos/b.txt":          "middle",
		"../escape.txt":         "no",
		"__MACOSX/._readme.txt": "no",
		"photos/.DS_Store":      "no",
		"/absolute.txt":         "no",
	})
	zipFile := libraryUpload(t, a, token, parent, "Holiday.zip", archive).Files[0].ID
	folderCall(t, a, token, "POST", "/api/folders", `{"name":"Holiday","parent_id":"`+parent+`"}`, 201)

	got := folderCall(t, a, token, "POST", "/api/library/files/"+zipFile+"/extract", `{}`, 201)
	if got["name"] != "Holiday (2)" || got["files"] != float64(3) || got["skipped"] != float64(4) {
		t.Fatal(got)
	}
	top := got["folder_id"].(string)
	var folders []Folder
	json.Unmarshal(call(a, "GET", "/api/folders", "", token).Body.Bytes(), &folders)
	path := map[string]string{}
	byID := map[string]Folder{}
	for _, f := range folders {
		byID[f.ID] = f
	}
	for _, f := range folders {
		name := f.Name
		for p := f.ParentID; p != "" && p != parent; p = byID[p].ParentID {
			name = byID[p].Name + "/" + name
		}
		path[f.ID] = name
	}
	if byID[top].ParentID != parent {
		t.Fatal("new folder not next to the archive", byID[top])
	}
	var files []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", token).Body.Bytes(), &files)
	where := map[string]string{}
	for _, f := range files {
		where[f.Name] = path[f.FolderID]
		if f.Name == "a.txt" {
			if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", token); w.Body.String() != "deep" {
				t.Fatal(w.Body)
			}
		}
	}
	if where["readme.txt"] != "Holiday (2)" || where["b.txt"] != "Holiday (2)/photos" || where["a.txt"] != "Holiday (2)/photos/2024" || where["escape.txt"] != "" {
		t.Fatal(where)
	}

	// Not a ZIP, or a ZIP whose contents lie about their size.
	notZip := libraryUpload(t, a, token, "", "fake.zip", "not a zip").Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/files/"+notZip+"/extract", `{}`, 400)
	text := libraryUpload(t, a, token, "", "plain.txt", "text").Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/files/"+text+"/extract", `{}`, 400)
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.CreateRaw(&zip.FileHeader{Name: "liar.txt", Method: zip.Store, CompressedSize64: 10, UncompressedSize64: 3, CRC32: 0})
	w.Write([]byte("ten bytes!"))
	z.Close()
	liar := libraryUpload(t, a, token, "", "liar.zip", b.String()).Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/files/"+liar+"/extract", `{}`, 400)
	json.Unmarshal(call(a, "GET", "/api/folders", "", token).Body.Bytes(), &folders)
	for _, f := range folders {
		if f.Name == "liar" {
			t.Fatal("failed extraction left its folder")
		}
	}
	// Someone else cannot unpack it.
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, login(t, a, "other"), "POST", "/api/library/files/"+zipFile+"/extract", `{}`, 404)
}

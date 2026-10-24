package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func reserveUpload(a *App, token, folder string, n int) int {
	files := []map[string]any{}
	for i := range n {
		files = append(files, map[string]any{"name": fmt.Sprintf("pending-%d.txt", i), "size": 1})
	}
	body, _ := json.Marshal(map[string]any{"files": files, "folder_id": folder})
	return call(a, "POST", "/api/library/uploads", string(body), token).Code
}

// Every library upload is stored as a hidden container, which must not use
// up the link limit; a folder holds 1000 files and its ZIP streams them all.
func TestLibraryUploadsAreNotLinksAndFoldersHoldMany(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	fid := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	for i := range ownerShareLimit + 20 {
		libraryUpload(t, a, token, fid, fmt.Sprintf("file-%03d.txt", i), fmt.Sprint(i))
	}
	sid := folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	w := call(a, "GET", "/api/public/"+sid+"/zip", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	z, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil || len(z.File) != ownerShareLimit+20 {
		t.Fatalf("zip: %v, %d entries", e, len(z.File))
	}
	added := ownerShareLimit + 20
	for added < folderFileLimit {
		n := min(100, folderFileLimit-added)
		if code := reserveUpload(a, token, fid, n); code != 201 {
			t.Fatalf("upload %d files at %d: %d", n, added, code)
		}
		added += n
	}
	w = call(a, "POST", "/api/library/uploads", `{"files":[{"name":"one-too-many.txt","size":1}],"folder_id":"`+fid+`"}`, token)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "folder_full") {
		t.Fatal("folder past its limit:", w.Code, w.Body)
	}
}

func TestAdminSetsFileLimits(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	p := folderCall(t, a, token, "GET", "/api/admin/policy", "", 200)
	if p["files_per_user"] != float64(defaultFilesPerUser) || p["files_total"] != float64(defaultFilesTotal) {
		t.Fatal("defaults:", p)
	}
	put := func(change map[string]any) int {
		next := map[string]any{}
		for k, v := range p {
			next[k] = v
		}
		for k, v := range change {
			next[k] = v
		}
		body, _ := json.Marshal(next)
		return call(a, "PUT", "/api/admin/policy", string(body), token).Code
	}
	for _, bad := range []map[string]any{
		{"files_per_user": 99},
		{"files_per_user": 2000, "files_total": 1000},
		{"files_total": maxFilesTotal + 1},
	} {
		if code := put(bad); code != 400 {
			t.Fatal(bad, code)
		}
	}
	if code := put(map[string]any{"files_per_user": 100}); code != 200 {
		t.Fatal(code)
	}
	// A client that does not know the fields keeps them.
	if code := put(map[string]any{"files_per_user": nil, "files_total": nil}); code != 200 {
		t.Fatal(code)
	}
	if p = folderCall(t, a, token, "GET", "/api/admin/policy", "", 200); p["files_per_user"] != float64(100) {
		t.Fatal("limit not kept:", p)
	}
	if code := reserveUpload(a, token, "", 100); code != 201 {
		t.Fatal(code)
	}
	if code := reserveUpload(a, token, "", 1); code != 429 {
		t.Fatal("past the file limit:", code)
	}
}

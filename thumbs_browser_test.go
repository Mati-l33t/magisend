package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// A PDF gets its picture from the owner's browser; nobody else may set it and
// only a real PNG or JPEG is kept.
func TestBrowserThumbnailForPDF(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	pdf := []byte("%PDF-1.4\n%%EOF\n")
	notes := []byte("plain text")
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Docs","days":7,"files":[{"name":"a.pdf","size":%d},{"name":"b.txt","size":%d}]}`, len(pdf), len(notes)), map[string][]byte{"a.pdf": pdf, "b.txt": notes})
	path := "/api/shares/" + id + "/files/" + ids["a.pdf"] + "/thumbnail"
	picture := string(pngFixture(t, 400, 300))

	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), session)
	if w := call(a, "PUT", path, picture, login(t, a, "bob")); w.Code != 404 {
		t.Fatalf("another account set a thumbnail: %d", w.Code)
	}
	if w := call(a, "PUT", path, "not a picture", session); w.Code != 400 {
		t.Fatalf("garbage accepted: %d", w.Code)
	}
	if w := call(a, "PUT", "/api/shares/"+id+"/files/"+ids["b.txt"]+"/thumbnail", picture, session); w.Code != 400 {
		t.Fatalf("text file accepted a thumbnail: %d", w.Code)
	}
	if w := call(a, "PUT", path, string(pngFixture(t, 2000, 10)), session); w.Code != 400 {
		t.Fatalf("oversized picture accepted: %d", w.Code)
	}
	if w := call(a, "PUT", path, picture, session); w.Code != 204 {
		t.Fatalf("owner thumbnail: %d %s", w.Code, w.Body)
	}
	var width, height int
	if e := a.db.QueryRow("SELECT width,height FROM thumbnails WHERE file_id=?", ids["a.pdf"]).Scan(&width, &height); e != nil || width != thumbMaxEdge || height != 192 {
		t.Fatalf("stored %dx%d %v", width, height, e)
	}
	w := call(a, "GET", "/api/public/"+id, "", "")
	var view struct {
		Files []struct {
			Name     string `json:"name"`
			HasThumb bool   `json:"has_thumb"`
		} `json:"files"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &view); e != nil {
		t.Fatal(e)
	}
	for _, f := range view.Files {
		if f.HasThumb != (f.Name == "a.pdf") {
			t.Fatalf("has_thumb for %s: %v", f.Name, f.HasThumb)
		}
	}
	if w := call(a, "GET", "/api/public/"+id+"/files/"+ids["a.pdf"]+"/thumbnail", "", ""); w.Code != 200 {
		t.Fatalf("visitor thumbnail: %d", w.Code)
	}
}

package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The answer to a publish already says which files wait for their scan, so
// the page shown next follows them.
func TestPublishAnswerSaysWhatWaits(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	w := call(a, "POST", "/api/shares", `{"title":"New","days":7,"files":[{"name":"a.txt","size":5}]}`, admin)
	var created Share
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatal(w.Code, w.Body)
	}
	if w = chunk(a, admin, created.Files[0].ID, 0, "hello"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "POST", "/api/shares/"+created.ID+"/publish", "{}", admin)
	var published Share
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &published) != nil {
		t.Fatal(w.Code, w.Body)
	}
	if published.Files[0].Scan != scanPending {
		t.Fatal("the publish answer does not say the file waits", w.Body)
	}
}

// A thumbnail sent while both decoding turns are taken waits a moment, then
// is told to come back; the turns it takes are always given back.
func TestBusyThumbnailUploadIsSentBackLater(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	pdf := []byte("%PDF-1.4\n%%EOF\n")
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Docs","days":7,"files":[{"name":"a.pdf","size":%d}]}`, len(pdf)), map[string][]byte{"a.pdf": pdf})
	path := "/api/shares/" + id + "/files/" + ids["a.pdf"] + "/thumbnail"
	picture := string(pngFixture(t, 200, 100))

	before := thumbUploadWait
	thumbUploadWait = 50 * time.Millisecond
	t.Cleanup(func() { thumbUploadWait = before })
	for range cap(a.thumbUploads) {
		a.thumbUploads <- struct{}{}
	}
	if w := call(a, "PUT", path, picture, session); w.Code != 503 || !strings.Contains(w.Body.String(), "thumbnail_busy") {
		t.Fatal("a busy upload was not sent back", w.Code, w.Body)
	}
	for range cap(a.thumbUploads) {
		<-a.thumbUploads
	}
	// Its size is readable, its picture is not: decoded, failed, turn given back.
	if w := call(a, "PUT", path, picture[:60], session); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "PUT", path, picture, session); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	if len(a.thumbUploads) != 0 {
		t.Fatal("a decoding turn was not given back", len(a.thumbUploads))
	}
}

// ZIP names that are not UTF-8 are from before zip knew UTF-8 and read as
// code page 437; UTF-8 names are kept as tar keeps them.
func TestZipNames(t *testing.T) {
	for raw, want := range map[string]string{
		"M\x81ller.txt":    "Müller.txt",
		"M\x8Dller.txt":    "Mìller.txt",
		"\x8e\x84\x99.txt": "ÄäÖ.txt",
		"plain.txt":        "plain.txt",
		"Müller.txt":       "Müller.txt",
		"a\u0085b.txt":     "a…b.txt",
	} {
		if got := zipName(raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

// A ZIP entry with an overlong name is skipped and counted, as in a tar file.
func TestZipOverlongNameIsCounted(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	archive := zipOf(t, map[string]string{"short.txt": "kept", strings.Repeat("a", tarNameLimit+1): "long"})
	id := libraryUpload(t, a, token, "", "Names.zip", archive).Files[0].ID
	got := folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 201)
	if got["files"] != float64(1) || got["skipped"] != float64(1) {
		t.Fatal(got)
	}
}

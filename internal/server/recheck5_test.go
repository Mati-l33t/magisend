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

// Text in older encodings is shown, as UTF-8; the file itself stays as it is.
func TestTextInOlderEncodingsIsShown(t *testing.T) {
	utf16le := []byte{0xff, 0xfe, 'H', 0, 0xe5, 0, 'j', 0}
	utf16be := []byte{0xfe, 0xff, 0, 'H', 0, 0xe5, 0, 'j'}
	for _, c := range []struct {
		in   []byte
		want string
		ok   bool
	}{
		{[]byte("plain ascii"), "plain ascii", true},
		{[]byte("Lösenord"), "Lösenord", true},
		{[]byte("L\xf6senord \x80 \x96 \x81"), "Lösenord € – \ufffd", true},
		{utf16le, "Håj", true},
		{utf16be, "Håj", true},
		{[]byte("a\x00b"), "", false},
		{[]byte{0xff, 0xfe, 'a', 0, 0, 0}, "", false},
	} {
		got, ok := readableText(c.in)
		if ok != c.ok || ok && string(got) != c.want {
			t.Errorf("%q: %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}

	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	id := libraryUpload(t, a, token, "", "Lösenord.txt", "L\xf6senord: hemligt").Files[0].ID
	w := call(a, "GET", "/api/library/files/"+id+"/media", "", token)
	if w.Code != 200 || w.Body.String() != "Lösenord: hemligt" || !strings.Contains(w.Header().Get("Content-Type"), "utf-8") {
		t.Fatal(w.Code, w.Header(), w.Body)
	}
	if w := call(a, "GET", "/api/library/files/"+id+"/download", "", token); w.Body.String() != "L\xf6senord: hemligt" {
		t.Fatal("the download changed", w.Body)
	}
}

// The library says which files a link shows now: picked files, files in a
// shared folder or below it, and files kept from Send while their link lasts.
func TestLibraryMarksSharedFiles(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	work := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	sub := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Sub","parent_id":"`+work+`"}`, 201)["id"].(string)
	top := libraryUpload(t, a, token, "", "top.txt", "top").Files[0].ID
	deep := libraryUpload(t, a, token, sub, "deep.txt", "deep").Files[0].ID
	alone := libraryUpload(t, a, token, "", "alone.txt", "alone").Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+top+`","`+deep+`"]}`, 201)
	folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+work+`"}`, 201)
	old := folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+alone+`"]}`, 201)["id"].(string)
	a.db.Exec("UPDATE shares SET expires=1 WHERE id=?", old)
	w := call(a, "POST", "/api/shares", `{"title":"Sent","days":7,"keep":true,"files":[{"name":"kept.txt","size":4}]}`, token)
	var sent Share
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &sent) != nil {
		t.Fatal(w.Code, w.Body)
	}
	chunk(a, token, sent.Files[0].ID, 0, "kept")
	if w = call(a, "POST", "/api/shares/"+sent.ID+"/publish", "{}", token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}

	shared := func() map[string]int {
		var files []LibraryFile
		json.Unmarshal(call(a, "GET", "/api/library/files", "", token).Body.Bytes(), &files)
		got := map[string]int{}
		for _, f := range files {
			got[f.Name] = f.Shared
		}
		return got
	}
	got := shared()
	if got["top.txt"] != 1 || got["deep.txt"] != 2 || got["alone.txt"] != 0 || got["kept.txt"] != 1 {
		t.Fatal("marks", got)
	}
	// Once the link it went out with has expired, a kept file is not shared.
	a.db.Exec("UPDATE shares SET expires=1 WHERE id=?", sent.ID)
	if got = shared(); got["kept.txt"] != 0 {
		t.Fatal("an expired link still marks its file", got)
	}
}

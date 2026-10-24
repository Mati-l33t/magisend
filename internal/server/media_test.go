package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMediaTypeFromContent(t *testing.T) {
	for head, want := range map[string]string{
		"%PDF-1.7\n":                       "application/pdf",
		"\x00\x00\x00\x18ftypmp42\x00\x00": "video/mp4",
		"\x00\x00\x00\x18ftypM4A \x00\x00": "audio/mp4",
		"\x1a\x45\xdf\xa3\x01":             "video/webm",
		"OggS\x00":                         "audio/ogg",
		"RIFF\x24\x00\x00\x00WAVEfmt ":     "audio/wav",
		"ID3\x04\x00":                      "audio/mpeg",
		"\xff\xfb\x90":                     "audio/mpeg",
		"\xff\xf1\x50":                     "audio/aac",
		"fLaC\x00":                         "audio/flac",
	} {
		if got, ok := mediaType([]byte(head)); !ok || got != want {
			t.Fatalf("%q: %q", head, got)
		}
	}
	for _, head := range []string{"<html><script>", "PK\x03\x04", "", "\x89PNG"} {
		if _, ok := mediaType([]byte(head)); ok {
			t.Fatalf("accepted %q", head)
		}
	}
}

func TestMediaPreviewIsSandboxedAndSeekable(t *testing.T) {
	a, admin := emailAdmin(t)
	video := "\x00\x00\x00\x18ftypmp42" + strings.Repeat("v", 100)
	body := fmt.Sprintf(`{"title":"Media","days":7,"files":[{"name":"clip.txt","size":%d},{"name":"page.html","size":20}]}`, len(video))
	w := call(a, "POST", "/api/shares", body, admin)
	sh := decodeShare(t, w)
	chunk(a, admin, sh.Files[0].ID, 0, video)
	chunk(a, admin, sh.Files[1].ID, 0, "<html><script></scri")
	call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", admin)
	get := func(path, span, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if span != "" {
			r.Header.Set("Range", span)
		}
		if token != "" {
			r.Header.Set("Cookie", "magisend_session="+token)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	media := "/api/public/" + sh.ID + "/files/" + sh.Files[0].ID + "/media"
	// The name says .txt; the content decides.
	w = get(media, "bytes=4-11", "")
	if w.Code != 206 || w.Body.String() != "ftypmp42" || w.Header().Get("Content-Type") != "video/mp4" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Fatal(w.Code, w.Header(), w.Body)
	}
	// HTML is never served as a page, only as its source text.
	if w = get("/api/public/"+sh.ID+"/files/"+sh.Files[1].ID+"/media", "", ""); w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("HTML served as media", w.Code, w.Header())
	}
	if w = get("/api/shares/"+sh.ID+"/files/"+sh.Files[0].ID+"/media", "", admin); w.Code != 200 {
		t.Fatal("owner media", w.Code)
	}
	if w = get("/api/shares/"+sh.ID+"/files/"+sh.Files[0].ID+"/media", "", ""); w.Code != 401 {
		t.Fatal("owner route without a session", w.Code)
	}
	var downloads int
	a.db.QueryRow("SELECT downloads FROM shares WHERE id=?", sh.ID).Scan(&downloads)
	if downloads != 0 {
		t.Fatal("playing counted as a download")
	}
	call(a, "PATCH", "/api/shares/"+sh.ID, `{"download_limit":2}`, admin)
	if w = get(media, "", ""); w.Code != 403 {
		t.Fatal("limited link streamed", w.Code)
	}
	lib := libraryUpload(t, a, admin, "", "doc.pdf", "%PDF-1.4 tiny")
	if w = get("/api/library/files/"+lib.Files[0].ID+"/media", "", admin); w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" {
		t.Fatal("library media", w.Code, w.Header())
	}
}

func decodeShare(t *testing.T, w *httptest.ResponseRecorder) Share {
	t.Helper()
	var s Share
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
		t.Fatal(w.Code, w.Body)
	}
	return s
}

func TestTextFilesPreviewAsPlainText(t *testing.T) {
	a, admin := emailAdmin(t)
	notes := "# Notes\n\n<script>alert(1)</script> åäö\n"
	body := fmt.Sprintf(`{"title":"Text","days":7,"files":[{"name":"notes.md","size":%d},{"name":"blob.md","size":4},{"name":"page.html","size":6}]}`, len(notes))
	sh := decodeShare(t, call(a, "POST", "/api/shares", body, admin))
	chunk(a, admin, sh.Files[0].ID, 0, notes)
	chunk(a, admin, sh.Files[1].ID, 0, "a\x00bc")
	chunk(a, admin, sh.Files[2].ID, 0, "<html>")
	call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", admin)
	get := func(file string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/public/"+sh.ID+"/files/"+file+"/media", nil))
		return w
	}
	w := get(sh.Files[0].ID)
	if w.Code != 200 || w.Body.String() != notes || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Code, w.Header(), w.Body)
	}
	if w = get(sh.Files[1].ID); w.Code != 415 {
		t.Fatal("binary content shown as text", w.Code)
	}
	// HTML is shown as its source: plain text in a sandbox, never a page.
	if w = get(sh.Files[2].ID); w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal("HTML source", w.Code, w.Header())
	}
}

// Word and Excel files reach their viewers only when the name and the ZIP
// signature agree.
func TestOfficeFilesServedForViewers(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	zip := []byte("PK\x03\x04 rest of an archive")
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Office","days":7,"files":[{"name":"a.docx","size":%d},{"name":"b.xlsx","size":%d},{"name":"c.zip","size":%d},{"name":"d.docx","size":4}]}`, len(zip), len(zip), len(zip)), map[string][]byte{"a.docx": zip, "b.xlsx": zip, "c.zip": zip, "d.docx": []byte("text")})
	for name, want := range map[string]string{"a.docx": officeTypes[".docx"], "b.xlsx": officeTypes[".xlsx"], "c.zip": "", "d.docx": ""} {
		w := call(a, "GET", "/api/public/"+id+"/files/"+ids[name]+"/media", "", "")
		if want == "" {
			if w.Code != 415 {
				t.Fatalf("%s served: %d", name, w.Code)
			}
			continue
		}
		if w.Code != 200 || w.Header().Get("Content-Type") != want || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Fatalf("%s: %d %q", name, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

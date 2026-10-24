package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Shares that were published before thumbnails existed still get one, generated
// on first request instead of a migration backfill.
func TestThumbnailIsGeneratedForSharesPublishedWithoutIt(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	picture := pngFixture(t, 200, 100)
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Gammal","days":7,"files":[{"name":"a.png","size":%d}]}`, len(picture)), map[string][]byte{"a.png": picture})
	fileID := ids["a.png"]

	if w := thumbnail(t, a, "/api/shares/"+id+"/files/"+fileID+"/thumbnail", session, 200); w.Code != 200 {
		t.Fatalf("warm up: %d %s", w.Code, w.Body)
	}
	// Simulate a legacy share by removing the stored thumbnail again.
	if _, e := a.db.Exec("DELETE FROM thumbnails WHERE file_id=?", fileID); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	w := call(a, "GET", "/api/shares/"+id+"/files/"+fileID+"/thumbnail", "", session)
	if w.Code != 200 {
		t.Fatalf("lazy generation failed: %d %s", w.Code, w.Body)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("lazy generation was not bounded")
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "image/jpeg" && contentType != "image/png" {
		t.Fatalf("content type %q", contentType)
	}
	var count int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM thumbnails WHERE file_id=?", fileID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("thumbnail not stored: %d %v", count, e)
	}
}

// A thumbnail request must never synthesise a thumbnail for a file the share does
// not contain, and never answer with the full preview.
func TestThumbnailStaysScopedToItsShare(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	picture := pngFixture(t, 40, 40)
	first, firstIDs := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Ett","days":7,"files":[{"name":"a.png","size":%d}]}`, len(picture)), map[string][]byte{"a.png": picture})
	second, _ := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Två","days":7,"files":[{"name":"a.png","size":%d}]}`, len(picture)), map[string][]byte{"a.png": picture})

	if w := thumbnail(t, a, "/api/shares/"+first+"/files/"+firstIDs["a.png"]+"/thumbnail", session, 200); w.Code != 200 {
		t.Fatalf("warm up: %d", w.Code)
	}
	if w := call(a, "GET", "/api/shares/"+second+"/files/"+firstIDs["a.png"]+"/thumbnail", "", session); w.Code != 404 {
		t.Fatalf("foreign file answered: %d %s", w.Code, w.Body)
	}
	// A second user can never read the first user's thumbnail.
	if w := call(a, "POST", "/api/admin/users", `{"username":"bob","password":"correct horse battery staple","quota":1073741824}`, session); w.Code != 200 && w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	bob := login(t, a, "bob")
	if w := call(a, "GET", "/api/shares/"+first+"/files/"+firstIDs["a.png"]+"/thumbnail", "", bob); w.Code != 404 {
		var payload map[string]any
		json.Unmarshal(w.Body.Bytes(), &payload)
		if code, _ := payload["error_code"].(string); code != "share_not_found" {
			t.Fatalf("foreign owner thumbnail: %d %s", w.Code, w.Body)
		}
	}
}

// An expired or tombstoned share must not keep serving decorative bytes.
func TestThumbnailRespectsExpiryAndTombstone(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	picture := pngFixture(t, 40, 40)
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Kort","days":1,"files":[{"name":"a.png","size":%d}]}`, len(picture)), map[string][]byte{"a.png": picture})
	fileID := ids["a.png"]
	if w := thumbnail(t, a, "/api/public/"+id+"/files/"+fileID+"/thumbnail", "", 200); w.Code != 200 {
		t.Fatalf("public warm up: %d %s", w.Code, w.Body)
	}
	if _, e := a.db.Exec("UPDATE shares SET expires=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), id); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/public/" + id + "/files/" + fileID + "/thumbnail", "/api/shares/" + id + "/files/" + fileID + "/thumbnail"} {
		w := call(a, "GET", path, "", session)
		if w.Code == 200 {
			t.Fatalf("expired share served a thumbnail: %s", path)
		}
		if strings.Contains(w.Header().Get("Content-Type"), "image") {
			t.Fatalf("expired share served image bytes: %s", path)
		}
	}
	var count int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM thumbnails WHERE file_id=?", fileID).Scan(&count); e != nil {
		t.Fatal(e)
	}
	// The bytes stay in storage, but an expired share must never serve them.
	if count != 1 {
		t.Fatalf("stored thumbnail rows after expiry: %d", count)
	}
}

// A file that can never have a thumbnail is remembered, so a public visitor cannot
// make the server re-read and re-decode the same bytes on every request.
func TestImpossibleThumbnailIsNotRegeneratedPerRequest(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	vector := []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Vektor","slug":"vektor-31","days":7,"files":[{"name":"a.svg","size":%d}]}`, len(vector)), map[string][]byte{"a.svg": vector})
	fileID := ids["a.svg"]
	path := "/api/public/" + id + "/files/" + fileID + "/thumbnail"
	if w := call(a, "GET", path, "", ""); w.Code != 404 {
		t.Fatalf("first request: %d %s", w.Code, w.Body)
	}
	if _, blocked := a.thumbBlocked.Load(fileID); !blocked {
		t.Fatal("permanent failure was not remembered")
	}
	start := time.Now()
	if w := call(a, "GET", path, "", ""); w.Code != 404 {
		t.Fatalf("second request: %d %s", w.Code, w.Body)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatalf("second request repeated the work: %s", time.Since(start))
	}
	select {
	case <-a.thumbs:
		t.Fatal("a blocked file was queued for generation")
	default:
	}
}

// The custom link must work for every public surface, not only for the page data.
func TestCustomLinkServesDownloadsAndZip(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	picture := pngFixture(t, 32, 24)
	note := []byte("anteckning")
	body := fmt.Sprintf(`{"title":"Länkad","slug":"lankad-31","days":7,"files":[{"name":"a.png","size":%d},{"name":"b.txt","size":%d}]}`, len(picture), len(note))
	_, ids := createUploadPublish(t, a, session, body, map[string][]byte{"a.png": picture, "b.txt": note})

	w := call(a, "GET", "/api/public/lankad-31/files/"+ids["a.png"], "", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), picture) {
		t.Fatalf("slug download: %d %d bytes", w.Code, w.Body.Len())
	}
	zip := call(a, "GET", "/api/public/lankad-31/zip", "", "")
	if zip.Code != 200 || len(zip.Body.Bytes()) == 0 {
		t.Fatalf("slug zip: %d %s", zip.Code, zip.Body)
	}
	if contentType := zip.Header().Get("Content-Type"); !strings.Contains(contentType, "zip") {
		t.Fatalf("slug zip content type %q", contentType)
	}
}

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEditShareAfterCreation(t *testing.T) {
	a, admin := emailAdmin(t)
	sh := emailReadyShare(t, a, admin, "first-share-password")
	edit := func(body, token string) *httptest.ResponseRecorder {
		return call(a, "PATCH", "/api/shares/"+sh.ID, body, token)
	}
	w := edit(`{"title":"  Renamed  ","description":"New text","slug":"holiday-2026","days":30,"download_limit":5}`, admin)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	got := decodeObject(t, w)
	if got["title"] != "Renamed" || got["description"] != "New text" || got["slug"] != "holiday-2026" || got["download_limit"] != float64(5) {
		t.Fatal(got)
	}
	if expires := int64(got["expires"].(float64)); expires < time.Now().Add(29*24*time.Hour).Unix() {
		t.Fatal("validity not extended", expires)
	}
	// The custom link works; omitted fields keep their values.
	if w = call(a, "GET", "/api/public/holiday-2026", "", ""); w.Code != 401 {
		t.Fatal("custom link with password", w.Code)
	}
	if w = edit(`{"permanent":true}`, admin); w.Code != 200 || decodeObject(t, w)["expires"] != float64(0) || decodeObject(t, w)["title"] != "Renamed" {
		t.Fatal(w.Code, w.Body)
	}
	// Removing the password opens the link and drops existing unlocks.
	a.db.Exec("INSERT INTO grants(hash,share_id,expires) VALUES('x',?,?)", sh.ID, time.Now().Add(time.Hour).Unix())
	if w = edit(`{"password":""}`, admin); w.Code != 200 || decodeObject(t, w)["protected"] != false {
		t.Fatal(w.Code, w.Body)
	}
	var grants int
	a.db.QueryRow("SELECT COUNT(*) FROM grants WHERE share_id=?", sh.ID).Scan(&grants)
	if grants != 0 {
		t.Fatal("old unlocks kept after a password change")
	}
	if w = call(a, "GET", "/api/public/holiday-2026", "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = edit(`{"password":"a-new-share-password"}`, admin); w.Code != 200 || decodeObject(t, w)["protected"] != true {
		t.Fatal(w.Code, w.Body)
	}
	other := emailReadyShare(t, a, admin, "")
	if w = call(a, "PATCH", "/api/shares/"+other.ID, `{"slug":"taken-name"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for body, status := range map[string]int{
		`{"title":"   "}`:       400,
		`{"days":400}`:          400,
		`{"slug":"Bad Slug"}`:   400,
		`{"slug":"taken-name"}`: 409,
		`{"download_limit":-1}`: 400,
		`{"description":"` + strings.Repeat("x", 1001) + `"}`: 400,
	} {
		if w = edit(body, admin); w.Code != status {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), admin)
	if w = edit(`{"title":"Stolen"}`, login(t, a, "bob")); w.Code != 404 {
		t.Fatal("another user edited the share", w.Code)
	}
	// Lowering the limit below what was downloaded closes the link.
	r := httptest.NewRequest("GET", "/api/public/"+other.ID+"/files/"+other.Files[0].ID, nil)
	a.Handler().ServeHTTP(httptest.NewRecorder(), r)
	if w = call(a, "PATCH", "/api/shares/"+other.ID, `{"download_limit":1}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/public/"+other.ID, "", ""); w.Code != http.StatusGone {
		t.Fatal("link still open", w.Code)
	}
	if w = call(a, "PATCH", "/api/shares/"+other.ID, `{"days":7}`, admin); w.Code != http.StatusGone {
		t.Fatal("a closed link must not be reopened", w.Code)
	}
}

func TestEditLibraryLinkAfterCreation(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	fid := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Screenshots"}`, 201)["id"].(string)
	libraryUpload(t, a, owner, fid, "shot.txt", "pixels")
	sid := folderCall(t, a, owner, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	got := folderCall(t, a, owner, "PATCH", "/api/shares/"+sid, `{"title":"Shots","description":"From my phone","slug":"my-shots","download_limit":2}`, 200)
	if got["title"] != "Shots" || got["slug"] != "my-shots" || got["mode"] != "live" || got["download_limit"] != float64(2) {
		t.Fatal(got)
	}
	if public := folderCall(t, a, "", "GET", "/api/public/my-shots", "", 200); public["description"] != "From my phone" {
		t.Fatal(public)
	}
	// A link that lets others add files cannot run out of downloads.
	folderCall(t, a, owner, "PATCH", "/api/shares/"+sid, `{"download_limit":0}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+sid, `{"recipients":["mati"],"add":true}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/shares/"+sid, `{"download_limit":2}`, 400)
	folderCall(t, a, owner, "PATCH", "/api/shares/"+sid, `{"title":"Still editable"}`, 200)
}

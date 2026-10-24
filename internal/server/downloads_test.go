package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func limitedShare(t *testing.T, a *App, owner string, limit int) Share {
	t.Helper()
	body := fmt.Sprintf(`{"title":"Limited","days":7,"download_limit":%d,"files":[{"name":"a.txt","size":5},{"name":"b.txt","size":5}]}`, limit)
	w := call(a, "POST", "/api/shares", body, owner)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var sh Share
	if e := json.Unmarshal(w.Body.Bytes(), &sh); e != nil {
		t.Fatal(e)
	}
	for _, f := range sh.Files {
		if w := chunk(a, owner, f.ID, 0, "hello"); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	return sh
}

func fetch(a *App, method, path, span string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if span != "" {
		r.Header.Set("Range", span)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	io.Copy(io.Discard, w.Body)
	return w
}

func ownerShare(t *testing.T, a *App, owner, id string) Share {
	t.Helper()
	var list []Share
	if e := json.Unmarshal(call(a, "GET", "/api/shares", "", owner).Body.Bytes(), &list); e != nil {
		t.Fatal(e)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatal("share missing", id)
	return Share{}
}

func TestDownloadLimitPerFileClosesTheLink(t *testing.T) {
	a, admin := emailAdmin(t)
	if w := call(a, "POST", "/api/shares", `{"days":7,"download_limit":1001,"files":[{"name":"a.txt","size":1}]}`, admin); w.Code != 400 {
		t.Fatal("accepted a limit above the maximum", w.Code)
	}
	sh := limitedShare(t, a, admin, 2)
	file := func(i int) string { return "/api/public/" + sh.ID + "/files/" + sh.Files[i].ID }
	for _, request := range []struct{ method, span string }{{"GET", ""}, {"GET", "bytes=2-"}, {"HEAD", ""}, {"GET", "bytes=0-"}} {
		if w := fetch(a, request.method, file(0), request.span); w.Code != 200 && w.Code != 206 {
			t.Fatal(request, w.Code)
		}
	}
	// Only the two fresh GETs counted, so the first file is used up, also for
	// ranges from anyone but the client that was counted, who may resume.
	if w := fetch(a, "GET", file(0), ""); w.Code != 410 {
		t.Fatal("used-up file still downloadable", w.Code)
	}
	if w := fetchFrom(a, "198.51.100.20", file(0), "bytes=1-"); w.Code != 410 {
		t.Fatal("used-up file still downloadable by range", w.Code)
	}
	if w := fetch(a, "GET", file(0), "bytes=1-"); w.Code != 206 {
		t.Fatal("the counted client could not resume", w.Code)
	}
	public := decodeObject(t, call(a, "GET", "/api/public/"+sh.ID, "", ""))
	files := public["files"].([]any)
	if files[0].(map[string]any)["remaining"] != float64(0) || files[1].(map[string]any)["remaining"] != float64(2) ||
		files[0].(map[string]any)["downloads"] != nil || public["download_limit"] != float64(2) {
		t.Fatal("public view", public)
	}
	if w := fetch(a, "GET", file(1)+"/preview", ""); w.Code != 403 {
		t.Fatal("full-size preview of a limited link", w.Code)
	}
	// The owner can still download without using up the limit.
	if w := call(a, "GET", "/api/shares/"+sh.ID+"/files/"+sh.Files[0].ID, "", admin); w.Code != 200 {
		t.Fatal("owner download", w.Code)
	}
	if w := fetch(a, "GET", "/api/public/"+sh.ID+"/zip", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	owner := ownerShare(t, a, admin, sh.ID)
	if owner.Files[0].Downloads != 2 || owner.Files[1].Downloads != 1 || owner.Downloads != 3 || owner.LastDownload == 0 || owner.Files[1].LastDownload == 0 {
		t.Fatal("statistics", owner.Downloads, owner.Files)
	}
	if shareExpired(owner.Expires, time.Now().Unix()) {
		t.Fatal("closed while a file had downloads left")
	}
	if w := fetch(a, "GET", file(1), ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// No new download can start; the counted client can still resume for a while.
	for _, path := range []string{file(1), "/api/public/" + sh.ID + "/zip"} {
		if w := fetch(a, "GET", path, ""); w.Code != 410 {
			t.Fatal("new download after the last one", path, w.Code)
		}
	}
	if w := fetch(a, "GET", file(1), "bytes=3-"); w.Code != 206 {
		t.Fatal("the last download could not be resumed", w.Code)
	}
	later := time.Now().Add(closeGrace + time.Second)
	if owner := ownerShare(t, a, admin, sh.ID); !shareExpired(owner.Expires, later.Unix()) {
		t.Fatal("the link does not close after the grace period", owner.Expires)
	}
	if e := a.Cleanup(later); e != nil {
		t.Fatal(e)
	}
	if w := fetch(a, "GET", "/api/public/"+sh.ID, ""); w.Code != 410 && w.Code != 404 {
		t.Fatal("link still open after closing", w.Code)
	}
	if _, e := os.Stat(a.blob(sh.Files[0].ID)); !os.IsNotExist(e) {
		t.Fatal("files kept after the link closed", e)
	}
	var rows int
	a.db.QueryRow("SELECT COUNT(*) FROM file_downloads").Scan(&rows)
	if rows != 0 {
		t.Fatal("statistics left behind", rows)
	}
}

func TestZipSkipsUsedUpFilesAndCountsEach(t *testing.T) {
	a, admin := emailAdmin(t)
	sh := limitedShare(t, a, admin, 1)
	if w := fetch(a, "GET", "/api/public/"+sh.ID+"/files/"+sh.Files[0].ID, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "/api/public/"+sh.ID+"/zip", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "a.txt") || !strings.Contains(w.Body.String(), "b.txt") {
		t.Fatal("ZIP must hold only the files with downloads left", w.Code)
	}
	if w := fetch(a, "GET", "/api/public/"+sh.ID+"/zip", ""); w.Code != 410 {
		t.Fatal(w.Code)
	}
}

func TestClosingLinkNotifiesAtOnce(t *testing.T) {
	a, admin := emailAdmin(t)
	configureEmail(t, a, admin)
	call(a, "PUT", "/api/account/email", `{"email":"mati@example.com"}`, admin)
	sh := limitedShare(t, a, admin, 1)
	if w := call(a, "PUT", "/api/shares/"+sh.ID+"/notify", `{"enabled":true}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	fetch(a, "GET", "/api/public/"+sh.ID+"/files/"+sh.Files[0].ID, "")
	a.flushNotifications(time.Now())
	if mails := queued(a); len(mails) != 1 || strings.Contains(mails[0].text, "limit") {
		t.Fatal("first notification", mails)
	}
	// Within the bundling interval, but the last download sends at once.
	fetch(a, "GET", "/api/public/"+sh.ID+"/zip", "")
	if mails := queued(a); len(mails) != 1 || !strings.Contains(mails[0].text, "download limit has been reached") {
		t.Fatal("closing notification", mails)
	}
}

func TestLimitedLibraryLinksCannotHaveCollaborators(t *testing.T) {
	a, admin := emailAdmin(t)
	body := fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword)
	call(a, "POST", "/api/admin/users", body, admin)
	w := call(a, "POST", "/api/library/uploads", `{"files":[{"name":"c.txt","size":5}]}`, admin)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var upload Share
	json.Unmarshal(w.Body.Bytes(), &upload)
	chunk(a, admin, upload.Files[0].ID, 0, "hello")
	call(a, "POST", "/api/shares/"+upload.ID+"/publish", "{}", admin)
	file := upload.Files[0].ID
	if w = call(a, "POST", "/api/library/shares", `{"files":["`+file+`"],"download_limit":3,"edit":true,"recipients":["bob"]}`, admin); w.Code != 400 {
		t.Fatal("limit combined with collaboration", w.Code, w.Body)
	}
	w = call(a, "POST", "/api/library/shares", `{"files":["`+file+`"],"download_limit":3}`, admin)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var link Share
	json.Unmarshal(w.Body.Bytes(), &link)
	if link.DownloadLimit != 3 {
		t.Fatal(link.DownloadLimit)
	}
	if w = call(a, "PATCH", "/api/library/shares/"+link.ID, `{"edit":true,"recipients":["bob"]}`, admin); w.Code < 400 {
		t.Fatal("collaboration added to a limited link", w.Code)
	}
	fetch(a, "GET", "/api/public/"+link.ID+"/files/"+file, "")
	if w = call(a, "PATCH", "/api/library/shares/"+link.ID, `{"download_limit":1}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = fetch(a, "GET", "/api/public/"+link.ID, ""); w.Code != 410 {
		t.Fatal("lowering the limit below the downloads must close the link", w.Code)
	}
	a.Cleanup(time.Now().Add(time.Second))
	if _, e := os.Stat(a.blob(file)); e != nil {
		t.Fatal("closing a library link must keep the library file", e)
	}
}

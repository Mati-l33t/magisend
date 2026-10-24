package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publicRequest(a *App, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-MagiSend", "1")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestPasswordGrantExpiryZipAndDelete(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	sh := createShare(t, a, s, "share-password", 5)
	chunk(a, s, sh.Files[0].ID, 0, "hello")
	call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", s)
	base := "/api/public/" + sh.ID
	if w := call(a, "GET", base, "", ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "GET", base+"/files/"+sh.Files[0].ID, "", ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", base+"/unlock", `{"password":"wrong"}`, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	w := call(a, "POST", base+"/unlock", `{"password":"share-password"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	w = publicRequest(a, "GET", base, "", cookies)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = publicRequest(a, "GET", base+"/zip", "", cookies)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	z, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	if len(z.File) != 1 || z.File[0].Name != "test.txt" {
		t.Fatal(z.File)
	}
	f, e := z.File[0].Open()
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "hello" {
		t.Fatal(string(data))
	}
	if w := call(a, "DELETE", "/api/shares/"+sh.ID, "{}", s); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, e := os.Stat(a.blob(sh.Files[0].ID)); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	w = call(a, "GET", "/api/me", "", s)
	var me struct {
		User User `json:"user"`
	}
	json.Unmarshal(w.Body.Bytes(), &me)
	if me.User.Used != 0 {
		t.Fatal(me)
	}
	if w := publicRequest(a, "GET", base, "", cookies); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	sh = createShare(t, a, s, "", 1)
	chunk(a, s, sh.Files[0].ID, 0, "x")
	call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", s)
	a.db.Exec("UPDATE shares SET expires=? WHERE id=?", time.Now().Unix()-1, sh.ID)
	if w := call(a, "GET", "/api/public/"+sh.ID, "", ""); w.Code != 410 {
		t.Fatal(w.Code, w.Body)
	}
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := a.loadShare(sh.ID); e == nil {
		t.Fatal("expired share not deleted")
	}
}
func TestStorageFailureDoesNotReleaseQuotaOrPublish(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	sh := createShare(t, a, s, "", 1)
	if e := os.Rename(filepath.Join(a.root, "blobs"), filepath.Join(a.root, "offline")); e != nil {
		t.Fatal(e)
	}
	if w := chunk(a, s, sh.Files[0].ID, 0, "x"); w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", s); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w := call(a, "GET", "/api/me", "", s)
	if !strings.Contains(w.Body.String(), `"used":1`) {
		t.Fatal(w.Body)
	}
	if _, e := os.Stat(filepath.Join(a.root, "blobs")); !os.IsNotExist(e) {
		t.Fatal("silent storage fallback")
	}
}
func TestDraftCleanupAfter24Hours(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	sh := createShare(t, a, s, "", 1)
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := a.loadShare(sh.ID); e != nil {
		t.Fatal(e)
	}
	a.db.Exec("UPDATE shares SET created=? WHERE id=?", time.Now().Add(-25*time.Hour).Unix(), sh.ID)
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := a.loadShare(sh.ID); e == nil {
		t.Fatal("stale draft survived")
	}
}

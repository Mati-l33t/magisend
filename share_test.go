package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func createShare(t *testing.T, a *App, s, extra string, size int64) Share {
	t.Helper()
	body := fmt.Sprintf(`{"title":"Testdelning","days":7,"password":"%s","files":[{"name":"test.txt","size":%d}]}`, extra, size)
	w := call(a, "POST", "/api/shares", body, s)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var sh Share
	if e := json.Unmarshal(w.Body.Bytes(), &sh); e != nil {
		t.Fatal(e)
	}
	return sh
}
func chunk(a *App, s, id string, offset int64, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PATCH", "/api/uploads/"+id, strings.NewReader(body))
	r.Header.Set("X-MagiSend", "1")
	r.Header.Set("Upload-Offset", fmt.Sprint(offset))
	r.AddCookie(&http.Cookie{Name: "magisend_session", Value: s})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestResumableShareCannotPublishIncomplete(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	sh := createShare(t, a, s, "", 11)
	if len(sh.Files) != 1 {
		t.Fatal(sh)
	}
	id := sh.Files[0].ID
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", s); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	if w := chunk(a, s, id, 0, "hello "); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := chunk(a, s, id, 0, "world"); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	w := call(a, "GET", "/api/uploads/"+id, "", s)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"offset":6`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := chunk(a, s, id, 6, "world"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", s); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = call(a, "GET", "/api/public/"+sh.ID+"/files/"+id, "", "")
	if w.Code != 200 || w.Body.String() != "hello world" {
		t.Fatal(w.Code, w.Body)
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Fatal("must be attachment")
	}
	if w := chunk(a, s, id, 11, "x"); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}

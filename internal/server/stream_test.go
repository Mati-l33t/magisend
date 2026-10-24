package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type deadlineProbe struct {
	headers              http.Header
	deadlineCalls, bytes int
}

func (p *deadlineProbe) Header() http.Header                { return p.headers }
func (p *deadlineProbe) WriteHeader(code int)               {}
func (p *deadlineProbe) Write(b []byte) (int, error)        { p.bytes += len(b); return len(b), nil }
func (p *deadlineProbe) SetWriteDeadline(t time.Time) error { p.deadlineCalls++; return nil }
func TestDownloadRefreshesIdleDeadlineWhileStreaming(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	s := login(t, a, "mati")
	body := strings.Repeat("x", 128<<10)
	sh := createShare(t, a, s, "", int64(len(body)))
	if w := chunk(a, s, sh.Files[0].ID, 0, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", s); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	p := &deadlineProbe{headers: make(http.Header)}
	r := httptest.NewRequest("GET", "/api/public/"+sh.ID+"/files/"+sh.Files[0].ID, nil)
	a.Handler().ServeHTTP(p, r)
	if p.bytes != len(body) {
		t.Fatal(p.bytes)
	}
	if p.deadlineCalls < 2 {
		t.Fatal("download has a fixed total-duration deadline rather than idle write deadline", p.deadlineCalls)
	}
}

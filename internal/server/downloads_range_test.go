package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestCoversStartFollowsServeContent(t *testing.T) {
	for span, want := range map[string]bool{
		"":                    true,
		"bytes=0-":            true,
		"bytes=0-0":           true,
		"bytes=-5":            true,
		"bytes=-99999999999":  true,
		"bytes=1-,1-":         true, // adds up to more than the file: served whole
		"bytes=4-4, 0-0":      true,
		"bytes=,":             true, // no ranges: served whole
		"bytes=1-":            false,
		"bytes=-3":            false,
		"bytes=1-2,3-4":       false,
		"bytes=10-":           false, // refused
		"bytes=abc":           false,
		"items=0-":            false,
		"bytes=3-1":           false,
		"bytes=--1":           false,
		"bytes= 0 - ":         true,
		"bytes=0-99999999999": true,
	} {
		if got := coversStart(span, 5); got != want {
			t.Errorf("%q: got %v, want %v", span, got, want)
		}
	}
}

func fetchFrom(a *App, client, path, span string, extra ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = client + ":40000"
	if span != "" {
		r.Header.Set("Range", span)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		r.Header.Set(extra[i], extra[i+1])
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

// No Range trick fetches a file past its download limit.
func TestRangesCannotDodgeTheDownloadLimit(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")

	// A suffix range for "the last 99999999999 bytes" is the whole file.
	sh := limitedShare(t, a, owner, 1)
	path := "/api/public/" + sh.ID + "/files/" + sh.Files[0].ID
	if w := fetchFrom(a, "198.51.100.1", path, "bytes=-99999999999"); w.Code/100 != 2 || w.Body.String() != "hello" {
		t.Fatal(w.Code, w.Body)
	}
	if w := fetchFrom(a, "198.51.100.2", path, ""); w.Code != 410 {
		t.Fatal("the suffix range was not counted", w.Code)
	}

	// If-Range cannot turn a counted-as-resume range into the whole file.
	sh = limitedShare(t, a, owner, 1)
	path = "/api/public/" + sh.ID + "/files/" + sh.Files[0].ID
	w := fetchFrom(a, "198.51.100.3", path, "bytes=1-", "If-Range", `"something else"`)
	if w.Code != 206 || w.Body.String() != "ello" {
		t.Fatal("If-Range changed what was served", w.Code, w.Body)
	}
	if w := fetchFrom(a, "198.51.100.4", path, ""); w.Code != 410 {
		t.Fatal("a range from a client never counted was free", w.Code)
	}

	// The client that was counted may resume; anyone else is counted.
	sh = limitedShare(t, a, owner, 2)
	path = "/api/public/" + sh.ID + "/files/" + sh.Files[0].ID
	fetchFrom(a, "198.51.100.5", path, "")
	if w := fetchFrom(a, "198.51.100.5", path, "bytes=2-"); w.Code != 206 {
		t.Fatal(w.Code)
	}
	if got := ownerShare(t, a, owner, sh.ID).Files[0].Downloads; got != 1 {
		t.Fatal("a resume was counted", got)
	}
	fetchFrom(a, "198.51.100.6", path, "bytes=2-")
	if w := fetchFrom(a, "198.51.100.5", path, ""); w.Code != 410 {
		t.Fatal("another client's range was not counted", w.Code)
	}
}

// A download that cannot be delivered uses nothing up.
func TestUndeliveredDownloadsAreNotCounted(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	sh := limitedShare(t, a, owner, 1)
	file := sh.Files[0].ID
	path := "/api/public/" + sh.ID + "/files/" + file
	blob := a.blob(file)
	if e := os.Rename(blob, blob+".away"); e != nil {
		t.Fatal(e)
	}
	if w := fetchFrom(a, "198.51.100.7", path, ""); w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
	if got := ownerShare(t, a, owner, sh.ID).Files[0].Downloads; got != 0 {
		t.Fatal("an undelivered download was counted", got)
	}
	os.Rename(blob+".away", blob)
	if w := fetchFrom(a, "198.51.100.7", path, ""); w.Code != 200 || w.Body.String() != "hello" {
		t.Fatal("the one allowed download was used up", w.Code, w.Body)
	}
}

// One client cannot take every download slot.
func TestDownloadSlotsPerClient(t *testing.T) {
	a := testApp(t)
	request := func(client string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = client + ":1"
		return r
	}
	var releases []func()
	for i := 0; i < downloadsPerClient; i++ {
		release, ok := a.acquireDownload(httptest.NewRecorder(), request("198.51.100.8"))
		if !ok {
			t.Fatal("slot", i)
		}
		releases = append(releases, release)
	}
	w := httptest.NewRecorder()
	if _, ok := a.acquireDownload(w, request("198.51.100.8")); ok || w.Code != 429 {
		t.Fatal("one client took more than its share", w.Code)
	}
	release, ok := a.acquireDownload(httptest.NewRecorder(), request("198.51.100.9"))
	if !ok {
		t.Fatal("another client was refused")
	}
	release()
	for _, release := range releases {
		release()
	}
	if len(a.downloads) != 0 || len(a.downloadClients.count) != 0 {
		t.Fatal("slots leaked", len(a.downloads), a.downloadClients.count)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines int
}

func (d *deadlineRecorder) SetWriteDeadline(time.Time) error {
	d.deadlines++
	return nil
}

// A crawling reader does not get its time renewed with every few bytes.
func TestSlowReadersMustMakeProgress(t *testing.T) {
	d := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &idleResponseWriter{ResponseWriter: d}
	for i := 0; i < 100; i++ {
		w.Write(make([]byte, 100))
	}
	if d.deadlines != 1 {
		t.Fatal("10 KB renewed the deadline", d.deadlines)
	}
	w.Write(make([]byte, minProgress))
	w.Write([]byte("x"))
	if d.deadlines != 2 {
		t.Fatal("progress did not renew the deadline", d.deadlines)
	}
}

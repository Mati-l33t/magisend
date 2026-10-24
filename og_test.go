package main

import (
	"image"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const shell = `<!doctype html><html><head><meta name="description" content="Private file sharing" /><meta property="og:title" content="MagiSend" /><meta property="og:image" content="/og.png" /><title>MagiSend</title></head><body></body></html>`

func cardFor(a *App, path, language string) string {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Accept-Language", language)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w.Body.String()
}

func TestShareLinksGetChatCards(t *testing.T) {
	a := testApp(t)
	a.ui = fstest.MapFS{"index.html": {Data: []byte(shell)}}
	setupAdmin(t, a)
	token := login(t, a, "mati")
	if e := exec(a, "UPDATE settings SET site_url='https://files.example' WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	open := createShare(t, a, token, "", 4)
	if w := chunk(a, token, open.Files[0].ID, 0, "data"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+open.ID+"/publish", `{}`, 200)
	if e := a.storeThumbnail(open.Files[0].ID, image.NewRGBA(image.Rect(0, 0, 40, 30))); e != nil {
		t.Fatal(e)
	}
	page := cardFor(a, "/s/"+open.ID, "sv-SE,sv;q=0.9")
	for _, want := range []string{
		`<title>Testdelning</title>`,
		`<meta property="og:title" content="Testdelning" />`,
		`<meta property="og:description" content="1 fil · `,
		`<meta property="og:image" content="https://files.example/api/public/` + open.ID + `/files/` + open.Files[0].ID + `/thumbnail" />`,
		`<meta property="og:url" content="https://files.example/s/` + open.ID + `" />`,
		`<meta name="twitter:card" content="summary" />`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s in %s", want, page)
		}
	}
	if strings.Count(page, "og:title") != 1 || strings.Count(page, "<title>") != 1 {
		t.Fatal("tags doubled", page)
	}
	if !strings.Contains(cardFor(a, "/s/"+open.ID, "en-US"), "1 file · ") {
		t.Fatal("English card")
	}

	// A protected link names neither its title nor its files.
	locked := createShare(t, a, token, "secret-words", 4)
	if w := chunk(a, token, locked.Files[0].ID, 0, "data"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+locked.ID+"/publish", `{}`, 200)
	page = cardFor(a, "/s/"+locked.ID, "en")
	if strings.Contains(page, "Testdelning") || strings.Contains(page, "/thumbnail") || !strings.Contains(page, "Password-protected files") {
		t.Fatal("protected card leaks", page)
	}

	// Expired: says so. Unknown or unpublished: the plain shell.
	if e := exec(a, "UPDATE shares SET expires=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), open.ID); e != nil {
		t.Fatal(e)
	}
	if page = cardFor(a, "/s/"+open.ID, "en"); strings.Contains(page, "Testdelning") || !strings.Contains(page, "This link has expired") {
		t.Fatal("expired card", page)
	}
	if page = cardFor(a, "/s/nothing-here", "en"); page != shell {
		t.Fatal("unknown link changed the shell", page)
	}
	draft := createShare(t, a, token, "", 4)
	if page = cardFor(a, "/s/"+draft.ID, "en"); page != shell {
		t.Fatal("draft card", page)
	}
}

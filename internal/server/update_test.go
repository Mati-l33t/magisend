package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"v1.0.1", "v1.0.0", true},
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.1.0", "dev", false},
		{"v1.1.0-rc1", "v1.0.0", false},
		{"", "v1.0.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.candidate, c.current); got != c.want {
			t.Fatal(c, got)
		}
	}
}

func fakeReleases(t *testing.T, tag string) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/magisend/releases/latest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/example/magisend/releases/tag/%s","published_at":"2026-10-01T00:00:00Z"}`, tag, tag)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestUpdateCheckAndRequest(t *testing.T) {
	previous := version
	version = "v1.0.0"
	t.Cleanup(func() { version = previous })
	a := testApp(t)
	a.updates.Repo, a.updates.API = "example/magisend", fakeReleases(t, "v1.2.0").URL
	setupAdmin(t, a)
	admin := login(t, a, "mati")

	w := call(a, "POST", "/api/admin/update/check", "{}", admin)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	info := decodeObject(t, w)
	if info["latest"] != "v1.2.0" || info["available"] != true || info["changelog_url"] != "https://github.com/example/magisend/blob/v1.2.0/CHANGELOG.md" {
		t.Fatal(info)
	}
	if w = call(a, "POST", "/api/admin/update", "{}", admin); w.Code != 409 {
		t.Fatal("manual installs cannot self-update", w.Code)
	}
	a.updates.Mode, a.updates.Updater = "native", true
	if w = call(a, "POST", "/api/admin/update", "{}", admin); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	if _, e := os.Stat(filepath.Join(a.root, updateTrigger)); e != nil {
		t.Fatal("update was not requested", e)
	}
	if decodeObject(t, call(a, "GET", "/api/admin/update", "", admin))["pending"] != true {
		t.Fatal("pending update not reported")
	}

	body := fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword)
	if w = call(a, "POST", "/api/admin/users", body, admin); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	bob := login(t, a, "bob")
	for _, path := range []string{"/api/admin/update", "/api/admin/update/check"} {
		if w = call(a, "POST", path, "{}", bob); w.Code != 403 {
			t.Fatal("non-administrator reached", path, w.Code)
		}
	}
}

func TestUpdateIgnoresOlderAndMalformedReleases(t *testing.T) {
	previous := version
	version = "v1.0.0"
	t.Cleanup(func() { version = previous })
	a := testApp(t)
	a.updates.Repo, a.updates.Mode, a.updates.Updater = "example/magisend", "native", true
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	for tag, status := range map[string]int{"v0.9.0": 200, "latest; rm -rf /": 502} {
		a.updates.API = fakeReleases(t, tag).URL
		if w := call(a, "POST", "/api/admin/update/check", "{}", admin); w.Code != status {
			t.Fatal(tag, w.Code, w.Body)
		}
		if w := call(a, "POST", "/api/admin/update", "{}", admin); w.Code != 409 {
			t.Fatal("offered update for", tag, w.Code)
		}
	}
}

func TestNewInstallSizesStorageBudgetToDisk(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	budget, e := a.storageBudget()
	if e != nil || budget != suggestedBudget(root) || budget < GiB || budget%GiB != 0 {
		t.Fatal(budget, e)
	}
	if _, e = a.db.Exec("UPDATE settings SET storage_budget=?", 3*GiB); e != nil {
		t.Fatal(e)
	}
	a.db.Close()
	if a, e = NewApp(root, ""); e != nil {
		t.Fatal(e)
	}
	defer a.db.Close()
	if budget, _ = a.storageBudget(); budget != 3*GiB {
		t.Fatal("restart replaced the administrator's budget", budget)
	}
}

func TestFrontendServesShellAssetsAndRoutes(t *testing.T) {
	a := testApp(t)
	a.ui = fstest.MapFS{
		"index.html":           {Data: []byte("shell")},
		"sw.js":                {Data: []byte("worker")},
		"assets/app-1.js":      {Data: []byte("code")},
		"manifest.webmanifest": {Data: []byte("{}")},
	}
	cases := []struct {
		path, body, cache string
		status            int
	}{
		{"/", "shell", "no-cache", 200},
		{"/s/some-link", "shell", "no-cache", 200},
		{"/assets/app-1.js", "code", "public, max-age=31536000, immutable", 200},
		{"/sw.js", "worker", "no-cache", 200},
		{"/missing.js", "", "", 404},
	}
	for _, c := range cases {
		w := call(a, "GET", c.path, "", "")
		if w.Code != c.status || c.body != "" && w.Body.String() != c.body || w.Header().Get("Cache-Control") != c.cache && c.status == 200 {
			t.Fatal(c.path, w.Code, w.Body, w.Header().Get("Cache-Control"))
		}
	}
}

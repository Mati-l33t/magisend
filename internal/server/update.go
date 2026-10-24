package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// version is the build version, passed in by the command (which sets it with
// -ldflags "-X main.version=v1.2.3").
var version = "dev"

const (
	updateInterval     = 6 * time.Hour
	updateTrigger      = ".update-requested"
	updateStatusFile   = "update-status.json"
	defaultUpdateRepo  = "Mati-l33t/magisend"
	defaultUpdateAPI   = "https://api.github.com"
	maxReleaseResponse = 1 << 20
)

var releaseTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// Updates holds what the release check found. The server, not the browser,
// asks GitHub, so the interface keeps its strict Content-Security-Policy and
// visitors' browsers never contact a third party.
type Updates struct {
	Repo, API, Mode string
	Enabled         bool
	Updater         bool
	client          *http.Client
	mu              sync.Mutex
	latest          string
	releaseURL      string
	published       string
	checked         time.Time
	failed          bool
}

func newUpdates() *Updates {
	return &Updates{
		Repo:    env("MAGISEND_UPDATE_REPO", defaultUpdateRepo),
		API:     strings.TrimRight(env("MAGISEND_UPDATE_API", defaultUpdateAPI), "/"),
		Mode:    env("MAGISEND_INSTALL", "manual"),
		Enabled: env("MAGISEND_UPDATE_CHECK", "true") != "false",
		Updater: env("MAGISEND_UPDATER", "") == "systemd",
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// newerVersion reports whether candidate is a higher vMAJOR.MINOR.PATCH than
// current. Development builds and unknown formats never offer an update.
func newerVersion(candidate, current string) bool {
	c, k := releaseTag.FindStringSubmatch(candidate), releaseTag.FindStringSubmatch(current)
	if c == nil || k == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		a, _ := strconv.Atoi(c[i])
		b, _ := strconv.Atoi(k[i])
		if a != b {
			return a > b
		}
	}
	return false
}

func (u *Updates) check(ctx context.Context) error {
	request, e := http.NewRequestWithContext(ctx, "GET", u.API+"/repos/"+u.Repo+"/releases/latest", nil)
	if e != nil {
		return e
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "MagiSend/"+version)
	response, e := u.client.Do(request)
	if e == nil && response.StatusCode != 200 {
		response.Body.Close()
		e = errors.New("release check: " + response.Status)
	}
	var release struct {
		Tag       string `json:"tag_name"`
		URL       string `json:"html_url"`
		Published string `json:"published_at"`
		Draft     bool   `json:"draft"`
		Pre       bool   `json:"prerelease"`
	}
	if e == nil {
		e = json.NewDecoder(io.LimitReader(response.Body, maxReleaseResponse)).Decode(&release)
		response.Body.Close()
	}
	if e == nil && (release.Draft || release.Pre || !releaseTag.MatchString(release.Tag) || !strings.HasPrefix(release.URL, "https://")) {
		e = errors.New("release check: unexpected release")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checked = time.Now()
	u.failed = e != nil
	if e == nil {
		u.latest, u.releaseURL, u.published = release.Tag, release.URL, release.Published
	}
	return e
}

// Run checks shortly after start and then periodically until ctx ends.
func (u *Updates) Run(ctx context.Context) {
	if !u.Enabled {
		return
	}
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if e := u.check(ctx); e != nil {
			log.Print("update check failed")
		}
		wait = updateInterval
	}
}

type updateStatus struct {
	State   string `json:"state"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Message string `json:"message,omitempty"`
	Time    string `json:"time,omitempty"`
}

func (a *App) updateStatus() *updateStatus {
	data, e := os.ReadFile(filepath.Join(a.root, updateStatusFile))
	if e != nil || len(data) > 8<<10 {
		return nil
	}
	var status updateStatus
	if json.Unmarshal(data, &status) != nil {
		return nil
	}
	return &status
}

func (a *App) updateInfo() map[string]any {
	u := a.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	_, pending := os.Stat(filepath.Join(a.root, updateTrigger))
	info := map[string]any{
		"current":   version,
		"latest":    u.latest,
		"available": newerVersion(u.latest, version),
		"enabled":   u.Enabled,
		"mode":      u.Mode,
		"can_apply": u.Mode == "native" && u.Updater,
		"pending":   pending == nil,
		"failed":    u.failed,
		"repo":      "https://github.com/" + u.Repo,
		"status":    a.updateStatus(),
	}
	if u.latest != "" {
		info["release_url"] = u.releaseURL
		info["changelog_url"] = "https://github.com/" + u.Repo + "/blob/" + u.latest + "/CHANGELOG.md"
		info["published"] = u.published
	}
	if !u.checked.IsZero() {
		info["checked"] = u.checked.Unix()
	}
	return info
}

func (a *App) updateRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/update", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		respond(w, 200, a.updateInfo())
	})
	m.HandleFunc("POST /api/admin/update/check", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		if !a.updates.Enabled {
			fail(w, 409, "update_check_disabled")
			return
		}
		a.mu.Lock()
		limited := a.limited("update-check:" + admin.ID)
		a.mu.Unlock()
		if limited {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		if e := a.updates.check(r.Context()); e != nil {
			fail(w, 502, "update_check_failed")
			return
		}
		respond(w, 200, a.updateInfo())
	})
	// The request only asks the root-owned updater to install the latest
	// official release. It carries no version, URL or command: the updater
	// looks the release up itself and verifies it before installing.
	m.HandleFunc("POST /api/admin/update", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		info := a.updateInfo()
		if info["can_apply"] != true {
			fail(w, 409, "update_not_supported")
			return
		}
		if info["available"] != true {
			fail(w, 409, "update_not_available")
			return
		}
		if internal(w, os.WriteFile(filepath.Join(a.root, updateTrigger), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)) {
			return
		}
		a.audit(r, admin.Username, "update.started", fmt.Sprint(info["latest"]))
		respond(w, 202, a.updateInfo())
	})
}

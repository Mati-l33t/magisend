package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Download limits and statistics. A share may allow each of its files to be
// downloaded a fixed number of times (0 = unlimited); a ZIP counts once for
// every file in it. When every file has reached the limit the share expires
// at once and the regular cleanup deletes it. Owner downloads are neither
// counted nor limited.
const maxDownloadLimit = 1000

func migrateDownloads(db *sql.DB) error {
	for _, c := range [][2]string{{"download_limit", "INTEGER NOT NULL DEFAULT 0"}, {"last_download", "INTEGER NOT NULL DEFAULT 0"}} {
		if e := addColumn(db, "shares", c[0], c[1]); e != nil {
			return e
		}
	}
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS file_downloads(share_id TEXT NOT NULL, file_id TEXT NOT NULL, count INTEGER NOT NULL DEFAULT 0, last INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(share_id, file_id))`)
	return e
}

func validDownloadLimit(limit int64) bool { return limit >= 0 && limit <= maxDownloadLimit }

// loadDownloads fills in the per-file statistics of a loaded share.
func (a *App) loadDownloads(s *Share) error {
	rows, e := a.db.Query("SELECT file_id,count,last FROM file_downloads WHERE share_id=?", s.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	counts := map[string][2]int64{}
	for rows.Next() {
		var id string
		var count, last int64
		if e = rows.Scan(&id, &count, &last); e != nil {
			return e
		}
		counts[id] = [2]int64{count, last}
	}
	for i := range s.Files {
		c := counts[s.Files[i].ID]
		s.Files[i].Downloads, s.Files[i].LastDownload = c[0], c[1]
		if s.DownloadLimit > 0 {
			remaining := max(s.DownloadLimit-c[0], 0)
			s.Files[i].Remaining = &remaining
		}
	}
	return rows.Err()
}

// publicView hides the owner's statistics from visitors; the remaining
// downloads per file stay visible.
// Where a file is stored and how far its upload got stay with the owner too.
type publicFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	HasThumb  bool   `json:"has_thumb"`
	Folder    string `json:"folder,omitempty"`
	Remaining *int64 `json:"remaining,omitempty"`
}
type publicShareView struct {
	Share
	Files []publicFile `json:"files"`
}

func publicView(s Share) publicShareView {
	s.Downloads, s.LastDownload, s.Notify = 0, 0, false
	files := make([]publicFile, len(s.Files))
	for i, f := range s.Files {
		files[i] = publicFile{ID: f.ID, Name: f.Name, Size: f.Size, HasThumb: f.HasThumb, Folder: f.Folder, Remaining: f.Remaining}
	}
	return publicShareView{Share: s, Files: files}
}

// A HEAD request or a resumed range is not a new download.
func freshDownload(r *http.Request) bool {
	span := r.Header.Get("Range")
	return r.Method == http.MethodGet && (span == "" || strings.HasPrefix(span, "bytes=0-"))
}

func usedUp(f File) bool { return f.Remaining != nil && *f.Remaining == 0 }

// countedDownload wraps a public authorization: a file that has reached the
// limit is refused, also for resumed ranges, and a fresh download is counted
// under the same lock as the authorization.
func (a *App) countedDownload(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool)) func() (Share, bool) {
	return func() (Share, bool) {
		s, ok := authorize()
		if !ok {
			return s, false
		}
		for _, f := range s.Files {
			if f.ID != r.PathValue("file") || f.Offset != f.Size {
				continue
			}
			if usedUp(f) {
				fail(w, 410, "download_limit_reached")
				return s, false
			}
			if freshDownload(r) {
				a.recordDownloads(s, []File{f})
			}
		}
		return s, true
	}
}

// usedUpShare reports whether every file has reached the limit, counting the
// downloads in extra as already made.
func usedUpShare(s Share, extra map[string]bool) bool {
	if s.DownloadLimit == 0 || len(s.Files) == 0 {
		return false
	}
	for _, f := range s.Files {
		count := f.Downloads
		if extra[f.ID] {
			count++
		}
		if count < s.DownloadLimit {
			return false
		}
	}
	return true
}

// Caller holds mu, and s was loaded under it.
func (a *App) recordDownloads(s Share, files []File) {
	now := time.Now().Unix()
	_, _ = a.db.Exec("UPDATE shares SET downloads=downloads+1, last_download=?, notify_pending=notify_pending+notify, push_pending=push_pending+1 WHERE id=?", now, s.ID)
	counted := map[string]bool{}
	for _, f := range files {
		counted[f.ID] = true
		_, _ = a.db.Exec(`INSERT INTO file_downloads(share_id,file_id,count,last) VALUES(?,?,1,?)
 ON CONFLICT(share_id,file_id) DO UPDATE SET count=count+1, last=excluded.last`, s.ID, f.ID, now)
	}
	if !usedUpShare(s, counted) {
		return
	}
	// The last allowed download closes the link. Transfers in progress keep
	// their open files; the owner hears about it before the cleanup runs.
	_, _ = a.db.Exec("UPDATE shares SET expires=? WHERE id=?", now, s.ID)
	a.audit(nil, "", "share.closed", s.Title)
	a.sendNotifications(time.Unix(now, 0), s.ID)
	var owner, origin string
	if a.db.QueryRow("SELECT owner,notify_origin FROM shares WHERE id=?", s.ID).Scan(&owner, &origin) == nil {
		if site := a.linkOrigin(); site != "" {
			origin = site
		}
		a.notify(owner, pushClosed, func(t map[string]string) (string, string) {
			return fmt.Sprintf(t["closed"], s.Title), ""
		}, linkTo(origin, "/shares"))
	}
}

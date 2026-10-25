package server

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/textproto"
	"strconv"
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
	Scan      string `json:"scan,omitempty"`
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
		files[i] = publicFile{ID: f.ID, Name: f.Name, Size: f.Size, HasThumb: f.HasThumb, Scan: f.Scan, Folder: f.Folder, Remaining: f.Remaining}
	}
	return publicShareView{Share: s, Files: files}
}

// coversStart reports whether a GET for a file of size bytes delivers its
// first byte, by http.ServeContent's own rules: no Range, a range starting at
// 0, a suffix range reaching back to it, or ranges ServeContent answers with
// the whole file (they add up to more than the file). A Range it refuses
// delivers nothing.
func coversStart(span string, size int64) bool {
	if span == "" {
		return true
	}
	if !strings.HasPrefix(span, "bytes=") {
		return false
	}
	var starts []int64
	var total int64
	overlaps := false
	for _, part := range strings.Split(span[len("bytes="):], ",") {
		part = textproto.TrimString(part)
		if part == "" {
			continue
		}
		first, last, ok := strings.Cut(part, "-")
		if !ok {
			return false
		}
		first, last = textproto.TrimString(first), textproto.TrimString(last)
		var start, length int64
		if first == "" {
			if last == "" || last[0] == '-' {
				return false
			}
			n, e := strconv.ParseInt(last, 10, 64)
			if e != nil || n < 0 {
				return false
			}
			if n > size {
				n = size
			}
			start, length = size-n, n
		} else {
			n, e := strconv.ParseInt(first, 10, 64)
			if e != nil || n < 0 {
				return false
			}
			if n >= size {
				overlaps = true
				continue
			}
			start = n
			if last == "" {
				length = size - start
			} else {
				end, e := strconv.ParseInt(last, 10, 64)
				if e != nil || start > end {
					return false
				}
				if end >= size {
					end = size - 1
				}
				length = end - start + 1
			}
		}
		starts = append(starts, start)
		total += length
	}
	if overlaps && len(starts) == 0 {
		return false
	}
	if len(starts) == 0 || total > size {
		return true
	}
	for _, start := range starts {
		if start == 0 {
			return true
		}
	}
	return false
}

func usedUp(f File) bool { return f.Remaining != nil && *f.Remaining == 0 }

const (
	// How long a used-up link stays open for resuming the last download.
	closeGrace   = time.Hour
	resumeWindow = 24 * time.Hour
	maxResumes   = 10000
)

// countedDownload wraps a public authorization: a file that has reached the
// limit is refused, also for resumed ranges. A request that delivers the
// file's first byte is a download; so is a later range from a client that
// was not counted for this file in the last day, so ranges cannot fetch a
// file uncounted. The count is made by the returned commit, which the caller
// runs under mu once the file is open and verified, so a request that
// delivers nothing uses nothing up. Conditional headers are dropped, so the
// Range that was counted is the one that is served.
func (a *App) countedDownload(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool)) (func() (Share, bool), func()) {
	for _, h := range []string{"If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		r.Header.Del(h)
	}
	var share Share
	var counted []File
	var keys []string
	check := func() (Share, bool) {
		s, ok := authorize()
		if !ok {
			return s, false
		}
		for _, f := range s.Files {
			if f.ID != r.PathValue("file") || f.Offset != f.Size {
				continue
			}
			key := s.ID + "|" + f.ID + "|" + a.clientIP(r)
			if usedUp(f) {
				// The client whose download used the last one may still resume it.
				if r.Method == http.MethodGet && !coversStart(r.Header.Get("Range"), f.Size) && a.resumable(key) {
					continue
				}
				fail(w, 410, "download_limit_reached")
				return s, false
			}
			if r.Method != http.MethodGet {
				continue
			}
			if coversStart(r.Header.Get("Range"), f.Size) || !a.resumable(key) {
				share, counted, keys = s, append(counted, f), append(keys, key)
			}
		}
		return s, true
	}
	commit := func() {
		if len(counted) == 0 {
			return
		}
		a.recordDownloads(share, counted)
		for _, key := range keys {
			a.rememberResume(key)
		}
	}
	return check, commit
}

// resumable reports whether this client was counted for this file recently,
// so a range it asks for continues that download. Caller holds mu.
func (a *App) resumable(key string) bool {
	at, ok := a.resumes[key]
	return ok && time.Since(at) < resumeWindow
}

// rememberResume notes a counted download. Caller holds mu.
func (a *App) rememberResume(key string) {
	if len(a.resumes) >= maxResumes {
		for k, at := range a.resumes {
			if time.Since(at) >= resumeWindow {
				delete(a.resumes, k)
			}
		}
		if len(a.resumes) >= maxResumes {
			a.resumes = map[string]time.Time{}
		}
	}
	a.resumes[key] = time.Now()
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
	// The last allowed download closes the link after closeGrace: no new
	// download can start, but the client that was counted can resume an
	// interrupted one. Then the link expires and its files are removed; the
	// owner hears about it now.
	closes := now + int64(closeGrace/time.Second)
	_, _ = a.db.Exec("UPDATE shares SET expires=? WHERE id=? AND (expires=0 OR expires>?)", closes, s.ID, closes)
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

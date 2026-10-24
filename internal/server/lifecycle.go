package server

import (
	"archive/zip"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) removeShare(id string) error {
	if e := a.retainManagedFiles(id); e != nil {
		return e
	}
	// Commit the tombstone before any irreversible storage operation. Keep the
	// rows and reservations until every blob and the final DB delete succeed.
	if e := exec(a, "INSERT OR IGNORE INTO deleting(share_id) VALUES(?)", id); e != nil {
		return e
	}
	rows, e := a.db.Query("SELECT id,backend FROM files WHERE share_id=?", id)
	if e != nil {
		return e
	}
	files := []File{}
	for rows.Next() {
		var f File
		if e = rows.Scan(&f.ID, &f.Backend); e != nil {
			rows.Close()
			return e
		}
		files = append(files, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range files {
		if !a.knownLocation(f.Backend) {
			return fmt.Errorf("unknown storage backend")
		}
		// Each file's location must be present; missing objects within a
		// present location can be safely removed.
		if e = a.removeFileBlobs(f.ID); e != nil {
			return e
		}
	}
	return exec(a, "DELETE FROM shares WHERE id=?", id)
}
func (a *App) Cleanup(now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	rows, e := a.db.Query("SELECT id FROM shares WHERE (expires<>0 AND expires<=?) OR (published=0 AND created<=?) OR EXISTS (SELECT 1 FROM deleting d WHERE d.share_id=shares.id)", now.Unix(), now.Add(-24*time.Hour).Unix())
	ids := []string{}
	var failures []error
	if e != nil {
		failures = append(failures, e)
	} else {
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				failures = append(failures, e)
				break
			}
			ids = append(ids, id)
		}
		if e = rows.Err(); e != nil {
			failures = append(failures, e)
		}
		rows.Close()
	}
	for _, id := range ids {
		if e = a.removeShare(id); e != nil && !errors.Is(e, errStorageOffline) {
			failures = append(failures, fmt.Errorf("delete share %s: %w", id, e))
		}
	}
	if e = exec(a, "DELETE FROM sessions WHERE expires<=?", now.Unix()); e != nil {
		failures = append(failures, e)
	}
	if e = exec(a, "DELETE FROM file_downloads WHERE NOT EXISTS(SELECT 1 FROM shares s WHERE s.id=file_downloads.share_id)"); e != nil {
		failures = append(failures, e)
	}
	if e = a.pruneAudit(now); e != nil {
		failures = append(failures, e)
	}
	if e = exec(a, "DELETE FROM request_grants WHERE expires<=?", now.Unix()); e != nil {
		failures = append(failures, e)
	}
	if e = exec(a, "DELETE FROM grants WHERE expires<=?", now.Unix()); e != nil {
		failures = append(failures, e)
	}
	if e = a.cleanupLibraryFiles(now); e != nil {
		failures = append(failures, e)
	}
	if e = a.collectOrphanBlobs(); e != nil {
		failures = append(failures, e)
	}
	return errors.Join(failures...)
}
func (a *App) lifecycleRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/public/{id}/unlock", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		clientKey := "unlock:" + a.clientIP(r)
		a.mu.Lock()
		blocked := a.blocked(clientKey, unlockFailuresPerClient)
		a.mu.Unlock()
		if blocked {
			fail(w, 429, "För många försök. Vänta 15 minuter")
			return
		}
		// Unlocking works for both the id link and the custom link name.
		ref := r.PathValue("id")
		s, _, password, e := a.loadShareRef(ref)
		if e != nil || !s.Published {
			fail(w, 404, "Delningen finns inte")
			return
		}
		if shareExpired(s.Expires, time.Now().Unix()) {
			fail(w, 410, "Delningen har gått ut")
			return
		}
		shareKey := "unlock-share:" + s.ID
		a.mu.Lock()
		blocked = a.blocked(shareKey, unlockFailuresPerShare)
		a.mu.Unlock()
		if blocked {
			fail(w, 429, "För många försök. Vänta 15 minuter")
			return
		}
		if password != "" && bcrypt.CompareHashAndPassword([]byte(password), []byte(b.Password)) != nil {
			a.mu.Lock()
			a.failed(clientKey)
			a.failed(shareKey)
			a.mu.Unlock()
			fail(w, 401, "Fel lösenord")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		current, _, currentPassword, e := a.loadShare(s.ID)
		if e != nil || !current.Published || currentPassword != password {
			fail(w, 404, "Delningen finns inte")
			return
		}
		if shareExpired(current.Expires, time.Now().Unix()) {
			fail(w, 410, "Delningen har gått ut")
			return
		}
		token := randomID()
		if internal(w, exec(a, "INSERT INTO grants(hash,share_id,expires) VALUES(?,?,?)", hash(token), s.ID, time.Now().Add(time.Hour).Unix())) {
			return
		}
		// The grant must be scoped to the exact link the visitor used, otherwise a
		// custom link and an id link would not share the unlocked state.
		http.SetCookie(w, a.cookie(r, "magisend_grant_"+s.ID, token, 3600, "/api/public/"+ref))
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("DELETE /api/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.ownShare(w, r)
		if !ok {
			return
		}
		if internal(w, deletionRecorded(a.removeShare(s.ID))) {
			return
		}
		if u, e := a.currentUser(r); e == nil {
			a.audit(r, u.Username, "share.deleted", s.Title)
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/public/{id}/zip", a.zipShare)
}
func (a *App) zipShare(w http.ResponseWriter, r *http.Request) {
	// Four shared download slots bound concurrent ZIPs. Every file is checked
	// before the response starts, then opened only when its turn comes, so a
	// large folder never holds a descriptor per file.
	if !acquire(w, a.downloads) {
		return
	}
	defer func() { <-a.downloads }()
	a.mu.Lock()
	s, ok := a.publicShare(w, r)
	if !ok {
		a.mu.Unlock()
		return
	}
	// A ZIP holds the files that can still be downloaded and counts once for each.
	available := []File{}
	for _, f := range s.Files {
		if !usedUp(f) {
			available = append(available, f)
		}
	}
	if len(available) == 0 {
		a.mu.Unlock()
		fail(w, 410, "download_limit_reached")
		return
	}
	full := s
	s.Files = available
	if len(s.Files) > zipFileLimit {
		a.mu.Unlock()
		fail(w, 503, "För många filer i delningen")
		return
	}
	for _, f := range s.Files {
		if f.Offset != f.Size || !a.knownLocation(f.Backend) || f.Size < 0 {
			a.mu.Unlock()
			fail(w, 503, "storage_unverified")
			return
		}
		file, e := a.openCurrent(f.ID)
		if e != nil {
			a.mu.Unlock()
			internal(w, e)
			return
		}
		info, e := file.Stat()
		file.Close()
		if e != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
			a.mu.Unlock()
			fail(w, 503, "Lagringen kan inte verifieras")
			return
		}
	}
	if r.Method == http.MethodGet {
		a.recordDownloads(full, s.Files)
	}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="MagiSend.zip"`)
	z := zip.NewWriter(w)
	// Archive entry names must be unique even when several members share the
	// same filename (two uploads, or a rename collision), so extraction can
	// never overwrite one member with another. Names stay deterministic, and a
	// readable ".ext" suffix is preserved (case-insensitive comparison).
	used := map[string]bool{}
	uniqueName := func(name string) string {
		// The extension is looked for in the file's own name, not its folders.
		base := strings.LastIndexByte(name, '/') + 1
		dot := strings.LastIndexByte(name[base:], '.')
		if dot <= 0 {
			dot = len(name) // No extension, or a dotfile: never split the name.
		} else {
			dot += base
		}
		stem, ext := name[:dot], name[dot:]
		lower := strings.ToLower(stem) + strings.ToLower(ext)
		if !used[lower] {
			used[lower] = true
			return name
		}
		for n := 2; ; n++ {
			candidate := stem + " (" + strconv.Itoa(n) + ")" + ext
			lowerCandidate := strings.ToLower(candidate)
			if !used[lowerCandidate] {
				used[lowerCandidate] = true
				return candidate
			}
		}
	}
	for _, f := range s.Files {
		// A file deleted while the ZIP streams is left out; one already
		// open is read to the end even if it is deleted meanwhile.
		a.mu.Lock()
		file, e := a.openCurrent(f.ID)
		a.mu.Unlock()
		if e != nil {
			continue
		}
		name := f.Name
		if f.Folder != "" {
			name = f.Folder + "/" + f.Name
		}
		entry, e := z.CreateHeader(&zip.FileHeader{Name: uniqueName(name), Method: zip.Store})
		if e == nil {
			_, e = io.Copy(entry, file)
		}
		file.Close()
		if e != nil {
			return
		}
	}
	z.Close()
}

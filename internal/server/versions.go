package server

import (
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Any library file can get a new version: the owner uploads the new bytes
// as an ordinary library file and then swaps them in. The file keeps its id,
// so every link, folder, star and statistic stays as it was, and the earlier
// versions stay in its history (the text_versions table, which text edits
// already use) until the file is deleted. Versions count towards the owner's
// storage like any other bytes.

type FileVersion struct {
	Version int    `json:"version"`
	Size    int64  `json:"size"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	// Who made it: empty for the original upload.
	By      string `json:"by"`
	Current bool   `json:"current"`
}

// Caller holds mu. A file that never changed gets its original recorded as
// version 1, pointing at the bytes it already has.
func (a *App) versionHistory(f LibraryFile) (int, error) {
	var created int64
	if e := a.db.QueryRow("SELECT created FROM shares WHERE id=?", f.ShareID).Scan(&created); e != nil {
		return 0, e
	}
	if e := exec(a, "INSERT OR IGNORE INTO text_versions(file_id,number,blob,size,name,created,actor) VALUES(?,1,?,?,?,?,?)", f.ID, f.ID, f.Size, f.Name, created, "original"); e != nil {
		return 0, e
	}
	var current int
	e := a.db.QueryRow("SELECT MAX(number) FROM text_versions WHERE file_id=?", f.ID).Scan(&current)
	return current, e
}

// Caller holds mu. Only complete files of published uploads have versions.
func (a *App) versionedFile(w http.ResponseWriter, r *http.Request, write bool) (User, LibraryFile, bool) {
	u, ok := a.require(w, r, false)
	if !ok {
		return u, LibraryFile{}, false
	}
	f, e := a.libraryFile(u.ID, r.PathValue("file"))
	if e != nil {
		fail(w, 404, "file_not_found")
		return u, f, false
	}
	var published bool
	if internal(w, a.db.QueryRow("SELECT published FROM shares WHERE id=?", f.ShareID).Scan(&published)) {
		return u, f, false
	}
	if !published || f.Offset != f.Size {
		fail(w, 409, "upload_incomplete")
		return u, f, false
	}
	if write && !a.allowed(w, u.ID, "edit_own") {
		return u, f, false
	}
	return u, f, true
}

func (a *App) versionRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/files/{file}/replace", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Source string `json:"source"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, f, ok := a.versionedFile(w, r, true)
		if !ok {
			return
		}
		source, e := a.libraryFile(u.ID, b.Source)
		var published bool
		if e == nil {
			e = a.db.QueryRow("SELECT published FROM shares WHERE id=?", source.ShareID).Scan(&published)
		}
		if e != nil || source.ID == f.ID || !published || source.Offset != source.Size {
			fail(w, 404, "file_not_found")
			return
		}
		// Every version of a file lives in the same storage location.
		if source.Backend != f.Backend {
			respond(w, 409, map[string]string{"error": "The new version was stored in another storage location", "error_code": "replace_location"})
			return
		}
		current, e := a.versionHistory(f)
		if internal(w, e) {
			return
		}
		if count, e := a.versionCount(f.ID); internal(w, e) {
			return
		} else if count >= textMaxVersions {
			fail(w, 429, "metadata_limit")
			return
		}
		// Links that include the file must have room for its new size; the
		// bytes themselves are already counted, as the uploaded file.
		var folder string
		if internal(w, a.db.QueryRow("SELECT COALESCE((SELECT folder_id FROM library_files WHERE file_id=?),'')", f.ID).Scan(&folder)) {
			return
		}
		if !a.libraryGrowthAllowed(w, u.ID, f.ID, source.Size, folder) {
			return
		}
		// A link that removes photo location data must not serve a new version
		// with it. Only done once the replacement is allowed, since it changes
		// the uploaded file for good.
		if shouldScrub(source.Name) || shouldScrub(f.Name) {
			if e := a.scrubIfStripped(f.ShareID, source.ID, source.Backend); errors.Is(e, errLocationNotRemoved) {
				fail(w, 422, "location_not_removed")
				return
			} else if internal(w, e) {
				return
			}
		}
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		for _, step := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO text_versions(file_id,number,blob,size,name,created,actor) VALUES(?,?,?,?,?,?,?)", []any{f.ID, current + 1, source.ID, source.Size, source.Name, time.Now().Unix(), u.ID}},
			{"UPDATE files SET size=?,offset=? WHERE id=?", []any{source.Size, source.Size, f.ID}},
			{"DELETE FROM thumbnails WHERE file_id=?", []any{f.ID}},
			{"UPDATE thumbnails SET file_id=? WHERE file_id=?", []any{f.ID, source.ID}},
			// The uploaded file's row goes; its bytes now belong to f's history.
			{"DELETE FROM files WHERE id=?", []any{source.ID}},
		} {
			if _, e = tx.Exec(step.query, step.args...); internal(w, e) {
				return
			}
		}
		if internal(w, tx.Commit()) {
			return
		}
		a.thumbBlocked.Delete(f.ID)
		if internal(w, a.gcEmptyContainers()) {
			return
		}
		a.queueThumbnails(f.ShareID)
		respond(w, 200, map[string]any{"version": current + 1, "size": source.Size})
	})
	m.HandleFunc("GET /api/library/files/{file}/history", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		_, f, ok := a.versionedFile(w, r, false)
		if !ok {
			return
		}
		current, e := a.versionHistory(f)
		if internal(w, e) {
			return
		}
		rows, e := a.db.Query("SELECT v.number,v.size,v.name,v.created,v.actor,COALESCE(u.username,'') FROM text_versions v LEFT JOIN users u ON u.id=v.actor WHERE v.file_id=? ORDER BY v.number DESC", f.ID)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		list := []FileVersion{}
		for rows.Next() {
			var v FileVersion
			var actor string
			if internal(w, rows.Scan(&v.Version, &v.Size, &v.Name, &v.Created, &actor, &v.By)) {
				return
			}
			v.Current = v.Version == current
			list = append(list, v)
		}
		if !internal(w, rows.Err()) {
			respond(w, 200, list)
		}
	})
	m.HandleFunc("GET /api/library/files/{file}/history/{version}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		_, f, ok := a.versionedFile(w, r, false)
		var file *os.File
		var name string
		if ok {
			file, name, ok = a.openVersion(w, f, r.PathValue("version"))
		}
		a.mu.Unlock()
		if !ok {
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if internal(w, e) {
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		http.ServeContent(w, r, name, info.ModTime(), file)
	})
	// Restoring copies an earlier version into a new one, so the history
	// keeps everything that happened.
	m.HandleFunc("POST /api/library/files/{file}/history/{version}/restore", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, f, ok := a.versionedFile(w, r, true)
		if !ok {
			return
		}
		current, e := a.versionHistory(f)
		if internal(w, e) {
			return
		}
		if r.PathValue("version") == strconv.Itoa(current) {
			fail(w, 409, "Ogiltiga indata")
			return
		}
		source, name, ok := a.openVersion(w, f, r.PathValue("version"))
		if !ok {
			return
		}
		defer source.Close()
		info, e := source.Stat()
		if internal(w, e) {
			return
		}
		store, ok := a.versionRoom(w, f.File, info.Size(), current)
		if !ok {
			return
		}
		blob := randomID()
		path := filepath.Join(store, blob)
		if internal(w, copyDurably(path, source)) {
			return
		}
		committed := false
		defer func() {
			if !committed {
				_ = os.Remove(path)
			}
		}()
		if shouldScrub(f.Name) {
			if e := a.scrubIfStripped(f.ShareID, blob, f.Backend); errors.Is(e, errLocationNotRemoved) {
				fail(w, 422, "location_not_removed")
				return
			} else if internal(w, e) {
				return
			}
		}
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("INSERT INTO text_versions(file_id,number,blob,size,name,created,actor) VALUES(?,?,?,?,?,?,?)", f.ID, current+1, blob, info.Size(), name, time.Now().Unix(), u.ID); internal(w, e) {
			return
		}
		if _, e = tx.Exec("UPDATE files SET size=?,offset=? WHERE id=?", info.Size(), info.Size(), f.ID); internal(w, e) {
			return
		}
		if _, e = tx.Exec("DELETE FROM thumbnails WHERE file_id=?", f.ID); internal(w, e) {
			return
		}
		if internal(w, tx.Commit()) {
			return
		}
		committed = true
		a.thumbBlocked.Delete(f.ID)
		a.queueThumbnails(f.ShareID)
		respond(w, 200, map[string]any{"version": current + 1, "size": info.Size()})
	})
}

// Caller holds mu. Opens one recorded version read-only, never following a
// symlink or blocking on a FIFO put in its place.
func (a *App) openVersion(w http.ResponseWriter, f LibraryFile, version string) (*os.File, string, bool) {
	number, e := strconv.Atoi(version)
	var blob, name string
	if e == nil {
		e = a.db.QueryRow("SELECT blob,name FROM text_versions WHERE file_id=? AND number=?", f.ID, number).Scan(&blob, &name)
	}
	if e == sql.ErrNoRows || (e == nil && !validToken(blob)) || number < 1 {
		fail(w, 404, "file_not_found")
		return nil, "", false
	}
	if internal(w, e) {
		return nil, "", false
	}
	dir, e := a.blobDir(f.Backend)
	if e != nil {
		fail(w, 503, "storage_unverified")
		return nil, "", false
	}
	file, e := os.OpenFile(filepath.Join(dir, blob), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		fail(w, 404, "file_not_found")
		return nil, "", false
	}
	return file, name, true
}

// copyDurably writes source to a new file at path and syncs it and its
// folder, so a committed version never points at missing bytes.
func copyDurably(path string, source io.Reader) error {
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(file, source)
	if e == nil {
		e = file.Sync()
	}
	if closeErr := file.Close(); e == nil {
		e = closeErr
	}
	if e == nil {
		var dir *os.File
		if dir, e = os.Open(filepath.Dir(path)); e == nil {
			e = dir.Sync()
			if closeErr := dir.Close(); e == nil {
				e = closeErr
			}
		}
	}
	if e != nil {
		_ = os.Remove(path)
	}
	return e
}

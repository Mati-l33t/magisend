package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func migrateLibraryLifecycle(db *sql.DB) error {
	_, e := db.Exec("CREATE TABLE IF NOT EXISTS file_deleting(file_id TEXT PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE)")
	return e
}
func (a *App) folderRoom(w http.ResponseWriter, folder, exclude string, adding int) bool {
	if folder == "" {
		return true
	}
	var n int
	if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM library_files WHERE folder_id=? AND file_id<>?", folder, exclude).Scan(&n)) {
		return false
	}
	if n+adding > folderFileLimit {
		respond(w, 409, map[string]string{"error": "A folder supports at most 1,000 files, including pending uploads", "error_code": "folder_full"})
		return false
	}
	return true
}
func (a *App) removeFileBlobs(id string) error {
	loc, e := a.fileLocation(id)
	if e == sql.ErrNoRows {
		loc = localLocation
	} else if e != nil {
		return e
	}
	// An offline location keeps the deletion pending until it is back.
	dir, e := a.blobDir(loc)
	if e != nil {
		return e
	}
	info, e := os.Stat(dir)
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return fmt.Errorf("storage is not a directory")
	}
	rows, e := a.db.Query("SELECT blob FROM text_versions WHERE file_id=? UNION SELECT id FROM files WHERE id=?", id, id)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var blob string
		if e = rows.Scan(&blob); e != nil {
			rows.Close()
			return e
		}
		if !validToken(blob) {
			rows.Close()
			return fmt.Errorf("invalid blob key")
		}
		ids = append(ids, blob)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, blob := range ids {
		if e = a.discardStored(filepath.Join(dir, blob)); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}

// Empty private containers must not park metadata reservations forever: while
// any files remain, only file deletion may free their bytes, but a container
// whose last file is gone can no longer be reached through any listing. Live
// and selection links are never touched.
func (a *App) gcEmptyContainers() error {
	// Files themselves are deleted one-by-one by removeLibraryFile only after
	// their blob removal succeeded; a tombstone for a still-pending deletion
	// (blob removal failed) keeps the file row, metadata and quota reservation
	// here, and cleanupLibraryFiles retries it on the next sweep. This sweep
	// only releases now-empty containers and clears completed tombstones, so a
	// successful unrelated delete can never destroy pending or healthy files.
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`DELETE FROM shares WHERE id IN (SELECT c.share_id FROM library_containers c WHERE NOT EXISTS(SELECT 1 FROM files f WHERE f.share_id=c.share_id))`); e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM file_deleting WHERE file_id NOT IN(SELECT id FROM files)`); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) removeLibraryFile(id string) error {
	// A file taken from the trash early is due at once.
	if e := exec(a, "INSERT INTO file_deleting(file_id) VALUES(?) ON CONFLICT(file_id) DO UPDATE SET purge_after=0", id); e != nil {
		return e
	}
	if e := a.removeFileBlobs(id); e != nil {
		return e
	}
	if e := exec(a, "DELETE FROM files WHERE id=?", id); e != nil {
		return e
	}
	return a.gcEmptyContainers()
}
func (a *App) cleanupLibraryFiles(now time.Time) error {
	// Files in the trash wait for their day.
	rows, e := a.db.Query("SELECT file_id FROM file_deleting WHERE purge_after<=?", now.Unix())
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		// Files on an offline location are deleted once it is back.
		if e = a.removeLibraryFile(id); e != nil && !errors.Is(e, errStorageOffline) {
			return e
		}
	}
	return nil
}

// collectOrphanBlobs handles stored objects no file refers to, in every
// reachable location. An object belongs to the location its file names, so
// the copy a move left behind elsewhere is deleted. Any other unknown object
// is set aside, with what the location's index knew about it, instead of
// deleted: it may belong to files a restored older database does not know.
// Versions whose file row is gone count as referenced everywhere.
func (a *App) collectOrphanBlobs() error {
	// Every stored object the database knows, by location, with the size it
	// should have.
	rows, e := a.db.Query(`SELECT f.id,f.backend,COALESCE((SELECT v.size FROM text_versions v WHERE v.blob=f.id),f.offset) FROM files f
 UNION ALL SELECT v.blob,COALESCE(f.backend,''),v.size FROM text_versions v LEFT JOIN files f ON f.id=v.file_id WHERE f.id IS NULL OR v.blob<>f.id`)
	if e != nil {
		return e
	}
	referenced := map[string]map[string]int64{}
	for rows.Next() {
		var id, loc string
		var size int64
		if e = rows.Scan(&id, &loc, &size); e != nil {
			rows.Close()
			return e
		}
		if referenced[loc] == nil {
			referenced[loc] = map[string]int64{}
		}
		referenced[loc][id] = size
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	a.stores.mu.RLock()
	locations := []string{localLocation}
	for id := range a.stores.locations {
		locations = append(locations, id)
	}
	moving := a.stores.move.Running
	a.stores.mu.RUnlock()
	for _, loc := range locations {
		dir, e := a.blobDir(loc)
		if errors.Is(e, errStorageOffline) {
			continue
		}
		if e != nil {
			return e
		}
		entries, e := os.ReadDir(dir)
		if e != nil {
			return e
		}
		var index map[string]indexEntry
		for _, entry := range entries {
			name := entry.Name()
			// Token uploads and moves left behind by a crash are removed after
			// two hours.
			if strings.HasPrefix(name, partialUploadTo) || strings.HasPrefix(name, movingPrefix) && !moving {
				if info, e := entry.Info(); e == nil && time.Since(info.ModTime()) > 2*time.Hour {
					_ = os.Remove(filepath.Join(dir, name))
				}
				continue
			}
			_, here := referenced[loc][name]
			_, anywhere := referenced[""][name]
			if validToken(name) && !here && !anywhere && !entry.IsDir() {
				if a.copyElsewhere(referenced, loc, name) {
					if e = a.discardStored(filepath.Join(dir, name)); e != nil && !os.IsNotExist(e) {
						return e
					}
					continue
				}
				if index == nil {
					index = readStorageIndex(dir)
					if index == nil {
						index = map[string]indexEntry{}
					}
				}
				var known *indexEntry
				if entry, ok := index[name]; ok {
					known = &entry
				}
				if e = setAside(dir, name, known, time.Now()); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
		}
	}
	return nil
}

// copyElsewhere reports whether the file that owns name is verifiably stored
// in another location: that location is reachable and holds the object at
// its recorded size. Only then is this copy a leftover that may be deleted;
// otherwise it may be the only one (the other location offline, or an older
// database restored) and is set aside instead.
func (a *App) copyElsewhere(referenced map[string]map[string]int64, loc, name string) bool {
	for other, names := range referenced {
		size, ok := names[name]
		if other == loc || other == "" || !ok {
			continue
		}
		dir, e := a.blobDir(other)
		if e != nil {
			continue
		}
		if info, e := os.Lstat(filepath.Join(dir, name)); e == nil && info.Mode().IsRegular() && info.Size() == size {
			return true
		}
	}
	return false
}
func (a *App) verifyHistoryStorage() error {
	rows, e := a.db.Query(`SELECT v.blob,v.size,f.backend FROM text_versions v JOIN files f ON f.id=v.file_id WHERE NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id) AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=f.share_id)`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var blob, loc string
		var size int64
		if e = rows.Scan(&blob, &size, &loc); e != nil {
			return e
		}
		if !validToken(blob) {
			return fmt.Errorf("invalid history blob")
		}
		// Files on an offline location are checked when they are read.
		dir, e := a.blobDir(loc)
		if errors.Is(e, errStorageOffline) {
			continue
		}
		if e != nil {
			return e
		}
		info, e := os.Lstat(filepath.Join(dir, blob))
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Size() != size {
			return fmt.Errorf("unverified history blob")
		}
	}
	return rows.Err()
}
func (a *App) libraryDeleteRoutes(m *http.ServeMux) {
	m.HandleFunc("DELETE /api/library/files/{file}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		f, e := a.libraryFile(u.ID, r.PathValue("file"))
		if e != nil {
			fail(w, 404, "file_not_found")
			return
		}
		if !a.allowed(w, u.ID, "edit_own") {
			return
		}
		if !internal(w, a.trashLibraryFile(f.ID, u.ID)) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
}

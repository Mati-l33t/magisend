package server

import (
	"database/sql"
	"net/http"
	"time"
)

// Deleted library files wait in the owner's trash for trashDays before they
// are removed for good. A file in the trash carries the same tombstone as one
// whose removal is pending (file_deleting), so it is hidden from the library,
// every link and every download; purge_after says when the cleanup may take
// it. Restoring drops the tombstone, and the file is back where it was, in
// its folder and in the links that shared it. Its bytes still count until
// it is gone.
const trashDays = 30

func migrateTrash(db *sql.DB) error {
	for _, c := range [][2]string{
		{"purge_after", "INTEGER NOT NULL DEFAULT 0"},
		{"deleted", "INTEGER NOT NULL DEFAULT 0"},
		{"deleted_by", "TEXT NOT NULL DEFAULT ''"},
	} {
		if e := addColumn(db, "file_deleting", c[0], c[1]); e != nil {
			return e
		}
	}
	return nil
}

type TrashedFile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	FolderID   string `json:"folder_id"`
	Deleted    int64  `json:"deleted"`
	PurgeAfter int64  `json:"purge_after"`
	// Set when someone the file was shared with deleted it.
	DeletedBy string `json:"deleted_by,omitempty"`
}

// Caller holds mu. An unfinished upload has nothing worth keeping and is
// removed at once.
func (a *App) trashLibraryFile(id, actor string) error {
	var size, offset int64
	var owner string
	if e := a.db.QueryRow("SELECT f.size,f.offset,s.owner FROM files f JOIN shares s ON s.id=f.share_id WHERE f.id=?", id).Scan(&size, &offset, &owner); e != nil {
		return e
	}
	if offset != size {
		return a.removeLibraryFile(id)
	}
	by := ""
	if actor != owner {
		by = a.username(actor)
	}
	now := time.Now()
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// A file sent on a link that does not keep its files would otherwise go
	// with the link; once in the trash it belongs to the library.
	if _, e = tx.Exec("INSERT OR IGNORE INTO library_files(file_id) VALUES(?)", id); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO file_deleting(file_id,purge_after,deleted,deleted_by) VALUES(?,?,?,?)", id, now.Add(trashDays*24*time.Hour).Unix(), now.Unix(), by); e != nil {
		return e
	}
	return tx.Commit()
}

// Caller holds mu. Only files in the owner's trash, not ones already being
// removed.
func (a *App) trashedFile(owner, id string) bool {
	var n int
	e := a.db.QueryRow(`SELECT COUNT(*) FROM file_deleting d JOIN files f ON f.id=d.file_id JOIN shares s ON s.id=f.share_id WHERE d.file_id=? AND d.purge_after>? AND s.owner=? AND NOT EXISTS(SELECT 1 FROM deleting x WHERE x.share_id=s.id)`, id, time.Now().Unix(), owner).Scan(&n)
	return e == nil && n == 1
}

func (a *App) trashRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/trash", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query(`SELECT f.id,f.name,f.size,COALESCE(l.folder_id,''),d.deleted,d.purge_after,d.deleted_by FROM file_deleting d JOIN files f ON f.id=d.file_id JOIN shares s ON s.id=f.share_id LEFT JOIN library_files l ON l.file_id=f.id WHERE s.owner=? AND d.purge_after>? AND NOT EXISTS(SELECT 1 FROM deleting x WHERE x.share_id=s.id) ORDER BY d.deleted DESC,f.rowid DESC`, u.ID, time.Now().Unix())
		if internal(w, e) {
			return
		}
		defer rows.Close()
		list := []TrashedFile{}
		for rows.Next() {
			var f TrashedFile
			if internal(w, rows.Scan(&f.ID, &f.Name, &f.Size, &f.FolderID, &f.Deleted, &f.PurgeAfter, &f.DeletedBy)) {
				return
			}
			list = append(list, f)
		}
		if internal(w, rows.Err()) {
			return
		}
		respond(w, 200, list)
	})
	m.HandleFunc("POST /api/trash/{file}/restore", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		id := r.PathValue("file")
		if !a.trashedFile(u.ID, id) {
			fail(w, 404, "file_not_found")
			return
		}
		if !a.allowed(w, u.ID, "edit_own") {
			return
		}
		if !internal(w, exec(a, "DELETE FROM file_deleting WHERE file_id=?", id)) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
	m.HandleFunc("DELETE /api/trash/{file}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		id := r.PathValue("file")
		if !a.trashedFile(u.ID, id) {
			fail(w, 404, "file_not_found")
			return
		}
		if !a.allowed(w, u.ID, "edit_own") {
			return
		}
		if !internal(w, deletionRecorded(a.removeLibraryFile(id))) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
	m.HandleFunc("DELETE /api/trash", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		if !a.allowed(w, u.ID, "edit_own") {
			return
		}
		ids, e := a.ids(`SELECT d.file_id FROM file_deleting d JOIN files f ON f.id=d.file_id JOIN shares s ON s.id=f.share_id WHERE s.owner=? AND d.purge_after>?`, u.ID, time.Now().Unix())
		if internal(w, e) {
			return
		}
		for _, id := range ids {
			if internal(w, deletionRecorded(a.removeLibraryFile(id))) {
				return
			}
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
}

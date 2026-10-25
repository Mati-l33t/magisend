package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Folders nest: ParentID is empty for a folder at the top of the library.
type Folder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

const (
	ownerFolderLimit = 1000
	maxFolderDepth   = 20
)

// folderTree lists the folder given as its parameter and every folder below
// it, each with its path relative to that folder ("" for the folder itself).
const folderTree = `WITH RECURSIVE tree(id,path,depth) AS (SELECT ?,'',0 UNION ALL SELECT f.id,CASE WHEN t.path='' THEN f.name ELSE t.path||'/'||f.name END,t.depth+1 FROM folders f JOIN tree t ON f.parent=t.id WHERE t.depth<64) `

// folderLine lists the folder given as its parameter and every folder above it.
const folderLine = `WITH RECURSIVE line(id,depth) AS (SELECT ?,0 UNION ALL SELECT f.parent,l.depth+1 FROM folders f JOIN line l ON f.id=l.id WHERE f.parent<>'' AND l.depth<64) `

type LibraryFile struct {
	File
	FolderID string `json:"folder_id"`
	ShareID  string `json:"share_id"`
	Starred  bool   `json:"starred"`
	// Set for files received through an upload request.
	Received     bool   `json:"received,omitempty"`
	ReceivedFrom string `json:"received_from,omitempty"`
	ReceivedVia  string `json:"received_via,omitempty"`
	// A note whose title follows its first line.
	AutoTitle bool `json:"auto_title,omitempty"`
}

func migrateFolders(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS folders(id TEXT PRIMARY KEY,owner TEXT NOT NULL REFERENCES users(id),parent TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,UNIQUE(owner,parent,name));
 CREATE TABLE IF NOT EXISTS library_files(file_id TEXT PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,folder_id TEXT REFERENCES folders(id));
 CREATE TABLE IF NOT EXISTS library_containers(share_id TEXT PRIMARY KEY REFERENCES shares(id) ON DELETE CASCADE);`)
	if e != nil {
		return e
	}
	return nestFolders(db)
}

// nestFolders rebuilds a flat folders table from before nesting: names were
// unique per owner and become unique per parent folder. The rows keep their
// ids, so files, links and requests keep pointing at them.
func nestFolders(db *sql.DB) error {
	var nested int
	if e := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('folders') WHERE name='parent'").Scan(&nested); e != nil || nested == 1 {
		return e
	}
	ctx := context.Background()
	c, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
		return e
	}
	defer c.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	tx, e := c.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE folders_nested(id TEXT PRIMARY KEY,owner TEXT NOT NULL REFERENCES users(id),parent TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,UNIQUE(owner,parent,name))`,
		`INSERT INTO folders_nested(id,owner,parent,name) SELECT id,owner,'',name FROM folders`,
		`DROP TABLE folders`,
		`ALTER TABLE folders_nested RENAME TO folders`,
	} {
		if _, e = tx.Exec(q); e != nil {
			return e
		}
	}
	var broken int
	if e = tx.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&broken); e != nil {
		return e
	}
	if broken != 0 {
		return fmt.Errorf("folder migration left %d broken references", broken)
	}
	return tx.Commit()
}
func validFolder(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) == s && validFilename(s) && len(s) <= 100
}
func (a *App) ownsFolder(user, id string) bool {
	var n int
	return a.db.QueryRow("SELECT COUNT(*) FROM folders WHERE id=? AND owner=?", id, user).Scan(&n) == nil && n == 1
}

// folderDepth is 0 for the top of the library, 1 for a folder there, and so on.
func (a *App) folderDepth(id string) (int, error) {
	if id == "" {
		return 0, nil
	}
	var n int
	e := a.db.QueryRow(folderLine+"SELECT COUNT(*) FROM line", id).Scan(&n)
	return n, e
}

// validParent reports whether parent is the top ("") or one of user's folders.
func (a *App) validParent(user, parent string) bool {
	return parent == "" || a.ownsFolder(user, parent)
}
func (a *App) libraryFile(user, id string) (LibraryFile, error) {
	var deleting int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM file_deleting WHERE file_id=?", id).Scan(&deleting); e != nil {
		return LibraryFile{}, e
	}
	if deleting != 0 {
		return LibraryFile{}, sql.ErrNoRows
	}
	var f LibraryFile
	e := a.db.QueryRow(`SELECT f.id,f.name,f.size,f.offset,f.backend,COALESCE(l.folder_id,''),s.id FROM files f JOIN shares s ON s.id=f.share_id LEFT JOIN library_files l ON l.file_id=f.id WHERE f.id=? AND s.owner=? AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=s.id) AND NOT EXISTS(SELECT 1 FROM file_deleting fd WHERE fd.file_id=f.id)`, id, user).Scan(&f.ID, &f.Name, &f.Size, &f.Offset, &f.Backend, &f.FolderID, &f.ShareID)
	return f, e
}
func (a *App) folderRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/uploads", a.createShare)
	m.HandleFunc("GET /api/folders", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query("SELECT id,name,parent FROM folders WHERE owner=? ORDER BY name", u.ID)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		folders := []Folder{}
		for rows.Next() {
			var f Folder
			if internal(w, rows.Scan(&f.ID, &f.Name, &f.ParentID)) {
				return
			}
			folders = append(folders, f)
		}
		if !internal(w, rows.Err()) {
			respond(w, 200, folders)
		}
	})
	m.HandleFunc("POST /api/folders", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok || !a.allowed(w, u.ID, "folders") {
			return
		}
		var b struct {
			Name     string `json:"name"`
			ParentID string `json:"parent_id"`
		}
		if !decode(w, r, &b) {
			return
		}
		if !validFolder(b.Name) {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		if !a.validParent(u.ID, b.ParentID) {
			fail(w, 404, "not_found")
			return
		}
		depth, e := a.folderDepth(b.ParentID)
		if internal(w, e) {
			return
		}
		if depth >= maxFolderDepth {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		var n int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM folders WHERE owner=?", u.ID).Scan(&n)) {
			return
		}
		if n >= ownerFolderLimit {
			fail(w, 429, "metadata_limit")
			return
		}
		f := Folder{ID: randomID(), Name: b.Name, ParentID: b.ParentID}
		if _, e := a.db.Exec("INSERT INTO folders(id,owner,parent,name) VALUES(?,?,?,?)", f.ID, u.ID, f.ParentID, f.Name); e != nil {
			fail(w, 409, "Ogiltiga indata")
			return
		}
		respond(w, 201, f)
	})
	m.HandleFunc("PATCH /api/folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !a.ownsFolder(u.ID, id) {
			fail(w, 404, "not_found")
			return
		}
		if !a.allowed(w, u.ID, "folders") {
			return
		}
		var b struct {
			Name     *string `json:"name"`
			ParentID *string `json:"parent_id"`
		}
		if !decode(w, r, &b) {
			return
		}
		f := Folder{ID: id}
		if internal(w, a.db.QueryRow("SELECT name,parent FROM folders WHERE id=?", id).Scan(&f.Name, &f.ParentID)) {
			return
		}
		if b.Name == nil && b.ParentID == nil || b.Name != nil && !validFolder(*b.Name) {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		if b.Name != nil {
			f.Name = *b.Name
		}
		if b.ParentID != nil && *b.ParentID != f.ParentID {
			if !a.folderMoveAllowed(w, u.ID, id, f.ParentID, *b.ParentID) {
				return
			}
			f.ParentID = *b.ParentID
		}
		if _, e := a.db.Exec("UPDATE folders SET name=?,parent=? WHERE id=?", f.Name, f.ParentID, id); e != nil {
			fail(w, 409, "Ogiltiga indata")
			return
		}
		respond(w, 200, f)
	})
	m.HandleFunc("DELETE /api/folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !a.ownsFolder(u.ID, id) {
			fail(w, 404, "not_found")
			return
		}
		if !a.allowed(w, u.ID, "folders") {
			return
		}
		var n int
		if internal(w, a.db.QueryRow("SELECT (SELECT COUNT(*) FROM library_files l WHERE l.folder_id=? AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=l.file_id))+(SELECT COUNT(*) FROM folders WHERE parent=?)", id, id).Scan(&n)) {
			return
		}
		if n > 0 {
			fail(w, 409, "Ogiltiga indata")
			return
		}
		// Files in the trash from this folder come back unfiled.
		if internal(w, exec(a, "UPDATE library_files SET folder_id=NULL WHERE folder_id=?", id)) {
			return
		}
		if !internal(w, exec(a, "DELETE FROM folders WHERE id=?", id)) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
	m.HandleFunc("GET /api/library/files", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query(`SELECT f.id,f.name,f.size,f.offset,f.backend,COALESCE(l.folder_id,''),s.id,EXISTS(SELECT 1 FROM stars st WHERE st.user_id=s.owner AND st.file_id=f.id),ru.share_id IS NOT NULL,COALESCE(ru.sender,''),COALESCE(rq.title,''),EXISTS(SELECT 1 FROM thumbnails t WHERE t.file_id=f.id),COALESCE((SELECT auto FROM note_titles n WHERE n.file_id=f.id),0) FROM files f JOIN shares s ON s.id=f.share_id LEFT JOIN library_files l ON l.file_id=f.id LEFT JOIN request_uploads ru ON ru.share_id=s.id LEFT JOIN upload_requests rq ON rq.id=ru.request_id WHERE s.owner=? AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=s.id) AND NOT EXISTS(SELECT 1 FROM file_deleting fd WHERE fd.file_id=f.id) ORDER BY f.rowid DESC`, u.ID)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		files := []LibraryFile{}
		for rows.Next() {
			var f LibraryFile
			if internal(w, rows.Scan(&f.ID, &f.Name, &f.Size, &f.Offset, &f.Backend, &f.FolderID, &f.ShareID, &f.Starred, &f.Received, &f.ReceivedFrom, &f.ReceivedVia, &f.HasThumb, &f.AutoTitle)) {
				return
			}
			files = append(files, f)
		}
		if internal(w, rows.Err()) {
			return
		}
		held := a.heldScans()
		for i := range files {
			files[i].Scan = held[files[i].ID]
		}
		respond(w, 200, files)
	})
	m.HandleFunc("PATCH /api/library/files/{file}", func(w http.ResponseWriter, r *http.Request) {
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
		if !a.allowed(w, u.ID, "folders") {
			return
		}
		var published bool
		if internal(w, a.db.QueryRow("SELECT published FROM shares WHERE id=?", f.ShareID).Scan(&published)) {
			return
		}
		if !published || f.Offset != f.Size {
			fail(w, 409, "upload_incomplete")
			return
		}
		var b struct {
			Name     *string `json:"name"`
			FolderID *string `json:"folder_id"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.Name == nil && b.FolderID == nil {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		if b.Name != nil {
			if !utf8.ValidString(*b.Name) || !validFilename(*b.Name) {
				fail(w, 400, "Ogiltiga indata")
				return
			}
			f.Name = *b.Name
		}
		if b.FolderID != nil {
			if *b.FolderID != "" && !a.ownsFolder(u.ID, *b.FolderID) {
				fail(w, 404, "not_found")
				return
			}
			f.FolderID = *b.FolderID
		}
		if !a.folderRoom(w, f.FolderID, f.ID, 1) || !a.libraryGrowthAllowed(w, u.ID, f.ID, f.Size, f.FolderID) {
			return
		}
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("UPDATE files SET name=? WHERE id=?", f.Name, f.ID); internal(w, e) {
			return
		}
		// A note renamed by hand keeps that name instead of following its text.
		if b.Name != nil {
			if _, e = tx.Exec("UPDATE note_titles SET auto=0 WHERE file_id=?", f.ID); internal(w, e) {
				return
			}
		}
		var folder any
		if f.FolderID != "" {
			folder = f.FolderID
		}
		if _, e = tx.Exec("INSERT INTO library_files(file_id,folder_id) VALUES(?,?) ON CONFLICT(file_id) DO UPDATE SET folder_id=excluded.folder_id", f.ID, folder); internal(w, e) {
			return
		}
		if !internal(w, tx.Commit()) {
			respond(w, 200, f)
		}
	})
	m.HandleFunc("GET /api/library/files/{file}/download", func(w http.ResponseWriter, r *http.Request) {
		a.downloadWith(w, r, func() (Share, bool) {
			u, ok := a.require(w, r, false)
			if !ok {
				return Share{}, false
			}
			f, e := a.libraryFile(u.ID, r.PathValue("file"))
			if e != nil {
				fail(w, 404, "file_not_found")
				return Share{}, false
			}
			return Share{Files: []File{f.File}}, true
		})
	})
}

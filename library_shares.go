package main

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func migrateLibraryShares(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS library_shares(share_id TEXT PRIMARY KEY REFERENCES shares(id) ON DELETE CASCADE,mode TEXT NOT NULL CHECK(mode IN('live','selection')),folder_id TEXT REFERENCES folders(id) ON DELETE SET NULL);
 CREATE TABLE IF NOT EXISTS library_selection(share_id TEXT NOT NULL REFERENCES library_shares(share_id) ON DELETE CASCADE,file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,PRIMARY KEY(share_id,file_id));`)
	return e
}

// Caller holds mu. Membership, not content, is fixed in selection mode.
func (a *App) loadLibraryMembership(s *Share) error {
	var folder sql.NullString
	e := a.db.QueryRow("SELECT mode,folder_id FROM library_shares WHERE share_id=?", s.ID).Scan(&s.Mode, &folder)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	s.FolderID = folder.String
	s.Files = []File{}
	const usable = `source.owner=(SELECT owner FROM shares WHERE id=?) AND source.published=1 AND f.offset=f.size AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id) AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=source.id)`
	var rows *sql.Rows
	switch {
	case s.Mode == "live" && s.FolderID == "":
		// The shared folder was deleted.
		return nil
	case s.Mode == "live":
		// A shared folder includes its subfolders; each file carries its path
		// below the shared folder, files at the top first.
		rows, e = a.db.Query(folderTree+`SELECT f.id,f.name,f.size,f.offset,f.backend,EXISTS(SELECT 1 FROM thumbnails t WHERE t.file_id=f.id),tree.path FROM files f JOIN shares source ON source.id=f.share_id JOIN library_files l ON l.file_id=f.id JOIN tree ON tree.id=l.folder_id WHERE `+usable+` ORDER BY tree.path<>'',tree.path,f.rowid`, s.FolderID, s.ID)
	default:
		rows, e = a.db.Query(`SELECT f.id,f.name,f.size,f.offset,f.backend,EXISTS(SELECT 1 FROM thumbnails t WHERE t.file_id=f.id),'' FROM files f JOIN shares source ON source.id=f.share_id WHERE `+usable+` AND EXISTS(SELECT 1 FROM library_selection ls WHERE ls.share_id=? AND ls.file_id=f.id) ORDER BY f.rowid`, s.ID, s.ID)
	}
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		if e = rows.Scan(&f.ID, &f.Name, &f.Size, &f.Offset, &f.Backend, &f.HasThumb, &f.Folder); e != nil {
			return e
		}
		s.Files = append(s.Files, f)
	}
	return rows.Err()
}
func (a *App) libraryShareRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/shares", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok || !a.allowed(w, u.ID, "share") {
			return
		}
		var b struct {
			LinkPermissions
			Title     string   `json:"title"`
			FolderID  string   `json:"folder_id"`
			Files     []string `json:"files"`
			Days      int      `json:"days"`
			Permanent bool     `json:"permanent"`
			Password  string   `json:"password"`
			Limit     int64    `json:"download_limit"`
		}
		if !decode(w, r, &b) {
			return
		}
		policy, e := a.policy()
		if internal(w, e) {
			return
		}
		if b.Days == 0 {
			b.Days = policy.DefaultDays
		}
		// Collaborators work through the link, so it cannot also run out.
		if b.Days < 1 || b.Days > 365 || len(b.Password) > 72 || !validDownloadLimit(b.Limit) || b.Limit > 0 && (b.Edit || b.Add || b.Delete) {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		expires := time.Now().Add(time.Duration(b.Days) * 24 * time.Hour).Unix()
		if b.Permanent {
			expires = 0
		}
		password := ""
		if b.Password != "" {
			pw, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
			if internal(w, e) {
				return
			}
			password = string(pw)
		}
		if len(b.Title) > 160 || (b.FolderID == "") == (len(b.Files) == 0) || len(b.Files) > folderFileLimit {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		mode := "selection"
		if b.FolderID != "" {
			mode = "live"
			if !a.ownsFolder(u.ID, b.FolderID) {
				fail(w, 404, "not_found")
				return
			}
		}
		seen := map[string]bool{}
		for _, id := range b.Files {
			if seen[id] {
				fail(w, 400, "Ogiltiga indata")
				return
			}
			seen[id] = true
			f, e := a.libraryFile(u.ID, id)
			if e != nil {
				fail(w, 404, "file_not_found")
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
		}
		if !a.newLibraryShareAllowed(w, u.ID, b.FolderID, b.Files) {
			return
		}
		if (b.Edit || b.Add || b.Delete) && !a.allowed(w, u.ID, "grant_edit") {
			return
		}
		if len(b.Recipients) > 50 || b.Add && mode != "live" || (b.Edit || b.Add || b.Delete) && len(b.Recipients) == 0 {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		recipients := []string{}
		names := map[string]bool{}
		for _, name := range b.Recipients {
			if !validUsername(name) || names[name] {
				fail(w, 400, "Ogiltiga indata")
				return
			}
			names[name] = true
			var id string
			e := a.db.QueryRow("SELECT id FROM users WHERE username=?", name).Scan(&id)
			if e == sql.ErrNoRows {
				fail(w, 400, "Ogiltiga indata")
				return
			}
			if internal(w, e) {
				return
			}
			recipients = append(recipients, id)
		}
		if !a.metadataAvailable(w, u.ID, true, 0) {
			return
		}
		title := strings.TrimSpace(b.Title)
		if title == "" {
			title = "Files"
		}
		s := Share{ID: randomID(), Title: title, Expires: expires, Protected: password != "", Created: time.Now().Unix(), Published: true, Mode: mode, FolderID: b.FolderID, DownloadLimit: b.Limit, Files: []File{}}
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("INSERT INTO shares(id,owner,title,expires,created,password,published,download_limit) VALUES(?,?,?,?,?,?,1,?)", s.ID, u.ID, s.Title, s.Expires, s.Created, password, s.DownloadLimit); internal(w, e) {
			return
		}
		var folder any
		if b.FolderID != "" {
			folder = b.FolderID
		}
		if _, e = tx.Exec("INSERT INTO library_shares(share_id,mode,folder_id) VALUES(?,?,?)", s.ID, mode, folder); internal(w, e) {
			return
		}
		for _, id := range b.Files {
			if _, e = tx.Exec("INSERT INTO library_selection(share_id,file_id) VALUES(?,?)", s.ID, id); internal(w, e) {
				return
			}
			if _, e = tx.Exec("INSERT OR IGNORE INTO library_files(file_id) VALUES(?)", id); internal(w, e) {
				return
			}
		}
		if _, e = tx.Exec("INSERT INTO link_permissions(share_id,edit,add_files,delete_files) VALUES(?,?,?,?)", s.ID, b.Edit, b.Add, b.Delete); internal(w, e) {
			return
		}
		for _, id := range recipients {
			if _, e = tx.Exec("INSERT INTO link_recipients(share_id,user_id) VALUES(?,?)", s.ID, id); internal(w, e) {
				return
			}
		}
		if internal(w, tx.Commit()) {
			return
		}
		if !internal(w, a.loadLibraryMembership(&s)) {
			a.audit(r, u.Username, "link.created", s.Title)
			respond(w, 201, s)
		}
	})
	m.HandleFunc("PATCH /api/library/shares/{id}", a.updateLibraryShare)
}

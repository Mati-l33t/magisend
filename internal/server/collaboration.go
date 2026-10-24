package server

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type LinkPermissions struct {
	Recipients []string `json:"recipients"`
	Edit       bool     `json:"edit"`
	Add        bool     `json:"add"`
	Delete     bool     `json:"delete"`
}

func migrateCollaboration(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS link_permissions(share_id TEXT PRIMARY KEY REFERENCES library_shares(share_id) ON DELETE CASCADE,edit INTEGER NOT NULL DEFAULT 0,add_files INTEGER NOT NULL DEFAULT 0,delete_files INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS link_recipients(share_id TEXT NOT NULL REFERENCES library_shares(share_id) ON DELETE CASCADE,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,PRIMARY KEY(share_id,user_id));`)
	return e
}
func (a *App) linkPermissions(id string) (LinkPermissions, error) {
	p := LinkPermissions{Recipients: []string{}}
	e := a.db.QueryRow("SELECT edit,add_files,delete_files FROM link_permissions WHERE share_id=?", id).Scan(&p.Edit, &p.Add, &p.Delete)
	if e != nil && e != sql.ErrNoRows {
		return p, e
	}
	rows, e := a.db.Query("SELECT u.username FROM users u JOIN link_recipients lr ON lr.user_id=u.id WHERE lr.share_id=? ORDER BY u.username", id)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return p, e
		}
		p.Recipients = append(p.Recipients, name)
	}
	return p, rows.Err()
}
func permissionDenied(w http.ResponseWriter) {
	respond(w, 403, map[string]string{"error": "Permission denied", "error_code": "permission_denied"})
}

// Reads remain available when sharing/grant capabilities are disabled. Writes
// never inherit from a session, bearer link, admin role, or historical grant.
// Caller holds mu through check and commit, so revocation is immediate.
func (a *App) collaborationAllowed(w http.ResponseWriter, r *http.Request, s Share, action string) (User, bool) {
	u, ok := a.require(w, r, false)
	if !ok {
		return u, false
	}
	var owner string
	if internal(w, a.db.QueryRow("SELECT owner FROM shares WHERE id=?", s.ID).Scan(&owner)) {
		return u, false
	}
	if u.ID == owner {
		key := "edit_own"
		if action == "add" {
			key = "upload"
		}
		return u, a.allowed(w, u.ID, key)
	}
	if s.Mode == "" {
		permissionDenied(w)
		return u, false
	}
	var n int
	if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM link_recipients WHERE share_id=? AND user_id=?", s.ID, u.ID).Scan(&n)) {
		return u, false
	}
	if n != 1 {
		permissionDenied(w)
		return u, false
	}
	p, e := a.linkPermissions(s.ID)
	if internal(w, e) {
		return u, false
	}
	enabled := action == "edit" && p.Edit || action == "add" && p.Add && s.Mode == "live" || action == "delete" && p.Delete
	if !enabled {
		permissionDenied(w)
		return u, false
	}
	if !a.allowed(w, owner, "share", "grant_edit") {
		return u, false
	}
	key := map[string]string{"edit": "edit_shared", "add": "add_shared", "delete": "delete_shared"}[action]
	if !a.allowed(w, u.ID, key) {
		return u, false
	}
	if action == "add" && (!a.allowed(w, u.ID, "upload") || !a.allowed(w, owner, "upload")) {
		return u, false
	}
	return u, true
}
func (a *App) updateLibraryShare(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.ownShare(w, r)
	if !ok {
		return
	}
	if s.Mode == "" {
		fail(w, 404, "not_found")
		return
	}
	u, ok := a.require(w, r, false)
	if !ok || !a.allowed(w, u.ID, "share") {
		return
	}
	var b struct {
		Title      *string   `json:"title"`
		Recipients *[]string `json:"recipients"`
		Edit       *bool     `json:"edit"`
		Add        *bool     `json:"add"`
		Delete     *bool     `json:"delete"`
		Days       *int      `json:"days"`
		Permanent  *bool     `json:"permanent"`
		Password   *string   `json:"password"`
		Limit      *int64    `json:"download_limit"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Days != nil || b.Permanent != nil {
		policy, e := a.policy()
		if internal(w, e) {
			return
		}
		days := policy.DefaultDays
		if b.Days != nil {
			days = *b.Days
		}
		if days == 0 {
			days = policy.DefaultDays
		}
		if days < 1 || days > 365 {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		s.Expires = time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
		if b.Permanent != nil && *b.Permanent {
			s.Expires = 0
		}
	}
	password := ""
	if b.Password != nil {
		if len(*b.Password) > 72 {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		if *b.Password != "" {
			pw, e := bcrypt.GenerateFromPassword([]byte(*b.Password), 12)
			if internal(w, e) {
				return
			}
			password = string(pw)
		}
		s.Protected = password != ""
	}
	p, e := a.linkPermissions(s.ID)
	if internal(w, e) {
		return
	}
	changed := b.Recipients != nil || b.Edit != nil || b.Add != nil || b.Delete != nil
	if b.Title != nil {
		if strings.TrimSpace(*b.Title) == "" || len(*b.Title) > 160 {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		s.Title = *b.Title
	}
	if b.Recipients != nil {
		p.Recipients = *b.Recipients
	}
	if b.Edit != nil {
		p.Edit = *b.Edit
	}
	if b.Add != nil {
		p.Add = *b.Add
	}
	if b.Delete != nil {
		p.Delete = *b.Delete
	}
	if changed && (p.Edit || p.Add || p.Delete) && !a.allowed(w, u.ID, "grant_edit") {
		return
	}
	if len(p.Recipients) > 50 || p.Add && s.Mode != "live" {
		fail(w, 400, "Ogiltiga indata")
		return
	}
	if b.Limit != nil {
		if !validDownloadLimit(*b.Limit) {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		s.DownloadLimit = *b.Limit
		// A limit lowered below what was already downloaded closes the link.
		if usedUpShare(s, nil) {
			s.Expires = time.Now().Unix()
		}
	}
	// Collaborators work through the link, so it cannot also run out.
	if (p.Edit || p.Add || p.Delete) && (len(p.Recipients) == 0 || s.DownloadLimit > 0) {
		fail(w, 400, "Ogiltiga indata")
		return
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, name := range p.Recipients {
		if !validUsername(name) || seen[name] {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		seen[name] = true
		var id string
		if a.db.QueryRow("SELECT id FROM users WHERE username=?", name).Scan(&id) != nil {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		ids = append(ids, id)
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE shares SET title=?,expires=?,download_limit=? WHERE id=?", s.Title, s.Expires, s.DownloadLimit, s.ID); internal(w, e) {
		return
	}
	if b.Password != nil {
		if _, e = tx.Exec("UPDATE shares SET password=? WHERE id=?", password, s.ID); internal(w, e) {
			return
		}
		if _, e = tx.Exec("DELETE FROM grants WHERE share_id=?", s.ID); internal(w, e) {
			return
		}
	}
	if _, e = tx.Exec("INSERT INTO link_permissions(share_id,edit,add_files,delete_files) VALUES(?,?,?,?) ON CONFLICT(share_id) DO UPDATE SET edit=excluded.edit,add_files=excluded.add_files,delete_files=excluded.delete_files", s.ID, p.Edit, p.Add, p.Delete); internal(w, e) {
		return
	}
	if _, e = tx.Exec("DELETE FROM link_recipients WHERE share_id=?", s.ID); internal(w, e) {
		return
	}
	for _, id := range ids {
		if _, e = tx.Exec("INSERT INTO link_recipients(share_id,user_id) VALUES(?,?)", s.ID, id); internal(w, e) {
			return
		}
	}
	if !internal(w, tx.Commit()) {
		respond(w, 200, s)
	}
}
func (a *App) collaborationRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/public/{id}/permissions", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.publicShare(w, r)
		if !ok {
			return
		}
		rights := map[string]bool{"authenticated": false, "edit": false, "add": false, "delete": false}
		u, e := a.currentUser(r)
		if e != nil {
			respond(w, 200, rights)
			return
		}
		rights["authenticated"] = true
		var owner string
		if internal(w, a.db.QueryRow("SELECT owner FROM shares WHERE id=?", s.ID).Scan(&owner)) {
			return
		}
		caps, e := a.capabilities(u.ID)
		if internal(w, e) {
			return
		}
		if u.ID == owner {
			rights["edit"] = caps["edit_own"]
			rights["delete"] = caps["edit_own"]
			rights["add"] = caps["upload"] && s.Mode == "live" && s.FolderID != ""
		} else if s.Mode != "" {
			sender, e := a.capabilities(owner)
			if internal(w, e) {
				return
			}
			var n int
			if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM link_recipients WHERE share_id=? AND user_id=?", s.ID, u.ID).Scan(&n)) {
				return
			}
			if n == 1 && sender["share"] && sender["grant_edit"] {
				p, e := a.linkPermissions(s.ID)
				if internal(w, e) {
					return
				}
				rights["edit"] = p.Edit && caps["edit_shared"]
				rights["delete"] = p.Delete && caps["delete_shared"]
				rights["add"] = p.Add && s.Mode == "live" && s.FolderID != "" && caps["add_shared"] && caps["upload"] && sender["upload"]
			}
		}
		respond(w, 200, rights)
	})
	m.HandleFunc("GET /api/library/shares/{id}/permissions", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.ownShare(w, r)
		if !ok {
			return
		}
		if s.Mode == "" {
			fail(w, 404, "not_found")
			return
		}
		p, e := a.linkPermissions(s.ID)
		if !internal(w, e) {
			respond(w, 200, p)
		}
	})
	for _, route := range []string{"GET /api/public/{id}/files/{file}/text", "PUT /api/public/{id}/files/{file}/text", "GET /api/public/{id}/files/{file}/versions", "POST /api/public/{id}/files/{file}/restore"} {
		m.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			a.mu.Lock()
			defer a.mu.Unlock()
			s, ok := a.publicShare(w, r)
			if !ok {
				return
			}
			// Reading the text whole would be an uncounted download on a limited
			// link (limited links cannot be edited).
			if s.DownloadLimit > 0 {
				fail(w, 403, "preview_unavailable")
				return
			}
			var f File
			found := false
			for _, candidate := range s.Files {
				if candidate.ID == r.PathValue("file") {
					f = candidate
					found = true
					break
				}
			}
			if !found {
				fail(w, 404, "file_not_found")
				return
			}
			user := ""
			if r.Method != "GET" || strings.HasSuffix(r.URL.Path, "/versions") {
				u, ok := a.collaborationAllowed(w, r, s, "edit")
				if !ok {
					return
				}
				user = u.ID
			}
			a.serveText(w, r, f, user, true)
		})
	}
}

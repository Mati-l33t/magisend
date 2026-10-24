package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"
)

// Collaboration adds are bounded single-request uploads (4 MiB), not owner
// impersonation or alternate upload tokens. Both principals and link membership
// are rechecked after buffering; bytes/quota belong to the sender, not recipient.
func (a *App) collaboratorUpload(w http.ResponseWriter, r *http.Request) {
	check := func() (Share, string, bool) {
		s, ok := a.publicShare(w, r)
		if !ok {
			return s, "", false
		}
		if _, ok = a.collaborationAllowed(w, r, s, "add"); !ok {
			return s, "", false
		}
		if s.Mode != "live" || s.FolderID == "" {
			permissionDenied(w)
			return s, "", false
		}
		var owner string
		if internal(w, a.db.QueryRow("SELECT owner FROM shares WHERE id=?", s.ID).Scan(&owner)) {
			return s, "", false
		}
		return s, owner, true
	}
	a.mu.Lock()
	_, _, ok := check()
	a.mu.Unlock()
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if !utf8.ValidString(name) || !validFilename(name) {
		fail(w, 400, "Ogiltiga indata")
		return
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, chunkMax))
	if e != nil {
		fail(w, 413, "Uppladdningsdel måste vara högst 4 MiB")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, owner, ok := check()
	if !ok {
		return
	}
	if !a.folderRoom(w, s.FolderID, "", 1) || !a.metadataAvailable(w, owner, false, 1) {
		return
	}
	p, e := a.policy()
	if internal(w, e) {
		return
	}
	size := int64(len(data))
	if !a.libraryGrowthAllowed(w, owner, "", size, s.FolderID) {
		return
	}
	var total int64
	for _, f := range s.Files {
		total += f.Size
	}
	if size > p.MaxFile || size > p.MaxShare-total {
		fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
		return
	}
	var quota, used, all, reserved int64
	if internal(w, a.db.QueryRow("SELECT quota FROM users WHERE id=?", owner).Scan(&quota)) {
		return
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files f ON f.id=c.file_id JOIN shares s ON s.id=f.share_id WHERE s.owner=?", owner).Scan(&used)) {
		return
	}
	if size > quota-used {
		fail(w, 413, "Din lagringskvot räcker inte")
		return
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all)) {
		return
	}
	budget, err := a.storageBudget()
	if internal(w, err) {
		return
	}
	if size > budget-all {
		fail(w, 507, "Appens lagringsbudget är full")
		return
	}
	loc, folder, err := a.activeLocation()
	if internal(w, err) {
		return
	}
	free, err := a.diskFree()
	if internal(w, err) {
		return
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size-offset),0) FROM files").Scan(&reserved)) {
		return
	}
	if size+reserved > free-(512<<20) {
		fail(w, 507, "Otillräckligt ledigt diskutrymme")
		return
	}
	f := File{ID: randomID(), Name: name, Size: size, Offset: size, Backend: loc}
	path := filepath.Join(folder, f.ID)
	blob, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if internal(w, e) {
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(path)
		}
	}()
	_, e = blob.Write(data)
	if e == nil {
		e = blob.Sync()
	}
	ce := blob.Close()
	if e == nil {
		e = ce
	}
	if internal(w, e) {
		return
	}
	if internal(w, syncDir(folder)) {
		return
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	container := randomID()
	if _, e = tx.Exec("INSERT INTO shares(id,owner,title,expires,created,published) VALUES(?,?,?,0,?,1)", container, owner, "Library", time.Now().Unix()); internal(w, e) {
		return
	}
	if _, e = tx.Exec("INSERT INTO library_containers(share_id) VALUES(?)", container); internal(w, e) {
		return
	}
	if _, e = tx.Exec("INSERT INTO files(id,share_id,name,size,offset,backend) VALUES(?,?,?,?,?,?)", f.ID, container, f.Name, size, size, loc); internal(w, e) {
		return
	}
	if _, e = tx.Exec("INSERT INTO file_origins(file_id,name) VALUES(?,?)", f.ID, name); internal(w, e) {
		return
	}
	if _, e = tx.Exec("INSERT INTO library_files(file_id,folder_id) VALUES(?,?)", f.ID, s.FolderID); internal(w, e) {
		return
	}
	if internal(w, tx.Commit()) {
		return
	}
	committed = true
	a.queueThumbnails(container)
	respond(w, 201, f)
}
func (a *App) collaboratorFileRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/public/{id}/files", a.collaboratorUpload)
	m.HandleFunc("DELETE /api/public/{id}/files/{file}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.publicShare(w, r)
		if !ok {
			return
		}
		found := false
		for _, f := range s.Files {
			if f.ID == r.PathValue("file") {
				found = true
				break
			}
		}
		if !found {
			fail(w, 404, "file_not_found")
			return
		}
		u, ok := a.collaborationAllowed(w, r, s, "delete")
		if !ok {
			return
		}
		// It goes to the owner's trash.
		if !internal(w, a.trashLibraryFile(r.PathValue("file"), u.ID)) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
}

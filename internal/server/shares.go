package server

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The default app-wide storage budget; administrators can change it.
const defaultStorageBudget int64 = 4 * GiB
const maxDescriptionRunes = 1000

func (a *App) blob(id string) string { return filepath.Join(a.root, "blobs", id) }
func validFilename(s string) bool {
	if len(s) < 1 || len(s) > 240 || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func (a *App) loadShare(id string) (Share, string, string, error) {
	s := Share{Files: []File{}}
	var owner, password string
	e := a.db.QueryRow("SELECT id,owner,title,description,slug,password,expires,created,published,downloads,notify,download_limit,last_download FROM shares WHERE id=?", id).Scan(&s.ID, &owner, &s.Title, &s.Description, &s.Slug, &password, &s.Expires, &s.Created, &s.Published, &s.Downloads, &s.Notify, &s.DownloadLimit, &s.LastDownload)
	if e != nil {
		return s, "", "", e
	}
	s.Published = s.Published && !a.isDeleting(id)
	s.Protected = password != ""
	rows, e := a.db.Query("SELECT id,name,size,offset,backend FROM files WHERE share_id=? AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=files.id) ORDER BY rowid", id)
	if e != nil {
		return s, "", "", e
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		if e = rows.Scan(&f.ID, &f.Name, &f.Size, &f.Offset, &f.Backend); e != nil {
			return s, "", "", e
		}
		s.Files = append(s.Files, f)
	}
	if e = rows.Err(); e != nil {
		return s, "", "", e
	}
	// Thumbnails are an additive decoration: a share must never fail to load
	// because a decorative row is unreadable.
	thumbs := map[string]bool{}
	if thumbRows, e := a.db.Query("SELECT t.file_id FROM thumbnails t JOIN files f ON f.id=t.file_id WHERE f.share_id=?", id); e == nil {
		for thumbRows.Next() {
			var fileID string
			if thumbRows.Scan(&fileID) == nil {
				thumbs[fileID] = true
			}
		}
		thumbRows.Close()
	}
	for i := range s.Files {
		s.Files[i].HasThumb = thumbs[s.Files[i].ID]
	}
	if e = a.loadLibraryMembership(&s); e != nil {
		return s, "", "", e
	}
	held := a.heldScans()
	for i := range s.Files {
		s.Files[i].Scan = held[s.Files[i].ID]
	}
	if e = a.loadDownloads(&s); e != nil {
		return s, "", "", e
	}
	return s, owner, password, nil
}
func (a *App) ownShare(w http.ResponseWriter, r *http.Request) (Share, bool) {
	u, ok := a.require(w, r, false)
	if !ok {
		return Share{}, false
	}
	s, owner, _, e := a.loadShare(r.PathValue("id"))
	if e != nil || owner != u.ID {
		fail(w, 404, "Delningen finns inte")
		return s, false
	}
	return s, true
}
func (a *App) createShare(w http.ResponseWriter, r *http.Request) {
	library := r.URL.Path == "/api/library/uploads"
	if _, ok := a.require(w, r, false); !ok {
		return
	}
	var b struct {
		FolderID string `json:"folder_id"`
		// A link made on Send keeps its files in the library (in FolderID)
		// once it expires or is deleted.
		Keep        bool   `json:"keep"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Slug        string `json:"slug"`
		Days        int    `json:"days"`
		Permanent   bool   `json:"permanent"`
		Password    string `json:"password"`
		Limit       int64  `json:"download_limit"`
		// For the expiry reminder.
		Language string `json:"language"`
		// Photos lose their location when the link is published.
		StripLocation bool `json:"strip_location"`
		Files         []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if !decode(w, r, &b) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.require(w, r, false)
	if !ok {
		return
	}
	if library || b.Keep {
		if !a.allowed(w, u.ID, "upload") {
			return
		}
		if b.FolderID != "" && (!a.allowed(w, u.ID, "folders") || !a.ownsFolder(u.ID, b.FolderID)) {
			if !a.ownsFolder(u.ID, b.FolderID) {
				fail(w, 404, "not_found")
			}
			return
		}
		if library {
			b.Permanent = true
			b.Password, b.Slug, b.Limit = "", "", 0
		}
		if !a.folderRoom(w, b.FolderID, "", len(b.Files)) {
			return
		}
	} else if b.FolderID != "" {
		fail(w, 400, "Ogiltiga indata")
		return
	}
	p, e := a.policy()
	if internal(w, e) {
		return
	}
	if b.Days == 0 && !b.Permanent {
		b.Days = p.DefaultDays
	}
	if len(b.Title) > 160 || (!b.Permanent && (b.Days < 1 || b.Days > 365)) || len(b.Files) < 1 || len(b.Files) > 100 || len(b.Password) > 72 || !validDownloadLimit(b.Limit) {
		fail(w, 400, "Ogiltig delning: 1–100 filer, giltighet 1–365 dagar")
		return
	}
	if strings.TrimSpace(b.Title) == "" {
		b.Title = b.Files[0].Name
	}
	b.Description = strings.TrimSpace(b.Description)
	if len([]rune(b.Description)) > maxDescriptionRunes {
		fail(w, 400, "invalid_description")
		return
	}
	if b.Slug != "" {
		if !validSlug(b.Slug) {
			fail(w, 400, "slug_invalid")
			return
		}
		available, e := a.slugAvailable(b.Slug)
		if internal(w, e) {
			return
		}
		if !available {
			fail(w, 409, "slug_taken")
			return
		}
	}
	var total int64
	names := map[string]bool{}
	for _, f := range b.Files {
		if !validFilename(f.Name) || names[f.Name] || f.Size < 0 || f.Size > p.MaxFile || f.Size > p.MaxShare-total {
			fail(w, 400, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
			return
		}
		names[f.Name] = true
		total += f.Size
	}
	if (library || b.Keep) && !a.libraryGrowthAllowed(w, u.ID, "", total, b.FolderID) {
		return
	}
	if !a.metadataAvailable(w, u.ID, !library, len(b.Files)) {
		return
	}
	if total > u.Quota-u.Used {
		fail(w, 413, "Din lagringskvot räcker inte")
		return
	}
	var all int64
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all)) {
		return
	}
	budget, e := a.storageBudget()
	if internal(w, e) {
		return
	}
	if total > budget-all {
		fail(w, 507, "Appens lagringsbudget är full")
		return
	}
	loc, _, e := a.activeLocation()
	if internal(w, e) {
		return
	}
	free, e := a.diskFree()
	if internal(w, e) {
		return
	}
	var reserved int64
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size-offset),0) FROM files").Scan(&reserved)) {
		return
	}
	if total+reserved > free-(512<<20) {
		fail(w, 507, "Otillräckligt ledigt diskutrymme")
		return
	}
	password := ""
	if b.Password != "" {
		pw, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
		if internal(w, e) {
			return
		}
		password = string(pw)
	}
	s := Share{ID: randomID(), Title: b.Title, Description: b.Description, Slug: b.Slug, Expires: time.Now().Add(time.Duration(b.Days) * 24 * time.Hour).Unix(), Created: time.Now().Unix(), Protected: password != "", DownloadLimit: b.Limit, Files: []File{}}
	if b.Permanent {
		s.Expires = 0
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO shares(id,owner,title,description,slug,password,expires,created,download_limit,notify_language,notify_origin,strip_location) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", s.ID, u.ID, s.Title, s.Description, s.Slug, password, s.Expires, s.Created, s.DownloadLimit, emailLanguage(b.Language), a.requestOrigin(r), b.StripLocation && !library)
	// The partial unique index is the last line of defence behind the mutex-held
	// availability check; if it ever fires, the answer is still the documented
	// conflict rather than an internal error.
	if slugConflictError(e) {
		fail(w, 409, "slug_taken")
		return
	}
	if internal(w, e) {
		return
	}
	for _, f := range b.Files {
		file := File{ID: randomID(), Name: f.Name, Size: f.Size, Backend: loc}
		if _, e = tx.Exec("INSERT INTO files(id,share_id,name,size,backend) VALUES(?,?,?,?,?)", file.ID, s.ID, file.Name, file.Size, file.Backend); internal(w, e) {
			return
		}
		s.Files = append(s.Files, file)
		if _, e = tx.Exec("INSERT INTO file_origins(file_id,name) VALUES(?,?)", file.ID, file.Name); internal(w, e) {
			return
		}
		if library || b.Keep {
			var folder any
			if b.FolderID != "" {
				folder = b.FolderID
			}
			if _, e = tx.Exec("INSERT INTO library_files(file_id,folder_id) VALUES(?,?)", file.ID, folder); internal(w, e) {
				return
			}
		}
	}
	if library {
		if _, e = tx.Exec("INSERT INTO library_containers(share_id) VALUES(?)", s.ID); internal(w, e) {
			return
		}
	}
	if internal(w, tx.Commit()) {
		return
	}
	respond(w, 201, s)
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	// Authenticate and authorize before any body read; recheck after buffering.
	u, ok := a.require(w, r, false)
	if !ok {
		return
	}
	offset, e := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if e != nil || offset < 0 {
		fail(w, 400, "Fel uppladdningsposition")
		return
	}
	var size, current int64
	var published bool
	var expires int64
	id := r.PathValue("id")
	authorized := false
	check := func() (int64, int64, bool) {
		// The session is checked again after the body was read.
		if authorized {
			if u, ok = a.require(w, r, false); !ok {
				return 0, 0, false
			}
		}
		authorized = true
		var shareID string
		e = a.db.QueryRow(`SELECT f.size,f.offset,s.published,s.expires,s.id FROM files f JOIN shares s ON s.id=f.share_id WHERE f.id=? AND s.owner=? AND NOT EXISTS (SELECT 1 FROM deleting d WHERE d.share_id=s.id)`, id, u.ID).Scan(&size, &current, &published, &expires, &shareID)
		// Files a ZIP is being unpacked into are written by the extraction only.
		if e != nil || a.extracting[shareID] {
			fail(w, 404, "Uppladdningen finns inte")
			return 0, 0, false
		}
		if published || shareExpired(expires, time.Now().Unix()) || offset != current {
			fail(w, 409, "Fel position, färdig eller utgången delning")
			return 0, 0, false
		}
		return size, current, true
	}
	a.writeChunk(w, r, id, check)
}

// writeChunk appends one piece of an upload. check runs under mu before and
// again after the body is read, and returns the file's size and offset.
func (a *App) writeChunk(w http.ResponseWriter, r *http.Request, id string, check func() (int64, int64, bool)) {
	a.mu.Lock()
	_, _, ok := check()
	var path string
	var e error
	if ok {
		// An offline location is refused before the piece is received.
		path, e = a.fileBlob(id, id)
		ok = !internal(w, e)
	}
	a.mu.Unlock()
	if !ok {
		return
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, chunkMax))
	if e != nil {
		fail(w, 413, "Uppladdningsdel måste vara högst 4 MiB")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	size, current, ok := check()
	if !ok {
		return
	}
	if int64(len(data)) > size-current {
		fail(w, 409, "Fel position, färdig eller utgången delning")
		return
	}
	// O_CREATE is only valid before the first committed byte. Never pad lost data.
	flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if current == 0 {
		flags |= os.O_CREATE
	}
	// Never follow a substituted symlink (which could truncate an unrelated
	// regular file) or block on a FIFO while mu is held. A symlinked path is
	// not the storage object: Lstat reports non-regular or a different target,
	// and OpenFile itself refuses O_NOFOLLOW with ELOOP, which fails closed.
	if path, e = a.fileBlob(id, id); internal(w, e) {
		return
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < current {
			fail(w, 503, "Lagringen kan inte verifieras")
			return
		}
	} else if current > 0 || !os.IsNotExist(err) {
		internal(w, err)
		return
	}
	f, e := os.OpenFile(path, flags, 0600)
	if internal(w, e) {
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if internal(w, e) {
		return
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < current {
		fail(w, 503, "Lagringen kan inte verifieras")
		return
	}
	if internal(w, f.Truncate(current)) {
		return
	}
	if _, e = f.WriteAt(data, current); internal(w, e) {
		f.Truncate(current)
		return
	}
	if internal(w, f.Sync()) {
		f.Truncate(current)
		return
	}
	next := current + int64(len(data))
	if internal(w, exec(a, "UPDATE files SET offset=? WHERE id=?", next, id)) {
		f.Truncate(current)
		return
	}
	respond(w, 200, map[string]int64{"offset": next})
}
func (a *App) shareRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/shares", a.createShare)
	m.HandleFunc("PATCH /api/shares/{id}", a.editShare)
	m.HandleFunc("GET /api/shares", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query("SELECT id FROM shares WHERE owner=? AND NOT EXISTS(SELECT 1 FROM library_containers c WHERE c.share_id=shares.id) ORDER BY created DESC", u.ID)
		if internal(w, e) {
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				internal(w, e)
				return
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if internal(w, e) {
			return
		}
		result := []Share{}
		for _, id := range ids {
			s, _, _, e := a.loadShare(id)
			if internal(w, e) {
				return
			}
			result = append(result, s)
		}
		respond(w, 200, result)
	})
	m.HandleFunc("GET /api/uploads/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		var f File
		e := a.db.QueryRow(`SELECT f.id,f.name,f.size,f.offset,f.backend FROM files f JOIN shares s ON s.id=f.share_id WHERE f.id=? AND s.owner=?`, r.PathValue("id"), u.ID).Scan(&f.ID, &f.Name, &f.Size, &f.Offset, &f.Backend)
		if e != nil {
			fail(w, 404, "Uppladdningen finns inte")
			return
		}
		respond(w, 200, f)
	})
	m.HandleFunc("PATCH /api/uploads/{id}", a.upload)
	m.HandleFunc("POST /api/shares/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.ownShare(w, r)
		if !ok {
			return
		}
		if a.isDeleting(s.ID) {
			fail(w, 404, "Delningen finns inte")
			return
		}
		if shareExpired(s.Expires, time.Now().Unix()) {
			fail(w, 410, "Delningen har gått ut")
			return
		}
		if !a.verifyUploadedFiles(w, s) {
			return
		}
		if e := a.scrubShare(s); errors.Is(e, errLocationNotRemoved) {
			fail(w, 422, "location_not_removed")
			return
		} else if internal(w, e) {
			return
		}
		if internal(w, exec(a, "UPDATE shares SET published=1 WHERE id=?", s.ID)) {
			return
		}
		// Decorative thumbnails are generated after the share is durable, and a
		// failure to generate one never fails or delays the publish itself.
		a.queueScans(s.ID, false)
		a.queueThumbnails(s.ID)
		s.Published = true
		if !a.isLibraryContainer(s.ID) {
			if u, e := a.currentUser(r); e == nil {
				a.audit(r, u.Username, "share.created", s.Title)
			}
		}
		respond(w, 200, s)
	})
	m.HandleFunc("GET /api/public/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.publicShare(w, r)
		if !ok {
			return
		}
		respond(w, 200, publicView(s))
	})
	m.HandleFunc("GET /api/public/{id}/files/{file}", a.download)
	m.HandleFunc("GET /api/public/{id}/files/{file}/preview", func(w http.ResponseWriter, r *http.Request) {
		a.previewWith(w, r, func() (Share, bool) {
			s, ok := a.publicShare(w, r)
			// A full-size preview would be a download that is never counted.
			if ok && s.DownloadLimit > 0 {
				fail(w, 403, "preview_unavailable")
				return s, false
			}
			return s, ok
		})
	})
	m.HandleFunc("GET /api/shares/{id}/files/{file}", func(w http.ResponseWriter, r *http.Request) {
		a.downloadWith(w, r, func() (Share, bool) { return a.ownerReadableShare(w, r) })
	})
	m.HandleFunc("GET /api/shares/{id}/files/{file}/preview", func(w http.ResponseWriter, r *http.Request) {
		a.previewWith(w, r, func() (Share, bool) { return a.ownerReadableShare(w, r) })
	})
	a.thumbnailRoutes(m)
}
func (a *App) publicShare(w http.ResponseWriter, r *http.Request) (Share, bool) {
	// A published share answers to its id and to its custom link name.
	s, _, password, e := a.loadShareRef(r.PathValue("id"))
	if e != nil || !s.Published || a.isLibraryContainer(s.ID) {
		fail(w, 404, "Delningen finns inte")
		return s, false
	}
	if shareExpired(s.Expires, time.Now().Unix()) {
		fail(w, 410, "Delningen har gått ut")
		return s, false
	}
	if password != "" {
		c, e := r.Cookie("magisend_grant_" + s.ID)
		var count int
		if e != nil {
			fail(w, 401, "Delningen kräver lösenord")
			return s, false
		}
		e = a.db.QueryRow("SELECT COUNT(*) FROM grants WHERE hash=? AND share_id=? AND expires>?", hash(c.Value), s.ID, time.Now().Unix()).Scan(&count)
		if e != nil || count != 1 {
			fail(w, 401, "Delningen kräver lösenord")
			return s, false
		}
	}
	return s, true
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	check, commit := a.countedDownload(w, r, func() (Share, bool) { return a.publicShare(w, r) })
	a.downloadWith(w, r, check, commit)
}

// downloadWith streams one file. verified, if set, runs under mu once the
// file is open and its size checked (counting a public download).
func (a *App) downloadWith(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool), verified ...func()) {
	release, ok := a.acquireDownload(w, r)
	if !ok {
		return
	}
	defer release()
	selected, file, ok := a.openReadableFile(w, r, authorize, verified...)
	if !ok {
		return
	}
	defer file.Close()
	info, e := file.Stat()
	if internal(w, e) {
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": selected.Name}))
	http.ServeContent(w, r, selected.Name, info.ModTime(), file)
}

// Caller owns a download slot. Authorization and opening form a deletion-safe
// snapshot under mu; decoding and streaming must happen after mu is released.
func (a *App) openReadableFile(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool), verified ...func()) (File, *os.File, bool) {
	a.mu.Lock()
	s, ok := authorize()
	if !ok {
		a.mu.Unlock()
		return File{}, nil, false
	}
	var selected File
	found := false
	for _, f := range s.Files {
		if f.ID == r.PathValue("file") {
			selected = f
			found = true
			break
		}
	}
	if !found || selected.Offset != selected.Size {
		a.mu.Unlock()
		fail(w, 404, "Filen finns inte")
		return File{}, nil, false
	}
	if !a.knownLocation(selected.Backend) || selected.Size < 0 {
		a.mu.Unlock()
		fail(w, 503, "storage_unverified")
		return File{}, nil, false
	}
	if held := a.scanHeld(selected.ID); held != "" {
		a.mu.Unlock()
		failHeld(w, held)
		return File{}, nil, false
	}
	// Never follow symlinks or block on a FIFO substituted for a blob.
	file, e := a.openCurrent(selected.ID)
	if e != nil {
		a.mu.Unlock()
		internal(w, e)
		return File{}, nil, false
	}
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != selected.Size {
		a.mu.Unlock()
		file.Close()
		fail(w, 503, "Lagringen kan inte verifieras")
		return File{}, nil, false
	}
	// Only now, with the file ready to send, is anything counted.
	for _, after := range verified {
		if after != nil {
			after()
		}
	}
	a.mu.Unlock()
	return selected, file, true
}

// verifyUploadedFiles checks, before publishing, that every file is complete
// and its storage object exists with the expected size. Caller holds mu.
func (a *App) verifyUploadedFiles(w http.ResponseWriter, s Share) bool {
	for _, file := range s.Files {
		if file.Offset != file.Size {
			fail(w, 409, "Alla filer måste laddas upp först")
			return false
		}
		// All content reads, including publication, must verify the storage
		// object that the current version actually points to. A zero-byte
		// file with no history may still create its original blob here;
		// anything versioned (or any nonzero size) is only ever validated.
		path, e := a.currentBlob(file.ID)
		if errors.Is(e, errStorageOffline) {
			internal(w, e)
			return false
		}
		if e != nil {
			fail(w, 503, "Lagringen kan inte verifieras")
			return false
		}
		creation := file.Size == 0 && filepath.Base(path) == file.ID
		flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
		if creation {
			flags = os.O_WRONLY | os.O_CREATE | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
		}
		f, e := os.OpenFile(path, flags, 0600)
		if e != nil {
			fail(w, 503, "Lagringen kan inte verifieras")
			return false
		}
		stat, e := f.Stat()
		closeErr := f.Close()
		if e != nil || closeErr != nil || !stat.Mode().IsRegular() || stat.Size() != file.Size {
			fail(w, 503, "Lagringen kan inte verifieras")
			return false
		}
	}
	return true
}

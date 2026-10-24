package server

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A ZIP file in the library can be unpacked into a new folder next to it,
// keeping the folders inside it. Everything is checked against the limits
// first, from the archive's own list of contents; then the files are written
// without holding the app lock, and each must come out at exactly the size
// the archive promised, so an archive that lies about its sizes stops instead
// of filling the disk. The new files appear only when all of them are done.

var skippedInZip = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

type zipEntry struct {
	file   *zip.File
	dir    string
	name   string
	id     string
	folder string
	path   string
}

// zipPlan reads the archive's list of contents. Folders, system files,
// encrypted entries and names that would escape the folder are left out.
func zipPlan(reader *zip.Reader) (entries []zipEntry, dirs []string, skipped int) {
	seen := map[string]bool{}
	for _, f := range reader.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			continue
		}
		clean := path.Clean("/" + name)[1:]
		parts := strings.Split(clean, "/")
		base := parts[len(parts)-1]
		ok := clean == name && f.Flags&1 == 0 && !skippedInZip[base] && !strings.HasPrefix(base, "._") && parts[0] != "__MACOSX" && validFilename(base)
		for _, p := range parts[:len(parts)-1] {
			ok = ok && validFolder(p)
		}
		if !ok || seen[clean] {
			skipped++
			continue
		}
		seen[clean] = true
		dir := strings.Join(parts[:len(parts)-1], "/")
		entries = append(entries, zipEntry{file: f, dir: dir, name: base})
		for d := dir; d != ""; d = path.Dir(d) {
			if !containsString(dirs, d) {
				dirs = append(dirs, d)
			}
			if !strings.Contains(d, "/") {
				break
			}
		}
	}
	return entries, dirs, skipped
}

func (a *App) extractRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/library/files/{file}/extract", func(w http.ResponseWriter, r *http.Request) {
		if !acquire(w, a.uploads) {
			return
		}
		defer func() { <-a.uploads }()
		a.mu.Lock()
		u, f, ok := a.versionedFile(w, r, false)
		if ok && !a.allowed(w, u.ID, "upload", "folders") {
			ok = false
		}
		var archive *os.File
		var reader *zip.Reader
		if ok && strings.ToLower(filepath.Ext(f.Name)) != ".zip" {
			ok = false
			fail(w, 400, "zip_invalid")
		}
		if ok {
			var e error
			archive, e = a.openCurrent(f.ID)
			if e == nil {
				reader, e = zip.NewReader(archive, f.Size)
			}
			if e != nil {
				ok = false
				fail(w, 400, "zip_invalid")
			}
		}
		if archive != nil {
			defer archive.Close()
		}
		var plan *extraction
		if ok {
			plan, ok = a.planExtraction(w, u, f, reader)
		}
		a.mu.Unlock()
		if !ok {
			return
		}
		e := plan.write()
		a.mu.Lock()
		defer a.mu.Unlock()
		if e == nil {
			e = plan.finish(a)
		}
		if e != nil {
			_ = a.removeShare(plan.share)
			for _, folder := range plan.folders {
				_ = exec(a, "DELETE FROM folders WHERE id=? AND NOT EXISTS(SELECT 1 FROM library_files WHERE folder_id=?)", folder, folder)
			}
			fail(w, 400, "zip_invalid")
			return
		}
		a.queueThumbnails(plan.share)
		respond(w, 201, map[string]any{"folder_id": plan.top, "name": plan.topName, "files": len(plan.entries), "skipped": plan.skipped})
	})
}

type extraction struct {
	share, top, topName string
	folders             []string
	entries             []zipEntry
	skipped             int
}

// planExtraction checks every limit and records the new folders and the
// files as unfinished uploads, which hold their space. Caller holds mu.
func (a *App) planExtraction(w http.ResponseWriter, u User, f LibraryFile, reader *zip.Reader) (*extraction, bool) {
	entries, dirs, skipped := zipPlan(reader)
	if len(entries) == 0 {
		fail(w, 400, "zip_empty")
		return nil, false
	}
	if len(entries) > folderFileLimit {
		fail(w, 413, "zip_too_many")
		return nil, false
	}
	p, e := a.policy()
	if internal(w, e) {
		return nil, false
	}
	var total int64
	for _, entry := range entries {
		size := int64(entry.file.UncompressedSize64)
		if entry.file.UncompressedSize64 > uint64(p.MaxFile) {
			fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
			return nil, false
		}
		total += size
	}
	parent := f.FolderID
	depth, e := a.folderDepth(parent)
	if internal(w, e) {
		return nil, false
	}
	deepest := 0
	for _, d := range dirs {
		deepest = max(deepest, strings.Count(d, "/")+1)
	}
	var folderCount int
	if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM folders WHERE owner=?", u.ID).Scan(&folderCount)) {
		return nil, false
	}
	if depth+1+deepest > maxFolderDepth || folderCount+1+len(dirs) > ownerFolderLimit {
		fail(w, 429, "metadata_limit")
		return nil, false
	}
	if !a.libraryGrowthAllowed(w, u.ID, "", total, parent) || !a.metadataAvailable(w, u.ID, false, len(entries)) {
		return nil, false
	}
	var used, all, reserved int64
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files x ON x.id=c.file_id JOIN shares s ON s.id=x.share_id WHERE s.owner=?", u.ID).Scan(&used)) {
		return nil, false
	}
	if total > u.Quota-used {
		fail(w, 413, "Din lagringskvot räcker inte")
		return nil, false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all)) {
		return nil, false
	}
	budget, e := a.storageBudget()
	if internal(w, e) {
		return nil, false
	}
	if total > budget-all {
		fail(w, 507, "Appens lagringsbudget är full")
		return nil, false
	}
	loc, dir, e := a.activeLocation()
	if internal(w, e) {
		return nil, false
	}
	free, e := a.diskFree()
	if internal(w, e) {
		return nil, false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size-offset),0) FROM files").Scan(&reserved)) {
		return nil, false
	}
	if total+reserved > free-(512<<20) {
		fail(w, 507, "Otillräckligt ledigt diskutrymme")
		return nil, false
	}

	// The new folder is named after the archive, numbered if that name is taken.
	plan := &extraction{share: randomID(), top: randomID(), entries: entries, skipped: skipped}
	base := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
	if !validFolder(base) {
		base = "Archive"
	}
	plan.topName = base
	for n := 2; ; n++ {
		var taken int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM folders WHERE owner=? AND parent=? AND name=?", u.ID, parent, plan.topName).Scan(&taken)) {
			return nil, false
		}
		if taken == 0 {
			break
		}
		plan.topName = base + " (" + strconv.Itoa(n) + ")"
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return nil, false
	}
	defer tx.Rollback()
	ids := map[string]string{"": plan.top}
	if _, e = tx.Exec("INSERT INTO folders(id,owner,parent,name) VALUES(?,?,?,?)", plan.top, u.ID, parent, plan.topName); internal(w, e) {
		return nil, false
	}
	plan.folders = append(plan.folders, plan.top)
	// Parents first: a shorter path never comes after a longer one below it.
	for depthNow := 1; depthNow <= deepest; depthNow++ {
		for _, d := range dirs {
			if strings.Count(d, "/")+1 != depthNow {
				continue
			}
			id := randomID()
			ids[d] = id
			plan.folders = append(plan.folders, id)
			if _, e = tx.Exec("INSERT INTO folders(id,owner,parent,name) VALUES(?,?,?,?)", id, u.ID, ids[parentDir(d)], path.Base(d)); internal(w, e) {
				return nil, false
			}
		}
	}
	if _, e = tx.Exec("INSERT INTO shares(id,owner,title,expires,created,published) VALUES(?,?,?,0,?,0)", plan.share, u.ID, plan.topName, time.Now().Unix()); internal(w, e) {
		return nil, false
	}
	if _, e = tx.Exec("INSERT INTO library_containers(share_id) VALUES(?)", plan.share); internal(w, e) {
		return nil, false
	}
	for i := range plan.entries {
		entry := &plan.entries[i]
		entry.id = randomID()
		entry.folder = ids[entry.dir]
		entry.path = filepath.Join(dir, entry.id)
		size := int64(entry.file.UncompressedSize64)
		for _, step := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO files(id,share_id,name,size,backend) VALUES(?,?,?,?,?)", []any{entry.id, plan.share, entry.name, size, loc}},
			{"INSERT INTO file_origins(file_id,name) VALUES(?,?)", []any{entry.id, entry.name}},
			{"INSERT INTO library_files(file_id,folder_id) VALUES(?,?)", []any{entry.id, entry.folder}},
		} {
			if _, e = tx.Exec(step.query, step.args...); internal(w, e) {
				return nil, false
			}
		}
	}
	if internal(w, tx.Commit()) {
		return nil, false
	}
	return plan, true
}

func parentDir(d string) string {
	if parent := path.Dir(d); parent != "." {
		return parent
	}
	return ""
}

// write unpacks every file; it runs without the app lock.
func (plan *extraction) write() error {
	for _, entry := range plan.entries {
		if e := writeEntry(entry); e != nil {
			return e
		}
	}
	return nil
}

func writeEntry(entry zipEntry) error {
	source, e := entry.file.Open()
	if e != nil {
		return e
	}
	defer source.Close()
	size := int64(entry.file.UncompressedSize64)
	target, e := os.OpenFile(entry.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	written, e := io.Copy(target, io.LimitReader(source, size+1))
	if e == nil && written != size {
		e = io.ErrUnexpectedEOF
	}
	if e == nil {
		e = target.Sync()
	}
	if closeErr := target.Close(); e == nil {
		e = closeErr
	}
	return e
}

// finish marks the files complete and shows them. Caller holds mu.
func (plan *extraction) finish(a *App) error {
	if e := exec(a, "UPDATE files SET offset=size WHERE share_id=?", plan.share); e != nil {
		return e
	}
	return exec(a, "UPDATE shares SET published=1 WHERE id=?", plan.share)
}

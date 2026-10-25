package server

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// A ZIP or tar file in the library can be unpacked into a new folder next to
// it, keeping the folders inside it. Everything is checked against the limits
// first, from the archive's own list of contents; then the files are written
// without holding the app lock, and each must come out at exactly the size
// the archive promised, so an archive that lies about its sizes stops instead
// of filling the disk. The new files appear only when all of them are done.
//
// A tar file has no list of contents: it is read through once to make one
// (outside the app lock) and again to unpack. Only plain files and folders
// are taken from it; links, devices and the like are left out.

var skippedInZip = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

// archiveFormat is how a file is unpacked, from its name: "zip", "tar",
// "tar.gz" or "tar.bz2"; base is the name without the extension.
func archiveFormat(name string) (format, base string) {
	lower := strings.ToLower(name)
	for _, k := range []struct{ suffix, format string }{
		{".zip", "zip"}, {".tar", "tar"},
		{".tar.gz", "tar.gz"}, {".tgz", "tar.gz"},
		{".tar.bz2", "tar.bz2"}, {".tbz2", "tar.bz2"}, {".tbz", "tar.bz2"},
	} {
		if strings.HasSuffix(lower, k.suffix) && len(name) > len(k.suffix) {
			return k.format, name[:len(name)-len(k.suffix)]
		}
	}
	return "", ""
}

// archiveItem is one entry in an archive's contents.
type archiveItem struct {
	name   string
	size   int64
	dir    bool
	usable bool      // a plain, unencrypted file
	zip    *zip.File // ZIP only
}

type zipEntry struct {
	item   archiveItem
	index  int // position in the archive
	dir    string
	name   string
	id     string
	folder string
	path   string
}

func zipItems(reader *zip.Reader) []archiveItem {
	items := make([]archiveItem, 0, len(reader.File))
	for _, f := range reader.File {
		dir := f.FileInfo().IsDir() || strings.HasSuffix(strings.ReplaceAll(f.Name, "\\", "/"), "/")
		items = append(items, archiveItem{name: f.Name, size: int64(f.UncompressedSize64), dir: dir, usable: f.Flags&1 == 0 && f.UncompressedSize64 <= 1<<62, zip: f})
	}
	return items
}

// Tar files are read through at most this far past what fits the owner's
// quota, and with at most this many entries, so a small file that unpacks to
// endless headers or skipped data cannot keep the server busy.
const (
	tarSlack      = 64 << 20
	tarEntryLimit = 10 * folderFileLimit
	// Longer names are skipped without being kept, and all kept names
	// together stay under tarNameBytes.
	tarNameLimit = 4096
	tarNameBytes = 16 << 20
	// Unpacking goes on when the request ends (a proxy's timeout, say), up
	// to this long.
	extractTimeout = 30 * time.Minute
)

var errArchiveTooLarge = errors.New("the archive unpacks to more than fits")

// cappedReader stops after limit bytes, or when the request ends.
type cappedReader struct {
	ctx   context.Context
	r     io.Reader
	limit int64
}

func (c *cappedReader) Read(b []byte) (int, error) {
	if e := c.ctx.Err(); e != nil {
		return 0, e
	}
	if c.limit <= 0 {
		return 0, errArchiveTooLarge
	}
	if int64(len(b)) > c.limit {
		b = b[:c.limit]
	}
	n, e := c.r.Read(b)
	c.limit -= int64(n)
	return n, e
}

// tarStream opens the tar data in the archive, read through at most limit
// bytes once decompressed.
func tarStream(ctx context.Context, archive io.ReaderAt, size int64, format string, limit int64) (*tar.Reader, error) {
	var r io.Reader = io.NewSectionReader(archive, 0, size)
	switch format {
	case "tar.gz":
		z, e := gzip.NewReader(r)
		if e != nil {
			return nil, e
		}
		r = z
	case "tar.bz2":
		r = bzip2.NewReader(r)
	}
	return tar.NewReader(&cappedReader{ctx: ctx, r: r, limit: limit}), nil
}

// tarItems reads a tar file's contents.
func tarItems(ctx context.Context, archive io.ReaderAt, size int64, format string, limit int64) ([]archiveItem, error) {
	reader, e := tarStream(ctx, archive, size, format, limit)
	if e != nil {
		return nil, e
	}
	var items []archiveItem
	var names int
	for {
		h, e := reader.Next()
		if e == io.EOF {
			return items, nil
		}
		if e != nil {
			return nil, e
		}
		if len(items) >= tarEntryLimit {
			return nil, errTooManyInArchive
		}
		// A name from a long-name header points into that whole header, so
		// the names kept are copies.
		item := archiveItem{size: h.Size, dir: h.Typeflag == tar.TypeDir, usable: h.Typeflag == tar.TypeReg && h.Size >= 0}
		if name, ok := tarName(h.Name); ok {
			item.name = name
		} else {
			item.usable = false
		}
		if names += len(item.name); names > tarNameBytes {
			return nil, errTooManyInArchive
		}
		items = append(items, item)
	}
}

var errTooManyInArchive = errors.New("too many entries in the archive")

// tarName is an entry's name as kept, or false when it is too long. A name
// that is not UTF-8 is from an old archive and read as Windows-1252 (Latin-1
// with typographic signs in 0x80–0x9F), as old tar files mostly were.
func tarName(name string) (string, bool) {
	if len(name) > tarNameLimit {
		return "", false
	}
	if utf8.ValidString(name) {
		return strings.Clone(name), true
	}
	runes := make([]rune, len(name))
	for i := 0; i < len(name); i++ {
		runes[i] = rune(name[i])
		if b := name[i]; b >= 0x80 && b <= 0x9f {
			runes[i] = cp1252[b-0x80]
		}
	}
	return string(runes), true
}

// cp1252 holds Windows-1252's signs for 0x80–0x9F; the five it leaves
// undefined become "_".
var cp1252 = [32]rune{
	'€', '_', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '_', 'Ž', '_',
	'_', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '_', 'ž', 'Ÿ',
}

// archivePlan picks what to unpack from an archive's contents. Folders,
// system files, encrypted entries, links and names that would escape the
// folder are left out.
func archivePlan(items []archiveItem) (entries []zipEntry, dirs []string, skipped int) {
	type candidate struct {
		zipEntry
		clean string
	}
	var candidates []candidate
	// A name that appears twice keeps its first entry in a zip file and its
	// last in a tar file, where "tar -u" adds newer copies at the end.
	keep := map[string]int{}
	for index, item := range items {
		name := strings.ReplaceAll(item.name, "\\", "/")
		// Tar files made from a folder ("tar -cf x.tar .") start every name with "./".
		if item.zip == nil {
			for strings.HasPrefix(name, "./") {
				name = name[2:]
			}
		}
		if item.dir || strings.HasSuffix(name, "/") || name == "." || (name == "" && item.usable) {
			continue
		}
		clean := path.Clean("/" + name)[1:]
		parts := strings.Split(clean, "/")
		base := parts[len(parts)-1]
		ok := name != "" && clean == name && item.usable && !skippedInZip[base] && !strings.HasPrefix(base, "._") && parts[0] != "__MACOSX" && validFilename(base)
		for _, p := range parts[:len(parts)-1] {
			ok = ok && validFolder(p)
		}
		if !ok {
			skipped++
			continue
		}
		if _, seen := keep[clean]; !seen || item.zip == nil {
			keep[clean] = index
		}
		candidates = append(candidates, candidate{zipEntry{item: item, index: index, dir: strings.Join(parts[:len(parts)-1], "/"), name: base}, clean})
	}
	for _, c := range candidates {
		if keep[c.clean] != c.index {
			skipped++
			continue
		}
		dir := c.dir
		entries = append(entries, c.zipEntry)
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
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), extractTimeout)
		defer cancel()
		a.mu.Lock()
		u, f, ok := a.versionedFile(w, r, false)
		if ok && !a.allowed(w, u.ID, "upload", "folders") {
			ok = false
		}
		if ok {
			if held := a.scanHeld(f.ID); held != "" {
				ok = false
				failHeld(w, held)
			}
		}
		format := ""
		if ok {
			if format, _ = archiveFormat(f.Name); format == "" {
				ok = false
				fail(w, 400, "zip_invalid")
			}
		}
		var archive *os.File
		if ok {
			var e error
			if archive, e = a.openCurrent(f.ID); e != nil {
				ok = false
				fail(w, 400, "zip_invalid")
			}
		}
		if archive != nil {
			defer archive.Close()
		}
		var items []archiveItem
		var limit int64
		if ok && format == "zip" {
			reader, e := zip.NewReader(archive, f.Size)
			if e != nil {
				ok = false
				fail(w, 400, "zip_invalid")
			} else {
				items = zipItems(reader)
			}
		} else if ok {
			// A tar file is read through without the lock, then the file is
			// looked up again: it may have moved or gone meanwhile.
			// It is read through no further than what could be stored: the
			// owner's quota, the storage budget and the free disk.
			room, e := a.extractRoom(u)
			if internal(w, e) {
				ok = false
			}
			limit = max(0, room) + tarSlack
			size := f.Size
			if ok {
				a.mu.Unlock()
				var e error
				items, e = tarItems(ctx, archive, size, format, limit)
				a.mu.Lock()
				switch {
				case errors.Is(e, errTooManyInArchive):
					ok = false
					fail(w, 413, "zip_too_many")
				case errors.Is(e, errArchiveTooLarge):
					ok = false
					fail(w, 413, "Din lagringskvot räcker inte")
				case errors.Is(e, context.DeadlineExceeded):
					ok = false
					fail(w, 503, "archive_timeout")
				case e != nil:
					ok = false
					fail(w, 400, "zip_invalid")
				}
			}
			if ok {
				if u, f, ok = a.versionedFile(w, r, false); ok && !a.allowed(w, u.ID, "upload", "folders") {
					ok = false
				} else if ok && f.Size != size {
					ok = false
					fail(w, 409, "archive_changed")
				} else if held := a.scanHeld(f.ID); ok && held != "" {
					ok = false
					failHeld(w, held)
				}
			}
		}
		var plan *extraction
		if ok {
			if plan, ok = a.planExtraction(w, u, f, items); ok {
				plan.format, plan.archive, plan.size, plan.limit = format, archive, f.Size, limit
			}
		}
		if ok {
			a.extracting[plan.share] = true
		}
		a.mu.Unlock()
		if !ok {
			return
		}
		e := plan.write(ctx)
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.extracting, plan.share)
		if e == nil {
			e = plan.finish(a)
		}
		if e != nil {
			_ = a.removeShare(plan.share)
			for _, folder := range plan.folders {
				_ = exec(a, "DELETE FROM folders WHERE id=? AND NOT EXISTS(SELECT 1 FROM library_files WHERE folder_id=?)", folder, folder)
			}
			if errors.Is(e, context.DeadlineExceeded) {
				fail(w, 503, "archive_timeout")
			} else {
				fail(w, 400, "zip_invalid")
			}
			return
		}
		// What comes out of an archive is scanned whenever scanning is on:
		// an archive too large to scan whole may hold anything.
		a.queueScans(plan.share, true)
		a.queueThumbnails(plan.share)
		respond(w, 201, map[string]any{"folder_id": plan.top, "name": plan.topName, "files": len(plan.entries), "skipped": plan.skipped})
	})
}

// extractRoom is how much the owner could still store. Caller holds mu.
func (a *App) extractRoom(u User) (int64, error) {
	var used, all, reserved int64
	if e := a.db.QueryRow("SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files x ON x.id=c.file_id JOIN shares s ON s.id=x.share_id WHERE s.owner=?", u.ID).Scan(&used); e != nil {
		return 0, e
	}
	if e := a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all); e != nil {
		return 0, e
	}
	if e := a.db.QueryRow("SELECT COALESCE(SUM(size-offset),0) FROM files").Scan(&reserved); e != nil {
		return 0, e
	}
	budget, e := a.storageBudget()
	if e != nil {
		return 0, e
	}
	free, e := a.diskFree()
	if e != nil {
		return 0, e
	}
	return min(u.Quota-used, budget-all, free-(512<<20)-reserved-a.tokenDiskHeld()), nil
}

type extraction struct {
	share, top, topName string
	folders             []string
	entries             []zipEntry
	skipped             int
	// Where the files are read from.
	format      string
	archive     io.ReaderAt
	size, limit int64
}

// planExtraction checks every limit and records the new folders and the
// files as unfinished uploads, which hold their space. Caller holds mu.
func (a *App) planExtraction(w http.ResponseWriter, u User, f LibraryFile, items []archiveItem) (*extraction, bool) {
	entries, dirs, skipped := archivePlan(items)
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
		size := entry.item.size
		if size > p.MaxFile {
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
	_, base := archiveFormat(f.Name)
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
		size := entry.item.size
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

// write unpacks every file; it runs without the app lock. A tar file is
// read through again, and each file must come where and as it was listed.
func (plan *extraction) write(ctx context.Context) error {
	if plan.format == "zip" {
		for _, entry := range plan.entries {
			if e := writeEntry(entry, entry.item.zip.Open); e != nil {
				return e
			}
		}
		return nil
	}
	reader, e := tarStream(ctx, plan.archive, plan.size, plan.format, plan.limit)
	if e != nil {
		return e
	}
	next := 0
	for index := 0; next < len(plan.entries); index++ {
		h, e := reader.Next()
		if e == io.EOF {
			e = io.ErrUnexpectedEOF
		}
		if e != nil {
			return e
		}
		entry := plan.entries[next]
		if index != entry.index {
			continue
		}
		if name, _ := tarName(h.Name); name != entry.item.name || h.Size != entry.item.size || h.Typeflag != tar.TypeReg {
			return errors.New("the archive is not the one that was listed")
		}
		if e := writeEntry(entry, func() (io.ReadCloser, error) { return io.NopCloser(reader), nil }); e != nil {
			return e
		}
		next++
	}
	return nil
}

func writeEntry(entry zipEntry, open func() (io.ReadCloser, error)) error {
	source, e := open()
	if e != nil {
		return e
	}
	defer source.Close()
	size := entry.item.size
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

// finish checks every unpacked file on disk, then marks them complete and
// shows them. Caller holds mu.
func (plan *extraction) finish(a *App) error {
	for _, entry := range plan.entries {
		info, e := os.Lstat(entry.path)
		if e != nil || !info.Mode().IsRegular() || info.Size() != entry.item.size {
			return errors.New("an unpacked file is missing or has the wrong size")
		}
	}
	res, e := a.db.Exec("UPDATE files SET offset=size WHERE share_id=?", plan.share)
	if e != nil {
		return e
	}
	if n, e := res.RowsAffected(); e != nil || n != int64(len(plan.entries)) {
		return errors.New("the unpacked files changed while they were written")
	}
	return exec(a, "UPDATE shares SET published=1 WHERE id=?", plan.share)
}

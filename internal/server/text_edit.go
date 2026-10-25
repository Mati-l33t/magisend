package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Text files and notes up to 256 KiB can be edited in the browser.
const textMaxBytes = 256 << 10

// Saves by the same person within this time are one version in the history,
// so autosave does not fill it.
const textMergeWindow = 10 * time.Minute
const textMaxVersions = 100

var textExtensions = map[string]bool{".txt": true, ".md": true, ".markdown": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".csv": true, ".go": true, ".py": true, ".rs": true, ".c": true, ".h": true, ".cpp": true, ".css": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".sql": true, ".sh": true}

func safeText(name string, data []byte) bool {
	if !textExtensions[strings.ToLower(filepath.Ext(name))] || len(data) > textMaxBytes || !utf8.Valid(data) {
		return false
	}
	for _, b := range data {
		if b < 32 && b != '\n' && b != '\r' && b != '\t' || b == 127 {
			return false
		}
	}
	return true
}
func migrateText(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS file_origins(file_id TEXT PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,name TEXT NOT NULL);
 INSERT OR IGNORE INTO file_origins(file_id,name) SELECT id,name FROM files;
 CREATE TABLE IF NOT EXISTS text_versions(file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,number INTEGER NOT NULL,blob TEXT NOT NULL UNIQUE,size INTEGER NOT NULL,name TEXT NOT NULL,created INTEGER NOT NULL,actor TEXT NOT NULL,PRIMARY KEY(file_id,number));
 CREATE VIEW IF NOT EXISTS storage_charges AS SELECT f.id AS file_id,f.size+COALESCE((SELECT SUM(v.size) FROM text_versions v WHERE v.file_id=f.id AND v.number<(SELECT MAX(newest.number) FROM text_versions newest WHERE newest.file_id=f.id)),0) AS size FROM files f;`)
	return e
}

var errBlobUnavailable = errors.New("storage object unavailable")

// Unversioned files retain their original blob identity. Lookup failures return
// an error instead of falling back to stale original bytes.
func (a *App) currentBlob(id string) (string, error) {
	loc, e := a.fileLocation(id)
	if e != nil {
		return "", e
	}
	dir, e := a.blobDir(loc)
	if e != nil {
		return "", e
	}
	var blob string
	e = a.db.QueryRow("SELECT blob FROM text_versions WHERE file_id=? ORDER BY number DESC LIMIT 1", id).Scan(&blob)
	if e == sql.ErrNoRows {
		return filepath.Join(dir, id), nil
	}
	if e != nil || !validToken(blob) {
		return "", errBlobUnavailable
	}
	return filepath.Join(dir, blob), nil
}

// openCurrent opens a file's current contents read-only, never following a
// symlink or blocking on a FIFO put in its place.
func (a *App) openCurrent(id string) (*os.File, error) {
	path, e := a.currentBlob(id)
	if e != nil {
		return nil, e
	}
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
func (a *App) readText(f File) ([]byte, bool) {
	var origin string
	if a.db.QueryRow("SELECT name FROM file_origins WHERE file_id=?", f.ID).Scan(&origin) != nil {
		return nil, false
	}
	if f.Offset != f.Size || f.Size < 0 || f.Size > textMaxBytes || !textExtensions[strings.ToLower(filepath.Ext(origin))] {
		return nil, false
	}
	file, e := a.openCurrent(f.ID)
	if e != nil {
		return nil, false
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
		return nil, false
	}
	data, e := io.ReadAll(io.LimitReader(file, textMaxBytes+1))
	return data, e == nil && int64(len(data)) == f.Size && safeText(f.Name, data)
}
func (a *App) textState(w http.ResponseWriter, f File) ([]byte, int, bool) {
	if held := a.scanHeld(f.ID); held != "" {
		failHeld(w, held)
		return nil, 0, false
	}
	data, ok := a.readText(f)
	if !ok {
		respond(w, 415, map[string]string{"error": "Unsupported or unsafe text file", "error_code": "text_unsupported"})
		return nil, 0, false
	}
	if internal(w, exec(a, "INSERT OR IGNORE INTO text_versions(file_id,number,blob,size,name,created,actor) VALUES(?,1,?,?,?,?,?)", f.ID, f.ID, f.Size, f.Name, time.Now().Unix(), "original")) {
		return nil, 0, false
	}
	var version int
	if internal(w, a.db.QueryRow("SELECT MAX(number) FROM text_versions WHERE file_id=?", f.ID).Scan(&version)) {
		return nil, 0, false
	}
	return data, version, true
}
func textUnsupported(w http.ResponseWriter) {
	respond(w, 415, map[string]string{"error": "Unsupported or unsafe text file", "error_code": "text_unsupported"})
}
func textConflict(w http.ResponseWriter) {
	respond(w, 409, map[string]string{"error": "File changed. Reload before saving.", "error_code": "text_conflict"})
}

// New bytes are durable before the atomic history+pointer commit. A failed
// commit never changes the readable current blob. Unreferenced random blobs can
// be safely collected at startup/cleanup without touching committed history.
func (a *App) saveText(w http.ResponseWriter, f File, user string, current int, data []byte, merge ...bool) bool {
	version, ok := a.storeText(w, f, user, current, data, merge...)
	if ok {
		respond(w, 200, map[string]any{"text": string(data), "version": version})
	}
	return ok
}

// storeText writes a new version and returns its number. When the newest
// version is the same person's from the last few minutes, the new one takes
// its place in the history; the number still goes up, so anyone editing the
// older text still gets a conflict.
func (a *App) storeText(w http.ResponseWriter, f File, user string, current int, data []byte, merge ...bool) (int, bool) {
	// A restore never merges: it must not replace the version it restores over.
	mayMerge := len(merge) == 0 || merge[0]
	fail := func() (int, bool) { return 0, false }
	if !safeText(f.Name, data) {
		textUnsupported(w)
		return fail()
	}
	if len(data) > textMaxBytes {
		textUnsupported(w)
		return fail()
	}
	var count int
	var replaced, replacedBlob string
	var newestActor string
	var newestAt int64
	if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM text_versions WHERE file_id=?", f.ID).Scan(&count)) {
		return fail()
	}
	if mayMerge && current > 1 && a.db.QueryRow("SELECT actor,created,blob FROM text_versions WHERE file_id=? AND number=?", f.ID, current).Scan(&newestActor, &newestAt, &replacedBlob) == nil &&
		newestActor == user && time.Since(time.Unix(newestAt, 0)) < textMergeWindow && replacedBlob != f.ID && validToken(replacedBlob) {
		replaced = replacedBlob
		count--
	}
	store, ok := a.versionRoom(w, f, int64(len(data)), count)
	if !ok {
		return fail()
	}
	blob := randomID()
	path := filepath.Join(store, blob)
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if internal(w, e) {
		return fail()
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(path)
		}
	}()
	_, e = file.Write(data)
	if e == nil {
		e = file.Sync()
	}
	closeErr := file.Close()
	if e == nil {
		e = closeErr
	}
	if internal(w, e) {
		return fail()
	}
	dir, e := os.Open(store)
	if internal(w, e) {
		return fail()
	}
	e = dir.Sync()
	closeErr = dir.Close()
	if e == nil {
		e = closeErr
	}
	if internal(w, e) {
		return fail()
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return fail()
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO text_versions(file_id,number,blob,size,name,created,actor) VALUES(?,?,?,?,?,?,?)", f.ID, current+1, blob, len(data), f.Name, time.Now().Unix(), user); internal(w, e) {
		return fail()
	}
	if replaced != "" {
		if _, e = tx.Exec("DELETE FROM text_versions WHERE file_id=? AND number=?", f.ID, current); internal(w, e) {
			return fail()
		}
	}
	if _, e = tx.Exec("UPDATE files SET size=?,offset=? WHERE id=?", len(data), len(data), f.ID); internal(w, e) {
		return fail()
	}
	if _, e = tx.Exec("INSERT OR IGNORE INTO library_files(file_id) VALUES(?)", f.ID); internal(w, e) {
		return fail()
	}
	if _, e = tx.Exec("DELETE FROM thumbnails WHERE file_id=?", f.ID); internal(w, e) {
		return fail()
	}
	if internal(w, tx.Commit()) {
		return fail()
	}
	committed = true
	a.thumbBlocked.Delete(f.ID)
	if replaced != "" {
		_ = a.discardStored(filepath.Join(store, replaced))
	}
	// Text written by someone other than the owner counts as received.
	var owner string
	_ = a.db.QueryRow("SELECT s.owner FROM files f JOIN shares s ON s.id=f.share_id WHERE f.id=?", f.ID).Scan(&owner)
	a.queueFileScan(f.ID, user != owner)
	return current + 1, true
}

// versionRoom checks that a new version of size bytes fits every limit the
// file is under, and returns the folder it is stored in. Caller holds mu.
func (a *App) versionRoom(w http.ResponseWriter, f File, size int64, current int) (string, bool) {
	p, e := a.policy()
	if internal(w, e) {
		return "", false
	}
	if size > p.MaxFile {
		fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
		return "", false
	}
	// The limit is on the versions kept, not on the highest number: merged
	// autosaves raise the number without keeping a version.
	if count, e := a.versionCount(f.ID); internal(w, e) {
		return "", false
	} else if count >= textMaxVersions {
		fail(w, 429, "metadata_limit")
		return "", false
	}
	var owner string
	var quota, used, all int64
	if internal(w, a.db.QueryRow("SELECT s.owner,u.quota FROM shares s JOIN users u ON u.id=s.owner WHERE s.id=(SELECT share_id FROM files WHERE id=?)", f.ID).Scan(&owner, &quota)) {
		return "", false
	}
	var folder string
	if internal(w, a.db.QueryRow("SELECT COALESCE((SELECT folder_id FROM library_files WHERE file_id=?),'')", f.ID).Scan(&folder)) {
		return "", false
	}
	if !a.libraryGrowthAllowed(w, owner, f.ID, size, folder) {
		return "", false
	}
	// Legacy direct shares containing this file are also subject to MaxShare.
	var legacyTotal int64
	var private bool
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0),EXISTS(SELECT 1 FROM library_containers c WHERE c.share_id=(SELECT share_id FROM files WHERE id=?)) FROM files WHERE share_id=(SELECT share_id FROM files WHERE id=?)", f.ID, f.ID).Scan(&legacyTotal, &private)) {
		return "", false
	}
	if !private && size > f.Size && size-f.Size > p.MaxShare-legacyTotal {
		fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
		return "", false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files f ON f.id=c.file_id JOIN shares s ON s.id=f.share_id WHERE s.owner=?", owner).Scan(&used)) {
		return "", false
	}
	if size > quota-used {
		fail(w, 413, "Din lagringskvot räcker inte")
		return "", false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all)) {
		return "", false
	}
	budget, err := a.storageBudget()
	if internal(w, err) {
		return "", false
	}
	if size > budget-all {
		fail(w, 507, "Appens lagringsbudget är full")
		return "", false
	}
	// A new version is stored next to the file's other versions.
	loc, e := a.fileLocation(f.ID)
	if internal(w, e) {
		return "", false
	}
	store, e := a.blobDir(loc)
	if internal(w, e) {
		return "", false
	}
	space, e := a.locationSpace(loc)
	if internal(w, e) {
		return "", false
	}
	var reserved int64
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size-offset),0) FROM files").Scan(&reserved)) {
		return "", false
	}
	if size+reserved > space.free-(512<<20) {
		fail(w, 507, "Otillräckligt ledigt diskutrymme")
		return "", false
	}
	return store, true
}
func (a *App) textRoutes(m *http.ServeMux) {
	for _, route := range []string{"GET /api/library/files/{file}/text", "PUT /api/library/files/{file}/text", "GET /api/library/files/{file}/versions", "POST /api/library/files/{file}/restore"} {
		m.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
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
			var published bool
			if internal(w, a.db.QueryRow("SELECT published FROM shares WHERE id=?", f.ShareID).Scan(&published)) {
				return
			}
			if !published {
				fail(w, 409, "upload_incomplete")
				return
			}
			a.serveText(w, r, f.File, u.ID, false)
		})
	}
}
func (a *App) serveText(w http.ResponseWriter, r *http.Request, f File, user string, shared bool) {
	if r.Method != "GET" && !shared && !a.allowed(w, user, "edit_own") {
		return
	}
	data, version, ok := a.textState(w, f)
	if !ok {
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, version))
	if r.Method == "GET" {
		if strings.HasSuffix(r.URL.Path, "/versions") {
			rows, e := a.db.Query("SELECT number,size,name,created,actor FROM text_versions WHERE file_id=? ORDER BY number DESC", f.ID)
			if internal(w, e) {
				return
			}
			defer rows.Close()
			history := []map[string]any{}
			for rows.Next() {
				var number int
				var size, created int64
				var name, actor string
				if internal(w, rows.Scan(&number, &size, &name, &created, &actor)) {
					return
				}
				history = append(history, map[string]any{"version": number, "size": size, "name": name, "created": created, "actor": actor})
			}
			if !internal(w, rows.Err()) {
				respond(w, 200, history)
			}
			return
		}
		respond(w, 200, map[string]any{"text": string(data), "version": version})
		return
	}
	var b struct {
		Version int    `json:"version"`
		Text    string `json:"text"`
		Restore int    `json:"restore"`
	}
	// Raw JSON text bodies need room for escape expansion, but decoded text is
	// independently bounded to textMaxBytes. Invalid UTF-8 must not be silently repaired.
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*textMaxBytes))
	if e != nil || !utf8.Valid(raw) {
		textUnsupported(w)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&b) != nil {
		fail(w, 400, "Ogiltiga indata")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		fail(w, 400, "Ogiltiga indata")
		return
	}
	if b.Version != version {
		textConflict(w)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/restore") {
		var blob, name string
		var size int64
		e = a.db.QueryRow("SELECT blob,name,size FROM text_versions WHERE file_id=? AND number=?", f.ID, b.Restore).Scan(&blob, &name, &size)
		if e != nil || !validToken(blob) || size > textMaxBytes {
			fail(w, 404, "not_found")
			return
		}
		// An earlier version can be infected too.
		if held := a.blobHeld(f.ID, blob); held != "" {
			failHeld(w, held)
			return
		}
		path, e := a.fileBlob(f.ID, blob)
		if internal(w, e) {
			return
		}
		file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if errors.Is(e, fs.ErrNotExist) {
			fail(w, 404, "file_not_found")
			return
		}
		if internal(w, e) {
			return
		}
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() != size {
			file.Close()
			fail(w, 503, "storage_unverified")
			return
		}
		data, e = io.ReadAll(io.LimitReader(file, textMaxBytes+1))
		file.Close()
		if internal(w, e) {
			return
		}
	} else {
		data = []byte(b.Text)
	}
	a.saveText(w, f, user, version, data, b.Restore == 0)
}

// versionCount is how many versions of a file are kept.
func (a *App) versionCount(file string) (int, error) {
	var n int
	e := a.db.QueryRow("SELECT COUNT(*) FROM text_versions WHERE file_id=?", file).Scan(&n)
	return n, e
}

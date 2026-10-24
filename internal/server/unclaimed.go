package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Stored files are named by random IDs; their names, owners and folders live
// in the database. So that a file the database no longer knows — after an
// older database was restored, say — is never lost, MagiSend keeps a small
// index next to the files in every storage folder, and sets unknown files
// aside in an .unclaimed folder for 30 days instead of deleting them.
// Administrators can put them back into the library, with their names,
// owners and folders when the index knew them, or delete them.
const (
	storageIndexFile = ".magisend-index.json"
	unclaimedDir     = ".unclaimed"
	unclaimedDays    = 30
	maxIndexBytes    = 256 << 20
	maxUnclaimedList = 200
	recoveredFolder  = "Recovered files"
)

type indexEntry struct {
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
	// The owner's account ID; files only go back to that account, never to
	// whoever has the name now.
	OwnerID string `json:"owner_id,omitempty"`
	Folder  string `json:"folder,omitempty"`
	Created int64  `json:"created,omitempty"`
	Size    int64  `json:"size"`
	// Set for an earlier version of a file whose text was edited or replaced.
	Version int `json:"version,omitempty"`
}

type storageIndex struct {
	Written int64                 `json:"written"`
	Files   map[string]indexEntry `json:"files"`
}

// UnclaimedFile is one file set aside, with what the index knew about it.
type UnclaimedFile struct {
	indexEntry
	ID       string `json:"id"`
	Location string `json:"location"`
	Path     string `json:"path"`
	SetAside int64  `json:"set_aside"`
	Known    bool   `json:"known"`
}

func readStorageIndex(dir string) map[string]indexEntry {
	data, e := readLimited(filepath.Join(dir, storageIndexFile), maxIndexBytes)
	if e != nil {
		return nil
	}
	var index storageIndex
	if json.Unmarshal(data, &index) != nil {
		return nil
	}
	return index.Files
}

// writeAtomically replaces path with data, never following a link.
func writeAtomically(path string, data []byte) error {
	tmp := path + ".tmp-" + randomID()
	file, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	if _, e = file.Write(data); e == nil {
		e = file.Sync()
	}
	if closeErr := file.Close(); e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(tmp, path)
	}
	if e != nil {
		_ = os.Remove(tmp)
	}
	return e
}

// setAside moves an unknown stored file into the location's .unclaimed folder
// with what the index knew about it.
func setAside(dir, name string, entry *indexEntry, now time.Time) error {
	held := filepath.Join(dir, unclaimedDir)
	if e := os.Mkdir(held, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if info, e := os.Lstat(held); e != nil || !info.IsDir() {
		return errors.New("the .unclaimed folder is not a folder")
	}
	record := struct {
		indexEntry
		SetAside int64 `json:"set_aside"`
		Known    bool  `json:"known"`
	}{SetAside: now.Unix()}
	if entry != nil {
		record.indexEntry, record.Known = *entry, true
	}
	data, _ := json.Marshal(record)
	if e := writeAtomically(filepath.Join(held, name+".json"), data); e != nil {
		return e
	}
	return os.Rename(filepath.Join(dir, name), filepath.Join(held, name))
}

// writeStorageIndexes records every stored file's name, owner and folder next
// to the files, in each reachable location, when anything changed. Caller
// holds mu.
func (a *App) writeStorageIndexes(now time.Time) error {
	folders := map[string][2]string{}
	rows, e := a.db.Query("SELECT id,parent,name FROM folders")
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, parent, name string
		if e = rows.Scan(&id, &parent, &name); e != nil {
			rows.Close()
			return e
		}
		folders[id] = [2]string{parent, name}
	}
	rows.Close()
	folderPath := func(id string) string {
		var parts []string
		for seen := 0; id != "" && seen < 64; seen++ {
			f, ok := folders[id]
			if !ok {
				break
			}
			parts = append([]string{f[1]}, parts...)
			id = f[0]
		}
		return strings.Join(parts, "/")
	}
	indexes := map[string]map[string]indexEntry{}
	add := func(loc, blob string, entry indexEntry) {
		if indexes[loc] == nil {
			indexes[loc] = map[string]indexEntry{}
		}
		indexes[loc][blob] = entry
	}
	rows, e = a.db.Query(`SELECT f.id,f.backend,f.name,COALESCE(u.username,''),COALESCE(s.owner,''),COALESCE(l.folder_id,''),s.created,f.size
 FROM files f JOIN shares s ON s.id=f.share_id LEFT JOIN users u ON u.id=s.owner LEFT JOIN library_files l ON l.file_id=f.id`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, loc, name, owner, ownerID, folder string
		var created, size int64
		if e = rows.Scan(&id, &loc, &name, &owner, &ownerID, &folder, &created, &size); e != nil {
			rows.Close()
			return e
		}
		add(loc, id, indexEntry{Name: name, Owner: owner, OwnerID: ownerID, Folder: folderPath(folder), Created: created, Size: size})
	}
	rows.Close()
	rows, e = a.db.Query(`SELECT v.blob,f.backend,v.name,COALESCE(u.username,''),COALESCE(s.owner,''),COALESCE(l.folder_id,''),v.created,v.size,v.number
 FROM text_versions v JOIN files f ON f.id=v.file_id JOIN shares s ON s.id=f.share_id LEFT JOIN users u ON u.id=s.owner LEFT JOIN library_files l ON l.file_id=f.id
 WHERE v.blob<>f.id`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var blob, loc, name, owner, ownerID, folder string
		var created, size int64
		var number int
		if e = rows.Scan(&blob, &loc, &name, &owner, &ownerID, &folder, &created, &size, &number); e != nil {
			rows.Close()
			return e
		}
		add(loc, blob, indexEntry{Name: name, Owner: owner, OwnerID: ownerID, Folder: folderPath(folder), Created: created, Size: size, Version: number})
	}
	rows.Close()

	a.stores.mu.RLock()
	locations := []string{localLocation}
	for id := range a.stores.locations {
		locations = append(locations, id)
	}
	a.stores.mu.RUnlock()
	var failures []error
	for _, loc := range locations {
		dir, e := a.blobDir(loc)
		if e != nil {
			continue
		}
		files := indexes[loc]
		if files == nil {
			files = map[string]indexEntry{}
		}
		body, _ := json.Marshal(files)
		sum := sha256.Sum256(body)
		if a.indexSums[loc] == sum {
			continue
		}
		data, _ := json.Marshal(storageIndex{Written: now.Unix(), Files: files})
		if e = writeAtomically(filepath.Join(dir, storageIndexFile), data); e != nil {
			failures = append(failures, e)
			continue
		}
		a.indexSums[loc] = sum
	}
	return errors.Join(failures...)
}

// unclaimed lists what was set aside, oldest first. Caller holds mu.
func (a *App) unclaimed() []UnclaimedFile {
	a.stores.mu.RLock()
	locations := []string{localLocation}
	for id := range a.stores.locations {
		locations = append(locations, id)
	}
	a.stores.mu.RUnlock()
	list := []UnclaimedFile{}
	for _, loc := range locations {
		dir, e := a.blobDir(loc)
		if e != nil {
			continue
		}
		held := filepath.Join(dir, unclaimedDir)
		entries, _ := os.ReadDir(held)
		for _, entry := range entries {
			name := entry.Name()
			if !validToken(name) || !entry.Type().IsRegular() {
				continue
			}
			f := UnclaimedFile{ID: name, Location: loc, Path: a.locationLabel(loc)}
			if data, e := readLimited(filepath.Join(held, name+".json"), 64<<10); e == nil {
				var record struct {
					indexEntry
					SetAside int64 `json:"set_aside"`
					Known    bool  `json:"known"`
				}
				if json.Unmarshal(data, &record) == nil {
					f.indexEntry, f.SetAside, f.Known = record.indexEntry, record.SetAside, record.Known
				}
			}
			if info, e := entry.Info(); e == nil {
				f.Size = info.Size()
				if f.SetAside == 0 {
					f.SetAside = info.ModTime().Unix()
				}
			}
			list = append(list, f)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].SetAside < list[j].SetAside })
	return list
}

func (a *App) heldPath(f UnclaimedFile) (string, error) {
	dir, e := a.blobDir(f.Location)
	if e != nil {
		return "", e
	}
	return filepath.Join(dir, unclaimedDir, f.ID), nil
}

func removeUnclaimed(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + ".json")
}

// expireUnclaimed deletes what was set aside more than 30 days ago. Caller
// holds mu.
func (a *App) expireUnclaimed(now time.Time) {
	for _, f := range a.unclaimed() {
		if now.Sub(time.Unix(f.SetAside, 0)) < unclaimedDays*24*time.Hour {
			continue
		}
		if path, e := a.heldPath(f); e == nil {
			removeUnclaimed(path)
		}
	}
}

// recoveredName gives a file without a known name one that shows its date and
// kind.
func recoveredName(path string, when time.Time, n int) string {
	extension := ""
	if file, e := os.Open(path); e == nil {
		head := make([]byte, 512)
		read, _ := io.ReadFull(file, head)
		file.Close()
		kind := http.DetectContentType(head[:read])
		for prefix, ext := range map[string]string{
			"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
			"application/pdf": ".pdf", "video/mp4": ".mp4", "video/webm": ".webm", "audio/mpeg": ".mp3",
			"application/zip": ".zip", "text/plain": ".txt", "text/html": ".html.txt",
		} {
			if strings.HasPrefix(kind, prefix) {
				extension = ext
			}
		}
	}
	return fmt.Sprintf("Recovered %s (%d)%s", when.Format("2006-01-02"), n, extension)
}

// ensureFolders returns the folder at path ("A/B") in user's library,
// creating what is missing. Caller holds mu.
func (a *App) ensureFolders(user, path string) (string, error) {
	parent := ""
	for _, name := range strings.Split(path, "/") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var id string
		e := a.db.QueryRow("SELECT id FROM folders WHERE owner=? AND parent=? AND name=?", user, parent, name).Scan(&id)
		if e != nil {
			if !validFolder(name) {
				return parent, nil
			}
			id = randomID()
			if _, e = a.db.Exec("INSERT INTO folders(id,owner,parent,name) VALUES(?,?,?,?)", id, user, parent, name); e != nil {
				return "", e
			}
		}
		parent = id
	}
	return parent, nil
}

// restoreUnclaimed puts one set-aside file back into a library: to its owner
// and folder when known, otherwise into the administrator's Recovered files
// folder. Caller holds mu.
func (a *App) restoreUnclaimed(f UnclaimedFile, admin User, n int) error {
	held, e := a.heldPath(f)
	if e != nil {
		return e
	}
	owner := admin.ID
	name, folderPath := f.Name, f.Folder
	if f.Known {
		// Only the very account that owned the file gets it back. When that
		// account is gone (or the record predates owner IDs), it goes to the
		// administrator, in a folder named after the old owner.
		var n int
		if f.OwnerID != "" && a.db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", f.OwnerID).Scan(&n) == nil && n == 1 {
			owner = f.OwnerID
		} else {
			who := f.Owner
			if who == "" {
				who = "unknown owner"
			}
			folderPath = recoveredFolder + "/" + who + "/" + f.Folder
		}
	}
	if !f.Known || name == "" {
		name = recoveredName(held, time.Unix(f.SetAside, 0), n)
		folderPath = recoveredFolder
	} else if f.Version > 0 {
		base := strings.TrimSuffix(name, filepath.Ext(name))
		name = fmt.Sprintf("%s (version %d)%s", base, f.Version, filepath.Ext(name))
	}
	if !validFilename(name) {
		name = recoveredName(held, time.Unix(f.SetAside, 0), n)
	}
	folder, e := a.ensureFolders(owner, folderPath)
	if e != nil {
		return e
	}
	info, e := os.Lstat(held)
	if e != nil || !info.Mode().IsRegular() {
		return errors.New("the file is gone")
	}
	dir := filepath.Dir(filepath.Dir(held))
	id := f.ID
	var taken int
	if e = a.db.QueryRow("SELECT COUNT(*) FROM files WHERE id=?", id).Scan(&taken); e != nil {
		return e
	}
	if taken > 0 {
		id = randomID()
	}
	target := filepath.Join(dir, id)
	if _, e = os.Lstat(target); e == nil {
		return errors.New("a stored file of that name already exists")
	}
	if e = os.Rename(held, target); e != nil {
		return e
	}
	share := randomID()
	var folderValue any
	if folder != "" {
		folderValue = folder
	}
	tx, e := a.db.Begin()
	if e == nil {
		defer tx.Rollback()
		for _, step := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO shares(id,owner,title,expires,created,published) VALUES(?,?,?,0,?,1)", []any{share, owner, "Library", time.Now().Unix()}},
			{"INSERT INTO library_containers(share_id) VALUES(?)", []any{share}},
			{"INSERT INTO files(id,share_id,name,size,offset,backend) VALUES(?,?,?,?,?,?)", []any{id, share, name, info.Size(), info.Size(), f.Location}},
			{"INSERT INTO file_origins(file_id,name) VALUES(?,?)", []any{id, name}},
			{"INSERT INTO library_files(file_id,folder_id) VALUES(?,?)", []any{id, folderValue}},
		} {
			if _, e = tx.Exec(step.query, step.args...); e != nil {
				break
			}
		}
		if e == nil {
			e = tx.Commit()
		}
	}
	if e != nil {
		_ = os.Rename(target, held)
		return e
	}
	_ = os.Remove(held + ".json")
	return nil
}

func (a *App) unclaimedRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/storage/unclaimed", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		list := a.unclaimed()
		var total int64
		for _, f := range list {
			total += f.Size
		}
		shown := list
		if len(shown) > maxUnclaimedList {
			shown = shown[:maxUnclaimedList]
		}
		respond(w, 200, map[string]any{"count": len(list), "size": total, "days": unclaimedDays, "files": shown})
	})
	m.HandleFunc("POST /api/admin/storage/unclaimed/restore", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		restored, failed := 0, 0
		for i, f := range a.unclaimed() {
			if e := a.restoreUnclaimed(f, admin, i+1); e != nil {
				failed++
				continue
			}
			restored++
		}
		a.audit(r, admin.Username, "storage.unclaimed_restored", fmt.Sprint(restored))
		_ = a.writeStorageIndexes(time.Now())
		respond(w, 200, map[string]int{"restored": restored, "failed": failed})
	})
	// Files the database has whose stored data is gone (after an older backup
	// was restored, say): listed so the administrator can remove them.
	// The disks are only read after mu is released: a hung share or a slow
	// one with many files must not stop the app.
	m.HandleFunc("GET /api/admin/storage/missing", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		_, ok := a.require(w, r, true)
		var files []storedFile
		var e error
		if ok {
			files, e = a.storedFiles()
		}
		a.mu.Unlock()
		if !ok || internal(w, e) {
			return
		}
		c := a.checkStored(files)
		missing, library := []MissingFile{}, 0
		for _, f := range c.missing {
			missing = append(missing, f.MissingFile)
			if f.Library {
				library++
			}
		}
		shown := missing
		if len(shown) > maxUnclaimedList {
			shown = shown[:maxUnclaimedList]
		}
		respond(w, 200, map[string]any{"count": len(missing), "library": library, "files": shown, "wrong": nonNil(c.wrong), "unreachable": nonNil(c.unreachable)})
	})
	// Only library files whose data is not there at all are removed, never
	// any on a location that looks like the wrong disk, and each is checked
	// again under mu just before.
	m.HandleFunc("DELETE /api/admin/storage/missing", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		admin, ok := a.require(w, r, true)
		var files []storedFile
		var e error
		if ok {
			files, e = a.storedFiles()
		}
		a.mu.Unlock()
		if !ok || internal(w, e) {
			return
		}
		c := a.checkStored(files)
		if len(c.wrong) > 0 {
			fail(w, 409, "storage_wrong_disk")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		removed := 0
		for _, f := range c.missing {
			if !f.Library || !c.gone[f.ID] {
				continue
			}
			if !a.stillGone(f) {
				continue
			}
			if a.removeLibraryFile(f.ID) == nil {
				removed++
			}
		}
		a.audit(r, admin.Username, "storage.missing_removed", fmt.Sprint(removed))
		respond(w, 200, map[string]int{"removed": removed})
	})
	m.HandleFunc("DELETE /api/admin/storage/unclaimed", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		deleted := 0
		for _, f := range a.unclaimed() {
			if path, e := a.heldPath(f); e == nil {
				removeUnclaimed(path)
				deleted++
			}
		}
		a.audit(r, admin.Username, "storage.unclaimed_deleted", fmt.Sprint(deleted))
		respond(w, 200, map[string]int{"deleted": deleted})
	})
}

// nonNil keeps an empty list a list in JSON.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

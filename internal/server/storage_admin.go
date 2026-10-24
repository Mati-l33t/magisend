package server

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type moveStatus struct {
	Running   bool   `json:"running"`
	Target    string `json:"target"`
	Files     int    `json:"files"`
	Done      int    `json:"done"`
	Bytes     int64  `json:"bytes"`
	DoneBytes int64  `json:"done_bytes"`
	Failed    int    `json:"failed"`
	// Why the last move stopped early, as an error code.
	Error    string `json:"error"`
	Finished int64  `json:"finished"`
}

type storageCandidate struct {
	Path  string `json:"path"`
	Type  string `json:"type"`
	Free  int64  `json:"free_bytes"`
	Total int64  `json:"total_bytes"`
}

// Folders that never hold user files, even when something is mounted there.
var systemFolders = []string{"/proc", "/sys", "/dev", "/run", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/var/lib/docker"}

func systemFolder(path string) bool {
	if path == "/" {
		return true
	}
	for _, s := range systemFolders {
		if path == s || strings.HasPrefix(path, s+"/") {
			return true
		}
	}
	return false
}

func within(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, strings.TrimSuffix(parent, "/")+"/")
}

var pseudoFilesystems = map[string]bool{"proc": true, "sysfs": true, "devpts": true, "tmpfs": true, "devtmpfs": true, "cgroup": true, "cgroup2": true, "mqueue": true, "securityfs": true, "debugfs": true, "tracefs": true, "fusectl": true, "configfs": true, "pstore": true, "bpf": true, "autofs": true, "hugetlbfs": true, "binfmt_misc": true, "nsfs": true, "rpc_pipefs": true, "efivarfs": true, "ramfs": true, "squashfs": true, "overlay": true, "fuse.lxcfs": true}

// mountCandidates lists mounted drives and shares that could hold files, so
// the administrator can pick one instead of typing a path.
func (a *App) mountCandidates() []storageCandidate {
	data, e := os.ReadFile("/proc/self/mountinfo")
	if e != nil {
		return nil
	}
	root, _ := filepath.EvalSymlinks(a.root)
	seen := map[string]bool{}
	found := []storageCandidate{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		separator := -1
		for i, f := range fields {
			if f == "-" {
				separator = i
				break
			}
		}
		if len(fields) < 5 || separator < 0 || separator+1 >= len(fields) {
			continue
		}
		path, e := strconv.Unquote(`"` + strings.ReplaceAll(fields[4], `"`, `\"`) + `"`)
		if e != nil {
			path = fields[4]
		}
		kind := fields[separator+1]
		if pseudoFilesystems[kind] || systemFolder(path) || seen[path] || root != "" && (within(path, root) || within(root, path)) {
			continue
		}
		seen[path] = true
		label := kind
		switch {
		case kind == "cifs" || kind == "smb3":
			label = "SMB"
		case strings.HasPrefix(kind, "nfs"):
			label = "NFS"
		}
		c := storageCandidate{Path: path, Type: label}
		type result struct {
			space diskSpace
			err   error
		}
		r := timed(func() result {
			s, e := statDisk(path)
			return result{s, e}
		}, result{err: errStorageOffline})
		if r.err != nil {
			continue
		}
		c.Free, c.Total = r.space.free, r.space.total
		found = append(found, c)
	}
	return found
}

// addLocation checks that a folder can hold files and puts the marker in it.
// It returns an error code for the administrator on failure.
func (a *App) addLocation(raw, language string) (string, string) {
	path := strings.TrimSpace(raw)
	if !filepath.IsAbs(path) || len(path) > 1024 || strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 }) {
		return "", "storage_path_invalid"
	}
	path = filepath.Clean(path)
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", "storage_path_missing"
	}
	if info, e := os.Stat(real); e != nil || !info.IsDir() {
		return "", "storage_path_missing"
	}
	root, e := filepath.EvalSymlinks(a.root)
	if e != nil || systemFolder(real) || within(real, root) || within(root, real) {
		return "", "storage_path_invalid"
	}
	var n int
	if e = a.db.QueryRow("SELECT COUNT(*) FROM storage_locations WHERE path=?", real).Scan(&n); e != nil || n > 0 {
		return "", "storage_exists"
	}
	dir := filepath.Join(real, storageDirName)
	if entries, e := os.ReadDir(dir); e == nil {
		// A folder another installation uses, or anything else already there,
		// is never taken over: the cleanup would delete what it does not know.
		if len(entries) > 0 {
			return "", "storage_in_use"
		}
	} else if !os.IsNotExist(e) {
		return "", "storage_not_writable"
	} else if e = os.Mkdir(dir, 0700); e != nil {
		return "", "storage_not_writable"
	}
	id := randomID()
	temp := filepath.Join(dir, storageMarker+".tmp")
	e = os.WriteFile(temp, []byte(id+"\n"), 0600)
	if e == nil {
		var f *os.File
		if f, e = os.OpenFile(temp, os.O_RDWR, 0); e == nil {
			e = f.Sync()
			f.Close()
		}
	}
	if e == nil {
		e = os.Rename(temp, filepath.Join(dir, storageMarker))
	}
	if e == nil {
		e = syncDir(dir)
	}
	if e != nil || !probeLocation(real, id) {
		_ = os.Remove(temp)
		_ = os.Remove(filepath.Join(dir, storageMarker))
		_ = os.Remove(dir)
		return "", "storage_not_writable"
	}
	if e = exec(a, "INSERT INTO storage_locations(id,path,language,created) VALUES(?,?,?,?)", id, real, emailLanguage(language), time.Now().Unix()); e != nil {
		_ = os.Remove(filepath.Join(dir, storageMarker))
		_ = os.Remove(dir)
		return "", "storage_exists"
	}
	a.stores.mu.Lock()
	a.stores.locations[id] = &storageLocation{ID: id, Path: real, Language: emailLanguage(language), online: true, changed: time.Now()}
	a.stores.mu.Unlock()
	return id, ""
}

// copyBlob copies one stored object and reads the copy back to compare its
// checksum. It reports false when the source does not exist.
func copyBlob(source, target string) (bool, error) {
	in, e := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil {
		return false, e
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("not a regular file")
	}
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return false, e
	}
	sum := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, sum), in)
	if e == nil {
		e = out.Sync()
	}
	if closeErr := out.Close(); e == nil {
		e = closeErr
	}
	if e == nil && n != info.Size() {
		e = fmt.Errorf("copy is incomplete")
	}
	if e == nil {
		e = sameContent(target, sum.Sum(nil))
	}
	if e != nil {
		_ = os.Remove(target)
		return false, e
	}
	return true, nil
}

func sameContent(path string, want []byte) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	sum := sha256.New()
	if _, e = io.Copy(sum, f); e != nil {
		return e
	}
	if !bytes.Equal(sum.Sum(nil), want) {
		return fmt.Errorf("copy differs from the original")
	}
	return nil
}

// moveSnapshot reads, under mu, where a file is and which objects belong to
// it. An empty source means the file needs no move.
func (a *App) moveSnapshot(id, target string) (source, sourceDir, targetDir string, names []string, e error) {
	var size, offset int64
	e = a.db.QueryRow(`SELECT f.backend,f.size,f.offset FROM files f WHERE f.id=? AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id) AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=f.share_id)`, id).Scan(&source, &size, &offset)
	if e == sql.ErrNoRows || e == nil && (source == target || offset != size) {
		return "", "", "", nil, nil
	}
	if e != nil {
		return "", "", "", nil, e
	}
	if sourceDir, e = a.blobDir(source); e != nil {
		return "", "", "", nil, e
	}
	if targetDir, e = a.blobDir(target); e != nil {
		return "", "", "", nil, e
	}
	rows, e := a.db.Query("SELECT blob FROM text_versions WHERE file_id=? UNION SELECT ?", id, id)
	if e != nil {
		return "", "", "", nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return "", "", "", nil, e
		}
		if !validToken(name) {
			return "", "", "", nil, fmt.Errorf("invalid blob key")
		}
		names = append(names, name)
	}
	return source, sourceDir, targetDir, names, rows.Err()
}

// moveFile copies a file's objects without holding mu, then switches the file
// to the new location in one step. Versions saved during the copy are copied
// under mu. The old copies are removed afterwards; what fails there is
// collected by the regular cleanup.
func (a *App) moveFile(id, target string) error {
	a.mu.Lock()
	source, sourceDir, targetDir, names, e := a.moveSnapshot(id, target)
	a.mu.Unlock()
	if e != nil || source == "" {
		return e
	}
	copied := map[string]bool{}
	discard := func() {
		for name := range copied {
			_ = os.Remove(filepath.Join(targetDir, movingPrefix+name))
		}
	}
	copyOne := func(name string) error {
		ok, e := copyBlob(filepath.Join(sourceDir, name), filepath.Join(targetDir, movingPrefix+name))
		if ok {
			copied[name] = true
		}
		return e
	}
	for _, name := range names {
		if e = copyOne(name); e != nil {
			discard()
			return e
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	again, againSource, againTarget, current, e := a.moveSnapshot(id, target)
	if e != nil || again != source || againSource != sourceDir || againTarget != targetDir {
		discard()
		return e
	}
	for _, name := range current {
		if !copied[name] {
			if e = copyOne(name); e != nil {
				discard()
				return e
			}
		}
	}
	for name := range copied {
		if e = os.Rename(filepath.Join(targetDir, movingPrefix+name), filepath.Join(targetDir, name)); e != nil {
			discard()
			return e
		}
	}
	if e = syncDir(targetDir); e != nil {
		return e
	}
	if e = exec(a, "UPDATE files SET backend=? WHERE id=? AND backend=?", target, id, source); e != nil {
		return e
	}
	for _, name := range current {
		_ = a.discardStored(filepath.Join(sourceDir, name))
	}
	return nil
}

// reachable probes a location now instead of waiting for the next check.
func (a *App) reachable(loc string) bool {
	if loc == localLocation {
		return true
	}
	a.stores.mu.RLock()
	l := a.stores.locations[loc]
	a.stores.mu.RUnlock()
	if l == nil {
		return false
	}
	return timed(func() bool { return probeLocation(l.Path, l.ID) }, false)
}

func (a *App) moveFiles(target string, ids []string, sizes []int64, stop chan struct{}) {
	finish := func(code string) {
		a.stores.mu.Lock()
		a.stores.move.Running, a.stores.move.Error, a.stores.move.Finished = false, code, time.Now().Unix()
		a.stores.mu.Unlock()
	}
	for i, id := range ids {
		select {
		case <-stop:
			finish("stopped")
			return
		default:
		}
		var source string
		_ = a.db.QueryRow("SELECT backend FROM files WHERE id=?", id).Scan(&source)
		e := a.moveFile(id, target)
		if e != nil && (errors.Is(e, errStorageOffline) || !a.reachable(target) || source != "" && !a.reachable(source)) {
			finish("storage_offline")
			return
		}
		a.stores.mu.Lock()
		if e != nil {
			log.Printf("moving a file failed: %v", e)
			a.stores.move.Failed++
		}
		a.stores.move.Done++
		a.stores.move.DoneBytes += sizes[i]
		a.stores.mu.Unlock()
	}
	finish("")
}

func (a *App) storageStatus() (map[string]any, error) {
	type location struct {
		ID     string `json:"id"`
		Path   string `json:"path"`
		Online bool   `json:"online"`
		Active bool   `json:"active"`
		Free   int64  `json:"free_bytes"`
		Total  int64  `json:"total_bytes"`
		Files  int64  `json:"files"`
		Bytes  int64  `json:"bytes"`
	}
	active, _, _ := a.activeLocation()
	if active == "" {
		active = localLocation
	}
	counts := map[string][2]int64{}
	rows, e := a.db.Query("SELECT backend,COUNT(*),COALESCE(SUM(size),0) FROM files GROUP BY backend")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var loc string
		var n, size int64
		if e = rows.Scan(&loc, &n, &size); e != nil {
			rows.Close()
			return nil, e
		}
		counts[loc] = [2]int64{n, size}
	}
	rows.Close()
	list := []location{{ID: localLocation, Path: filepath.Join(a.root, "blobs"), Online: true}}
	a.stores.mu.RLock()
	for _, l := range a.stores.locations {
		list = append(list, location{ID: l.ID, Path: l.Path, Online: l.online})
	}
	move := a.stores.move
	a.stores.mu.RUnlock()
	free := int64(0)
	for i := range list {
		l := &list[i]
		l.Active = l.ID == active
		l.Files, l.Bytes = counts[l.ID][0], counts[l.ID][1]
		if l.Online {
			if s, e := a.locationSpace(l.ID); e == nil {
				l.Free, l.Total = s.free, s.total
			}
		}
		if l.Active {
			free = l.Free
		}
	}
	var used int64
	if e = a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&used); e != nil {
		return nil, e
	}
	budget, e := a.storageBudget()
	if e != nil {
		return nil, e
	}
	return map[string]any{"backend": active, "reserved_bytes": used, "capacity_bytes": budget, "disk_free_bytes": free, "locations": list, "move": move, "uid": os.Getuid(), "gid": os.Getgid()}, nil
}

func (a *App) storageRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/storage", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		status, e := a.storageStatus()
		if internal(w, e) {
			return
		}
		respond(w, 200, status)
	})
	m.HandleFunc("GET /api/admin/storage/candidates", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		respond(w, 200, a.mountCandidates())
	})
	m.HandleFunc("POST /api/admin/storage/locations", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Path     string `json:"path"`
			Language string `json:"language"`
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, true)
		if !ok || !decode(w, r, &b) {
			return
		}
		id, code := a.addLocation(b.Path, b.Language)
		if code != "" {
			fail(w, 400, code)
			return
		}
		a.audit(r, u.Username, "storage.added", b.Path)
		respond(w, 201, map[string]string{"id": id})
	})
	m.HandleFunc("DELETE /api/admin/storage/locations/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, true)
		if !ok {
			return
		}
		id := r.PathValue("id")
		a.stores.mu.RLock()
		l := a.stores.locations[id]
		moving := a.stores.move.Running && a.stores.move.Target == id
		a.stores.mu.RUnlock()
		if l == nil {
			fail(w, 404, "not_found")
			return
		}
		active, _, _ := a.activeLocation()
		var n int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM files WHERE backend=?", id).Scan(&n)) {
			return
		}
		if n > 0 || active == id || moving {
			fail(w, 409, "storage_busy")
			return
		}
		if internal(w, exec(a, "DELETE FROM storage_locations WHERE id=?", id)) {
			return
		}
		a.stores.mu.Lock()
		delete(a.stores.locations, id)
		a.stores.mu.Unlock()
		// Without the marker the folder can be added again later.
		timed(func() bool {
			_ = os.Remove(filepath.Join(l.Path, storageDirName, storageMarker))
			_ = os.Remove(filepath.Join(l.Path, storageDirName))
			return true
		}, false)
		a.audit(r, u.Username, "storage.removed", l.Path)
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("PUT /api/admin/storage/active", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ID string `json:"id"`
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, true)
		if !ok || !decode(w, r, &b) {
			return
		}
		if !a.knownLocation(b.ID) {
			fail(w, 404, "not_found")
			return
		}
		dir, e := a.blobDir(b.ID)
		if internal(w, e) {
			return
		}
		if internal(w, exec(a, "UPDATE settings SET storage_active=? WHERE id=1", b.ID)) {
			return
		}
		a.audit(r, u.Username, "storage.active", dir)
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/admin/storage/move", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			To string `json:"to"`
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, true)
		if !ok || !decode(w, r, &b) {
			return
		}
		if !a.knownLocation(b.To) {
			fail(w, 404, "not_found")
			return
		}
		space, e := a.locationSpace(b.To)
		if internal(w, e) {
			return
		}
		a.stores.mu.RLock()
		running := a.stores.move.Running
		a.stores.mu.RUnlock()
		if running {
			fail(w, 409, "storage_busy")
			return
		}
		rows, e := a.db.Query(`SELECT f.id,f.size FROM files f WHERE f.backend<>? AND f.offset=f.size AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id) AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=f.share_id) ORDER BY f.rowid`, b.To)
		if internal(w, e) {
			return
		}
		ids, sizes := []string{}, []int64{}
		var total int64
		for rows.Next() {
			var id string
			var size int64
			if e = rows.Scan(&id, &size); e != nil {
				break
			}
			ids, sizes, total = append(ids, id), append(sizes, size), total+size
		}
		rows.Close()
		if internal(w, e) {
			return
		}
		if total > space.free-(512<<20) {
			fail(w, 507, "storage_full")
			return
		}
		stop := make(chan struct{})
		a.stores.mu.Lock()
		a.stores.move = moveStatus{Running: true, Target: b.To, Files: len(ids), Bytes: total}
		a.stores.stop = stop
		a.stores.mu.Unlock()
		go a.moveFiles(b.To, ids, sizes, stop)
		a.audit(r, u.Username, "storage.move", strconv.Itoa(len(ids)))
		respond(w, 202, map[string]bool{"ok": true})
	})
	m.HandleFunc("DELETE /api/admin/storage/move", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		a.stores.mu.Lock()
		if a.stores.move.Running && a.stores.stop != nil {
			close(a.stores.stop)
			a.stores.stop = nil
		}
		a.stores.mu.Unlock()
		respond(w, 200, map[string]bool{"ok": true})
	})
}

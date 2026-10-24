package server

import (
	"archive/tar"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Backups are single .tar files in a folder the administrator chooses
// (<data>/backups unless set). Each holds a consistent copy of the database,
// made by SQLite while the app keeps running, a short restore guide and,
// when asked for, every stored file. They are made on request or on a
// schedule, and only the newest few are kept. Files deleted while a backup
// runs are left out of it; nothing is ever paused.

const (
	backupPrefix   = "magisend-backup-"
	maxBackupsKept = 100
)

var backupName = regexp.MustCompile(`^magisend-backup-\d{8}-\d{6}\.tar$`)

func migrateBackup(db *sql.DB) error {
	for _, c := range [][2]string{
		{"backup_dir", "TEXT NOT NULL DEFAULT ''"},
		{"backup_every", "INTEGER NOT NULL DEFAULT 0"},
		{"backup_keep", "INTEGER NOT NULL DEFAULT 7"},
		{"backup_files", "INTEGER NOT NULL DEFAULT 0"},
		{"backup_last", "INTEGER NOT NULL DEFAULT 0"},
		{"backup_error", "TEXT NOT NULL DEFAULT ''"},
		{"backup_warning", "TEXT NOT NULL DEFAULT ''"},
	} {
		if e := addColumn(db, "settings", c[0], c[1]); e != nil {
			return e
		}
	}
	return nil
}

type BackupSettings struct {
	// Empty means <data>/backups.
	Dir string `json:"dir"`
	// Hours between scheduled backups: 0 (off), 24 or 168.
	Every int  `json:"every"`
	Keep  int  `json:"keep"`
	Files bool `json:"files"`
}

type backupState struct {
	sync.Mutex
	running     bool
	done, total int64
	// Stored files deleted while a backup runs, removed once it is done, so
	// the backup's database copy never refers to a file its archive lacks.
	held []string
}

type BackupFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Created int64  `json:"created"`
}

func (a *App) backupSettings() (BackupSettings, int64, string, error) {
	var s BackupSettings
	var last int64
	var failure string
	e := a.db.QueryRow("SELECT backup_dir,backup_every,backup_keep,backup_files,backup_last,backup_error FROM settings WHERE id=1").Scan(&s.Dir, &s.Every, &s.Keep, &s.Files, &last, &failure)
	return s, last, failure, e
}

func (a *App) backupDir(s BackupSettings) string {
	if s.Dir == "" {
		return filepath.Join(a.root, "backups")
	}
	return s.Dir
}

// listBackups returns the backups in the folder, newest first.
func listBackups(dir string) []BackupFile {
	entries, _ := os.ReadDir(dir)
	list := []BackupFile{}
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || !backupName.MatchString(entry.Name()) {
			continue
		}
		list = append(list, BackupFile{entry.Name(), info.Size(), info.ModTime().Unix()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list
}

// startBackup runs one backup in the background unless one is running.
func (a *App) startBackup(now time.Time) bool {
	a.backup.Lock()
	if a.backup.running {
		a.backup.Unlock()
		return false
	}
	a.backup.running, a.backup.done, a.backup.total = true, 0, 0
	a.backup.Unlock()
	go func() {
		missing, e := a.makeBackup(now)
		failure, warning := "", ""
		if e != nil {
			failure = e.Error()
			log.Print("backup failed")
		} else if len(missing) > 0 {
			warning = missingSummary(missing)
			log.Printf("backup incomplete: %s", warning)
		}
		_, _ = a.db.Exec("UPDATE settings SET backup_last=?, backup_error=?, backup_warning=? WHERE id=1", now.Unix(), failure, warning)
		a.finishBackup()
	}()
	return true
}

// dueBackup starts a scheduled backup when its time has come.
func (a *App) dueBackup(now time.Time) {
	s, last, _, e := a.backupSettings()
	if e == nil && s.Every > 0 && now.Sub(time.Unix(last, 0)) >= time.Duration(s.Every)*time.Hour {
		a.startBackup(now)
	}
}

const restoreGuide = `MagiSend backup

magisend.sqlite  the database: accounts, settings, links, folders and file details
files/<location>/  the stored files (only when the backup includes files); "local" is
                   the data folder's blobs folder, any other name is a storage location
                   added under Administration → Storage (its magisend-files folder)

To restore:
1. Stop MagiSend (for example: systemctl stop magisend).
2. Copy magisend.sqlite over the one in the data folder (/var/lib/magisend by default)
   and remove any magisend.sqlite-wal and magisend.sqlite-shm files next to it.
3. If the backup has files, copy files/local/* into <data folder>/blobs/ and each
   files/<location>/* into that location's magisend-files folder.
4. Start MagiSend again.
`

func (a *App) makeBackup(now time.Time) ([]string, error) {
	s, _, _, e := a.backupSettings()
	if e != nil {
		return nil, e
	}
	dir := a.backupDir(s)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, errors.New("the backup folder cannot be created")
	}
	// SQLite writes a consistent copy while the app keeps working.
	snapshot := filepath.Join(dir, ".magisend-snapshot-"+randomID()+".sqlite")
	defer os.Remove(snapshot)
	if _, e = a.db.Exec("VACUUM INTO ?", snapshot); e != nil {
		return nil, errors.New("the database could not be copied")
	}
	// What could not be copied; the backup is then incomplete, and says so.
	var missing []string
	type item struct{ name, path string }
	items := []item{{"magisend.sqlite", snapshot}}
	if s.Files {
		locations := []string{localLocation}
		a.stores.mu.RLock()
		for id := range a.stores.locations {
			locations = append(locations, id)
		}
		a.stores.mu.RUnlock()
		for _, loc := range locations {
			blobs, e := a.blobDir(loc)
			if e != nil {
				missing = append(missing, "storage location "+a.locationLabel(loc)+" (unavailable)")
				continue
			}
			entries, e := os.ReadDir(blobs)
			if e != nil {
				missing = append(missing, "storage location "+a.locationLabel(loc)+" (cannot be read)")
				continue
			}
			for _, entry := range entries {
				if entry.Type().IsRegular() && validToken(entry.Name()) {
					items = append(items, item{"files/" + loc + "/" + entry.Name(), filepath.Join(blobs, entry.Name())})
				}
			}
		}
	}
	var total int64
	for _, it := range items {
		if info, e := os.Stat(it.path); e == nil {
			total += info.Size()
		}
	}
	if space, e := statDisk(dir); e == nil && total+(256<<20) > space.free {
		return nil, errors.New("there is not enough free space in the backup folder")
	}
	a.backup.Lock()
	a.backup.total = total
	a.backup.Unlock()

	name := backupPrefix + now.UTC().Format("20060102-150405") + ".tar"
	partial := filepath.Join(dir, "."+name+".partial")
	out, e := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, errors.New("the backup file cannot be written")
	}
	done := false
	defer func() {
		if !done {
			out.Close()
			os.Remove(partial)
		}
	}()
	archive := tar.NewWriter(out)
	if e = archive.WriteHeader(&tar.Header{Name: "RESTORE.txt", Mode: 0600, Size: int64(len(restoreGuide)), ModTime: now, Format: tar.FormatPAX}); e != nil {
		return nil, e
	}
	if _, e = io.WriteString(archive, restoreGuide); e != nil {
		return nil, e
	}
	for _, it := range items {
		copied, e := a.addToBackup(archive, it.name, it.path)
		if e != nil {
			return nil, errors.New("the backup file cannot be written")
		}
		if !copied {
			missing = append(missing, it.name)
		}
	}
	if len(missing) > 0 {
		list := "This backup is incomplete. These could not be copied:\n\n" + strings.Join(missing, "\n") + "\n"
		if e = archive.WriteHeader(&tar.Header{Name: "MISSING.txt", Mode: 0600, Size: int64(len(list)), ModTime: now, Format: tar.FormatPAX}); e != nil {
			return nil, e
		}
		if _, e = io.WriteString(archive, list); e != nil {
			return nil, e
		}
	}
	if e = archive.Close(); e == nil {
		e = out.Sync()
	}
	if closeErr := out.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return nil, errors.New("the backup file cannot be written")
	}
	if e = os.Rename(partial, filepath.Join(dir, name)); e != nil {
		return nil, errors.New("the backup file cannot be written")
	}
	done = true
	// Only the newest are kept.
	for i, old := range listBackups(dir) {
		if i >= max(1, s.Keep) {
			_ = os.Remove(filepath.Join(dir, old.Name))
		}
	}
	return missing, nil
}

// addToBackup copies one file and reports whether it could be read.
func (a *App) addToBackup(archive *tar.Writer, name, path string) (bool, error) {
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return false, nil
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return false, nil
	}
	if e = archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), ModTime: info.ModTime(), Format: tar.FormatPAX}); e != nil {
		return false, e
	}
	// Exactly the size recorded, even if an upload is still adding to it.
	if _, e = io.CopyN(archive, &progress{file, a}, info.Size()); e != nil {
		return false, e
	}
	return true, nil
}

// discardStored deletes a stored file, or, while a backup runs, once it is
// done.
func (a *App) discardStored(path string) error {
	a.backup.Lock()
	if a.backup.running {
		a.backup.held = append(a.backup.held, path)
		a.backup.Unlock()
		return nil
	}
	a.backup.Unlock()
	return os.Remove(path)
}

func (a *App) finishBackup() {
	a.backup.Lock()
	held := a.backup.held
	a.backup.running, a.backup.held = false, nil
	a.backup.Unlock()
	for _, path := range held {
		_ = os.Remove(path)
	}
}

// missingSummary is the warning shown for an incomplete backup.
func missingSummary(missing []string) string {
	shown := missing
	if len(shown) > 5 {
		shown = shown[:5]
	}
	text := strings.Join(shown, ", ")
	if more := len(missing) - len(shown); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text
}

func (a *App) locationLabel(loc string) string {
	a.stores.mu.RLock()
	defer a.stores.mu.RUnlock()
	if l := a.stores.locations[loc]; l != nil {
		return l.Path
	}
	return loc
}

type progress struct {
	r io.Reader
	a *App
}

func (p *progress) Read(b []byte) (int, error) {
	n, e := p.r.Read(b)
	p.a.backup.Lock()
	p.a.backup.done += int64(n)
	p.a.backup.Unlock()
	return n, e
}

// validBackupDir accepts an absolute folder that exists or can be made.
func validBackupDir(dir string) (string, bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", true
	}
	if !filepath.IsAbs(dir) || len(dir) > 500 || strings.ContainsAny(dir, "\x00\n\r") {
		return "", false
	}
	dir = filepath.Clean(dir)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", false
	}
	probe, e := os.CreateTemp(dir, ".magisend-probe-*")
	if e != nil {
		return "", false
	}
	probe.Close()
	os.Remove(probe.Name())
	return dir, true
}

func (a *App) backupRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/backups", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		s, last, failure, e := a.backupSettings()
		if internal(w, e) {
			return
		}
		a.backup.Lock()
		state := map[string]any{"running": a.backup.running, "done": a.backup.done, "total": a.backup.total}
		a.backup.Unlock()
		var warning string
		_ = a.db.QueryRow("SELECT backup_warning FROM settings WHERE id=1").Scan(&warning)
		respond(w, 200, map[string]any{"settings": s, "folder": a.backupDir(s), "last": last, "error": failure, "warning": warning, "state": state, "backups": listBackups(a.backupDir(s))})
	})
	m.HandleFunc("PUT /api/admin/backups", func(w http.ResponseWriter, r *http.Request) {
		var b BackupSettings
		if !decode(w, r, &b) {
			return
		}
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		dir, valid := validBackupDir(b.Dir)
		if !valid || (b.Every != 0 && b.Every != 24 && b.Every != 168) || b.Keep < 1 || b.Keep > maxBackupsKept {
			fail(w, 400, "backup_invalid")
			return
		}
		b.Dir = dir
		if internal(w, exec(a, "UPDATE settings SET backup_dir=?,backup_every=?,backup_keep=?,backup_files=? WHERE id=1", b.Dir, b.Every, b.Keep, b.Files)) {
			return
		}
		a.audit(r, admin.Username, "settings.backup", "")
		respond(w, 200, b)
	})
	m.HandleFunc("POST /api/admin/backups", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		if !a.startBackup(time.Now()) {
			fail(w, 409, "backup_running")
			return
		}
		a.audit(r, admin.Username, "backup.started", "")
		respond(w, 202, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/admin/backups/{name}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		name := r.PathValue("name")
		s, _, _, e := a.backupSettings()
		if internal(w, e) {
			return
		}
		if !backupName.MatchString(name) {
			fail(w, 404, "not_found")
			return
		}
		file, e := os.OpenFile(filepath.Join(a.backupDir(s), name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			fail(w, 404, "not_found")
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if internal(w, e) {
			return
		}
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeContent(w, r, name, info.ModTime(), file)
	})
	m.HandleFunc("DELETE /api/admin/backups/{name}", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		name := r.PathValue("name")
		s, _, _, e := a.backupSettings()
		if internal(w, e) {
			return
		}
		if !backupName.MatchString(name) {
			fail(w, 404, "not_found")
			return
		}
		if e = os.Remove(filepath.Join(a.backupDir(s), name)); e != nil && !os.IsNotExist(e) {
			internal(w, e)
			return
		}
		a.audit(r, admin.Username, "backup.deleted", name)
		respond(w, 200, map[string]bool{"ok": true})
	})
}

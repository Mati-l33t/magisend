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

// magisend-backup-<installation>-<time>[-incomplete].tar; archives from
// before 1.0.6 have no installation part.
var backupName = regexp.MustCompile(`^magisend-backup-(?:([0-9a-f]{8})-)?(\d{8}-\d{6})(-incomplete)?\.tar$`)

const backupStall = 10 * time.Minute

func migrateBackup(db *sql.DB) error {
	// Backups that included files used to include every storage location;
	// they keep doing so when the separate switch arrives.
	var hadStorage int
	if e := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('settings') WHERE name='backup_storage'").Scan(&hadStorage); e != nil {
		return e
	}
	for _, c := range [][2]string{
		{"backup_dir", "TEXT NOT NULL DEFAULT ''"},
		{"backup_every", "INTEGER NOT NULL DEFAULT 0"},
		{"backup_keep", "INTEGER NOT NULL DEFAULT 7"},
		{"backup_files", "INTEGER NOT NULL DEFAULT 0"},
		{"backup_last", "INTEGER NOT NULL DEFAULT 0"},
		{"backup_error", "TEXT NOT NULL DEFAULT ''"},
		{"backup_warning", "TEXT NOT NULL DEFAULT ''"},
		{"backup_storage", "INTEGER NOT NULL DEFAULT 0"},
		{"install_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if e := addColumn(db, "settings", c[0], c[1]); e != nil {
			return e
		}
	}
	if hadStorage == 0 {
		if _, e := db.Exec("UPDATE settings SET backup_storage=backup_files"); e != nil {
			return e
		}
	}
	_, e := db.Exec("UPDATE settings SET install_id=? WHERE id=1 AND install_id=''", randomID()[:8])
	return e
}

type BackupSettings struct {
	// Empty means <data>/backups.
	Dir string `json:"dir"`
	// Hours between scheduled backups: 0 (off), 24 or 168.
	Every int  `json:"every"`
	Keep  int  `json:"keep"`
	Files bool `json:"files"`
	// Also the files on added storage locations, not only the data folder's.
	Storage bool `json:"storage"`
}

type backupState struct {
	sync.Mutex
	running     bool
	done, total int64
	// Stored files deleted while a backup runs, removed once it is done, so
	// the backup's database copy never refers to a file its archive lacks.
	held []string
	// Which run is current, and when it last made progress: a run stuck on a
	// share that stopped answering is given up after backupStall.
	generation int
	progressAt time.Time
}

type BackupFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Created  int64  `json:"created"`
	Complete bool   `json:"complete"`
	// When it was made, from its name.
	stamp string
}

func (a *App) backupSettings() (BackupSettings, int64, string, error) {
	var s BackupSettings
	var last int64
	var failure string
	e := a.db.QueryRow("SELECT backup_dir,backup_every,backup_keep,backup_files,backup_storage,backup_last,backup_error FROM settings WHERE id=1").Scan(&s.Dir, &s.Every, &s.Keep, &s.Files, &s.Storage, &last, &failure)
	return s, last, failure, e
}

func (a *App) backupDir(s BackupSettings) string {
	if s.Dir == "" {
		return filepath.Join(a.root, "backups")
	}
	return s.Dir
}

// installID names this installation's backups, so a shared backup folder
// is never pruned or listed across installations.
func (a *App) installID() string {
	return a.install
}

var validInstallID = regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString

// loadInstallID reads the installation's ID from a file in the data folder,
// outside the database that backups hold, so restoring an older database
// keeps it. The first time, the ID the database had (or a new one) is
// written there.
func (a *App) loadInstallID() error {
	path := filepath.Join(a.root, "install-id")
	if data, e := os.ReadFile(path); e == nil && validInstallID(strings.TrimSpace(string(data))) {
		a.install = strings.TrimSpace(string(data))
		return nil
	}
	var id string
	_ = a.db.QueryRow("SELECT install_id FROM settings WHERE id=1").Scan(&id)
	if !validInstallID(id) {
		id = randomID()[:8]
	}
	temp := path + ".new"
	_ = os.Remove(temp)
	if e := os.WriteFile(temp, []byte(id+"\n"), 0600); e != nil {
		return e
	}
	if e := os.Rename(temp, path); e != nil {
		return e
	}
	_ = syncDir(a.root)
	a.install = id
	return nil
}

// listBackups returns this installation's backups in the folder, newest
// first; archives from before installation IDs count as its own, so they
// are listed, kept and pruned like the rest.
func listBackups(dir, install string) []BackupFile {
	entries, _ := os.ReadDir(dir)
	list := []BackupFile{}
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() {
			continue
		}
		m := backupName.FindStringSubmatch(entry.Name())
		if m == nil || m[1] != "" && m[1] != install {
			continue
		}
		list = append(list, BackupFile{Name: entry.Name(), Size: info.Size(), Created: info.ModTime().Unix(), Complete: m[3] == "", stamp: m[2]})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].stamp > list[j].stamp })
	return list
}

// pruneBackups keeps the newest keep of this installation's archives, and
// always the newest complete one.
func pruneBackups(dir, install string, keep int) {
	keptComplete := false
	for i, b := range listBackups(dir, install) {
		if i < max(1, keep) || b.Complete && !keptComplete {
			keptComplete = keptComplete || b.Complete
			continue
		}
		_ = os.Remove(filepath.Join(dir, b.Name))
	}
}

// cleanBackupLeftovers removes what an interrupted backup of this
// installation left in the folder.
func cleanBackupLeftovers(dir, install string) {
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".magisend-snapshot-"+install+"-") || strings.HasPrefix(name, "."+backupPrefix+install+"-") && strings.HasSuffix(name, ".partial") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// startBackup runs one backup in the background unless one is running. A
// watchdog gives a run up when it makes no progress for backupStall (a share
// that stopped answering), so deletions held for it are not held forever.
func (a *App) startBackup(now time.Time) bool {
	a.backup.Lock()
	if a.backup.running {
		a.backup.Unlock()
		return false
	}
	a.backup.running, a.backup.done, a.backup.total = true, 0, 0
	a.backup.generation++
	run := a.backup.generation
	a.backup.progressAt = time.Now()
	a.backup.Unlock()
	finished := make(chan struct{})
	record := func(failure, warning string) bool {
		a.backup.Lock()
		current := a.backup.generation == run && a.backup.running
		if current {
			a.backup.generation++
		}
		a.backup.Unlock()
		if !current {
			return false
		}
		// A run cut off by shutdown does not count as this period's backup.
		if a.stopping.Load() {
			_, _ = a.db.Exec("UPDATE settings SET backup_error=?, backup_warning='' WHERE id=1", failure)
		} else {
			_, _ = a.db.Exec("UPDATE settings SET backup_last=?, backup_error=?, backup_warning=? WHERE id=1", now.Unix(), failure, warning)
		}
		a.finishBackup()
		return true
	}
	go func() {
		missing, e := a.makeBackup(now, run)
		close(finished)
		failure, warning := "", ""
		if e != nil {
			failure = e.Error()
			log.Print("backup failed")
		} else if len(missing) > 0 {
			warning = missingSummary(missing)
			log.Printf("backup incomplete: %s", warning)
		}
		record(failure, warning)
	}()
	go func() {
		ticker := time.NewTicker(backupStall / 10)
		defer ticker.Stop()
		for {
			select {
			case <-finished:
				return
			case <-ticker.C:
				a.backup.Lock()
				stalled := a.backup.generation == run && time.Since(a.backup.progressAt) > backupStall
				a.backup.Unlock()
				if stalled && record("a storage folder stopped answering during the backup", "") {
					log.Print("backup given up: no progress")
					return
				}
			}
		}
	}()
	return true
}

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

Files of an added storage location are under files/<location ID>/; LOCATIONS.txt
lists each ID and the folder it was added as. When you restore them to a new disk
or share, put them in its magisend-files folder together with a file named
.magisend-storage that contains just that location ID, then mount it at the same
path, so MagiSend recognises the location again.

Files in the storage folders that the restored database does not know (made after
this backup) are not deleted: MagiSend sets them aside and lists them under
Administration → Storage, where they can be put back into the library.

Restoring only the database does not bring back files deleted or replaced after
this backup; MagiSend still starts and lists missing files under
Administration → Storage.
`

// errBackupAbandoned stops a run that was given up or is cut off by shutdown.
var errBackupAbandoned = errors.New("the backup was stopped")

// backupProgress records that run made progress (bytes more of the total),
// and reports whether it should go on: false once the watchdog gave it up,
// a newer run took over, or MagiSend is shutting down.
func (a *App) backupProgress(run int, bytes int64) bool {
	a.backup.Lock()
	defer a.backup.Unlock()
	if a.backup.generation != run || !a.backup.running || a.stopping.Load() {
		return false
	}
	a.backup.done += bytes
	a.backup.progressAt = time.Now()
	return true
}

// waitBackup lets a running backup end (it stops at its next step once
// shutdown began), for at most limit.
func (a *App) waitBackup(limit time.Duration) {
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		a.backup.Lock()
		running := a.backup.running
		a.backup.Unlock()
		if !running {
			return
		}
	}
	log.Print("shutdown: the backup did not stop in time")
}

func (a *App) makeBackup(now time.Time, run int) ([]string, error) {
	s, _, _, e := a.backupSettings()
	if e != nil {
		return nil, e
	}
	dir := a.backupDir(s)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, errors.New("the backup folder cannot be created")
	}
	install := a.installID()
	cleanBackupLeftovers(dir, install)
	// SQLite writes a consistent copy while the app keeps working; it holds
	// password hashes and secrets, so only its owner may read it.
	snapshot := filepath.Join(dir, ".magisend-snapshot-"+install+"-"+randomID()+".sqlite")
	defer os.Remove(snapshot)
	if _, e = a.db.Exec("VACUUM INTO ?", snapshot); e != nil {
		return nil, errors.New("the database could not be copied")
	}
	// Folders on CIFS, exFAT or vfat do not keep permissions: the mount
	// decides them, so a refused change does not stop the backup.
	if e = os.Chmod(snapshot, 0600); e != nil {
		log.Print("backup: the database copy's permissions could not be limited: ", e)
	}
	if !a.backupProgress(run, 0) {
		return nil, errBackupAbandoned
	}
	// What could not be copied; the backup is then incomplete, and says so.
	var missing []string
	type item struct{ name, path string }
	items := []item{{"magisend.sqlite", snapshot}}
	// Each added location's marker and path, so a restored folder can be
	// attached again (see RESTORE.txt).
	var locations strings.Builder
	a.stores.mu.RLock()
	for id, l := range a.stores.locations {
		fmt.Fprintf(&locations, "%s  %s\n", id, l.Path)
	}
	a.stores.mu.RUnlock()
	if s.Files {
		locations := []string{localLocation}
		if s.Storage {
			a.stores.mu.RLock()
			for id := range a.stores.locations {
				locations = append(locations, id)
			}
			a.stores.mu.RUnlock()
		}
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
			if !a.backupProgress(run, 0) {
				return nil, errBackupAbandoned
			}
		}
	}
	var total int64
	for i, it := range items {
		if info, e := os.Stat(it.path); e == nil {
			total += info.Size()
		}
		if i%256 == 0 && !a.backupProgress(run, 0) {
			return nil, errBackupAbandoned
		}
	}
	if space, e := statDisk(dir); e == nil && total+(256<<20) > space.free {
		return nil, errors.New("there is not enough free space in the backup folder")
	}
	a.backup.Lock()
	if a.backup.generation == run {
		a.backup.total = total
	}
	a.backup.Unlock()

	stamp := backupPrefix + install + "-" + now.UTC().Format("20060102-150405")
	partial := filepath.Join(dir, "."+stamp+".partial")
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
	if locations.Len() > 0 {
		list := "Storage locations: ID, then the folder it was added as.\n\n" + locations.String()
		if e = archive.WriteHeader(&tar.Header{Name: "LOCATIONS.txt", Mode: 0600, Size: int64(len(list)), ModTime: now, Format: tar.FormatPAX}); e != nil {
			return nil, e
		}
		if _, e = io.WriteString(archive, list); e != nil {
			return nil, e
		}
	}
	for _, it := range items {
		copied, e := a.addToBackup(archive, it.name, it.path, run)
		if e == errBackupAbandoned {
			return nil, e
		}
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
	if !a.backupProgress(run, 0) {
		return nil, errBackupAbandoned
	}
	name := stamp + ".tar"
	if len(missing) > 0 {
		name = stamp + "-incomplete.tar"
	}
	if e = os.Rename(partial, filepath.Join(dir, name)); e != nil {
		return nil, errors.New("the backup file cannot be written")
	}
	done = true
	pruneBackups(dir, install, s.Keep)
	return missing, nil
}

// addToBackup copies one file and reports whether it could be read whole.
// A read that fails partway (a bad sector, a share that dropped) fills the
// rest with zeros, so the archive stays valid; only writing the archive can
// fail the backup.
func (a *App) addToBackup(archive *tar.Writer, name, path string, run int) (bool, error) {
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return false, nil
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return false, nil
	}
	size := info.Size()
	if e = archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, ModTime: info.ModTime(), Format: tar.FormatPAX}); e != nil {
		return false, e
	}
	buf := make([]byte, 256<<10)
	whole := true
	for written := int64(0); written < size; {
		n := int(min(int64(len(buf)), size-written))
		got := 0
		if whole {
			got, e = io.ReadFull(file, buf[:n])
			if e != nil {
				whole = false
			}
		}
		clear(buf[got:n])
		if _, e = archive.Write(buf[:n]); e != nil {
			return false, e
		}
		written += int64(n)
		if !a.backupProgress(run, int64(n)) {
			return false, errBackupAbandoned
		}
	}
	return whole, nil
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

// finishBackup removes what was deleted during the backup, unless the file
// is in use there again (moved away and back meanwhile, say). A removal on a
// share that does not answer in time is given up, with the rest in that
// folder: those files are set aside as unknown at the next start.
func (a *App) finishBackup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.backup.Lock()
	held := a.backup.held
	a.backup.running, a.backup.held = false, nil
	a.backup.Unlock()
	hung := map[string]bool{}
	for _, path := range held {
		if hung[filepath.Dir(path)] || a.storedInUse(path) {
			continue
		}
		if !timed(func() bool { _ = os.Remove(path); return true }, false) {
			hung[filepath.Dir(path)] = true
		}
	}
}

// storedInUse reports whether a file or version the database knows is
// stored at path. Caller holds mu.
func (a *App) storedInUse(path string) bool {
	name := filepath.Base(path)
	rows, e := a.db.Query("SELECT backend FROM files WHERE id=? UNION SELECT f.backend FROM text_versions v JOIN files f ON f.id=v.file_id WHERE v.blob=?", name, name)
	if e != nil {
		return true
	}
	defer rows.Close()
	for rows.Next() {
		var loc string
		if rows.Scan(&loc) != nil {
			return true
		}
		// A location that is offline now may still be where it is kept.
		if dir, e := a.blobDir(loc); errors.Is(e, errStorageOffline) || e == nil && dir == filepath.Dir(path) {
			return true
		}
	}
	return false
}

type locationSize struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Online bool   `json:"online"`
}

type backupSize struct {
	Database  int64          `json:"database"`
	Local     int64          `json:"local"`
	Locations []locationSize `json:"locations"`
}

// backupSizes tells the administrator how large a backup gets with and
// without files, from what MagiSend stores.
func (a *App) backupSizes() (backupSize, error) {
	sizes := backupSize{Locations: []locationSize{}}
	if info, e := os.Stat(filepath.Join(a.root, "magisend.sqlite")); e == nil {
		sizes.Database = info.Size()
	}
	rows, e := a.db.Query("SELECT f.backend,COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files f ON f.id=c.file_id GROUP BY f.backend")
	if e != nil {
		return sizes, e
	}
	stored := map[string]int64{}
	for rows.Next() {
		var loc string
		var size int64
		if e = rows.Scan(&loc, &size); e != nil {
			rows.Close()
			return sizes, e
		}
		stored[loc] = size
	}
	rows.Close()
	sizes.Local = stored[localLocation]
	a.stores.mu.RLock()
	for id, l := range a.stores.locations {
		sizes.Locations = append(sizes.Locations, locationSize{l.Path, stored[id], l.online})
	}
	a.stores.mu.RUnlock()
	sort.Slice(sizes.Locations, func(i, j int) bool { return sizes.Locations[i].Path < sizes.Locations[j].Path })
	return sizes, nil
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
	if loc == localLocation {
		return filepath.Join(a.root, "blobs")
	}
	a.stores.mu.RLock()
	defer a.stores.mu.RUnlock()
	if l := a.stores.locations[loc]; l != nil {
		return l.Path
	}
	return loc
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
		sizes, e := a.backupSizes()
		if internal(w, e) {
			return
		}
		respond(w, 200, map[string]any{"settings": s, "folder": a.backupDir(s), "last": last, "error": failure, "warning": warning, "state": state, "sizes": sizes, "backups": listBackups(a.backupDir(s), a.installID())})
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
		b.Storage = b.Storage && b.Files
		if internal(w, exec(a, "UPDATE settings SET backup_dir=?,backup_every=?,backup_keep=?,backup_files=?,backup_storage=? WHERE id=1", b.Dir, b.Every, b.Keep, b.Files, b.Storage)) {
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
		if m := backupName.FindStringSubmatch(name); m == nil || m[1] != "" && m[1] != a.installID() {
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
		if m := backupName.FindStringSubmatch(name); m == nil || m[1] != "" && m[1] != a.installID() {
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

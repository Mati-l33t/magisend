package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// File contents can live in more than one place: the data folder ("local")
// and folders an administrator added, usually network shares mounted into the
// system. The database always stays in the data folder, so the app keeps
// working when a share is away. Each file row names its location in
// files.backend; new files go to the active location.
const (
	localLocation       = "local"
	storageDirName      = "magisend-files"
	storageMarker       = ".magisend-storage"
	movingPrefix        = ".moving-"
	storageCheckEvery   = 5 * time.Second
	storageCheckTimeout = 3 * time.Second
	storageNotifyAfter  = time.Minute
)

var errStorageOffline = errors.New("storage offline")

// deletionRecorded treats a deletion that waits for an offline location as
// done: the file is already hidden and the cleanup finishes it later.
func deletionRecorded(e error) error {
	if errors.Is(e, errStorageOffline) {
		return nil
	}
	return e
}

type storageLocation struct {
	ID       string
	Path     string
	Language string
	online   bool
	checking bool
	// When the location last went offline or came back, and whether the
	// administrators were told it is offline.
	changed  time.Time
	notified bool
}

type storages struct {
	mu        sync.RWMutex
	locations map[string]*storageLocation
	move      moveStatus
	stop      chan struct{}
}

func migrateStorage(db *sql.DB) error {
	if _, e := db.Exec(`CREATE TABLE IF NOT EXISTS storage_locations(id TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE, language TEXT NOT NULL DEFAULT 'en', created INTEGER NOT NULL)`); e != nil {
		return e
	}
	return addColumn(db, "settings", "storage_active", "TEXT NOT NULL DEFAULT 'local'")
}

// loadStorage reads the added locations and checks which are reachable
// before the app verifies its files, so files on an offline share are skipped
// instead of stopping the start.
func (a *App) loadStorage() error {
	rows, e := a.db.Query("SELECT id,path,language FROM storage_locations")
	if e != nil {
		return e
	}
	defer rows.Close()
	a.stores.locations = map[string]*storageLocation{}
	for rows.Next() {
		l := &storageLocation{changed: time.Now()}
		if e = rows.Scan(&l.ID, &l.Path, &l.Language); e != nil {
			return e
		}
		a.stores.locations[l.ID] = l
	}
	if e = rows.Err(); e != nil {
		return e
	}
	a.checkStorage(false)
	return nil
}

// probeLocation reads the marker inside the location's folder. An unmounted
// share leaves an empty mount point without the folder, so it reads as
// offline and nothing is ever written to the local disk by mistake.
func probeLocation(path, id string) bool {
	data, e := os.ReadFile(filepath.Join(path, storageDirName, storageMarker))
	if e != nil || strings.TrimSpace(string(data)) != id {
		return false
	}
	info, e := os.Lstat(filepath.Join(path, storageDirName))
	return e == nil && info.IsDir()
}

// timed runs f but gives up after the timeout, so a share that stopped
// answering cannot hold up the caller.
func timed[T any](f func() T, fallback T) T {
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(storageCheckTimeout):
		return fallback
	}
}

func (a *App) storageMonitor() {
	for range time.Tick(storageCheckEvery) {
		a.checkStorage(true)
	}
}

// checkStorage updates which locations are reachable. A probe still running
// from an earlier check counts as offline and is not started twice.
func (a *App) checkStorage(notify bool) {
	a.stores.mu.Lock()
	type job struct {
		l    *storageLocation
		path string
		id   string
	}
	jobs := []job{}
	for _, l := range a.stores.locations {
		if l.checking {
			a.setOnline(l, false)
			continue
		}
		l.checking = true
		jobs = append(jobs, job{l, l.Path, l.ID})
	}
	a.stores.mu.Unlock()
	results := make([]bool, len(jobs))
	var wait sync.WaitGroup
	for i, j := range jobs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			done := make(chan bool, 1)
			go func() {
				ok := probeLocation(j.path, j.id)
				a.stores.mu.Lock()
				j.l.checking = false
				a.stores.mu.Unlock()
				done <- ok
			}()
			select {
			case results[i] = <-done:
			case <-time.After(storageCheckTimeout):
			}
		}()
	}
	wait.Wait()
	a.stores.mu.Lock()
	for i, j := range jobs {
		a.setOnline(j.l, results[i])
	}
	mails := []storageLocation{}
	for _, l := range a.stores.locations {
		if !notify {
			break
		}
		if !l.online && !l.notified && time.Since(l.changed) >= storageNotifyAfter {
			l.notified = true
			mails = append(mails, *l)
		} else if l.online && l.notified {
			l.notified = false
			mails = append(mails, *l)
		}
	}
	a.stores.mu.Unlock()
	for _, l := range mails {
		a.storageMail(l)
	}
}

// setOnline records a change of state. Caller holds stores.mu.
func (a *App) setOnline(l *storageLocation, online bool) {
	if l.online == online {
		return
	}
	l.online, l.changed = online, time.Now()
	state := "offline"
	if online {
		state = "online"
	}
	log.Printf("storage %s is %s", l.ID, state)
}

func (a *App) storageMail(l storageLocation) {
	action := "storage.offline"
	if l.online {
		action = "storage.online"
	}
	a.audit(nil, "system", action, l.Path)
	// The database has one connection, so everything else is read before the
	// list of recipients.
	origin := a.linkOrigin()
	rows, e := a.db.Query("SELECT email FROM users WHERE role='admin' AND email<>'' AND disabled=0")
	if e != nil {
		return
	}
	recipients := []string{}
	for rows.Next() {
		var to string
		if rows.Scan(&to) == nil {
			recipients = append(recipients, to)
		}
	}
	rows.Close()
	t := emailText[emailLanguage(l.Language)]
	subject, line := fmt.Sprintf(t["storageOfflineSubject"], l.Path), fmt.Sprintf(t["storageOffline"], l.Path)
	if l.online {
		subject, line = fmt.Sprintf(t["storageOnlineSubject"], l.Path), fmt.Sprintf(t["storageOnline"], l.Path)
	}
	body := `<p style="font-size:16px;margin:0">` + html.EscapeString(line) + `</p>`
	if origin != "" {
		body += emailButton(origin+"/admin", t["manage"])
	}
	for _, to := range recipients {
		a.queueMail(outgoingMail{to: to, subject: subject, text: line + "\n", body: emailFrame(body)})
	}
}

func (a *App) knownLocation(loc string) bool {
	if loc == localLocation {
		return true
	}
	a.stores.mu.RLock()
	defer a.stores.mu.RUnlock()
	return a.stores.locations[loc] != nil
}

// blobDir is the folder holding the file contents of a location.
func (a *App) blobDir(loc string) (string, error) {
	if loc == localLocation {
		return filepath.Join(a.root, "blobs"), nil
	}
	a.stores.mu.RLock()
	defer a.stores.mu.RUnlock()
	l := a.stores.locations[loc]
	if l == nil {
		return "", fmt.Errorf("unknown storage location")
	}
	if !l.online {
		return "", errStorageOffline
	}
	return filepath.Join(l.Path, storageDirName), nil
}

func (a *App) fileLocation(id string) (string, error) {
	var loc string
	e := a.db.QueryRow("SELECT backend FROM files WHERE id=?", id).Scan(&loc)
	return loc, e
}

// fileBlob is the path of one stored object belonging to a file.
func (a *App) fileBlob(id, blob string) (string, error) {
	loc, e := a.fileLocation(id)
	if e != nil {
		return "", e
	}
	dir, e := a.blobDir(loc)
	if e != nil {
		return "", e
	}
	return filepath.Join(dir, blob), nil
}

// activeLocation is where new files go. A removed location falls back to the
// data folder.
func (a *App) activeLocation() (string, string, error) {
	loc := localLocation
	if e := a.db.QueryRow("SELECT storage_active FROM settings WHERE id=1").Scan(&loc); e != nil {
		return "", "", e
	}
	if !a.knownLocation(loc) {
		loc = localLocation
	}
	dir, e := a.blobDir(loc)
	return loc, dir, e
}

type diskSpace struct{ free, total int64 }

func statDisk(dir string) (diskSpace, error) {
	var stat syscall.Statfs_t
	if e := syscall.Statfs(dir, &stat); e != nil {
		return diskSpace{}, e
	}
	return diskSpace{int64(stat.Bavail) * stat.Bsize, int64(stat.Blocks) * stat.Bsize}, nil
}

// locationSpace measures a location's disk, giving up on a share that does
// not answer.
func (a *App) locationSpace(loc string) (diskSpace, error) {
	dir, e := a.blobDir(loc)
	if e != nil {
		return diskSpace{}, e
	}
	if loc == localLocation {
		return statDisk(dir)
	}
	type result struct {
		space diskSpace
		err   error
	}
	r := timed(func() result {
		s, e := statDisk(dir)
		return result{s, e}
	}, result{err: errStorageOffline})
	return r.space, r.err
}

// diskFree is the free space where new files go.
func (a *App) diskFree() (int64, error) {
	loc, _, e := a.activeLocation()
	if e != nil {
		return 0, e
	}
	s, e := a.locationSpace(loc)
	return s.free, e
}

// syncDir makes renames and new files in a folder durable.
func syncDir(dir string) error {
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	e = d.Sync()
	if closeErr := d.Close(); e == nil {
		e = closeErr
	}
	return e
}

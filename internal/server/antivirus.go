package server

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Virus scanning with ClamAV. MagiSend sends new files to a clamd daemon
// (over a Unix socket or TCP) after they are stored, one at a time, so
// uploads are never slowed down. A file waiting for its scan, or one in which
// a virus was found, cannot be downloaded, previewed or unpacked by anyone;
// a waiting file is released again when scanning is switched off, an
// infected one stays blocked until it is deleted.
//
// Scans are kept per stored object (a file's current contents), so a file
// replaced by a new version is scanned again, and files stored before
// scanning was switched on are not held back.

const (
	scanOff      = "off"
	scanReceived = "received" // only files from upload requests and collaborators
	scanAll      = "all"

	scanPending   = "pending"
	scanClean     = "clean"
	scanInfected  = "infected"
	scanUnscanned = "unscanned" // too large to scan; not held back

	defaultScanMaxSize = 100 << 20
	maxScanMaxSize     = 4 << 30
	// The Debian and Ubuntu clamav-daemon socket.
	defaultClamdAddress = "unix:/run/clamav/clamd.ctl"
	scanChunk           = 1 << 20
)

var errScanTooLarge = errors.New("the file is larger than the scanner accepts")
var errScanHeld = errors.New("the file is held back by its scan")

type ScanSettings struct {
	Mode    string `json:"mode"`
	Address string `json:"address"`
	MaxSize int64  `json:"max_size"`
}

func migrateAntivirus(db *sql.DB) error {
	if _, e := db.Exec(`CREATE TABLE IF NOT EXISTS file_scans(file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,blob TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN('pending','clean','infected','unscanned')),signature TEXT NOT NULL DEFAULT '',queued INTEGER NOT NULL,scanned INTEGER NOT NULL DEFAULT 0,tries INTEGER NOT NULL DEFAULT 0,retry_at INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(file_id,blob));
 CREATE INDEX IF NOT EXISTS file_scans_state ON file_scans(state,retry_at);`); e != nil {
		return e
	}
	for _, c := range [][2]string{
		{"scan_mode", "TEXT NOT NULL DEFAULT 'off'"},
		{"scan_address", "TEXT NOT NULL DEFAULT ''"},
		{"scan_max_size", "INTEGER NOT NULL DEFAULT 104857600"},
	} {
		if e := addColumn(db, "settings", c[0], c[1]); e != nil {
			return e
		}
	}
	return nil
}

func (a *App) scanSettings() ScanSettings {
	s, _ := a.readScanSettings()
	return s
}

func (a *App) readScanSettings() (ScanSettings, error) {
	s := ScanSettings{Mode: scanOff, MaxSize: defaultScanMaxSize}
	e := a.db.QueryRow("SELECT scan_mode,scan_address,scan_max_size FROM settings WHERE id=1").Scan(&s.Mode, &s.Address, &s.MaxSize)
	return s, e
}

// scanningOff reports whether scanning is known to be switched off; when the
// setting cannot be read, waiting files stay held back.
func (a *App) scanningOff() bool {
	s, e := a.readScanSettings()
	return e == nil && s.Mode == scanOff
}

// clamdAddress is where clamd listens: the setting, else MAGISEND_CLAMAV
// (set by the Docker setup), else the usual Debian socket.
func clamdAddress(s ScanSettings) string {
	if s.Address != "" {
		return s.Address
	}
	if env := os.Getenv("MAGISEND_CLAMAV"); env != "" {
		return env
	}
	return defaultClamdAddress
}

// validClamdAddress accepts unix:/absolute/path or host:port.
func validClamdAddress(address string) bool {
	if address == "" {
		return true
	}
	if len(address) > 300 || strings.ContainsAny(address, "\x00\r\n") {
		return false
	}
	if path, ok := strings.CutPrefix(address, "unix:"); ok {
		return filepath.IsAbs(path) && filepath.Clean(path) == path
	}
	host, port, e := net.SplitHostPort(address)
	return e == nil && host != "" && port != ""
}

// currentBlobSQL picks a file's current stored object, as currentBlob does.
const currentBlobSQL = `COALESCE((SELECT v.blob FROM text_versions v WHERE v.file_id=f.id ORDER BY v.number DESC LIMIT 1),f.id)`

// queueScans marks the contents of a new share's files for scanning, when
// the setting covers them: received says they came from someone other than
// the owner. Caller holds mu.
func (a *App) queueScans(shareID string, received bool) {
	a.queueScansWhere("f.share_id=?", shareID, received)
}

// queueFileScan is queueScans for one file whose contents just changed.
// Caller holds mu.
func (a *App) queueFileScan(fileID string, received bool) {
	a.queueScansWhere("f.id=?", fileID, received)
}

func (a *App) queueScansWhere(where, id string, received bool) {
	// When the setting cannot be read, the files are queued all the same.
	s, e := a.readScanSettings()
	if e == nil && (s.Mode == scanOff || s.Mode == scanReceived && !received) {
		return
	}
	_, e = a.db.Exec(`INSERT OR IGNORE INTO file_scans(file_id,blob,state,queued) SELECT f.id,`+currentBlobSQL+`,'pending',? FROM files f WHERE `+where+` AND f.offset=f.size`, time.Now().Unix(), id)
	if e != nil {
		log.Printf("antivirus: %s could not be queued: %v", id, e)
		return
	}
	select {
	case a.scanWake <- struct{}{}:
	default:
	}
}

// heldScans returns the files whose current contents are held back: waiting
// for a scan (while scanning is on) or infected. It is for showing them;
// what serves bytes asks scanHeld. Caller holds mu.
func (a *App) heldScans() map[string]string {
	held, _ := a.heldScansChecked()
	return held
}

// heldScansChecked is heldScans, with whether all of it could be read.
func (a *App) heldScansChecked() (map[string]string, error) {
	held := map[string]string{}
	// Read first: the one database connection is busy while rows are open.
	waiting := !a.scanningOff()
	rows, e := a.db.Query(`SELECT s.file_id,s.state FROM file_scans s JOIN files f ON f.id=s.file_id WHERE s.state IN('pending','infected') AND s.blob=` + currentBlobSQL)
	if e != nil {
		return held, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		if rows.Scan(&id, &state) == nil && (state == scanInfected || waiting) {
			held[id] = state
		}
	}
	if e := rows.Err(); e != nil {
		log.Printf("antivirus: scan states could not be read: %v", e)
		return held, e
	}
	return held, nil
}

// scanHeld reports why one file's contents are held back, or "". Caller
// holds mu.
func (a *App) scanHeld(fileID string) string {
	var state string
	e := a.db.QueryRow(`SELECT s.state FROM file_scans s JOIN files f ON f.id=s.file_id WHERE s.file_id=? AND s.state IN('pending','infected') AND s.blob=`+currentBlobSQL, fileID).Scan(&state)
	return a.heldState(state, e)
}

// heldState turns a looked-up scan state into scanHeld's answer. A failed
// lookup holds the file back.
func (a *App) heldState(state string, e error) string {
	if errors.Is(e, sql.ErrNoRows) {
		return ""
	}
	if e != nil {
		log.Printf("antivirus: scan state could not be read: %v", e)
		return scanPending
	}
	if state == scanPending && a.scanningOff() {
		return ""
	}
	return state
}

// blobHeld is scanHeld for one stored object of a file, such as an earlier
// version. Caller holds mu.
func (a *App) blobHeld(fileID, blob string) string {
	var state string
	e := a.db.QueryRow("SELECT state FROM file_scans WHERE file_id=? AND blob=? AND state IN('pending','infected')", fileID, blob).Scan(&state)
	return a.heldState(state, e)
}

// failHeld answers for a file that is held back.
func failHeld(w http.ResponseWriter, state string) {
	if state == scanInfected {
		fail(w, 403, "file_infected")
		return
	}
	fail(w, 423, "file_scanning")
}

// clamd speaks a small line protocol; z-commands end in a NUL byte.
func clamdDial(ctx context.Context, address string) (net.Conn, error) {
	var d net.Dialer
	d.Timeout = 5 * time.Second
	if path, ok := strings.CutPrefix(address, "unix:"); ok {
		return d.DialContext(ctx, "unix", path)
	}
	return d.DialContext(ctx, "tcp", address)
}

func clamdReply(conn net.Conn) (string, error) {
	reply, e := bufio.NewReader(io.LimitReader(conn, 4096)).ReadString(0)
	if e != nil && reply == "" {
		return "", e
	}
	return strings.TrimSpace(strings.TrimRight(reply, "\x00")), nil
}

// clamdVersion checks that clamd answers, and returns its version line.
func clamdVersion(ctx context.Context, address string) (string, error) {
	conn, e := clamdDial(ctx, address)
	if e != nil {
		return "", e
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, e = conn.Write([]byte("zVERSION\x00")); e != nil {
		return "", e
	}
	reply, e := clamdReply(conn)
	if e == nil && !strings.HasPrefix(reply, "ClamAV ") {
		e = errors.New("not a ClamAV answer")
	}
	return reply, e
}

// clamdScan streams size bytes to clamd and returns the virus found, or "".
func clamdScan(ctx context.Context, address string, data io.Reader, size int64) (string, error) {
	conn, e := clamdDial(ctx, address)
	if e != nil {
		return "", e
	}
	defer conn.Close()
	// About 2 MB/s at the least, as on a small NAS.
	_ = conn.SetDeadline(time.Now().Add(time.Minute + time.Duration(size/(2<<20))*time.Second))
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	var written int64
	var local error // reading the file failed: tried again, never "too large"
	// Storage that stalled long enough for clamd to give up waiting is not
	// a file clamd refused.
	var stalled bool
	sent := func() error {
		if _, e := conn.Write([]byte("zINSTREAM\x00")); e != nil {
			return e
		}
		buf := make([]byte, 4+scanChunk)
		for {
			started := time.Now()
			n, re := io.ReadFull(data, buf[4:])
			stalled = stalled || time.Since(started) > 30*time.Second
			if n > 0 {
				binary.BigEndian.PutUint32(buf, uint32(n))
				if _, e := conn.Write(buf[:4+n]); e != nil {
					return e
				}
				written += int64(n)
			}
			if re == io.EOF || re == io.ErrUnexpectedEOF {
				break
			}
			if re != nil {
				local = re
				return re
			}
		}
		_, e := conn.Write([]byte{0, 0, 0, 0})
		return e
	}()
	if local != nil {
		return "", local
	}
	// clamd closes the connection once a stream passes its limit, so its
	// answer is read even when sending failed.
	reply, re := clamdReply(conn)
	switch {
	case re == nil && strings.Contains(reply, "size limit exceeded"):
		return "", errScanTooLarge
	// With AlertExceedsMax, a file clamd could not scan whole (too large, an
	// archive too deep or with too many files) is reported like this.
	case re == nil && strings.HasPrefix(reply, "stream: Heuristics.Limits.Exceeded"):
		return "", errScanTooLarge
	case re == nil && reply == "stream: OK":
		return "", nil
	case re == nil && strings.HasPrefix(reply, "stream: ") && strings.HasSuffix(reply, " FOUND"):
		signature := strings.TrimSuffix(strings.TrimPrefix(reply, "stream: "), " FOUND")
		if signature == "" || len(signature) > 200 {
			signature = "unknown"
		}
		return signature, nil
	case re != nil && written > scanChunk && !stalled && cutOff(sent, re) && clamdAnswers(address):
		// clamd's answer to a stream past its limit can be lost when it
		// closes the connection; it still answers, so the stream was
		// refused, not lost.
		return "", errScanTooLarge
	case sent != nil:
		return "", sent
	case re != nil:
		return "", re
	}
	return "", errors.New("clamd: " + reply)
}

// cutOff reports whether clamd closed the connection while the stream was
// sent or before its answer was read; a timeout is not that.
func cutOff(sent, read error) bool {
	closed := func(e error) bool { return errors.Is(e, syscall.EPIPE) || errors.Is(e, syscall.ECONNRESET) }
	return closed(sent) || sent == nil && closed(read)
}

// clamdAnswers checks that clamd is up, after a stream was cut off.
func clamdAnswers(address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, e := clamdVersion(ctx, address)
	return e == nil
}

// scanStatus is the last thing the scanner reported, for the admin page.
type scanStatus struct {
	Error string `json:"error,omitempty"`
	At    int64  `json:"at,omitempty"`
}

func (a *App) scanWorker(ctx context.Context) {
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		for a.scanNext(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-a.scanWake:
		case <-timer.C:
		}
	}
}

// scanNext scans the oldest waiting file that is due, and reports whether
// there may be more.
func (a *App) scanNext(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	a.mu.Lock()
	settings := a.scanSettings()
	if settings.Mode == scanOff {
		a.mu.Unlock()
		return false
	}
	now := time.Now().Unix()
	var id, blob string
	var tries int
	var size int64
	var owner, name string
	e := a.db.QueryRow("SELECT s.file_id,s.blob,s.tries,f.size,sh.owner,f.name FROM file_scans s JOIN files f ON f.id=s.file_id JOIN shares sh ON sh.id=f.share_id WHERE s.state='pending' AND s.retry_at<=? ORDER BY s.queued,s.rowid LIMIT 1", now).Scan(&id, &blob, &tries, &size, &owner, &name)
	if e != nil {
		a.mu.Unlock()
		return false
	}
	// When the result cannot be written, the worker stops until its next
	// round instead of scanning the same file again at once.
	// gone says the stored object stopped being scanned meanwhile (a merged
	// save took its place), so nothing is reported for it.
	gone := false
	record := func(state, signature string) bool {
		// By the stored object alone: it may have become another file's
		// version meanwhile (stored objects are never shared).
		res, e := a.db.Exec("UPDATE file_scans SET state=?,signature=?,scanned=? WHERE blob=?", state, signature, time.Now().Unix(), blob)
		if e != nil {
			log.Printf("antivirus: the scan of file %s could not be saved: %v", id, e)
			return false
		}
		if n, e := res.RowsAffected(); e == nil && n == 0 {
			gone = true
		}
		return true
	}
	var file *os.File
	path, e := a.fileBlob(id, blob)
	if e == nil {
		loc, _ := a.fileLocation(id)
		if !a.knownLocation(loc) {
			e = errStorageOffline
		}
	}
	if e == nil {
		file, e = os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	var info os.FileInfo
	if e == nil {
		if info, e = file.Stat(); e == nil && !info.Mode().IsRegular() {
			e = errors.New("not a regular file")
		}
	}
	if e != nil {
		if file != nil {
			file.Close()
		}
		// A stored object that is no longer one of the file's versions is not
		// scanned at all.
		var versions int
		if blob != id && a.db.QueryRow("SELECT COUNT(*) FROM text_versions WHERE file_id=? AND blob=?", id, blob).Scan(&versions) == nil && versions == 0 {
			more := exec(a, "DELETE FROM file_scans WHERE file_id=? AND blob=?", id, blob) == nil
			a.mu.Unlock()
			return more
		}
		// The stored object is gone (its storage is offline, say): tried
		// again later.
		more := a.retryScan(id, blob, tries, e)
		a.mu.Unlock()
		return more
	}
	if info.Size() > settings.MaxSize {
		more := record(scanUnscanned, "too large")
		a.mu.Unlock()
		file.Close()
		return more
	}
	a.mu.Unlock()
	signature, e := clamdScan(ctx, clamdAddress(settings), file, info.Size())
	file.Close()
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case ctx.Err() != nil:
		return false
	case errors.Is(e, errScanTooLarge):
		return record(scanUnscanned, "too large for clamd")
	case e != nil:
		a.retryScan(id, blob, tries, e)
		a.scanState.Store(scanStatus{Error: e.Error(), At: time.Now().Unix()})
		return false
	case signature != "":
		if !record(scanInfected, signature) {
			return false
		}
		if gone {
			return true
		}
		a.scanState.Store(scanStatus{})
		log.Printf("antivirus: %s found in file %s", signature, id)
		a.audit(nil, a.username(owner), "file.infected", name+" ("+signature+")")
		a.notify(owner, pushInfected, func(t map[string]string) (string, string) {
			return fmt.Sprintf(t["infected"], name), signature
		}, a.linkOrigin())
	default:
		if !record(scanClean, "") {
			return false
		}
		a.scanState.Store(scanStatus{})
	}
	return true
}

// retryScan puts a scan off: after a minute, doubling up to an hour. It
// reports whether that was saved. Caller holds mu.
func (a *App) retryScan(id, blob string, tries int, cause error) bool {
	wait := time.Minute << min(tries, 6)
	if wait > time.Hour {
		wait = time.Hour
	}
	if e := exec(a, "UPDATE file_scans SET tries=tries+1,retry_at=? WHERE blob=?", time.Now().Add(wait).Unix(), blob); e != nil {
		log.Printf("antivirus: the retry of file %s could not be saved: %v", id, e)
		return false
	}
	if tries == 0 {
		log.Printf("antivirus: file %s could not be scanned, trying again later: %v", id, cause)
	}
	return true
}

// ClamAV can be installed from the admin page on a native installation: the
// updater (update.sh, which magisend-update.service runs as root) installs it
// from the system's packages when it finds clamavTrigger next to the update
// request. The request carries nothing but that, and its progress is written
// where only root can write.
const (
	clamavTrigger    = ".clamav-requested"
	clamavStatusPath = "/var/lib/magisend-update/clamav-status.json"
)

type clamavInstall struct {
	State   string `json:"state"`
	Step    string `json:"step,omitempty"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

// clamavMemory is the memory (MB) ClamAV is installed with: about 1.5 GB for
// clamd itself, and MagiSend's own share.
const clamavMemory = 2400

// clamavStale is how long an install request or a running install is waited
// for; after that the admin page offers the button again.
const clamavStale = 20 * time.Minute

type clamavInfo struct {
	Running bool   `json:"running"`
	Version string `json:"version,omitempty"`
	// "native", "docker" or "manual": how MagiSend was installed.
	Install    string         `json:"install"`
	CanInstall bool           `json:"can_install"`
	Memory     int64          `json:"memory,omitempty"` // MB
	Requested  bool           `json:"requested,omitempty"`
	Progress   *clamavInstall `json:"progress,omitempty"`
	// ClamAV on this server would pass files it cannot scan whole as clean,
	// or stops below MagiSend's limit; the install fixes its settings.
	NeedsSettings bool `json:"needs_settings,omitempty"`
}

// clamdConfPath is the Debian and Ubuntu clamd configuration.
var clamdConfPath = "/etc/clamav/clamd.conf"

// clamdNeedsSettings reports whether clamd on this server, when address
// reaches it, lacks the settings the install gives it: files over its limits
// reported (AlertExceedsMax), and files up to maxSize (at most 100 MB)
// scanned whole.
func clamdNeedsSettings(address string, maxSize int64) bool {
	data, e := os.ReadFile(clamdConfPath)
	if e != nil || len(data) > 1<<20 {
		return false
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && !strings.HasPrefix(f[0], "#") {
			values[f[0]] = f[1]
		}
	}
	if !localClamd(address, values) {
		return false
	}
	if alert := strings.ToLower(values["AlertExceedsMax"]); alert != "true" && alert != "yes" {
		return true
	}
	want := min(maxSize, defaultScanMaxSize)
	for _, key := range []string{"MaxFileSize", "StreamMaxLength", "PCREMaxFileSize"} {
		if n, ok := clamdSize(strings.ToLower(values[key])); ok && n > 0 && n < want {
			return true
		}
	}
	return false
}

// localClamd reports whether address is where this server's clamd, as its
// configuration says, listens.
func localClamd(address string, values map[string]string) bool {
	if path, ok := strings.CutPrefix(address, "unix:"); ok {
		path = strings.TrimPrefix(path, "/var")
		socket := strings.TrimPrefix(values["LocalSocket"], "/var")
		return path == strings.TrimPrefix(defaultClamdAddress, "unix:") || socket != "" && path == socket
	}
	host, port, e := net.SplitHostPort(address)
	return e == nil && (host == "127.0.0.1" || host == "localhost" || host == "::1") && port != "" && port == values["TCPSocket"]
}

// clamdSize reads a clamd size such as 25M, 1G, 4000K or 1048576.
func clamdSize(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	unit := int64(1)
	switch value[len(value)-1] {
	case 'k':
		unit, value = 1<<10, value[:len(value)-1]
	case 'm':
		unit, value = 1<<20, value[:len(value)-1]
	case 'g':
		unit, value = 1<<30, value[:len(value)-1]
	}
	var n int64
	if _, e := fmt.Sscan(value, &n); e != nil || n < 0 {
		return 0, false
	}
	return n * unit, true
}

// updaterScript is the updater next to this program.
var updaterScript = func() string {
	exe, e := os.Executable()
	if e != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "update.sh")
}

// updaterInstallsClamAV reports whether the updater can install ClamAV (it
// came with 1.0.10).
func updaterInstallsClamAV() bool {
	path := updaterScript()
	if path == "" {
		return false
	}
	data, e := os.ReadFile(path)
	return e == nil && strings.Contains(string(data), "--clamav")
}

// memoryMB is the memory this system (or container) has.
func memoryMB() int64 {
	data, e := os.ReadFile("/proc/meminfo")
	if e != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemTotal:" {
			var kb int64
			fmt.Sscan(fields[1], &kb)
			return kb / 1024
		}
	}
	return 0
}

func (a *App) clamavInfo(ctx context.Context, s ScanSettings) clamavInfo {
	info := clamavInfo{Install: a.updates.Mode}
	if info.Install != "native" && info.Install != "docker" {
		info.Install = "manual"
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if version, e := clamdVersion(check, clamdAddress(s)); e == nil {
		info.Running, info.Version = true, version
	}
	if info.Install == "native" {
		info.CanInstall = a.updates.Updater && updaterInstallsClamAV()
		info.Memory = memoryMB()
		// Only ClamAV on this server can be checked.
		if info.Running {
			info.NeedsSettings = clamdNeedsSettings(clamdAddress(s), s.MaxSize)
		}
	}
	if stat, e := os.Stat(filepath.Join(a.root, clamavTrigger)); e == nil {
		info.Requested = time.Since(stat.ModTime()) < clamavStale
	}
	if data, e := os.ReadFile(clamavStatusPath); e == nil && len(data) < 4096 {
		var progress clamavInstall
		if json.Unmarshal(data, &progress) == nil {
			// An install that stopped without saying so counts as failed.
			if at, e := time.Parse(time.RFC3339, progress.Time); progress.State == "running" && (e != nil || time.Since(at) > clamavStale) {
				progress.State, progress.Step, progress.Message = "failed", "stopped", "The ClamAV installation stopped"
			}
			info.Progress = &progress
		}
	}
	return info
}

type infectedFile struct {
	FileID    string `json:"file_id"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Signature string `json:"signature"`
	Scanned   int64  `json:"scanned"`
}

func (a *App) antivirusRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/antivirus", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		// Whether ClamAV answers is asked without the lock.
		clamav := a.clamavInfo(r.Context(), a.scanSettings())
		a.mu.Lock()
		defer a.mu.Unlock()
		s := a.scanSettings()
		counts := map[string]int64{scanPending: 0, scanClean: 0, scanInfected: 0, scanUnscanned: 0}
		rows, e := a.db.Query("SELECT state,COUNT(*) FROM file_scans GROUP BY state")
		if internal(w, e) {
			return
		}
		for rows.Next() {
			var state string
			var n int64
			if rows.Scan(&state, &n) == nil {
				counts[state] = n
			}
		}
		rows.Close()
		infected := []infectedFile{}
		rows, e = a.db.Query(`SELECT f.id,f.name,COALESCE(u.username,''),s.signature,s.scanned FROM file_scans s JOIN files f ON f.id=s.file_id JOIN shares sh ON sh.id=f.share_id LEFT JOIN users u ON u.id=sh.owner WHERE s.state='infected' AND s.blob=` + currentBlobSQL + ` ORDER BY s.scanned DESC LIMIT 200`)
		if internal(w, e) {
			return
		}
		for rows.Next() {
			var f infectedFile
			if rows.Scan(&f.FileID, &f.Name, &f.Owner, &f.Signature, &f.Scanned) == nil {
				infected = append(infected, f)
			}
		}
		rows.Close()
		status, _ := a.scanState.Load().(scanStatus)
		respond(w, 200, map[string]any{"settings": s, "address": clamdAddress(s), "counts": counts, "infected": infected, "status": status, "clamav": clamav})
	})
	m.HandleFunc("PUT /api/admin/antivirus", func(w http.ResponseWriter, r *http.Request) {
		var b ScanSettings
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		b.Address = strings.TrimSpace(b.Address)
		if (b.Mode != scanOff && b.Mode != scanReceived && b.Mode != scanAll) || !validClamdAddress(b.Address) || b.MaxSize < 1<<20 || b.MaxSize > maxScanMaxSize {
			fail(w, 400, "antivirus_invalid")
			return
		}
		if internal(w, exec(a, "UPDATE settings SET scan_mode=?,scan_address=?,scan_max_size=? WHERE id=1", b.Mode, b.Address, b.MaxSize)) {
			return
		}
		a.scanState.Store(scanStatus{})
		a.audit(r, admin.Username, "settings.antivirus", b.Mode)
		select {
		case a.scanWake <- struct{}{}:
		default:
		}
		respond(w, 200, b)
	})
	// Asks the updater to install ClamAV.
	m.HandleFunc("POST /api/admin/antivirus/install", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		if a.updates.Mode != "native" || !a.updates.Updater || !updaterInstallsClamAV() {
			fail(w, 409, "antivirus_cannot_install")
			return
		}
		// Repairing ClamAV that already runs needs no more memory than it has.
		check, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		_, running := clamdVersion(check, clamdAddress(a.scanSettings()))
		cancel()
		if memory := memoryMB(); running != nil && memory > 0 && memory < clamavMemory {
			fail(w, 409, "antivirus_memory")
			return
		}
		stamp := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
		if internal(w, os.WriteFile(filepath.Join(a.root, clamavTrigger), stamp, 0600)) {
			return
		}
		if internal(w, os.WriteFile(filepath.Join(a.root, updateTrigger), stamp, 0600)) {
			return
		}
		a.audit(r, admin.Username, "antivirus.install", "")
		respond(w, 202, map[string]bool{"ok": true})
	})
	// Checks that clamd answers at an address, before it is saved.
	m.HandleFunc("POST /api/admin/antivirus/test", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Address string `json:"address"`
		}
		if !decode(w, r, &b) {
			return
		}
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		b.Address = strings.TrimSpace(b.Address)
		if !validClamdAddress(b.Address) {
			fail(w, 400, "antivirus_invalid")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		version, e := clamdVersion(ctx, clamdAddress(ScanSettings{Address: b.Address}))
		if e != nil {
			fail(w, 502, "antivirus_unreachable")
			return
		}
		respond(w, 200, map[string]string{"version": version})
	})
}

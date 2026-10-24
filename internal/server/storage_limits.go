package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Links people create. Library uploads are stored as hidden containers and
// are not links; files are limited separately by the policy.
const ownerShareLimit = 100
const totalShareLimit = 1000

// A library folder and a link to selected files hold at most this many
// files; a ZIP, which may include a shared folder's subfolders, more.
const folderFileLimit = 1000
const zipFileLimit = 10000

// Default and highest file limits; Administration → Limits changes them.
const (
	defaultFilesPerUser = 10000
	defaultFilesTotal   = 100000
	maxFilesTotal       = 10000000
)

// Library uploads may come in many small batches; links keep a tighter rate.
const libraryUploadsPer15Min = 300

// Never queue a slow client behind another slow client. Slots belong to App,
// not Handler, so repeated Handler construction cannot bypass the limits.
func acquire(w http.ResponseWriter, slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		fail(w, 429, "För många samtidiga förfrågningar")
		return false
	}
}

const (
	// Downloads, previews, media and thumbnails share these slots; other
	// writes and uploads have their own. One visitor gets only a few of each,
	// so a few slow or hostile connections cannot take them all.
	downloadSlots      = 32
	downloadsPerClient = 6
	mutationSlots      = 32
	mutationsPerClient = 6
	uploadSlots        = 16
	uploadsPerClient   = 4
	// The largest request body that is not a file stream (a profile picture).
	maxBufferedBody = 4<<20 + 512<<10
)

type clientSlots struct {
	sync.Mutex
	count map[string]int
}

// streamingRequest tells file uploads, which are read as they arrive and
// outside mu, from every other request, which is read whole first.
func streamingRequest(r *http.Request) bool {
	path := r.URL.Path
	return path == uploadEndpoint ||
		r.Method == "PATCH" && (strings.HasPrefix(path, "/api/uploads/") || strings.HasPrefix(path, "/api/requests/") && strings.Contains(path, "/files/")) ||
		r.Method == "POST" && strings.HasPrefix(path, "/api/public/") && strings.HasSuffix(path, "/files")
}

// slotClient groups IPv6 clients by /56, what a home connection usually gets,
// so one household cannot hold the slots of many /64 networks.
func slotClient(client string) string {
	prefix, found := strings.CutSuffix(client, "/64")
	if !found {
		return client
	}
	ip := net.ParseIP(prefix)
	if ip == nil {
		return client
	}
	return ip.Mask(net.CIDRMask(56, 128)).String() + "/56"
}

// acquireFor takes one of slots for this request's client, at most
// perClient at a time.
func (a *App) acquireFor(w http.ResponseWriter, r *http.Request, slots chan struct{}, clients *clientSlots, perClient int) (func(), bool) {
	client := slotClient(a.clientIP(r))
	clients.Lock()
	if clients.count[client] >= perClient {
		clients.Unlock()
		fail(w, 429, "För många samtidiga förfrågningar")
		return nil, false
	}
	clients.count[client]++
	clients.Unlock()
	release := func() {
		clients.Lock()
		if clients.count[client]--; clients.count[client] <= 0 {
			delete(clients.count, client)
		}
		clients.Unlock()
	}
	if !acquire(w, slots) {
		release()
		return nil, false
	}
	return func() {
		<-slots
		release()
	}, true
}

// acquireDownload takes a download slot for this request's client.
func (a *App) acquireDownload(w http.ResponseWriter, r *http.Request) (func(), bool) {
	return a.acquireFor(w, r, a.downloads, &a.downloadClients, downloadsPerClient)
}

// Query errors hide a share rather than making deletion fail open.
func (a *App) isDeleting(id string) bool {
	var n int
	e := a.db.QueryRow("SELECT COUNT(*) FROM deleting WHERE share_id=?", id).Scan(&n)
	return e != nil || n != 0
}

// Deleting rows may already have lost blobs; main's existing startup cleanup
// retries them. Other committed bytes must exist before accepting requests.
func (a *App) verifyCommittedStorage() error {
	files, e := a.storedFiles()
	if e != nil {
		return e
	}
	c := a.checkStored(files)
	if len(c.wrong) > 0 {
		return fmt.Errorf("none of the expected files are in %s and it has no MagiSend index: is the right disk or share mounted?", strings.Join(c.wrong, ", "))
	}
	if len(c.unreachable) > 0 {
		log.Printf("storage: %s did not answer; its files are checked when they are read", strings.Join(c.unreachable, ", "))
	}
	if len(c.missing) > 0 {
		log.Printf("storage: %d stored files are missing or damaged; see Administration → Storage", len(c.missing))
	}
	return nil
}

// MissingFile is a file whose stored data is gone or has the wrong size,
// for example after an older database was restored.
type MissingFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Owner    string `json:"owner"`
	Size     int64  `json:"size"`
	Location string `json:"location"`
	Library  bool   `json:"library"`
}

// storedFile is a committed file and where its data should be.
type storedFile struct {
	MissingFile
	blob      string
	offset    int64
	published bool
	dir       string
}

// storedCheck is what checkStored found. gone holds the missing files whose
// data does not exist at all (rather than having the wrong size or being
// unreadable). wrong lists locations where none of many expected files exist
// and MagiSend's index is missing too, which looks like an unmounted disk
// rather than lost files; unreachable lists those that stopped answering.
type storedCheck struct {
	missing     []storedFile
	gone        map[string]bool
	wrong       []string
	unreachable []string
}

const storedFilesQuery = `SELECT f.id,f.name,COALESCE(u.username,''),f.offset,f.size,f.backend,s.published,EXISTS(SELECT 1 FROM library_files l WHERE l.file_id=f.id),
 COALESCE((SELECT v.blob FROM text_versions v WHERE v.file_id=f.id ORDER BY v.number DESC LIMIT 1),f.id)
 FROM files f JOIN shares s ON s.id=f.share_id LEFT JOIN users u ON u.id=s.owner
 WHERE NOT EXISTS (SELECT 1 FROM deleting d WHERE d.share_id=s.id) AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id)`

// storedFiles lists the committed files whose data should be in a reachable
// location (offline ones are checked when read). It only reads the
// database; caller holds mu, or the app is starting.
func (a *App) storedFiles() ([]storedFile, error) {
	return a.queryStoredFiles(storedFilesQuery)
}

func (a *App) queryStoredFiles(query string, args ...any) ([]storedFile, error) {
	rows, e := a.db.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var list []storedFile
	for rows.Next() {
		var f storedFile
		if e = rows.Scan(&f.ID, &f.Name, &f.Owner, &f.offset, &f.Size, &f.Location, &f.published, &f.Library, &f.blob); e != nil {
			return nil, e
		}
		if !a.knownLocation(f.Location) || f.offset < 0 || f.offset > f.Size {
			return nil, fmt.Errorf("invalid committed storage metadata for %s", f.ID)
		}
		if f.offset == 0 && !f.published {
			continue
		}
		if !validToken(f.blob) {
			return nil, fmt.Errorf("invalid committed blob key")
		}
		dir, e := a.blobDir(f.Location)
		if errors.Is(e, errStorageOffline) {
			continue
		}
		if e != nil {
			return nil, e
		}
		f.dir = dir
		list = append(list, f)
	}
	return list, rows.Err()
}

// storedFileState is why f's data does not match it: nil when it does,
// fs.ErrNotExist when it is not there at all.
func storedFileState(f storedFile) (found bool, problem error) {
	info, e := os.Lstat(filepath.Join(f.dir, f.blob))
	if e != nil {
		return false, e
	}
	if !info.Mode().IsRegular() || info.Size() < f.offset || (f.published && (f.offset != f.Size || info.Size() != f.Size)) {
		return true, errors.New("damaged")
	}
	return true, nil
}

// checkStored compares files with their stored data without holding mu, one
// location at a time in parallel. A location that stops answering (a hung
// network share) is given up after storageCheckTimeout without progress and
// listed as unreachable; none of its files count as missing.
func (a *App) checkStored(files []storedFile) storedCheck {
	c := storedCheck{gone: map[string]bool{}}
	byLocation := map[string][]storedFile{}
	for _, f := range files {
		byLocation[f.Location] = append(byLocation[f.Location], f)
	}
	type outcome struct {
		location string
		missing  []storedFile
		gone     []string
		wrong    bool
	}
	done := make(chan outcome, len(byLocation))
	progress := map[string]*atomic.Int64{}
	for location, list := range byLocation {
		checked := new(atomic.Int64)
		progress[location] = checked
		go func() {
			o := outcome{location: location}
			found := 0
			for _, f := range list {
				exists, problem := storedFileState(f)
				checked.Add(1)
				if exists {
					found++
				}
				if problem != nil {
					o.missing = append(o.missing, f)
					if errors.Is(problem, fs.ErrNotExist) {
						o.gone = append(o.gone, f.ID)
					}
				}
			}
			if found == 0 && len(list) >= 3 {
				if _, e := os.Lstat(filepath.Join(list[0].dir, storageIndexFile)); e != nil {
					o.wrong = true
				}
			}
			done <- o
		}()
	}
	type watch struct {
		seen  int64
		moved time.Time
	}
	pending := map[string]*watch{}
	for location := range byLocation {
		pending[location] = &watch{moved: time.Now()}
	}
	tick := time.NewTicker(storageCheckTimeout / 6)
	defer tick.Stop()
	for len(pending) > 0 {
		select {
		case o := <-done:
			if pending[o.location] == nil {
				continue
			}
			delete(pending, o.location)
			c.missing = append(c.missing, o.missing...)
			for _, id := range o.gone {
				c.gone[id] = true
			}
			if o.wrong {
				c.wrong = append(c.wrong, a.locationLabel(o.location))
			}
		case now := <-tick.C:
			for location, w := range pending {
				if n := progress[location].Load(); n != w.seen {
					w.seen, w.moved = n, now
				} else if now.Sub(w.moved) > storageCheckTimeout {
					delete(pending, location)
					c.unreachable = append(c.unreachable, a.locationLabel(location))
				}
			}
		}
	}
	sort.Slice(c.missing, func(i, j int) bool { return c.missing[i].ID < c.missing[j].ID })
	sort.Strings(c.wrong)
	sort.Strings(c.unreachable)
	return c
}

// stillGone reports whether f is still committed with the same data in the
// same place and that data still does not exist. Caller holds mu; the check
// gives up (false) when the location does not answer in time.
func (a *App) stillGone(f storedFile) bool {
	now, e := a.queryStoredFiles(storedFilesQuery+" AND f.id=?", f.ID)
	if e != nil || len(now) != 1 || now[0].Location != f.Location || now[0].blob != f.blob || now[0].dir != f.dir {
		return false
	}
	return timed(func() bool {
		_, problem := storedFileState(now[0])
		return errors.Is(problem, fs.ErrNotExist)
	}, false)
}

// metadataRoom reports whether owner may add one more link (when link is
// set) and files more files; errLinkLimit or errFileLimit says why not.
// Called under mu so the check and the insertion form one reservation.
func (a *App) metadataRoom(owner string, link bool, files int) (bool, error) {
	p, e := a.policy()
	if e != nil {
		return false, e
	}
	var links, ownedFiles, allLinks, allFiles int
	e = a.db.QueryRow(`SELECT (SELECT COUNT(*) FROM shares s WHERE s.owner=? AND NOT EXISTS(SELECT 1 FROM library_containers c WHERE c.share_id=s.id)), (SELECT COUNT(*) FROM files f JOIN shares s ON s.id=f.share_id WHERE s.owner=?), (SELECT COUNT(*) FROM shares s WHERE NOT EXISTS(SELECT 1 FROM library_containers c WHERE c.share_id=s.id)), (SELECT COUNT(*) FROM files)`, owner, owner).Scan(&links, &ownedFiles, &allLinks, &allFiles)
	if e != nil {
		return false, e
	}
	if link && (links >= ownerShareLimit || allLinks >= totalShareLimit) {
		return false, errLinkLimit
	}
	if files > p.FilesPerUser-ownedFiles || files > p.FilesTotal-allFiles {
		return false, errFileLimit
	}
	return true, nil
}

var (
	errLinkLimit = errors.New("metadata_limit")
	errFileLimit = errors.New("file_limit")
)

func (a *App) metadataAvailable(w http.ResponseWriter, owner string, link bool, files int) bool {
	if _, e := a.metadataRoom(owner, link, files); e == errLinkLimit || e == errFileLimit {
		fail(w, 429, e.Error())
		return false
	} else if internal(w, e) {
		return false
	}
	if link && a.limited("create:"+owner) {
		fail(w, 429, "För många nya delningar. Vänta 15 minuter")
		return false
	}
	if !link {
		a.failed("upload:" + owner)
		if a.blocked("upload:"+owner, libraryUploadsPer15Min) {
			fail(w, 429, "För många nya delningar. Vänta 15 minuter")
			return false
		}
	}
	return true
}

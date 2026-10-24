package server

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	missing, wrong, e := a.checkStoredFiles()
	if e != nil {
		return e
	}
	if len(wrong) > 0 {
		return fmt.Errorf("none of the expected files are in %s and it has no MagiSend index: is the right disk or share mounted?", strings.Join(wrong, ", "))
	}
	if len(missing) > 0 {
		log.Printf("storage: %d stored files are missing or damaged; see Administration → Storage", len(missing))
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

// checkStoredFiles compares every committed file with its stored data in
// the reachable locations (offline ones are checked when read). wrong lists
// locations where none of many expected files exist and MagiSend's index is
// missing too, which looks like an unmounted disk rather than lost files.
func (a *App) checkStoredFiles() (missing []MissingFile, wrong []string, err error) {
	rows, e := a.db.Query(`SELECT f.id,f.name,COALESCE(u.username,''),f.offset,f.size,f.backend,s.published,EXISTS(SELECT 1 FROM library_files l WHERE l.file_id=f.id),
 COALESCE((SELECT v.blob FROM text_versions v WHERE v.file_id=f.id ORDER BY v.number DESC LIMIT 1),f.id)
 FROM files f JOIN shares s ON s.id=f.share_id LEFT JOIN users u ON u.id=s.owner
 WHERE NOT EXISTS (SELECT 1 FROM deleting d WHERE d.share_id=s.id) AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id)`)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	expected, found := map[string]int{}, map[string]int{}
	for rows.Next() {
		var f MissingFile
		var blob string
		var offset int64
		var published bool
		if e = rows.Scan(&f.ID, &f.Name, &f.Owner, &offset, &f.Size, &f.Location, &published, &f.Library, &blob); e != nil {
			return nil, nil, e
		}
		if !a.knownLocation(f.Location) || offset < 0 || offset > f.Size {
			return nil, nil, fmt.Errorf("invalid committed storage metadata for %s", f.ID)
		}
		if offset == 0 && !published {
			continue
		}
		if !validToken(blob) {
			return nil, nil, fmt.Errorf("invalid committed blob key")
		}
		dir, e := a.blobDir(f.Location)
		if errors.Is(e, errStorageOffline) {
			continue
		}
		if e != nil {
			return nil, nil, e
		}
		expected[f.Location]++
		info, e := os.Lstat(filepath.Join(dir, blob))
		if e == nil {
			found[f.Location]++
		}
		if e != nil || !info.Mode().IsRegular() || info.Size() < offset || (published && (offset != f.Size || info.Size() != f.Size)) {
			missing = append(missing, f)
		}
	}
	if e = rows.Err(); e != nil {
		return nil, nil, e
	}
	for loc, n := range expected {
		if found[loc] > 0 || n < 3 {
			continue
		}
		dir, _ := a.blobDir(loc)
		if _, e := os.Lstat(filepath.Join(dir, storageIndexFile)); e != nil {
			wrong = append(wrong, a.locationLabel(loc))
		}
	}
	return missing, wrong, nil
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

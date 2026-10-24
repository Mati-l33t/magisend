package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	// Downloads, previews, media and thumbnails share these slots. One visitor
	// gets at most as many as a browser opens to one site, so a few slow or
	// hostile connections cannot take them all.
	downloadSlots      = 32
	downloadsPerClient = 6
)

// acquireDownload takes a download slot for this request's client.
func (a *App) acquireDownload(w http.ResponseWriter, r *http.Request) (func(), bool) {
	client := a.clientIP(r)
	a.downloadClients.Lock()
	if a.downloadClients.count[client] >= downloadsPerClient {
		a.downloadClients.Unlock()
		fail(w, 429, "För många samtidiga förfrågningar")
		return nil, false
	}
	a.downloadClients.count[client]++
	a.downloadClients.Unlock()
	release := func() {
		a.downloadClients.Lock()
		if a.downloadClients.count[client]--; a.downloadClients.count[client] <= 0 {
			delete(a.downloadClients.count, client)
		}
		a.downloadClients.Unlock()
	}
	if !acquire(w, a.downloads) {
		release()
		return nil, false
	}
	return func() {
		<-a.downloads
		release()
	}, true
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
	rows, e := a.db.Query(`SELECT f.id,f.offset,f.size,f.backend,s.published,COALESCE((SELECT v.blob FROM text_versions v WHERE v.file_id=f.id ORDER BY v.number DESC LIMIT 1),f.id) FROM files f JOIN shares s ON s.id=f.share_id WHERE NOT EXISTS (SELECT 1 FROM deleting d WHERE d.share_id=s.id) AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id)`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, backend, blob string
		var offset, size int64
		var published bool
		if e = rows.Scan(&id, &offset, &size, &backend, &published, &blob); e != nil {
			return e
		}
		if !a.knownLocation(backend) || offset < 0 || offset > size {
			return fmt.Errorf("invalid committed storage metadata for %s", id)
		}
		if offset == 0 && !published {
			continue
		}
		if !validToken(blob) {
			return fmt.Errorf("invalid committed blob key")
		}
		// Files on an offline location are checked when they are read.
		dir, e := a.blobDir(backend)
		if errors.Is(e, errStorageOffline) {
			continue
		}
		if e != nil {
			return e
		}
		info, e := os.Lstat(filepath.Join(dir, blob))
		if e != nil {
			return fmt.Errorf("committed blob %s unavailable: %w", id, e)
		}
		if !info.Mode().IsRegular() || info.Size() < offset || (published && (offset != size || info.Size() != size)) {
			return fmt.Errorf("committed blob %s invalid", id)
		}
	}
	return rows.Err()
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

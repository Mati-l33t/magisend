package server

import (
	"time"
)

func (a *App) isLibraryContainer(id string) bool {
	var n int
	// Fail closed on lookup errors: an uncertain share must not become public.
	e := a.db.QueryRow("SELECT COUNT(*) FROM library_containers WHERE share_id=?", id).Scan(&n)
	return e != nil || n != 0
}

// Before deleting a historical link, atomically move explicitly managed, fully
// published originals to a permanent private container. No bytes are copied or
// freed and file IDs, membership, quota and history remain unchanged. Incomplete
// upload reservations keep the existing 24h cleanup semantics.
func (a *App) retainManagedFiles(id string) error {
	var n int
	e := a.db.QueryRow(`SELECT COUNT(*) FROM files f JOIN library_files l ON l.file_id=f.id JOIN shares s ON s.id=f.share_id WHERE s.id=? AND s.published=1 AND f.offset=f.size`, id).Scan(&n)
	if e != nil || n == 0 {
		return e
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	next := randomID()
	if _, e = tx.Exec(`INSERT INTO shares(id,owner,title,expires,created,published) SELECT ?,owner,'Library',0,?,1 FROM shares WHERE id=?`, next, time.Now().Unix(), id); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO library_containers(share_id) VALUES(?)", next); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE files SET share_id=? WHERE share_id=? AND offset=size AND EXISTS(SELECT 1 FROM library_files l WHERE l.file_id=files.id)`, next, id); e != nil {
		return e
	}
	return tx.Commit()
}

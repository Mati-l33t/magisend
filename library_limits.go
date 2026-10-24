package main

import "net/http"

// Caller holds mu. Reservations count too: concurrent pending library uploads
// must not collectively overfill an existing live link. Check only increasing
// operations, allowing removals/shrinks after an administrator lowers a limit.
func (a *App) libraryGrowthAllowed(w http.ResponseWriter, owner, file string, size int64, folder string) bool {
	p, e := a.policy()
	if internal(w, e) {
		return false
	}
	// A shared folder includes its subfolders, so links on every folder above
	// this one grow too.
	rows, e := a.db.Query(folderLine+`SELECT l.share_id,l.mode,COALESCE(l.folder_id,'') FROM library_shares l JOIN shares s ON s.id=l.share_id WHERE s.owner=? AND ((l.mode='live' AND l.folder_id IN (SELECT id FROM line)) OR (l.mode='selection' AND EXISTS(SELECT 1 FROM library_selection x WHERE x.share_id=l.share_id AND x.file_id=?)))`, folder, owner, file)
	if internal(w, e) {
		return false
	}
	type link struct{ id, mode, folder string }
	links := []link{}
	for rows.Next() {
		var l link
		if e = rows.Scan(&l.id, &l.mode, &l.folder); e != nil {
			rows.Close()
			internal(w, e)
			return false
		}
		links = append(links, l)
	}
	e = rows.Err()
	rows.Close()
	if internal(w, e) {
		return false
	}
	for _, l := range links {
		var total, previous int64
		query := folderTree + `SELECT COALESCE(SUM(f.size),0),COALESCE(SUM(CASE WHEN f.id=? THEN f.size ELSE 0 END),0) FROM files f JOIN shares s ON s.id=f.share_id JOIN library_files lf ON lf.file_id=f.id WHERE s.owner=? AND lf.folder_id IN (SELECT id FROM tree)`
		args := []any{l.folder, file, owner}
		if l.mode == "selection" {
			query = `SELECT COALESCE(SUM(f.size),0),COALESCE(SUM(CASE WHEN f.id=? THEN f.size ELSE 0 END),0) FROM files f JOIN library_selection x ON x.file_id=f.id WHERE x.share_id=?`
			args = []any{file, l.id}
		}
		if internal(w, a.db.QueryRow(query, args...).Scan(&total, &previous)) {
			return false
		}
		growth := size - previous
		if growth > 0 && growth > p.MaxShare-total {
			fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
			return false
		}
	}
	return true
}

func (a *App) newLibraryShareAllowed(w http.ResponseWriter, owner, folder string, files []string) bool {
	p, e := a.policy()
	if internal(w, e) {
		return false
	}
	var total int64
	if folder != "" {
		if internal(w, a.db.QueryRow(folderTree+`SELECT COALESCE(SUM(f.size),0) FROM files f JOIN shares s ON s.id=f.share_id JOIN library_files l ON l.file_id=f.id WHERE s.owner=? AND l.folder_id IN (SELECT id FROM tree)`, folder, owner).Scan(&total)) {
			return false
		}
	} else {
		for _, id := range files {
			var size int64
			if internal(w, a.db.QueryRow("SELECT size FROM files WHERE id=?", id).Scan(&size)) {
				return false
			}
			total += size
		}
	}
	if total > p.MaxShare {
		fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
		return false
	}
	return true
}

// folderMoveAllowed checks moving folder (and everything below it) from
// parent from to parent to: no loops, not too deep, and links on the new
// folders above may not grow past the largest share.
func (a *App) folderMoveAllowed(w http.ResponseWriter, owner, folder, from, to string) bool {
	if !a.validParent(owner, to) {
		fail(w, 404, "not_found")
		return false
	}
	var inside, height int
	var size int64
	if internal(w, a.db.QueryRow(folderTree+`SELECT (SELECT COUNT(*) FROM tree WHERE id=?),(SELECT MAX(depth) FROM tree),(SELECT COALESCE(SUM(f.size),0) FROM files f JOIN library_files l ON l.file_id=f.id WHERE l.folder_id IN (SELECT id FROM tree))`, folder, to).Scan(&inside, &height, &size)) {
		return false
	}
	depth, e := a.folderDepth(to)
	if internal(w, e) {
		return false
	}
	if inside != 0 || depth+1+height > maxFolderDepth {
		fail(w, 400, "Ogiltiga indata")
		return false
	}
	p, e := a.policy()
	if internal(w, e) {
		return false
	}
	// Links that already include the folder (above both places) do not grow.
	rows, e := a.db.Query(folderLine+`SELECT COALESCE(l.folder_id,'') FROM library_shares l JOIN shares s ON s.id=l.share_id WHERE s.owner=? AND l.mode='live' AND l.folder_id IN (SELECT id FROM line)`, to, owner)
	if internal(w, e) {
		return false
	}
	targets := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			internal(w, e)
			return false
		}
		targets = append(targets, id)
	}
	e = rows.Err()
	rows.Close()
	if internal(w, e) {
		return false
	}
	for _, link := range targets {
		var already int
		if internal(w, a.db.QueryRow(folderLine+`SELECT COUNT(*) FROM line WHERE id=?`, from, link).Scan(&already)) {
			return false
		}
		if already != 0 {
			continue
		}
		var total int64
		if internal(w, a.db.QueryRow(folderTree+`SELECT COALESCE(SUM(f.size),0) FROM files f JOIN library_files l ON l.file_id=f.id WHERE l.folder_id IN (SELECT id FROM tree)`, link).Scan(&total)) {
			return false
		}
		if size > p.MaxShare-total {
			fail(w, 413, "Ogiltigt filnamn eller fil-/delningsgräns överskriden")
			return false
		}
	}
	return true
}

package main

import (
	"database/sql"
	"net/http"
)

// Unfinished uploads can be continued after a lost connection or a closed
// tab: the browser asks how far each file got and sends the rest.
type pendingFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Offset   int64  `json:"offset"`
	FolderID string `json:"folder_id,omitempty"`
}

type pendingUpload struct {
	ID      string        `json:"id"`
	Created int64         `json:"created"`
	Files   []pendingFile `json:"files"`
}

func (a *App) pendingFiles(share string) ([]pendingFile, error) {
	rows, e := a.db.Query("SELECT f.id,f.name,f.size,f.offset,l.folder_id FROM files f LEFT JOIN library_files l ON l.file_id=f.id WHERE f.share_id=? ORDER BY f.rowid", share)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	list := []pendingFile{}
	for rows.Next() {
		var f pendingFile
		var folder sql.NullString
		if e = rows.Scan(&f.ID, &f.Name, &f.Size, &f.Offset, &folder); e != nil {
			return nil, e
		}
		f.FolderID = folder.String
		list = append(list, f)
	}
	return list, rows.Err()
}

func (a *App) resumeRoutes(m *http.ServeMux) {
	// The signed-in user's library uploads that never finished. Batches sent
	// through upload requests belong to their senders and are left out.
	m.HandleFunc("GET /api/library/uploads", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query(`SELECT s.id,s.created FROM shares s JOIN library_containers c ON c.share_id=s.id
 WHERE s.owner=? AND s.published=0 AND NOT EXISTS(SELECT 1 FROM request_uploads q WHERE q.share_id=s.id)
 AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=s.id) ORDER BY s.created, s.rowid`, u.ID)
		if internal(w, e) {
			return
		}
		list := []pendingUpload{}
		for rows.Next() {
			var p pendingUpload
			if e = rows.Scan(&p.ID, &p.Created); e != nil {
				break
			}
			list = append(list, p)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if internal(w, e) {
			return
		}
		for i := range list {
			if list[i].Files, e = a.pendingFiles(list[i].ID); internal(w, e) {
				return
			}
		}
		respond(w, 200, list)
	})
	m.HandleFunc("GET /api/requests/{id}/uploads/{upload}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		_, s, ok := a.requestBatch(w, r)
		if !ok {
			return
		}
		files, e := a.pendingFiles(s.ID)
		if internal(w, e) {
			return
		}
		// Senders never learn where the owner keeps their files.
		for i := range files {
			files[i].FolderID = ""
		}
		respond(w, 200, pendingUpload{ID: s.ID, Created: s.Created, Files: files})
	})
	m.HandleFunc("DELETE /api/requests/{id}/uploads/{upload}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		_, s, ok := a.requestBatch(w, r)
		if !ok {
			return
		}
		if !internal(w, deletionRecorded(a.removeShare(s.ID))) {
			respond(w, 200, map[string]bool{"ok": true})
		}
	})
}

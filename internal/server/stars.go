package server

import (
	"database/sql"
	"net/http"
	"time"
)

// Starred files are a personal bookmark on the owner's own library files.
func migrateStars(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS stars(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE, created INTEGER NOT NULL, PRIMARY KEY(user_id, file_id))`)
	return e
}

func (a *App) starRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /api/library/files/{file}/star", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Starred bool `json:"starred"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		f, e := a.libraryFile(u.ID, r.PathValue("file"))
		if e != nil {
			fail(w, 404, "file_not_found")
			return
		}
		if b.Starred {
			e = exec(a, "INSERT OR IGNORE INTO stars(user_id,file_id,created) VALUES(?,?,?)", u.ID, f.ID, time.Now().Unix())
		} else {
			e = exec(a, "DELETE FROM stars WHERE user_id=? AND file_id=?", u.ID, f.ID)
		}
		if !internal(w, e) {
			respond(w, 200, map[string]bool{"starred": b.Starred})
		}
	})
}

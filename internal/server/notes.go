package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Notes are Markdown files in the library, so they get folders, sharing,
// version history, trash and backups like any file. A note's title is its
// file name. When the title is left empty it follows the note's first line
// on every save, until a title is typed (note_titles.auto).
const (
	noteTitleRunes = 60
	maxNotesListed = 500
	noteExcerpt    = 400
)

var noteFallback = map[string]string{"en": "Note", "sv": "Anteckning"}

func migrateNotes(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS note_titles(file_id TEXT PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE, auto INTEGER NOT NULL)`)
	return e
}

var markdownLead = regexp.MustCompile(`^(#{1,6}\s+|[-*+]\s+(\[[ xX]\]\s+)?|>\s*|\d+[.)]\s+)+`)

// cleanTitle makes typed or derived text a safe file name stem.
func cleanTitle(s string, limit int) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".md")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '-'
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > limit {
		cut := []rune(s)[:limit]
		if i := strings.LastIndex(string(cut), " "); i > limit/2 {
			s = string(cut)[:i] + "…"
		} else {
			s = string(cut) + "…"
		}
	}
	// File names are limited in bytes too: leave room for "…" and ".md".
	const maxBytes = 230
	if len(s) > maxBytes {
		cut := s[:maxBytes]
		for !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		s = strings.TrimSpace(cut) + "…"
	}
	s = strings.Trim(s, ". ")
	return s
}

// noteName returns the file name for a note and whether the title follows
// the text.
func noteName(title, text, language string) (string, bool) {
	if t := cleanTitle(title, 120); t != "" {
		return t + ".md", false
	}
	for _, line := range strings.Split(text, "\n") {
		line = markdownLead.ReplaceAllString(strings.TrimSpace(line), "")
		line = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "").Replace(line)
		line = strings.Trim(line, "*_ ")
		if t := cleanTitle(line, noteTitleRunes); t != "" {
			return t + ".md", true
		}
	}
	return noteFallback[emailLanguage(language)] + " " + time.Now().Format("2006-01-02 15.04") + ".md", true
}

type Note struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	FolderID string `json:"folder_id"`
	ShareID  string `json:"share_id"`
	Modified int64  `json:"modified"`
	Auto     bool   `json:"auto"`
	Starred  bool   `json:"starred"`
	Excerpt  string `json:"excerpt"`
	// "pending" or "infected" while held back by its virus scan.
	Scan string `json:"scan,omitempty"`
	// Only when it was just made: the text's version, for the editor.
	Version int `json:"version,omitempty"`
}

func isNote(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

func (a *App) noteRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/library/notes", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query(`SELECT f.id,f.name,f.size,COALESCE(l.folder_id,''),s.id,
 COALESCE((SELECT MAX(v.created) FROM text_versions v WHERE v.file_id=f.id),s.created),COALESCE(n.auto,0),
 EXISTS(SELECT 1 FROM stars st WHERE st.user_id=s.owner AND st.file_id=f.id)
 FROM files f JOIN shares s ON s.id=f.share_id LEFT JOIN library_files l ON l.file_id=f.id LEFT JOIN note_titles n ON n.file_id=f.id
 WHERE s.owner=? AND s.published=1 AND f.offset=f.size AND (lower(f.name) LIKE '%.md' OR lower(f.name) LIKE '%.markdown')
 AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=s.id) AND NOT EXISTS(SELECT 1 FROM file_deleting fd WHERE fd.file_id=f.id)
 ORDER BY 6 DESC LIMIT ?`, u.ID, maxNotesListed)
		if internal(w, e) {
			return
		}
		list := []Note{}
		for rows.Next() {
			var n Note
			if e = rows.Scan(&n.ID, &n.Name, &n.Size, &n.FolderID, &n.ShareID, &n.Modified, &n.Auto, &n.Starred); e != nil {
				rows.Close()
				internal(w, e)
				return
			}
			list = append(list, n)
		}
		rows.Close()
		// The opening of each note, for the list; not of one held back by its
		// scan (or whose scan cannot be read).
		for i := range list {
			if list[i].Scan = a.scanHeld(list[i].ID); list[i].Scan != "" {
				continue
			}
			if file, e := a.openCurrent(list[i].ID); e == nil {
				head := make([]byte, noteExcerpt)
				n, _ := io.ReadFull(file, head)
				file.Close()
				head = head[:n]
				for len(head) > 0 && !utf8.Valid(head) {
					head = head[:len(head)-1]
				}
				list[i].Excerpt = string(head)
			}
		}
		respond(w, 200, list)
	})
	m.HandleFunc("POST /api/library/notes", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Title    string `json:"title"`
			Text     string `json:"text"`
			FolderID string `json:"folder_id"`
			Language string `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok || !a.allowed(w, u.ID, "upload") {
			return
		}
		data := []byte(b.Text)
		name, auto := noteName(b.Title, b.Text, b.Language)
		if !validFilename(name) || !safeText(name, data) {
			textUnsupported(w)
			return
		}
		// A new note goes into the folder it was written in, or stays unfiled;
		// the notes list shows them all wherever they are.
		folder := b.FolderID
		if folder != "" && !a.ownsFolder(u.ID, folder) {
			fail(w, 404, "not_found")
			return
		}
		if !a.folderRoom(w, folder, "", 1) || !a.libraryGrowthAllowed(w, u.ID, "", int64(len(data)), folder) || !a.metadataAvailable(w, u.ID, false, 1) {
			return
		}
		var used, all int64
		if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files x ON x.id=c.file_id JOIN shares s ON s.id=x.share_id WHERE s.owner=?", u.ID).Scan(&used)) {
			return
		}
		if int64(len(data)) > u.Quota-used {
			fail(w, 413, "Din lagringskvot räcker inte")
			return
		}
		if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all)) {
			return
		}
		budget, e := a.storageBudget()
		if internal(w, e) {
			return
		}
		if int64(len(data)) > budget-all {
			fail(w, 507, "Appens lagringsbudget är full")
			return
		}
		loc, dir, e := a.activeLocation()
		if internal(w, e) {
			return
		}
		n := Note{ID: randomID(), Name: name, Size: int64(len(data)), FolderID: folder, ShareID: randomID(), Modified: time.Now().Unix(), Auto: auto}
		path := filepath.Join(dir, n.ID)
		if internal(w, copyDurably(path, bytes.NewReader(data))) {
			return
		}
		committed := false
		defer func() {
			if !committed {
				_ = os.Remove(path)
			}
		}()
		tx, e := a.db.Begin()
		if internal(w, e) {
			return
		}
		defer tx.Rollback()
		var folderValue any
		if folder != "" {
			folderValue = folder
		}
		for _, step := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO shares(id,owner,title,expires,created,published) VALUES(?,?,?,0,?,1)", []any{n.ShareID, u.ID, "Library", n.Modified}},
			{"INSERT INTO library_containers(share_id) VALUES(?)", []any{n.ShareID}},
			{"INSERT INTO files(id,share_id,name,size,offset,backend) VALUES(?,?,?,?,?,?)", []any{n.ID, n.ShareID, n.Name, n.Size, n.Size, loc}},
			{"INSERT INTO file_origins(file_id,name) VALUES(?,?)", []any{n.ID, n.Name}},
			{"INSERT INTO library_files(file_id,folder_id) VALUES(?,?)", []any{n.ID, folderValue}},
			{"INSERT INTO note_titles(file_id,auto) VALUES(?,?)", []any{n.ID, auto}},
		} {
			if _, e = tx.Exec(step.query, step.args...); internal(w, e) {
				return
			}
		}
		if internal(w, tx.Commit()) {
			return
		}
		committed = true
		a.queueFileScan(n.ID, false)
		n.Version = 1
		respond(w, 201, n)
	})
	// Saves a note's text and title together; the text is versioned like any
	// text file and autosaves merge into one version.
	m.HandleFunc("PUT /api/library/notes/{file}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Version  int    `json:"version"`
			Text     string `json:"text"`
			Title    string `json:"title"`
			Language string `json:"language"`
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*textMaxBytes))
		if e != nil || !utf8.Valid(raw) || json.Unmarshal(raw, &b) != nil {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, f, ok := a.versionedFile(w, r, true)
		if !ok {
			return
		}
		if !isNote(f.Name) {
			textUnsupported(w)
			return
		}
		current, version, ok := a.textState(w, f.File)
		if !ok {
			return
		}
		if b.Version != version {
			textConflict(w)
			return
		}
		// The name is checked before anything is saved, so a refused title
		// never leaves the text saved under a version the editor does not know.
		name, auto := noteName(b.Title, b.Text, b.Language)
		if !validFilename(name) {
			fail(w, 400, "Ogiltiga indata")
			return
		}
		if b.Text != string(current) {
			if version, ok = a.storeText(w, f.File, u.ID, version, []byte(b.Text)); !ok {
				return
			}
		}
		if internal(w, exec(a, "UPDATE files SET name=? WHERE id=?", name, f.ID)) ||
			internal(w, exec(a, "INSERT INTO note_titles(file_id,auto) VALUES(?,?) ON CONFLICT(file_id) DO UPDATE SET auto=excluded.auto", f.ID, auto)) {
			return
		}
		respond(w, 200, map[string]any{"version": version, "name": name, "auto": auto})
	})
}

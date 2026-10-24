package server

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Personal API tokens for uploading from scripts and screenshot tools such
// as ShareX. A token can only do one thing: upload files and get a share
// link back (POST /api/v1/upload). It never signs in to the web interface.
const (
	apiTokenPrefix  = "ms_"
	maxAPITokens    = 20
	uploadEndpoint  = "/api/v1/upload"
	partialUploadTo = ".incoming-"
)

type APIToken struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used"`
}

func migrateAPITokens(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS api_tokens(id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0)`)
	return e
}

// apiRequest reports whether a request uses a token instead of a browser
// session; such requests skip the browser-only CSRF header check.
func apiRequest(r *http.Request) bool {
	return r.URL.Path == uploadEndpoint && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (a *App) tokenUser(r *http.Request) (User, error) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var u User
	var id string
	if !strings.HasPrefix(token, apiTokenPrefix) {
		return u, errors.New("invalid token")
	}
	e := a.db.QueryRow(`SELECT t.id,u.id,u.username,u.role,u.quota FROM api_tokens t JOIN users u ON u.id=t.user_id WHERE t.hash=? AND u.disabled=0`, hash(token)).Scan(&id, &u.ID, &u.Username, &u.Role, &u.Quota)
	if e != nil {
		return u, e
	}
	_, _ = a.db.Exec("UPDATE api_tokens SET last_used=? WHERE id=?", time.Now().Unix(), id)
	return u, nil
}

func (a *App) apiTokenRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/account/tokens", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query("SELECT id,name,created,last_used FROM api_tokens WHERE user_id=? ORDER BY created DESC", u.ID)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		list := []APIToken{}
		for rows.Next() {
			var t APIToken
			if internal(w, rows.Scan(&t.ID, &t.Name, &t.Created, &t.LastUsed)) {
				return
			}
			list = append(list, t)
		}
		if !internal(w, rows.Err()) {
			respond(w, 200, list)
		}
	})
	m.HandleFunc("POST /api/account/tokens", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok || !a.allowed(w, u.ID, "upload", "share") {
			return
		}
		name := strings.TrimSpace(headerText(b.Name))
		if name == "" || len([]rune(name)) > 60 {
			fail(w, 400, "invalid_input")
			return
		}
		var count int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM api_tokens WHERE user_id=?", u.ID).Scan(&count)) {
			return
		}
		if count >= maxAPITokens {
			fail(w, 429, "too_many_requests")
			return
		}
		token := apiTokenPrefix + randomID()
		t := APIToken{ID: randomID(), Name: name, Created: time.Now().Unix()}
		if internal(w, exec(a, "INSERT INTO api_tokens(id,user_id,name,hash,created) VALUES(?,?,?,?,?)", t.ID, u.ID, t.Name, hash(token), t.Created)) {
			return
		}
		a.audit(r, u.Username, "token.created", t.Name)
		respond(w, 201, map[string]any{"token": token, "info": t})
	})
	m.HandleFunc("DELETE /api/account/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		res, e := a.db.Exec("DELETE FROM api_tokens WHERE id=? AND user_id=?", r.PathValue("id"), u.ID)
		if internal(w, e) {
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, 404, "not_found")
			return
		}
		a.audit(r, u.Username, "token.revoked", "")
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST "+uploadEndpoint, a.apiUpload)
}

// apiUpload takes one or more files as multipart form data (any field name)
// and answers with a share link. Optional query parameters: title, days (0
// for no expiry), downloads (per-file limit) and password.
func (a *App) apiUpload(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	u, e := a.tokenUser(r)
	a.mu.Unlock()
	if e != nil {
		fail(w, 401, "invalid_token")
		return
	}
	a.mu.Lock()
	allowed := a.allowed(w, u.ID, "upload", "share")
	limited := allowed && a.limited("api-upload:"+u.ID)
	p, pe := a.policy()
	a.mu.Unlock()
	if !allowed {
		return
	}
	if limited {
		fail(w, 429, "too_many_attempts_wait")
		return
	}
	if internal(w, pe) {
		return
	}
	query := r.URL.Query()
	days, permanent := p.DefaultDays, false
	if v := query.Get("days"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 || n > 365 {
			fail(w, 400, "invalid_input")
			return
		}
		days, permanent = n, n == 0
	}
	var limit int64
	if v := query.Get("downloads"); v != "" {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || !validDownloadLimit(n) {
			fail(w, 400, "invalid_input")
			return
		}
		limit = n
	}
	password := query.Get("password")
	if len(password) > 72 {
		fail(w, 400, "invalid_input")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, p.MaxShare+1<<20)
	reader, e := r.MultipartReader()
	if e != nil {
		fail(w, 400, "invalid_upload")
		return
	}
	// Each part is streamed to a temporary file in the storage directory and
	// only becomes a stored file once its size is known and fits every limit.
	type received struct {
		name, path string
		size       int64
	}
	loc, folder, e := a.activeLocation()
	if internal(w, e) {
		return
	}
	var files []received
	defer func() {
		for _, f := range files {
			if f.path != "" {
				_ = os.Remove(f.path)
			}
		}
	}()
	var total int64
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			fail(w, 400, "invalid_upload")
			return
		}
		if part.FileName() == "" {
			_ = part.Close()
			continue
		}
		name := part.FileName()
		if !validFilename(name) || len(files) >= 100 {
			fail(w, 400, "invalid_upload")
			return
		}
		path := filepath.Join(folder, partialUploadTo+randomID())
		out, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if internal(w, e) {
			return
		}
		files = append(files, received{name: name, path: path})
		room := min(p.MaxFile, p.MaxShare-total)
		free, e := a.diskFree()
		if internal(w, e) {
			return
		}
		room = min(room, free-(512<<20))
		size, e := io.Copy(out, io.LimitReader(part, room+1))
		if e == nil {
			e = out.Sync()
		}
		if ce := out.Close(); e == nil {
			e = ce
		}
		if internal(w, e) {
			return
		}
		if size > room {
			fail(w, 413, "invalid_upload")
			return
		}
		files[len(files)-1].size = size
		total += size
	}
	if len(files) == 0 {
		fail(w, 400, "invalid_upload")
		return
	}
	// Names in one share are unique; a repeated name gets a number.
	seen := map[string]bool{}
	for i := range files {
		name := files[i].name
		dot := strings.LastIndexByte(name, '.')
		if dot <= 0 {
			dot = len(name)
		}
		for n := 2; seen[strings.ToLower(name)]; n++ {
			name = files[i].name[:dot] + " (" + strconv.Itoa(n) + ")" + files[i].name[dot:]
		}
		seen[strings.ToLower(name)] = true
		files[i].name = name
	}
	hashed := ""
	if password != "" {
		h, e := bcrypt.GenerateFromPassword([]byte(password), 12)
		if internal(w, e) {
			return
		}
		hashed = string(h)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.storageRoom(w, u.ID, total) {
		return
	}
	if room, e := a.metadataRoom(u.ID, true, len(files)); e != nil && e != errLinkLimit && e != errFileLimit {
		internal(w, e)
		return
	} else if !room {
		fail(w, 429, "metadata_limit")
		return
	}
	title := strings.TrimSpace(query.Get("title"))
	if title == "" || len(title) > 160 {
		title = files[0].name
	}
	s := Share{ID: randomID(), Title: title, Created: time.Now().Unix(), Expires: time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix(), DownloadLimit: limit, Protected: hashed != "", Published: true}
	if permanent {
		s.Expires = 0
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO shares(id,owner,title,description,slug,password,expires,created,download_limit,published) VALUES(?,?,?,'','',?,?,?,?,1)", s.ID, u.ID, s.Title, hashed, s.Expires, s.Created, s.DownloadLimit); internal(w, e) {
		return
	}
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = randomID()
		if _, e = tx.Exec("INSERT INTO files(id,share_id,name,size,offset,backend) VALUES(?,?,?,?,?,?)", ids[i], s.ID, f.name, f.size, f.size, loc); internal(w, e) {
			return
		}
		if _, e = tx.Exec("INSERT INTO file_origins(file_id,name) VALUES(?,?)", ids[i], f.name); internal(w, e) {
			return
		}
	}
	// Move the received files into place before committing; a failure rolls
	// back the rows and the deferred cleanup removes what was moved.
	for i := range files {
		if internal(w, os.Rename(files[i].path, filepath.Join(folder, ids[i]))) {
			return
		}
		files[i].path = filepath.Join(folder, ids[i])
	}
	_ = syncDir(folder)
	if internal(w, tx.Commit()) {
		return
	}
	files = nil
	a.queueThumbnails(s.ID)
	a.audit(r, u.Username, "share.created", s.Title)
	link := a.linkOrigin()
	if link == "" {
		link = a.requestOrigin(r)
	}
	respond(w, 201, map[string]any{"id": s.ID, "url": link + "/s/" + s.ID, "expires": s.Expires, "files": len(ids)})
}

func (a *App) blobEntries() ([]string, error) {
	entries, e := os.ReadDir(a.blob(""))
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, e
}

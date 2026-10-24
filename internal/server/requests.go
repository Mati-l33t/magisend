package server

import (
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Upload requests: a link through which anyone can upload files to the
// owner. Uploads land in a folder of the owner's library and count against
// the owner's quota. Visitors never see what others uploaded.
//
// An upload is a batch of files reserved together (like a library upload),
// written in pieces with a per-batch token, and published on completion.
// Unfinished batches expire with the regular 24-hour draft cleanup.
const (
	maxRequestMessageRunes  = 1000
	maxOpenRequestBatches   = 10
	maxSenderRunes          = 80
	requestGrantLifetime    = time.Hour
	requestUploadsPerClient = 21
)

type UploadRequest struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	FolderID string `json:"folder_id"`
	Expires  int64  `json:"expires"`
	Created  int64  `json:"created"`
	Closed   bool   `json:"closed"`
	Notify   bool   `json:"notify"`
	// Protected is set when visitors must enter a password first.
	Protected bool `json:"protected"`
	// Owner statistics; hidden from visitors.
	Uploads    int64 `json:"uploads"`
	Files      int64 `json:"files"`
	Bytes      int64 `json:"bytes"`
	LastUpload int64 `json:"last_upload"`
}

func migrateRequests(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS upload_requests(id TEXT PRIMARY KEY, owner TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', folder_id TEXT REFERENCES folders(id) ON DELETE SET NULL, password TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL, created INTEGER NOT NULL, closed INTEGER NOT NULL DEFAULT 0, notify INTEGER NOT NULL DEFAULT 0, notify_pending INTEGER NOT NULL DEFAULT 0, notify_last INTEGER NOT NULL DEFAULT 0, notify_language TEXT NOT NULL DEFAULT 'en', notify_origin TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS request_uploads(share_id TEXT PRIMARY KEY REFERENCES shares(id) ON DELETE CASCADE, request_id TEXT NOT NULL REFERENCES upload_requests(id) ON DELETE CASCADE, token TEXT NOT NULL, sender TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS request_uploads_request ON request_uploads(request_id);
 CREATE TABLE IF NOT EXISTS request_grants(hash TEXT PRIMARY KEY, request_id TEXT NOT NULL REFERENCES upload_requests(id) ON DELETE CASCADE, expires INTEGER NOT NULL);`)
	return e
}

const requestColumns = `r.id,r.title,r.message,COALESCE(r.folder_id,''),r.expires,r.created,r.closed,r.notify,r.password<>'',
 (SELECT COUNT(*) FROM request_uploads u JOIN shares s ON s.id=u.share_id WHERE u.request_id=r.id AND s.published=1),
 (SELECT COUNT(*) FROM request_uploads u JOIN shares s ON s.id=u.share_id JOIN files f ON f.share_id=s.id WHERE u.request_id=r.id AND s.published=1),
 (SELECT COALESCE(SUM(f.size),0) FROM request_uploads u JOIN shares s ON s.id=u.share_id JOIN files f ON f.share_id=s.id WHERE u.request_id=r.id AND s.published=1),
 (SELECT COALESCE(MAX(u.created),0) FROM request_uploads u JOIN shares s ON s.id=u.share_id WHERE u.request_id=r.id AND s.published=1)`

func scanRequest(row interface{ Scan(...any) error }) (UploadRequest, error) {
	var q UploadRequest
	e := row.Scan(&q.ID, &q.Title, &q.Message, &q.FolderID, &q.Expires, &q.Created, &q.Closed, &q.Notify, &q.Protected, &q.Uploads, &q.Files, &q.Bytes, &q.LastUpload)
	return q, e
}

// openRequest loads a request a visitor may upload to, with its owner and
// password hash. Caller holds mu.
func (a *App) openRequest(w http.ResponseWriter, id string) (UploadRequest, string, string, bool) {
	var owner, password string
	q, e := scanRequest(a.db.QueryRow(`SELECT `+requestColumns+` FROM upload_requests r JOIN users u ON u.id=r.owner WHERE r.id=? AND u.disabled=0`, id))
	if e == nil {
		e = a.db.QueryRow("SELECT owner,password FROM upload_requests WHERE id=?", id).Scan(&owner, &password)
	}
	if e != nil {
		fail(w, 404, "request_not_found")
		return q, "", "", false
	}
	if q.Closed || shareExpired(q.Expires, time.Now().Unix()) {
		fail(w, 410, "request_closed")
		return q, "", "", false
	}
	return q, owner, password, true
}

// requestAccess checks the visitor's unlock cookie for a protected request.
// Caller holds mu.
func (a *App) requestAccess(w http.ResponseWriter, r *http.Request, q UploadRequest) bool {
	if !q.Protected {
		return true
	}
	c, e := r.Cookie("magisend_request_" + q.ID)
	var n int
	if e == nil {
		e = a.db.QueryRow("SELECT COUNT(*) FROM request_grants WHERE hash=? AND request_id=? AND expires>?", hash(c.Value), q.ID, time.Now().Unix()).Scan(&n)
	}
	if e != nil || n != 1 {
		fail(w, 401, "request_password_required")
		return false
	}
	return true
}

func requestForVisitor(q UploadRequest) UploadRequest {
	q.Uploads, q.Files, q.Bytes, q.LastUpload, q.Notify, q.FolderID = 0, 0, 0, 0, false, ""
	return q
}

// storageRoom checks the owner's quota, the storage budget and free disk
// space for size more bytes. Caller holds mu.
func (a *App) storageRoom(w http.ResponseWriter, owner string, size int64) bool {
	if !a.quotaRoom(w, owner, size) {
		return false
	}
	var reserved int64
	free, e := a.diskFree()
	if internal(w, e) {
		return false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size-offset),0) FROM files").Scan(&reserved)) {
		return false
	}
	if size+reserved+a.tokenUploads.total > free-(512<<20) {
		fail(w, 507, "request_full")
		return false
	}
	return true
}

// quotaRoom checks size against the owner's quota and the storage budget,
// counting what token uploads have reserved. Caller holds mu.
func (a *App) quotaRoom(w http.ResponseWriter, owner string, size int64) bool {
	var quota, used, all int64
	if internal(w, a.db.QueryRow("SELECT quota FROM users WHERE id=?", owner).Scan(&quota)) {
		return false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(c.size),0) FROM storage_charges c JOIN files f ON f.id=c.file_id JOIN shares s ON s.id=f.share_id WHERE s.owner=?", owner).Scan(&used)) {
		return false
	}
	if size > quota-used-a.tokenUploads.user[owner] {
		fail(w, 413, "request_full")
		return false
	}
	if internal(w, a.db.QueryRow("SELECT COALESCE(SUM(size),0) FROM storage_charges").Scan(&all)) {
		return false
	}
	budget, e := a.storageBudget()
	if internal(w, e) {
		return false
	}
	if size > budget-all-a.tokenUploads.total {
		fail(w, 507, "request_full")
		return false
	}
	return true
}

func (a *App) requestRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/requests", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		rows, e := a.db.Query(`SELECT `+requestColumns+` FROM upload_requests r WHERE r.owner=? ORDER BY r.created DESC`, u.ID)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		list := []UploadRequest{}
		for rows.Next() {
			q, e := scanRequest(rows)
			if internal(w, e) {
				return
			}
			list = append(list, q)
		}
		if !internal(w, rows.Err()) {
			respond(w, 200, list)
		}
	})
	m.HandleFunc("POST /api/requests", func(w http.ResponseWriter, r *http.Request) {
		a.saveRequest(w, r, "")
	})
	m.HandleFunc("PATCH /api/requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.saveRequest(w, r, r.PathValue("id"))
	})
	m.HandleFunc("DELETE /api/requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		// Received files stay in the library; unfinished batches are dropped
		// by the draft cleanup.
		res, e := a.db.Exec("DELETE FROM upload_requests WHERE id=? AND owner=?", r.PathValue("id"), u.ID)
		if internal(w, e) {
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, 404, "request_not_found")
			return
		}
		a.audit(r, u.Username, "request.deleted", "")
		respond(w, 200, map[string]bool{"ok": true})
	})

	// Public side.
	m.HandleFunc("GET /api/requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		q, owner, _, ok := a.openRequest(w, r.PathValue("id"))
		if !ok {
			return
		}
		p, e := a.policy()
		if internal(w, e) {
			return
		}
		var name string
		if internal(w, a.db.QueryRow("SELECT username FROM users WHERE id=?", owner).Scan(&name)) {
			return
		}
		unlocked := a.requestAccess(httptestDiscard{}, r, q)
		respond(w, 200, map[string]any{"request": requestForVisitor(q), "owner": name, "unlocked": unlocked, "max_file": p.MaxFile, "max_upload": p.MaxShare})
	})
	m.HandleFunc("POST /api/requests/{id}/unlock", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		clientKey, requestKey := "request-unlock:"+a.clientIP(r), "request-unlock-id:"+r.PathValue("id")
		a.mu.Lock()
		blocked := a.blocked(clientKey, unlockFailuresPerClient)
		q, _, password, ok := a.openRequest(w, r.PathValue("id"))
		a.mu.Unlock()
		if blocked {
			if ok {
				fail(w, 429, "too_many_attempts")
			}
			return
		}
		if !ok {
			return
		}
		// The request-wide count only refuses wrong guesses, like share links.
		if password != "" && bcrypt.CompareHashAndPassword([]byte(password), []byte(b.Password)) != nil {
			a.mu.Lock()
			blocked = a.blocked(requestKey, unlockFailuresPerShare)
			a.failed(clientKey)
			a.failed(requestKey)
			a.mu.Unlock()
			if blocked {
				fail(w, 429, "too_many_attempts")
				return
			}
			fail(w, 401, "wrong_password")
			return
		}
		token := randomID()
		a.mu.Lock()
		defer a.mu.Unlock()
		if internal(w, exec(a, "INSERT INTO request_grants(hash,request_id,expires) VALUES(?,?,?)", hash(token), q.ID, time.Now().Add(requestGrantLifetime).Unix())) {
			return
		}
		http.SetCookie(w, a.cookie(r, "magisend_request_"+q.ID, token, int(requestGrantLifetime.Seconds()), "/api/requests/"+q.ID))
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/requests/{id}/uploads", a.startRequestUpload)
	m.HandleFunc("PATCH /api/requests/{id}/uploads/{upload}/files/{file}", a.requestChunk)
	m.HandleFunc("POST /api/requests/{id}/uploads/{upload}/complete", a.completeRequestUpload)
}

// httptestDiscard lets an access check run without writing a response.
type httptestDiscard struct{}

func (httptestDiscard) Header() http.Header         { return http.Header{} }
func (httptestDiscard) Write(b []byte) (int, error) { return len(b), nil }
func (httptestDiscard) WriteHeader(int)             {}

func (a *App) saveRequest(w http.ResponseWriter, r *http.Request, id string) {
	var b struct {
		Title     *string `json:"title"`
		Message   *string `json:"message"`
		FolderID  *string `json:"folder_id"`
		Days      *int    `json:"days"`
		Permanent *bool   `json:"permanent"`
		Password  *string `json:"password"`
		Notify    *bool   `json:"notify"`
		Closed    *bool   `json:"closed"`
		Language  string  `json:"language"`
	}
	if !decode(w, r, &b) {
		return
	}
	password := ""
	if b.Password != nil && *b.Password != "" {
		if len(*b.Password) > 72 {
			fail(w, 400, "invalid_input")
			return
		}
		hashed, e := bcrypt.GenerateFromPassword([]byte(*b.Password), 12)
		if internal(w, e) {
			return
		}
		password = string(hashed)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.require(w, r, false)
	if !ok || !a.allowed(w, u.ID, "upload", "share") {
		return
	}
	q := UploadRequest{ID: id}
	if id == "" {
		var count int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM upload_requests WHERE owner=?", u.ID).Scan(&count)) {
			return
		}
		if count >= ownerShareLimit {
			fail(w, 429, "too_many_requests")
			return
		}
		p, e := a.policy()
		if internal(w, e) {
			return
		}
		q = UploadRequest{ID: randomID(), Created: time.Now().Unix(), Expires: time.Now().Add(time.Duration(p.DefaultDays) * 24 * time.Hour).Unix()}
	} else {
		var e error
		q, e = scanRequest(a.db.QueryRow(`SELECT `+requestColumns+` FROM upload_requests r WHERE r.id=? AND r.owner=?`, id, u.ID))
		if e != nil {
			fail(w, 404, "request_not_found")
			return
		}
	}
	if b.Title != nil {
		q.Title = strings.TrimSpace(*b.Title)
	}
	if q.Title == "" || len(q.Title) > 160 {
		fail(w, 400, "invalid_input")
		return
	}
	if b.Message != nil {
		q.Message = strings.TrimSpace(*b.Message)
		if len([]rune(q.Message)) > maxRequestMessageRunes {
			fail(w, 400, "invalid_description")
			return
		}
	}
	if b.FolderID != nil {
		if *b.FolderID != "" && !a.ownsFolder(u.ID, *b.FolderID) {
			fail(w, 404, "not_found")
			return
		}
		q.FolderID = *b.FolderID
	}
	if b.Permanent != nil && *b.Permanent {
		q.Expires = 0
	} else if b.Days != nil {
		if *b.Days < 1 || *b.Days > 365 {
			fail(w, 400, "invalid_input")
			return
		}
		q.Expires = time.Now().Add(time.Duration(*b.Days) * 24 * time.Hour).Unix()
	}
	if b.Closed != nil {
		q.Closed = *b.Closed
	}
	if b.Notify != nil {
		if *b.Notify && (!a.emailEnabled() || u.Email == "") {
			fail(w, 409, "email_not_configured")
			return
		}
		q.Notify = *b.Notify
	}
	var folder any
	if q.FolderID != "" {
		folder = q.FolderID
	}
	var e error
	if id == "" {
		_, e = a.db.Exec("INSERT INTO upload_requests(id,owner,title,message,folder_id,password,expires,created,closed,notify,notify_language,notify_origin) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
			q.ID, u.ID, q.Title, q.Message, folder, password, q.Expires, q.Created, q.Closed, q.Notify, emailLanguage(b.Language), a.requestOrigin(r))
	} else {
		_, e = a.db.Exec("UPDATE upload_requests SET title=?,message=?,folder_id=?,expires=?,closed=?,notify=?,notify_origin=? WHERE id=?", q.Title, q.Message, folder, q.Expires, q.Closed, q.Notify, a.requestOrigin(r), q.ID)
		if e == nil && b.Language != "" {
			_, e = a.db.Exec("UPDATE upload_requests SET notify_language=? WHERE id=?", emailLanguage(b.Language), q.ID)
		}
	}
	if internal(w, e) {
		return
	}
	if b.Password != nil {
		if internal(w, exec(a, "UPDATE upload_requests SET password=? WHERE id=?", password, q.ID)) {
			return
		}
		if internal(w, exec(a, "DELETE FROM request_grants WHERE request_id=?", q.ID)) {
			return
		}
	}
	saved, e := scanRequest(a.db.QueryRow(`SELECT `+requestColumns+` FROM upload_requests r WHERE r.id=?`, q.ID))
	if internal(w, e) {
		return
	}
	status, action := 200, "request.edited"
	if id == "" {
		status, action = 201, "request.created"
	}
	a.audit(r, u.Username, action, saved.Title)
	respond(w, status, saved)
}

func (a *App) startRequestUpload(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Sender string `json:"sender"`
		Files  []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if !decode(w, r, &b) {
		return
	}
	sender := strings.TrimSpace(headerText(b.Sender))
	if len([]rune(sender)) > maxSenderRunes || len(b.Files) < 1 || len(b.Files) > 100 {
		fail(w, 400, "invalid_input")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	q, owner, _, ok := a.openRequest(w, r.PathValue("id"))
	if !ok || !a.requestAccess(w, r, q) {
		return
	}
	// The owner's rights apply to everything that lands in their library.
	caps, e := a.capabilities(owner)
	if internal(w, e) {
		return
	}
	if !caps["upload"] {
		fail(w, 410, "request_closed")
		return
	}
	if a.limited("request-upload:"+a.clientIP(r)) || a.limited("request-upload-id:"+q.ID) {
		fail(w, 429, "too_many_attempts")
		return
	}
	var open int
	if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM request_uploads u JOIN shares s ON s.id=u.share_id WHERE u.request_id=? AND s.published=0", q.ID).Scan(&open)) {
		return
	}
	if open >= maxOpenRequestBatches {
		fail(w, 429, "too_many_attempts")
		return
	}
	p, e := a.policy()
	if internal(w, e) {
		return
	}
	var total int64
	names := map[string]bool{}
	for _, f := range b.Files {
		if !validFilename(f.Name) || names[f.Name] || f.Size < 0 || f.Size > p.MaxFile || f.Size > p.MaxShare-total {
			fail(w, 400, "invalid_upload")
			return
		}
		names[f.Name] = true
		total += f.Size
	}
	if room, e := a.metadataRoom(owner, false, len(b.Files)); e != nil && e != errFileLimit {
		internal(w, e)
		return
	} else if !room {
		fail(w, 507, "request_full")
		return
	}
	if !a.folderRoom(w, q.FolderID, "", len(b.Files)) || !a.libraryGrowthAllowed(w, owner, "", total, q.FolderID) || !a.storageRoom(w, owner, total) {
		return
	}
	title := q.Title
	if sender != "" {
		title = sender
	}
	s := Share{ID: randomID(), Title: title, Created: time.Now().Unix(), Files: []File{}}
	token := randomID()
	loc, _, e := a.activeLocation()
	if internal(w, e) {
		return
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO shares(id,owner,title,description,slug,password,expires,created) VALUES(?,?,?,'','','',0,?)", s.ID, owner, s.Title, s.Created); internal(w, e) {
		return
	}
	var folder any
	if q.FolderID != "" {
		folder = q.FolderID
	}
	for _, f := range b.Files {
		file := File{ID: randomID(), Name: f.Name, Size: f.Size, Backend: loc}
		if _, e = tx.Exec("INSERT INTO files(id,share_id,name,size,backend) VALUES(?,?,?,?,?)", file.ID, s.ID, file.Name, file.Size, file.Backend); internal(w, e) {
			return
		}
		if _, e = tx.Exec("INSERT INTO file_origins(file_id,name) VALUES(?,?)", file.ID, file.Name); internal(w, e) {
			return
		}
		if _, e = tx.Exec("INSERT INTO library_files(file_id,folder_id) VALUES(?,?)", file.ID, folder); internal(w, e) {
			return
		}
		s.Files = append(s.Files, file)
	}
	if _, e = tx.Exec("INSERT INTO library_containers(share_id) VALUES(?)", s.ID); internal(w, e) {
		return
	}
	if _, e = tx.Exec("INSERT INTO request_uploads(share_id,request_id,token,sender,created) VALUES(?,?,?,?,?)", s.ID, q.ID, hash(token), sender, s.Created); internal(w, e) {
		return
	}
	if internal(w, tx.Commit()) {
		return
	}
	respond(w, 201, map[string]any{"upload": s.ID, "token": token, "files": s.Files})
}

// requestBatch finds an unfinished batch of a request and checks its token.
// Caller holds mu.
func (a *App) requestBatch(w http.ResponseWriter, r *http.Request) (UploadRequest, Share, bool) {
	q, _, _, ok := a.openRequest(w, r.PathValue("id"))
	if !ok || !a.requestAccess(w, r, q) {
		return q, Share{}, false
	}
	var token string
	e := a.db.QueryRow("SELECT token FROM request_uploads WHERE share_id=? AND request_id=?", r.PathValue("upload"), q.ID).Scan(&token)
	if e != nil || token != hash(r.Header.Get("X-Upload-Token")) {
		fail(w, 404, "upload_not_found")
		return q, Share{}, false
	}
	s, _, _, e := a.loadShare(r.PathValue("upload"))
	if e != nil || a.isDeleting(s.ID) {
		fail(w, 404, "upload_not_found")
		return q, Share{}, false
	}
	if s.Published {
		fail(w, 409, "upload_complete")
		return q, s, false
	}
	return q, s, true
}

func (a *App) requestChunk(w http.ResponseWriter, r *http.Request) {
	offset, e := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if e != nil || offset < 0 {
		fail(w, 400, "invalid_upload")
		return
	}
	id := r.PathValue("file")
	check := func() (int64, int64, bool) {
		_, s, ok := a.requestBatch(w, r)
		if !ok {
			return 0, 0, false
		}
		var size, current int64
		if a.db.QueryRow("SELECT size,offset FROM files WHERE id=? AND share_id=?", id, s.ID).Scan(&size, &current) != nil {
			fail(w, 404, "upload_not_found")
			return 0, 0, false
		}
		if offset != current {
			fail(w, 409, "upload_offset")
			return 0, 0, false
		}
		return size, current, true
	}
	a.writeChunk(w, r, id, check)
}

func (a *App) completeRequestUpload(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	q, s, ok := a.requestBatch(w, r)
	if !ok || !a.verifyUploadedFiles(w, s) {
		return
	}
	if internal(w, exec(a, "UPDATE shares SET published=1 WHERE id=?", s.ID)) {
		return
	}
	a.queueThumbnails(s.ID)
	_ = exec(a, "UPDATE upload_requests SET notify_pending=notify_pending+? WHERE id=? AND notify=1", len(s.Files), q.ID)
	_ = exec(a, "UPDATE upload_requests SET push_pending=push_pending+? WHERE id=?", len(s.Files), q.ID)
	var sender string
	_ = a.db.QueryRow("SELECT sender FROM request_uploads WHERE share_id=?", s.ID).Scan(&sender)
	a.audit(r, sender, "request.received", fmt.Sprintf("%s (%d)", q.Title, len(s.Files)))
	respond(w, 200, map[string]int{"files": len(s.Files)})
}

// flushRequestNotifications tells owners about received files, bundled like
// download notifications.
func (a *App) flushRequestNotifications(now time.Time) {
	if !a.emailEnabled() {
		return
	}
	rows, e := a.db.Query(`SELECT r.id,r.title,r.notify_pending,r.notify_language,r.notify_origin,u.email FROM upload_requests r JOIN users u ON u.id=r.owner
 WHERE r.notify=1 AND r.notify_pending>0 AND r.notify_last<=? AND u.email<>'' AND u.disabled=0`, now.Add(-notifyInterval).Unix())
	if e != nil {
		return
	}
	type due struct {
		id, title, language, origin, email string
		count                              int64
	}
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.title, &d.count, &d.language, &d.origin, &d.email) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		if a.queueMail(receivedMail(d.email, d.title, d.count, d.language, d.origin)) {
			_, _ = a.db.Exec("UPDATE upload_requests SET notify_pending=notify_pending-?, notify_last=? WHERE id=?", d.count, now.Unix(), d.id)
		}
	}
}

func receivedMail(to, title string, count int64, language, origin string) outgoingMail {
	t := emailText[emailLanguage(language)]
	line := fmt.Sprintf(t["receivedOne"], title)
	if count > 1 {
		line = fmt.Sprintf(t["received"], count, title)
	}
	text := line + "\n"
	body := `<p style="font-size:16px;margin:0">` + html.EscapeString(line) + `</p>`
	if origin != "" {
		link := origin + "/library"
		text += "\n" + t["manage"] + ": " + link + "\n"
		body += emailButton(link, t["manage"])
	}
	return outgoingMail{to: to, subject: fmt.Sprintf(t["receivedSubject"], title), text: text, body: emailFrame(body)}
}

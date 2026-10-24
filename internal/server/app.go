package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"

	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const GiB int64 = 1 << 30
const chunkMax int64 = 4 << 20

// App requires exclusive single-process ownership of its database and storage
// root. Quota reservations and filesystem mutations are serialized by mu;
// running another App against the same root is not supported.
type App struct {
	db                 *sql.DB
	root, publicOrigin string
	setupCode          string
	indexSums          map[string][32]byte
	// When a client was last counted downloading a file, so its later ranges
	// continue that download instead of counting again.
	resumes map[string]time.Time
	// Thumbnails decoded in a request because the queue was full.
	thumbInline chan struct{}
	// The site URL for links (linkOrigin).
	siteURL atomic.Value
	// Library containers a ZIP is being unpacked into.
	extracting map[string]bool
	// Bytes reserved by token uploads in progress, in total and per owner.
	tokenUploads struct {
		total int64
		user  map[string]int64
	}
	// Slots each client holds.
	downloadClients, mutationClients, uploadClients clientSlots
	mu                                              sync.Mutex
	attempts                                        map[string]attempt
	mutations, uploads, downloads                   chan struct{}
	thumbs                                          chan string
	thumbInflight                                   sync.Map
	thumbBlocked                                    sync.Map
	trustedProxies                                  []*net.IPNet
	ui                                              fs.FS
	updates                                         *Updates
	mail                                            chan outgoingMail
	push                                            chan pushJob
	backup                                          backupState
	sendMail                                        func(EmailSettings, outgoingMail) error
	stores                                          storages
}
type attempt struct {
	count int
	until time.Time
}
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Quota    int64  `json:"quota"`
	Used     int64  `json:"used"`
	Disabled bool   `json:"disabled"`
	Email    string `json:"email"`
	// TwoFactor is set when the account signs in with an authenticator code.
	TwoFactor bool `json:"two_factor"`
	// DisplayName is optional; Avatar is the picture's version, 0 for none.
	DisplayName string `json:"display_name"`
	Avatar      int64  `json:"avatar"`
	// Emails a day before one of the person's links expires.
	RemindExpiry bool `json:"remind_expiry"`
}
type Policy struct {
	RegistrationOpen bool  `json:"registration_open"`
	DefaultQuota     int64 `json:"default_quota"`
	MaxFile          int64 `json:"max_file"`
	MaxShare         int64 `json:"max_share"`
	DefaultDays      int   `json:"default_days"`
	StorageBudget    int64 `json:"storage_budget"`
	// Files an account, and the whole server, may hold.
	FilesPerUser int `json:"files_per_user"`
	FilesTotal   int `json:"files_total"`
}

func NewApp(root, origin string) (*App, error) {
	_, existing := os.Stat(filepath.Join(root, "magisend.sqlite"))
	if existing == nil {
		info, e := os.Stat(filepath.Join(root, "blobs"))
		if e != nil {
			return nil, e
		}
		if !info.IsDir() {
			return nil, errors.New("storage is not a directory")
		}
	} else if !os.IsNotExist(existing) {
		return nil, existing
	} else if e := os.MkdirAll(filepath.Join(root, "blobs"), 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite3", filepath.Join(root, "magisend.sqlite")+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_synchronous=FULL")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	schema := `CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, password TEXT NOT NULL, role TEXT NOT NULL, quota INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY, user_id TEXT REFERENCES users(id) ON DELETE CASCADE, expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS settings(id INTEGER PRIMARY KEY CHECK(id=1),registration_open INTEGER NOT NULL DEFAULT 0,default_quota INTEGER NOT NULL DEFAULT 1073741824,max_file INTEGER NOT NULL DEFAULT 1073741824,max_share INTEGER NOT NULL DEFAULT 2147483648,default_days INTEGER NOT NULL DEFAULT 7);
 INSERT OR IGNORE INTO settings(id) VALUES(1);
 CREATE TABLE IF NOT EXISTS shares(id TEXT PRIMARY KEY, owner TEXT REFERENCES users(id),title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', slug TEXT NOT NULL DEFAULT '', password TEXT NOT NULL DEFAULT '',expires INTEGER NOT NULL,created INTEGER NOT NULL,published INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS files(id TEXT PRIMARY KEY,share_id TEXT REFERENCES shares(id) ON DELETE CASCADE,name TEXT NOT NULL,size INTEGER NOT NULL,offset INTEGER NOT NULL DEFAULT 0,backend TEXT NOT NULL DEFAULT 'local');
 CREATE TABLE IF NOT EXISTS thumbnails(file_id TEXT PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,bytes BLOB NOT NULL,format TEXT NOT NULL CHECK(format IN('png','jpeg')),width INTEGER NOT NULL,height INTEGER NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS grants(hash TEXT PRIMARY KEY,share_id TEXT REFERENCES shares(id) ON DELETE CASCADE,expires INTEGER NOT NULL);
 DROP TABLE IF EXISTS handoffs;
 CREATE TABLE IF NOT EXISTS deleting(share_id TEXT PRIMARY KEY REFERENCES shares(id) ON DELETE CASCADE);
 CREATE TABLE IF NOT EXISTS installation(id INTEGER PRIMARY KEY CHECK(id=1),completed INTEGER NOT NULL DEFAULT 0 CHECK(completed IN(0,1)));
 INSERT OR IGNORE INTO installation(id) VALUES(1);
 UPDATE installation SET completed=1 WHERE EXISTS(SELECT 1 FROM users);`
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	if e = migrate(db); e != nil {
		db.Close()
		return nil, e
	}
	if existing != nil {
		// A new installation sizes its storage budget to the disk it runs on.
		if _, e = db.Exec("UPDATE settings SET storage_budget=? WHERE id=1", suggestedBudget(root)); e != nil {
			db.Close()
			return nil, e
		}
	}
	a := &App{db: db, root: root, publicOrigin: origin, attempts: make(map[string]attempt), mutations: make(chan struct{}, mutationSlots), uploads: make(chan struct{}, uploadSlots), downloads: make(chan struct{}, downloadSlots), thumbs: make(chan string, thumbQueueDepth), ui: webInterface(), updates: newUpdates(), mail: make(chan outgoingMail, emailQueueDepth), push: make(chan pushJob, emailQueueDepth), sendMail: sendSMTP, indexSums: map[string][32]byte{}, resumes: map[string]time.Time{}}
	a.downloadClients.count = map[string]int{}
	a.tokenUploads.user = map[string]int64{}
	a.extracting = map[string]bool{}
	a.thumbInline = make(chan struct{}, 2)
	a.mutationClients.count = map[string]int{}
	a.uploadClients.count = map[string]int{}
	go a.thumbnailWorker()
	if e = a.loadStorage(); e != nil {
		db.Close()
		return nil, e
	}
	go a.storageMonitor()
	if e = a.verifyCommittedStorage(); e != nil {
		db.Close()
		return nil, e
	}
	if e = a.verifyHistoryStorage(); e != nil {
		db.Close()
		return nil, e
	}
	if e = a.collectOrphanBlobs(); e != nil {
		db.Close()
		return nil, e
	}
	// After unknown files were set aside, so a restored older database cannot
	// erase what the index knew about them.
	if e = a.writeStorageIndexes(time.Now()); e != nil {
		log.Print("storage index: ", e)
	}
	a.loadSiteURL()
	if e = a.prepareSetupCode(); e != nil {
		db.Close()
		return nil, e
	}
	return a, nil
}

// Additive only, and idempotent: an existing preview or production database keeps
// every row and gains the new columns with their defaults.
func migrate(db *sql.DB) error {
	columns := map[string]bool{}
	rows, e := db.Query("PRAGMA table_info(shares)")
	if e != nil {
		return e
	}
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var fallback any
		if e = rows.Scan(&cid, &name, &kind, &notNull, &fallback, &primaryKey); e != nil {
			rows.Close()
			return e
		}
		columns[name] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, column := range []string{"description", "slug"} {
		if !columns[column] {
			if _, e = db.Exec("ALTER TABLE shares ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''"); e != nil {
				return e
			}
		}
	}
	_, e = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS shares_slug_unique ON shares(slug) WHERE slug<>''")
	if e != nil {
		return e
	}
	if e = migrateCapabilities(db); e != nil {
		return e
	}
	if e = migrateFolders(db); e != nil {
		return e
	}
	if e = migrateLibraryShares(db); e != nil {
		return e
	}
	if e = migrateText(db); e != nil {
		return e
	}
	if e = migrateLibraryLifecycle(db); e != nil {
		return e
	}
	if e = migrateTrash(db); e != nil {
		return e
	}
	if e = migrateReminders(db); e != nil {
		return e
	}
	if e = migrateCollaboration(db); e != nil {
		return e
	}
	if e = migrateAccounts(db); e != nil {
		return e
	}
	if e = migrateEmail(db); e != nil {
		return e
	}
	if e = migrateDownloads(db); e != nil {
		return e
	}
	if e = migrateStars(db); e != nil {
		return e
	}
	if e = migrateRequests(db); e != nil {
		return e
	}
	if e = migrateTwoFactor(db); e != nil {
		return e
	}
	if e = migrateInvites(db); e != nil {
		return e
	}
	if e = migrateAudit(db); e != nil {
		return e
	}
	if e = migrateAPITokens(db); e != nil {
		return e
	}
	if e = migrateBranding(db); e != nil {
		return e
	}
	if e = migrateProfiles(db); e != nil {
		return e
	}
	if e = migratePush(db); e != nil {
		return e
	}
	if e = migrateScrub(db); e != nil {
		return e
	}
	if e = migrateBackup(db); e != nil {
		return e
	}
	if e = migrateNotes(db); e != nil {
		return e
	}
	if e = migrateDevices(db); e != nil {
		return e
	}
	return migrateStorage(db)
}
func randomID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	value, ok := apiErrors[msg]
	if !ok {
		value = apiErrors["operation_failed"]
	}
	respond(w, status, map[string]string{"error": value.message, "error_code": value.code})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "Ogiltiga indata")
		return false
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		fail(w, 400, "Ogiltiga indata")
		return false
	}
	return true
}
func (a *App) policy() (Policy, error) {
	p := Policy{}
	e := a.db.QueryRow(`SELECT registration_open,default_quota,max_file,max_share,default_days,storage_budget,files_per_user,files_total FROM settings WHERE id=1`).Scan(&p.RegistrationOpen, &p.DefaultQuota, &p.MaxFile, &p.MaxShare, &p.DefaultDays, &p.StorageBudget, &p.FilesPerUser, &p.FilesTotal)
	return p, e
}
func (a *App) countUsers() int {
	var n int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); e != nil {
		return -1
	}
	return n
}
func validUsername(s string) bool {
	if len(s) < 3 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func (a *App) addUser(name, password, role string, quota int64) (User, error) {
	u := User{ID: randomID(), Username: name, Role: role, Quota: quota}
	if !validUsername(name) || !validPassword(password) {
		return u, errors.New("Användarnamn: 3–40 gemener/siffror. Lösenord: 12–72 byte")
	}
	p, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return u, e
	}
	_, e = a.db.Exec("INSERT INTO users(id,username,password,role,quota) VALUES(?,?,?,?,?)", u.ID, name, string(p), role, quota)
	return u, e
}
func (a *App) setupRequired() (bool, error) {
	var completed bool
	e := a.db.QueryRow("SELECT completed FROM installation WHERE id=1").Scan(&completed)
	return !completed, e
}
func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	required, e := a.setupRequired()
	if internal(w, e) {
		return
	}
	if !required {
		fail(w, 409, "Installationen är redan slutförd")
		return
	}
	var b struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		Email     string `json:"email"`
		SetupCode string `json:"setup_code"`
	}
	if !decode(w, r, &b) {
		return
	}
	email, valid := optionalEmail(b.Email)
	if !valid {
		fail(w, 400, "invalid_email")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	required, e = a.setupRequired()
	if internal(w, e) {
		return
	}
	if !required || a.countUsers() != 0 {
		fail(w, 409, "Installationen är redan slutförd")
		return
	}
	key := "setup:" + a.clientIP(r)
	if a.blocked(key, setupFailures) {
		fail(w, 429, "too_many_attempts_wait")
		return
	}
	if !a.setupCodeMatches(b.SetupCode) {
		a.failed(key)
		fail(w, 403, "setup_code_invalid")
		return
	}
	if !validUsername(b.Username) || !validPassword(b.Password) {
		fail(w, 400, "Användarnamn: 3–40 gemener/siffror. Lösenord: 12–72 byte")
		return
	}
	password, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
	if internal(w, e) {
		return
	}
	u := User{ID: randomID(), Username: b.Username, Role: "admin", Quota: GiB, Email: email}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO users(id,username,password,role,quota,email) VALUES(?,?,?,?,?,?)", u.ID, u.Username, string(password), u.Role, u.Quota, u.Email)
	if internal(w, e) {
		return
	}
	_, e = tx.Exec("UPDATE installation SET completed=1 WHERE id=1")
	if internal(w, e) {
		return
	}
	if internal(w, tx.Commit()) {
		return
	}
	a.setupDone()
	a.audit(r, u.Username, "account.setup", "")
	respond(w, 201, u)
}
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if e := a.db.Ping(); e != nil {
			fail(w, 503, "Databasen är inte tillgänglig")
			return
		}
		respond(w, 200, map[string]string{"status": "ok", "version": version})
	})
	m.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.policy()
		if e != nil {
			fail(w, 503, "Databasen är inte tillgänglig")
			return
		}
		required, e := a.setupRequired()
		if internal(w, e) {
			return
		}
		respond(w, 200, map[string]any{"setup_required": required, "registration_open": p.RegistrationOpen, "version": version, "password_reset": a.passwordResetAvailable(), "branding": a.branding()})
	})
	m.HandleFunc("POST /api/setup", a.setup)
	a.authRoutes(m)
	a.shareRoutes(m)
	a.adminRoutes(m)
	a.capabilityRoutes(m)
	a.folderRoutes(m)
	a.starRoutes(m)
	a.requestRoutes(m)
	a.twoFactorRoutes(m)
	a.inviteRoutes(m)
	a.mediaRoutes(m)
	a.auditRoutes(m)
	a.apiTokenRoutes(m)
	a.brandingRoutes(m)
	a.profileRoutes(m)
	a.resumeRoutes(m)
	a.storageRoutes(m)
	a.unclaimedRoutes(m)
	a.libraryShareRoutes(m)
	a.textRoutes(m)
	a.versionRoutes(m)
	a.pushRoutes(m)
	a.extractRoutes(m)
	a.backupRoutes(m)
	a.noteRoutes(m)
	a.libraryDeleteRoutes(m)
	a.trashRoutes(m)
	a.collaborationRoutes(m)
	a.collaboratorFileRoutes(m)
	a.lifecycleRoutes(m)
	a.accountRoutes(m)
	a.updateRoutes(m)
	a.emailRoutes(m)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Hittades inte") })
	m.HandleFunc("/", a.frontend)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; font-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if a.secureOrigin() {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !apiRequest(r) {
			if r.Header.Get("X-MagiSend") != "1" {
				fail(w, 403, "Saknad säkerhetsheader")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				scheme := "http"
				if a.secureRequest(r) {
					scheme = "https"
				}
				if e != nil || !(u.Scheme == scheme && u.Host == r.Host || a.publicOrigin != "" && origin == a.publicOrigin) {
					fail(w, 403, "Felaktigt ursprung")
					return
				}
			}
		}
		defer func() {
			if recover() != nil {
				log.Print("request panic")
				fail(w, 500, "Internt fel")
			}
		}()
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
				slots, clients, perClient, deadline := a.mutations, &a.mutationClients, mutationsPerClient, 30*time.Second
				if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/uploads/") {
					slots, clients, perClient = a.uploads, &a.uploadClients, uploadsPerClient
				}
				// A token upload sends whole files in one request.
				if r.URL.Path == uploadEndpoint {
					slots, clients, perClient, deadline = a.uploads, &a.uploadClients, uploadsPerClient, time.Hour
				}
				release, ok := a.acquireFor(w, r, slots, clients, perClient)
				if !ok {
					return
				}
				defer release()
				// ResponseController also bounds bodies when Handler is served by a caller
				// other than main. Test recorders do not support connection deadlines.
				_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(deadline))
				// Every request that is not a file stream is read whole here, so no
				// handler ever waits on a slow client while it holds mu.
				if !streamingRequest(r) {
					body, e := io.ReadAll(io.LimitReader(r.Body, maxBufferedBody+1))
					if e != nil || len(body) > maxBufferedBody {
						fail(w, 413, "Ogiltiga indata")
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
				}
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		}
		m.ServeHTTP(&idleResponseWriter{ResponseWriter: w}, r)
	})
}
func internal(w http.ResponseWriter, e error) bool {
	if errors.Is(e, errStorageOffline) {
		fail(w, 503, "storage_offline")
		return true
	}
	if e != nil {
		log.Printf("operation failed: %T", e)
		fail(w, 503, "Operationen kunde inte slutföras")
		return true
	}
	return false
}

// validToken checks the shape of a random id: 48 lowercase hex characters.
func validToken(token string) bool {
	if len(token) != 48 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

package server

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// The activity log: security-relevant events for administrators. Entries
// are kept for 90 days. Downloads are not logged; owners see their counts.
const auditRetention = 90 * 24 * time.Hour

type AuditEvent struct {
	ID     int64  `json:"id"`
	At     int64  `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	IP     string `json:"ip"`
}

func migrateAudit(db *sql.DB) error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, actor TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, target TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS audit_at ON audit(at);`)
	return e
}

// audit records one event; a failure to log never fails the action itself.
// actor is a username (or empty for visitors); target names what was acted on.
func (a *App) audit(r *http.Request, actor, action, target string) {
	ip := ""
	if r != nil {
		ip = a.clientIP(r)
	}
	if len(target) > 200 {
		target = target[:200]
	}
	_, _ = a.db.Exec("INSERT INTO audit(at,actor,action,target,ip) VALUES(?,?,?,?,?)", time.Now().Unix(), actor, action, target, ip)
}

func (a *App) username(id string) string {
	var name string
	_ = a.db.QueryRow("SELECT username FROM users WHERE id=?", id).Scan(&name)
	return name
}

func (a *App) auditRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/activity", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if before <= 0 {
			before = 1 << 62
		}
		// The page asks for a few at a time; 50 when it does not say.
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 100 {
			limit = 50
		}
		query, args := "SELECT id,at,actor,action,target,ip FROM audit WHERE id<?", []any{before}
		if action := r.URL.Query().Get("action"); action != "" {
			query += " AND action LIKE ?"
			args = append(args, action+"%")
		}
		rows, e := a.db.Query(query+" ORDER BY id DESC LIMIT ?", append(args, limit)...)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		list := []AuditEvent{}
		for rows.Next() {
			var ev AuditEvent
			if internal(w, rows.Scan(&ev.ID, &ev.At, &ev.Actor, &ev.Action, &ev.Target, &ev.IP)) {
				return
			}
			list = append(list, ev)
		}
		if !internal(w, rows.Err()) {
			respond(w, 200, list)
		}
	})
	m.HandleFunc("GET /api/admin/overview", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		now := time.Now().Unix()
		var o struct {
			Users        int64 `json:"users"`
			Disabled     int64 `json:"disabled"`
			TwoFactor    int64 `json:"two_factor"`
			ActiveLinks  int64 `json:"active_links"`
			OpenRequests int64 `json:"open_requests"`
			Files        int64 `json:"files"`
			Used         int64 `json:"used"`
			Budget       int64 `json:"budget"`
			// Free space where new files go; left out while that place is offline.
			DiskFree      *int64 `json:"disk_free,omitempty"`
			Downloads     int64  `json:"downloads"`
			FailedLogins  int64  `json:"failed_logins"`
			UploadsToday  int64  `json:"uploads_today"`
			LastDownload  int64  `json:"last_download"`
			EmailEnabled  bool   `json:"email_enabled"`
			PublicAddress bool   `json:"public_address"`
			// Paths of storage folders that cannot be reached right now.
			StorageOffline []string `json:"storage_offline"`
		}
		day := time.Now().Add(-24 * time.Hour).Unix()
		e := a.db.QueryRow(`SELECT
 (SELECT COUNT(*) FROM users),
 (SELECT COUNT(*) FROM users WHERE disabled=1),
 (SELECT COUNT(*) FROM users WHERE totp_enabled=1),
 (SELECT COUNT(*) FROM shares s WHERE s.published=1 AND (s.expires=0 OR s.expires>?) AND NOT EXISTS(SELECT 1 FROM library_containers c WHERE c.share_id=s.id)),
 (SELECT COUNT(*) FROM upload_requests WHERE closed=0 AND (expires=0 OR expires>?)),
 (SELECT COUNT(*) FROM files f JOIN shares s ON s.id=f.share_id WHERE s.published=1),
 (SELECT COALESCE(SUM(size),0) FROM storage_charges),
 (SELECT COALESCE(SUM(downloads),0) FROM shares),
 (SELECT COUNT(*) FROM audit WHERE action='login.failed' AND at>?),
 (SELECT COUNT(*) FROM files f JOIN shares s ON s.id=f.share_id WHERE s.published=1 AND s.created>?),
 (SELECT COALESCE(MAX(last_download),0) FROM shares)`, now, now, day, day).Scan(
			&o.Users, &o.Disabled, &o.TwoFactor, &o.ActiveLinks, &o.OpenRequests, &o.Files, &o.Used, &o.Downloads, &o.FailedLogins, &o.UploadsToday, &o.LastDownload)
		if internal(w, e) {
			return
		}
		if o.Budget, e = a.storageBudget(); internal(w, e) {
			return
		}
		if free, e := a.diskFree(); e == nil {
			o.DiskFree = &free
		}
		o.EmailEnabled = a.emailEnabled()
		o.PublicAddress = a.publicOrigin != ""
		o.StorageOffline = []string{}
		a.stores.mu.RLock()
		for _, l := range a.stores.locations {
			if !l.online {
				o.StorageOffline = append(o.StorageOffline, l.Path)
			}
		}
		a.stores.mu.RUnlock()
		respond(w, 200, o)
	})
}

func (a *App) pruneAudit(now time.Time) error {
	return exec(a, "DELETE FROM audit WHERE at<?", now.Add(-auditRetention).Unix())
}

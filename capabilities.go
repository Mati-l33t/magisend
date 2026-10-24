package main

import (
	"database/sql"
	"net/http"
	"strings"
)

var capabilityDefaults = map[string]bool{"folders": true, "upload": true, "share": true, "edit_own": false, "grant_edit": false, "edit_shared": false, "add_shared": false, "delete_shared": false}

// Separate tables preserve existing users and policy payloads. Missing individual
// rows inherit dynamically. Administrators always have every capability; the
// rights on someone else's link still have to be granted by its sender.
func migrateCapabilities(db *sql.DB) error {
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`CREATE TABLE IF NOT EXISTS capability_defaults(name TEXT PRIMARY KEY,value INTEGER NOT NULL CHECK(value IN(0,1)));
 CREATE TABLE IF NOT EXISTS capability_overrides(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL REFERENCES capability_defaults(name),value INTEGER NOT NULL CHECK(value IN(0,1)),PRIMARY KEY(user_id,name));`); e != nil {
		return e
	}
	for name, value := range capabilityDefaults {
		if _, e = tx.Exec("INSERT OR IGNORE INTO capability_defaults(name,value) VALUES(?,?)", name, value); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (a *App) capabilities(user string) (map[string]bool, error) {
	var role string
	if e := a.db.QueryRow("SELECT role FROM users WHERE id=?", user).Scan(&role); e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if role == "admin" {
		all := map[string]bool{}
		for name := range capabilityDefaults {
			all[name] = true
		}
		return all, nil
	}
	rows, e := a.db.Query(`SELECT d.name,COALESCE(o.value,d.value) FROM capability_defaults d LEFT JOIN capability_overrides o ON o.name=d.name AND o.user_id=?`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var name string
		var value bool
		if e = rows.Scan(&name, &value); e != nil {
			return nil, e
		}
		result[name] = value
	}
	return result, rows.Err()
}
func (a *App) allowed(w http.ResponseWriter, user string, names ...string) bool {
	caps, e := a.capabilities(user)
	if internal(w, e) {
		return false
	}
	for _, name := range names {
		if !caps[name] {
			respond(w, 403, map[string]string{"error": "Permission denied", "error_code": "permission_denied"})
			return false
		}
	}
	return true
}

// Called by require, including existing mutation handlers' mutex-held rechecks.
// Keep admin management independent, and never gate ordinary read/download paths.
func (a *App) legacyCapabilities(w http.ResponseWriter, r *http.Request, user string) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/uploads/") && (r.Method == "GET" || r.Method == "PATCH") {
		return a.allowed(w, user, "upload")
	}
	if r.Method == "POST" && p == "/api/shares" {
		return a.allowed(w, user, "upload", "share")
	}
	if r.Method == "POST" && strings.HasPrefix(p, "/api/shares/") {
		if strings.HasSuffix(p, "/publish") {
			if a.isLibraryContainer(r.PathValue("id")) {
				return a.allowed(w, user, "upload")
			}
			return a.allowed(w, user, "upload", "share")
		}
	}
	return true
}
func (a *App) capabilityRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		caps, e := a.capabilities(u.ID)
		if !internal(w, e) {
			respond(w, 200, caps)
		}
	})
	m.HandleFunc("GET /api/admin/capabilities", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		defaults, e := a.capabilities("")
		if internal(w, e) {
			return
		}
		rows, e := a.db.Query("SELECT user_id,name,value FROM capability_overrides ORDER BY user_id,name")
		if internal(w, e) {
			return
		}
		defer rows.Close()
		overrides := map[string]map[string]bool{}
		for rows.Next() {
			var id, name string
			var value bool
			if internal(w, rows.Scan(&id, &name, &value)) {
				return
			}
			if overrides[id] == nil {
				overrides[id] = map[string]bool{}
			}
			overrides[id][name] = value
		}
		if internal(w, rows.Err()) {
			return
		}
		respond(w, 200, map[string]any{"defaults": defaults, "overrides": overrides})
	})
	update := func(defaults bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, ok := a.require(w, r, true); !ok {
				return
			}
			var values map[string]*bool
			if !decode(w, r, &values) {
				return
			}
			if len(values) == 0 {
				fail(w, 400, "Ogiltiga indata")
				return
			}
			for key, value := range values {
				if _, ok := capabilityDefaults[key]; !ok || defaults && value == nil {
					fail(w, 400, "Ogiltiga indata")
					return
				}
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if _, ok := a.require(w, r, true); !ok {
				return
			}
			id := r.PathValue("id")
			if !defaults {
				var n int
				if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", id).Scan(&n)) {
					return
				}
				if n != 1 {
					fail(w, 404, "Kontot finns inte")
					return
				}
			}
			tx, e := a.db.Begin()
			if internal(w, e) {
				return
			}
			defer tx.Rollback()
			for key, value := range values {
				if defaults {
					_, e = tx.Exec("UPDATE capability_defaults SET value=? WHERE name=?", *value, key)
				} else if value == nil {
					_, e = tx.Exec("DELETE FROM capability_overrides WHERE user_id=? AND name=?", id, key)
				} else {
					_, e = tx.Exec("INSERT INTO capability_overrides(user_id,name,value) VALUES(?,?,?) ON CONFLICT(user_id,name) DO UPDATE SET value=excluded.value", id, key, *value)
				}
				if internal(w, e) {
					return
				}
			}
			if !internal(w, tx.Commit()) {
				if admin, e := a.currentUser(r); e == nil {
					target := ""
					if !defaults {
						target = a.username(id)
					}
					a.audit(r, admin.Username, "settings.permissions", target)
				}
				respond(w, 200, map[string]bool{"ok": true})
			}
		}
	}
	m.HandleFunc("PUT /api/admin/capabilities/defaults", update(true))
	m.HandleFunc("PUT /api/admin/capabilities/users/{id}", update(false))
}

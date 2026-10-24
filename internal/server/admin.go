package server

import (
	"net/http"
)

func (a *App) adminRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		rows, e := a.db.Query(`SELECT u.id,u.username,u.role,u.quota,u.disabled,u.email,u.totp_enabled,u.display_name,CASE WHEN u.avatar IS NULL THEN 0 ELSE u.avatar_version END,COALESCE((SELECT SUM(c.size) FROM storage_charges c JOIN files f ON f.id=c.file_id JOIN shares s ON f.share_id=s.id WHERE s.owner=u.id),0) FROM users u ORDER BY username`)
		if internal(w, e) {
			return
		}
		defer rows.Close()
		users := []User{}
		for rows.Next() {
			u := User{}
			if internal(w, rows.Scan(&u.ID, &u.Username, &u.Role, &u.Quota, &u.Disabled, &u.Email, &u.TwoFactor, &u.DisplayName, &u.Avatar, &u.Used)) {
				return
			}
			users = append(users, u)
		}
		if internal(w, rows.Err()) {
			return
		}
		respond(w, 200, users)
	})
	m.HandleFunc("POST /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		var b struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Quota    int64  `json:"quota"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		budget, e := a.storageBudget()
		if internal(w, e) {
			return
		}
		if b.Quota < 0 || b.Quota > budget {
			fail(w, 400, "invalid_quota")
			return
		}
		u, e := a.addUser(b.Username, b.Password, "user", b.Quota)
		if e != nil {
			fail(w, 400, "Kontot kunde inte skapas. Kontrollera namn och lösenord")
			return
		}
		if admin, e := a.currentUser(r); e == nil {
			a.audit(r, admin.Username, "account.created", u.Username)
		}
		respond(w, 201, u)
	})
	m.HandleFunc("PATCH /api/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		var b struct {
			Quota    *int64  `json:"quota"`
			Password *string `json:"password"`
			Disabled *bool   `json:"disabled"`
			// ResetTwoFactor turns off two-step login for a lost device.
			ResetTwoFactor bool `json:"reset_two_factor"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.ResetTwoFactor {
			a.mu.Lock()
			defer a.mu.Unlock()
			if _, ok := a.require(w, r, true); !ok {
				return
			}
			var n int
			if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", r.PathValue("id")).Scan(&n)) {
				return
			}
			if n != 1 {
				fail(w, 404, "account_not_found")
				return
			}
			if !internal(w, a.disableTwoFactor(r.PathValue("id"))) {
				if admin, ok := a.currentUser(r); ok == nil {
					a.audit(r, admin.Username, "twofactor.reset", a.username(r.PathValue("id")))
				}
				respond(w, 200, map[string]bool{"ok": true})
			}
			return
		}
		if b.Quota == nil && b.Password == nil && b.Disabled == nil {
			fail(w, 400, "invalid_input")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		a.updateAccount(w, r, admin, r.PathValue("id"), b.Quota, b.Password, b.Disabled)
	})
	m.HandleFunc("DELETE /api/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if id == admin.ID {
			fail(w, 409, "cannot_change_own_account")
			return
		}
		var exists int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", id).Scan(&exists)) {
			return
		}
		if exists != 1 {
			fail(w, 404, "account_not_found")
			return
		}
		name := a.username(id)
		if internal(w, a.deleteAccount(id)) {
			return
		}
		a.audit(r, admin.Username, "account.deleted", name)
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/admin/policy", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		p, e := a.policy()
		if internal(w, e) {
			return
		}
		respond(w, 200, p)
	})
	m.HandleFunc("PUT /api/admin/policy", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		p := Policy{}
		if !decode(w, r, &p) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		if p.StorageBudget == 0 {
			// Clients that predate the budget setting keep the current value.
			budget, e := a.storageBudget()
			if internal(w, e) {
				return
			}
			p.StorageBudget = budget
		}
		if p.FilesPerUser == 0 || p.FilesTotal == 0 {
			current, e := a.policy()
			if internal(w, e) {
				return
			}
			if p.FilesPerUser == 0 {
				p.FilesPerUser = current.FilesPerUser
			}
			if p.FilesTotal == 0 {
				p.FilesTotal = current.FilesTotal
			}
		}
		if p.StorageBudget < GiB || p.StorageBudget > maxStorageBudget || p.DefaultQuota < 0 || p.DefaultQuota > p.StorageBudget || p.MaxFile < 1 || p.MaxFile > p.StorageBudget || p.MaxShare < p.MaxFile || p.MaxShare > p.StorageBudget || p.DefaultDays < 1 || p.DefaultDays > 365 || p.FilesPerUser < 100 || p.FilesPerUser > p.FilesTotal || p.FilesTotal > maxFilesTotal {
			fail(w, 400, "invalid_policy")
			return
		}
		if internal(w, exec(a, "UPDATE settings SET registration_open=?,default_quota=?,max_file=?,max_share=?,default_days=?,storage_budget=?,files_per_user=?,files_total=? WHERE id=1", p.RegistrationOpen, p.DefaultQuota, p.MaxFile, p.MaxShare, p.DefaultDays, p.StorageBudget, p.FilesPerUser, p.FilesTotal)) {
			return
		}
		if admin, e := a.currentUser(r); e == nil {
			a.audit(r, admin.Username, "settings.policy", "")
		}
		respond(w, 200, p)
	})
}

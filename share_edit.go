package main

import (
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// editShare changes a sent share after it was created. Every field is
// optional; an omitted field keeps its value. It also edits library links;
// their sharing permissions have their own endpoint (PATCH
// /api/library/shares/{id}).
func (a *App) editShare(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Slug        *string `json:"slug"`
		Days        *int    `json:"days"`
		Permanent   *bool   `json:"permanent"`
		Password    *string `json:"password"`
		Limit       *int64  `json:"download_limit"`
	}
	if !decode(w, r, &b) {
		return
	}
	// Hash outside the lock; bcrypt is deliberately slow.
	password := ""
	if b.Password != nil {
		if len(*b.Password) > 72 {
			fail(w, 400, "invalid_input")
			return
		}
		if *b.Password != "" {
			hashed, e := bcrypt.GenerateFromPassword([]byte(*b.Password), 12)
			if internal(w, e) {
				return
			}
			password = string(hashed)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.require(w, r, false)
	if !ok || !a.allowed(w, u.ID, "share") {
		return
	}
	s, ok := a.ownShare(w, r)
	if !ok {
		return
	}
	if a.isLibraryContainer(s.ID) || a.isDeleting(s.ID) {
		fail(w, 404, "share_not_found")
		return
	}
	now := time.Now()
	if shareExpired(s.Expires, now.Unix()) {
		fail(w, 410, "share_expired")
		return
	}
	if b.Title != nil {
		title := strings.TrimSpace(*b.Title)
		if title == "" || len(title) > 160 {
			fail(w, 400, "invalid_input")
			return
		}
		s.Title = title
	}
	if b.Description != nil {
		description := strings.TrimSpace(*b.Description)
		if len([]rune(description)) > maxDescriptionRunes {
			fail(w, 400, "invalid_description")
			return
		}
		s.Description = description
	}
	if b.Slug != nil && *b.Slug != s.Slug {
		slug := *b.Slug
		if slug != "" && !validSlug(slug) {
			fail(w, 400, "slug_invalid")
			return
		}
		available, e := a.slugAvailable(slug)
		if internal(w, e) {
			return
		}
		if !available {
			fail(w, 409, "slug_taken")
			return
		}
		s.Slug = slug
	}
	if b.Permanent != nil && *b.Permanent {
		s.Expires = 0
	} else if b.Days != nil {
		if *b.Days < 1 || *b.Days > 365 {
			fail(w, 400, "invalid_input")
			return
		}
		s.Expires = now.Add(time.Duration(*b.Days) * 24 * time.Hour).Unix()
	}
	if b.Limit != nil {
		if !validDownloadLimit(*b.Limit) {
			fail(w, 400, "invalid_input")
			return
		}
		s.DownloadLimit = *b.Limit
	}
	// Collaborators work through a library link, so it cannot also run out.
	if s.Mode != "" && s.DownloadLimit > 0 {
		p, e := a.linkPermissions(s.ID)
		if internal(w, e) {
			return
		}
		if p.Edit || p.Add || p.Delete {
			fail(w, 400, "invalid_input")
			return
		}
	}
	tx, e := a.db.Begin()
	if internal(w, e) {
		return
	}
	defer tx.Rollback()
	// A new expiry gets a new reminder.
	_, e = tx.Exec("UPDATE shares SET title=?,description=?,slug=?,expires=?,download_limit=?,reminded=CASE WHEN expires=? THEN reminded ELSE 0 END WHERE id=?", s.Title, s.Description, s.Slug, s.Expires, s.DownloadLimit, s.Expires, s.ID)
	if slugConflictError(e) {
		fail(w, 409, "slug_taken")
		return
	}
	if internal(w, e) {
		return
	}
	// A new or removed password signs out everyone who unlocked the old one.
	if b.Password != nil {
		if _, e = tx.Exec("UPDATE shares SET password=? WHERE id=?", password, s.ID); internal(w, e) {
			return
		}
		if _, e = tx.Exec("DELETE FROM grants WHERE share_id=?", s.ID); internal(w, e) {
			return
		}
	}
	if internal(w, tx.Commit()) {
		return
	}
	updated, _, _, e := a.loadShare(s.ID)
	if internal(w, e) {
		return
	}
	// A limit lowered below what was already downloaded closes the link.
	if usedUpShare(updated, nil) {
		if internal(w, exec(a, "UPDATE shares SET expires=? WHERE id=?", now.Unix(), s.ID)) {
			return
		}
		updated.Expires = now.Unix()
	}
	a.audit(r, u.Username, "share.edited", updated.Title)
	respond(w, 200, updated)
}

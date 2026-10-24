package server

import (
	"database/sql"
	"regexp"
	"strings"
)

// A custom link must never shadow an id, the API surface or a reserved word.
var slugShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`)

var reservedSlugs = map[string]bool{"api": true, "s": true, "assets": true, "admin": true, "login": true, "settings": true}

const shareIDLength = 48

func validSlug(slug string) bool {
	if !slugShape.MatchString(slug) || strings.Contains(slug, "--") || reservedSlugs[slug] {
		return false
	}
	// 48 hex characters is an id, not a name: a slug must never alias another
	// share's identifier.
	return !(len(slug) == shareIDLength && isLowerHex(slug))
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// SQLite reports a violation of the partial unique index naming either the index
// or the column, depending on version and statement shape, so match both instead of
// trusting one exact message.
func slugConflictError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") && strings.Contains(message, "slug")
}

// Caller holds mu. Empty slugs are not names: many shares may have none, but a
// real slug is reserved for exactly one share and never equals an existing id.
func (a *App) slugAvailable(slug string) (bool, error) {
	if slug == "" {
		return true, nil
	}
	var count int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM shares WHERE slug=?", slug).Scan(&count); e != nil {
		return false, e
	}
	if count > 0 {
		return false, nil
	}
	if e := a.db.QueryRow("SELECT COUNT(*) FROM shares WHERE id=?", slug).Scan(&count); e != nil {
		return false, e
	}
	return count == 0, nil
}

// Caller holds mu. Identifiers are resolved id-first so existing links keep
// working after a share gains a slug, and a foreign ref never exposes another
// share's error detail.
func (a *App) loadShareRef(ref string) (Share, string, string, error) {
	s, owner, password, e := a.loadShare(ref)
	if e == nil {
		return s, owner, password, nil
	}
	if e != sql.ErrNoRows || ref == "" {
		return s, owner, password, e
	}
	var id string
	if e2 := a.db.QueryRow("SELECT id FROM shares WHERE slug=?", ref).Scan(&id); e2 != nil {
		return s, owner, password, e
	}
	return a.loadShare(id)
}

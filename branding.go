package main

import (
	"bytes"
	"database/sql"
	"image"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Branding: an instance name, accent color, logo and default look chosen by
// the administrator. They replace the product name where visitors and users
// see it (header, page titles, public pages, emails). The logo must be a
// PNG; it is decoded before it is stored and served with a sandbox.
const (
	maxLogoBytes  = 256 << 10
	maxLogoPixels = 1024
)

var brandColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type Branding struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Logo  bool   `json:"logo"`
	// Look is the visual style for visitors and for users who have not
	// picked one themselves; empty means the built-in default (Aurora).
	Look string `json:"look"`
}

var brandLooks = map[string]bool{"": true, "classic": true, "refined": true, "aurora": true, "studio": true}

func migrateBranding(db *sql.DB) error {
	for _, c := range [][2]string{{"brand_name", "TEXT NOT NULL DEFAULT ''"}, {"brand_color", "TEXT NOT NULL DEFAULT ''"}, {"brand_logo", "BLOB"}, {"brand_look", "TEXT NOT NULL DEFAULT ''"}} {
		if e := addColumn(db, "settings", c[0], c[1]); e != nil {
			return e
		}
	}
	return nil
}

func (a *App) branding() Branding {
	var b Branding
	_ = a.db.QueryRow("SELECT brand_name,brand_color,brand_logo IS NOT NULL,brand_look FROM settings WHERE id=1").Scan(&b.Name, &b.Color, &b.Logo, &b.Look)
	return b
}

// brandName is what emails and pages call this installation.
func (a *App) brandName() string {
	if name := a.branding().Name; name != "" {
		return name
	}
	return "MagiSend"
}

func validLogo(data []byte) bool {
	if len(data) == 0 || len(data) > maxLogoBytes || !boundedPNG(data) {
		return false
	}
	config, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width < 1 || config.Height < 1 || config.Width > maxLogoPixels || config.Height > maxLogoPixels {
		return false
	}
	_, _, e = image.Decode(bytes.NewReader(data))
	return e == nil
}

func (a *App) brandingRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/branding/logo", func(w http.ResponseWriter, r *http.Request) {
		var logo []byte
		if a.db.QueryRow("SELECT brand_logo FROM settings WHERE id=1 AND brand_logo IS NOT NULL").Scan(&logo) != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Write(logo)
	})
	m.HandleFunc("PUT /api/admin/branding", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name  string `json:"name"`
			Color string `json:"color"`
			// Left out by older clients: the look then stays as it is.
			Look *string `json:"look"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		name := strings.TrimSpace(headerText(b.Name))
		if utf8.RuneCountInString(name) > 40 || b.Color != "" && !brandColor.MatchString(b.Color) || b.Look != nil && !brandLooks[*b.Look] {
			fail(w, 400, "invalid_input")
			return
		}
		if internal(w, exec(a, "UPDATE settings SET brand_name=?,brand_color=? WHERE id=1", name, strings.ToLower(b.Color))) {
			return
		}
		if b.Look != nil && internal(w, exec(a, "UPDATE settings SET brand_look=? WHERE id=1", *b.Look)) {
			return
		}
		a.audit(r, admin.Username, "settings.branding", name)
		respond(w, 200, a.branding())
	})
	m.HandleFunc("PUT /api/admin/branding/logo", func(w http.ResponseWriter, r *http.Request) {
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogoBytes+1))
		if e != nil || !validLogo(data) {
			fail(w, 400, "invalid_logo")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if internal(w, exec(a, "UPDATE settings SET brand_logo=? WHERE id=1", data)) {
			return
		}
		a.audit(r, admin.Username, "settings.branding", "logo")
		respond(w, 200, a.branding())
	})
	m.HandleFunc("DELETE /api/admin/branding/logo", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		if !internal(w, exec(a, "UPDATE settings SET brand_logo=NULL WHERE id=1")) {
			respond(w, 200, a.branding())
		}
	})
}

package server

import (
	"bytes"
	"database/sql"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Profiles: a display name and a profile picture per account. A picture is
// decoded, cropped to a centred square, scaled to at most avatarEdge pixels
// and stored re-encoded as JPEG, so nothing the browser sent (metadata, extra
// chunks, animation) is ever served back. Pictures are shown to signed-in
// users only.
const (
	maxAvatarBytes = 4 << 20
	minAvatarEdge  = 32
	avatarEdge     = 256
	maxDisplayName = 60
)

func migrateProfiles(db *sql.DB) error {
	for _, c := range [][2]string{
		{"display_name", "TEXT NOT NULL DEFAULT ''"},
		{"avatar", "BLOB"},
		{"avatar_version", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if e := addColumn(db, "users", c[0], c[1]); e != nil {
			return e
		}
	}
	return nil
}

// avatarImage turns an uploaded PNG, JPEG or GIF into the stored JPEG.
func avatarImage(data []byte) ([]byte, bool) {
	if len(data) == 0 || len(data) > maxAvatarBytes {
		return nil, false
	}
	if _, ok := rasterPreviewType(data); !ok {
		return nil, false
	}
	source, _, e := image.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, false
	}
	bounds := source.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	if side < minAvatarEdge {
		return nil, false
	}
	// Centred square on white, so transparent pictures do not turn black.
	square := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(square, square.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	origin := image.Pt(bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2)
	draw.Draw(square, square.Bounds(), source, origin, draw.Over)
	small, e := scaleDown(square, avatarEdge)
	if e != nil {
		return nil, false
	}
	var encoded bytes.Buffer
	if jpeg.Encode(&encoded, small, &jpeg.Options{Quality: 88}) != nil || encoded.Len() == 0 {
		return nil, false
	}
	return encoded.Bytes(), true
}

func (a *App) profileRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /api/account/profile", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			DisplayName string `json:"display_name"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		name := strings.TrimSpace(headerText(b.DisplayName))
		if utf8.RuneCountInString(name) > maxDisplayName {
			fail(w, 400, "invalid_input")
			return
		}
		if internal(w, exec(a, "UPDATE users SET display_name=? WHERE id=?", name, u.ID)) {
			return
		}
		u.DisplayName = name
		respond(w, 200, u)
	})
	m.HandleFunc("PUT /api/account/avatar", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, false); !ok {
			return
		}
		data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAvatarBytes+1))
		if e != nil {
			fail(w, 400, "invalid_avatar")
			return
		}
		picture, ok := avatarImage(data)
		if !ok {
			fail(w, 400, "invalid_avatar")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		// Signed in again under the lock: the session may have ended meanwhile.
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		if internal(w, exec(a, "UPDATE users SET avatar=?,avatar_version=avatar_version+1 WHERE id=?", picture, u.ID)) {
			return
		}
		u, e = a.currentUser(r)
		if internal(w, e) {
			return
		}
		respond(w, 200, u)
	})
	m.HandleFunc("DELETE /api/account/avatar", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		if internal(w, exec(a, "UPDATE users SET avatar=NULL,avatar_version=avatar_version+1 WHERE id=?", u.ID)) {
			return
		}
		u.Avatar = 0
		respond(w, 200, u)
	})
	m.HandleFunc("GET /api/avatars/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		_, ok := a.require(w, r, false)
		var picture []byte
		found := ok && a.db.QueryRow("SELECT avatar FROM users WHERE id=? AND avatar IS NOT NULL", r.PathValue("id")).Scan(&picture) == nil
		a.mu.Unlock()
		if !ok {
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		// The address carries the picture's version, so a new picture is a new URL.
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Write(picture)
	})
}

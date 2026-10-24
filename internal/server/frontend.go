package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func init() {
	// Not in every system MIME table; browsers expect it for the app manifest.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// frontend serves the built web interface from a.ui. Hashed assets are cached
// for a year; the app shell and service worker are always revalidated so an
// update reaches open browsers on their next load.
func (a *App) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" {
		if info, e := fs.Stat(a.ui, name); e == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, a.ui, name)
			return
		}
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	if page := a.cardPage(r, name); page != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == "GET" {
			_, _ = w.Write(page)
		}
		return
	}
	http.ServeFileFS(w, r, a.ui, "index.html")
}

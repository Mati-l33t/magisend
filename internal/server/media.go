package server

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Text files shown as plain text in the viewer: a known text extension, at
// most 20 MiB, valid UTF-8 and no NUL bytes. Served as text/plain, so nothing
// in them is ever run or rendered; HTML shows as its source.
const textPreviewMaxBytes = 20 << 20

var textPreviewExtensions = map[string]bool{}

func init() {
	for _, extension := range strings.Fields("txt md markdown json yaml yml toml csv log ini conf env xml html htm go py rs c h cc cpp hpp cs java kt swift rb php lua css js jsx mjs cjs ts tsx sql sh bash zsh ps1 diff patch") {
		textPreviewExtensions["."+extension] = true
	}
}

func textPreview(name string, size int64, file io.ReaderAt) ([]byte, bool) {
	if !textPreviewExtensions[strings.ToLower(filepath.Ext(name))] || size > textPreviewMaxBytes {
		return nil, false
	}
	data := make([]byte, size)
	if n, e := file.ReadAt(data, 0); int64(n) != size || e != nil && e != io.EOF {
		return nil, false
	}
	return data, utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

// Media previews: PDF documents, video and audio, served for the app's own
// viewers (pdf.js renders PDFs to canvases; <video> and <audio> play the
// rest). The type comes from the file's first bytes, never its name, and the
// response is sandboxed so a crafted file cannot run in MagiSend's origin.
// Ranges are supported so players can seek.
func mediaType(head []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf", true
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		switch string(head[8:12]) {
		case "M4A ", "M4B ":
			return "audio/mp4", true
		case "qt  ":
			return "video/quicktime", true
		}
		return "video/mp4", true
	case bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return "video/webm", true
	case bytes.HasPrefix(head, []byte("OggS")):
		return "audio/ogg", true
	case bytes.HasPrefix(head, []byte("fLaC")):
		return "audio/flac", true
	case len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && string(head[8:12]) == "WAVE":
		return "audio/wav", true
	case bytes.HasPrefix(head, []byte("ID3")):
		return "audio/mpeg", true
	case len(head) >= 2 && head[0] == 0xff && head[1]&0xe0 == 0xe0:
		// An MPEG frame: layer bits 00 mean ADTS AAC, anything else MP3.
		if head[1]&0x06 == 0 {
			return "audio/aac", true
		}
		return "audio/mpeg", true
	}
	return "", false
}

// Word and Excel files are ZIP archives; the app's own viewers read them in
// the browser. Only the two formats with a viewer are served, by name and
// ZIP signature together.
var officeTypes = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

func officeType(name string, head []byte) (string, bool) {
	kind, known := officeTypes[strings.ToLower(filepath.Ext(name))]
	return kind, known && bytes.HasPrefix(head, []byte("PK\x03\x04"))
}

func (a *App) mediaWith(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool)) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	release, ok := a.acquireDownload(w, r)
	if !ok {
		return
	}
	defer release()
	selected, file, ok := a.openReadableFile(w, r, authorize)
	if !ok {
		return
	}
	defer file.Close()
	head := make([]byte, 16)
	n, e := file.ReadAt(head, 0)
	if e != nil && e != io.EOF {
		internal(w, e)
		return
	}
	kind, ok := mediaType(head[:n])
	if !ok {
		kind, ok = officeType(selected.Name, head[:n])
	}
	if !ok {
		data, text := textPreview(selected.Name, selected.Size, file)
		if !text {
			fail(w, 415, "preview_unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": selected.Name}))
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": selected.Name}))
	http.ServeContent(w, r, "", time.Time{}, file)
}

func (a *App) mediaRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/public/{id}/files/{file}/media", func(w http.ResponseWriter, r *http.Request) {
		a.mediaWith(w, r, func() (Share, bool) {
			s, ok := a.publicShare(w, r)
			// Streaming would be an uncounted download on a limited link.
			if ok && s.DownloadLimit > 0 {
				fail(w, 403, "preview_unavailable")
				return s, false
			}
			return s, ok
		})
	})
	m.HandleFunc("GET /api/shares/{id}/files/{file}/media", func(w http.ResponseWriter, r *http.Request) {
		a.mediaWith(w, r, func() (Share, bool) { return a.ownerReadableShare(w, r) })
	})
	library := func(w http.ResponseWriter, r *http.Request) func() (Share, bool) {
		return func() (Share, bool) {
			u, ok := a.require(w, r, false)
			if !ok {
				return Share{}, false
			}
			f, e := a.libraryReadable(u.ID, r.PathValue("file"))
			if e != nil {
				fail(w, 404, "file_not_found")
				return Share{}, false
			}
			return Share{Files: []File{f.File}}, true
		}
	}
	m.HandleFunc("GET /api/library/files/{file}/media", func(w http.ResponseWriter, r *http.Request) {
		a.mediaWith(w, r, library(w, r))
	})
	m.HandleFunc("GET /api/library/files/{file}/preview", func(w http.ResponseWriter, r *http.Request) {
		a.previewWith(w, r, library(w, r))
	})
}

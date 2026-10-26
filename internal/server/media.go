package server

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Text files shown as plain text in the viewer: a known text extension and at
// most 20 MiB. Served as UTF-8 text/plain, so nothing in them is ever run or
// rendered; HTML shows as its source. Text saved in older encodings is shown
// too (see readableText); the stored file is never changed.
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
	return readableText(data)
}

// readableText is text as UTF-8 for the viewer. UTF-16 with its byte order
// mark ("Unicode" in Windows Notepad) is converted; other text that is not
// UTF-8 is read as Windows-1252, which older Windows programs saved (å, ä and
// ö as single bytes). Anything with NUL bytes is not text.
func readableText(data []byte) ([]byte, bool) {
	if len(data) >= 2 && len(data)%2 == 0 && (data[0] == 0xff && data[1] == 0xfe || data[0] == 0xfe && data[1] == 0xff) {
		units := make([]uint16, 0, len(data)/2-1)
		for i := 2; i < len(data); i += 2 {
			if data[0] == 0xff {
				units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
			} else {
				units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
			}
		}
		text := string(utf16.Decode(units))
		return []byte(text), !strings.ContainsRune(text, 0)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, false
	}
	if utf8.Valid(data) {
		return data, true
	}
	var b strings.Builder
	b.Grow(len(data) + len(data)/8)
	for _, c := range data {
		switch {
		case c < 0x80 || c >= 0xa0:
			b.WriteRune(rune(c))
		case cp1252[c-0x80] != 0:
			b.WriteRune(cp1252[c-0x80])
		default:
			b.WriteRune(utf8.RuneError)
		}
	}
	return []byte(b.String()), true
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
				failLibraryRead(w, e)
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

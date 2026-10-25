package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	// Registered so shared/thumbnail decoding knows GIF beyond the test binary.
	_ "image/gif"
)

const thumbMaxEdge = 256
const thumbQueueDepth = 100
const thumbMaxStoredBytes = 1 << 20
const thumbWaitStep = 100 * time.Millisecond
const thumbWaitBudget = 2 * time.Second

var errNoThumbnail = errors.New("no thumbnail")

func (a *App) thumbnailWorker() {
	for id := range a.thumbs {
		if e := a.generateThumbnail(id); e != nil {
			a.noteThumbnailFailure(id, e)
		}
	}
}

// A file that can never have a thumbnail is remembered, so a repeated request
// costs one map lookup instead of another multi-megabyte read and decode. The set
// is bounded by the files that exist. Transient failures are only logged and stay
// retryable.
func (a *App) noteThumbnailFailure(fileID string, err error) {
	if errors.Is(err, errNoThumbnail) || errors.Is(err, sql.ErrNoRows) {
		a.thumbBlocked.Store(fileID, struct{}{})
		return
	}
	log.Printf("thumbnail skipped: %v", err)
}

// Lazy fill for shares published before thumbnails existed, and for work the
// bounded queue dropped. The decode itself runs on the single worker, not in the
// request, so a visitor cannot occupy download capacity with decode work; this
// request only waits a bounded time for the row to appear.
func (a *App) requestThumbnail(fileID string) {
	if _, blocked := a.thumbBlocked.Load(fileID); blocked {
		return
	}
	if _, busy := a.thumbInflight.LoadOrStore(fileID, struct{}{}); busy {
		return
	}
	defer a.thumbInflight.Delete(fileID)
	select {
	case a.thumbs <- fileID:
	default:
		// Queue saturated: decode here, but only a couple at a time; beyond
		// that the request goes without (the page shows an icon and asks again
		// on a later visit), so a burst cannot run the server out of memory.
		select {
		case a.thumbInline <- struct{}{}:
			defer func() { <-a.thumbInline }()
		default:
			return
		}
		if e := a.generateThumbnail(fileID); e != nil {
			a.noteThumbnailFailure(fileID, e)
		}
		return
	}
	deadline := time.Now().Add(thumbWaitBudget)
	for time.Now().Before(deadline) {
		time.Sleep(thumbWaitStep)
		a.mu.Lock()
		data, _ := a.readThumbnail(fileID)
		a.mu.Unlock()
		if len(data) > 0 {
			return
		}
		if _, blocked := a.thumbBlocked.Load(fileID); blocked {
			return
		}
	}
}

// Caller holds mu. Called after publish: a saturated queue drops work instead of
// blocking the request, and nothing here retries in a loop.
func (a *App) queueThumbnails(shareID string) {
	rows, e := a.db.Query("SELECT id FROM files WHERE share_id=? ORDER BY rowid", shareID)
	if e != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		select {
		case a.thumbs <- id:
		default:
			return
		}
	}
}

// Caller holds mu.
func (a *App) hasThumbnail(fileID string) bool {
	var count int
	return a.db.QueryRow("SELECT COUNT(*) FROM thumbnails WHERE file_id=?", fileID).Scan(&count) == nil && count > 0
}

// Decoding stays outside mu: only the bounded read and the final insert touch
// shared state, and a concurrent delete removes the row again through the
// foreign key rather than leaving metadata for missing bytes.
func (a *App) generateThumbnail(fileID string) error {
	a.mu.Lock()
	var size int64
	var backend string
	e := a.db.QueryRow("SELECT size,backend FROM files WHERE id=?", fileID).Scan(&size, &backend)
	existing := e == nil && a.hasThumbnail(fileID)
	a.mu.Unlock()
	if e != nil {
		return e
	}
	if existing {
		return nil
	}
	if !a.knownLocation(backend) || size <= 0 || size > previewMaxBytes {
		return errNoThumbnail
	}
	file, e := a.openCurrent(fileID)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != size {
		return errNoThumbnail
	}
	data, e := io.ReadAll(io.LimitReader(file, previewMaxBytes+1))
	if e != nil || int64(len(data)) != size {
		return errNoThumbnail
	}
	// Reuse the preview preflight so animated or malformed rasters never reach
	// the encoder, then decode exactly the bytes that preflight accepted.
	release, _ := a.decodeSlot(context.Background())
	source, ok := decodeRaster(data)
	release()
	if !ok {
		return errNoThumbnail
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.storeThumbnail(fileID, source)
}

// Caller holds mu. The picture is scaled down and encoded again here, so what
// is stored is always a fresh small PNG or JPEG.
func (a *App) storeThumbnail(fileID string, source image.Image) error {
	small, e := scaleDown(source, thumbMaxEdge)
	if e != nil {
		return e
	}
	var encoded bytes.Buffer
	format := "png"
	if opaque(small) {
		format = "jpeg"
		e = jpeg.Encode(&encoded, small, &jpeg.Options{Quality: 85})
	} else {
		e = png.Encode(&encoded, small)
	}
	if e != nil || encoded.Len() == 0 || encoded.Len() > thumbMaxStoredBytes {
		return errNoThumbnail
	}
	_, e = a.db.Exec("INSERT OR REPLACE INTO thumbnails(file_id,bytes,format,width,height,created) VALUES(?,?,?,?,?,?)", fileID, encoded.Bytes(), format, small.Bounds().Dx(), small.Bounds().Dy(), time.Now().Unix())
	return e
}

// Files the server cannot draw itself (a PDF's first page, a video frame,
// WebP, AVIF, SVG, HEIC) get their picture from the owner's browser, which
// sends it as a PNG or JPEG. Only the owner may, only for these types, and
// only once: the picture is decoded with the same checks as any preview and
// stored through storeThumbnail.
const thumbUploadMaxBytes = 2 << 20
const thumbUploadMaxEdge = 1024

var browserThumbExtensions = map[string]bool{}

func init() {
	for _, extension := range strings.Fields("pdf mp4 m4v mov webm webp avif svg heic heif bmp") {
		browserThumbExtensions["."+extension] = true
	}
}

func (a *App) putThumbnail(w http.ResponseWriter, r *http.Request) {
	data, e := io.ReadAll(io.LimitReader(r.Body, thumbUploadMaxBytes+1))
	if e != nil || len(data) > thumbUploadMaxBytes {
		fail(w, 413, "thumbnail_unavailable")
		return
	}
	a.mu.Lock()
	share, ok := a.ownerReadableShare(w, r)
	var file *File
	for i := range share.Files {
		if ok && share.Files[i].ID == r.PathValue("file") {
			file = &share.Files[i]
		}
	}
	existing := file != nil && a.hasThumbnail(file.ID)
	a.mu.Unlock()
	switch {
	case !ok:
		return
	case file == nil:
		fail(w, 404, "file_not_found")
		return
	case existing:
		w.WriteHeader(204)
		return
	case file.Offset != file.Size || !browserThumbExtensions[strings.ToLower(filepath.Ext(file.Name))]:
		fail(w, 400, "thumbnail_unavailable")
		return
	}
	config, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || format != "png" && format != "jpeg" || config.Width > thumbUploadMaxEdge || config.Height > thumbUploadMaxEdge {
		fail(w, 400, "thumbnail_unavailable")
		return
	}
	var source image.Image
	if _, ok = rasterPreviewType(data); ok {
		source, _, e = image.Decode(bytes.NewReader(data))
	}
	if !ok || e != nil {
		fail(w, 400, "thumbnail_unavailable")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// The file may have been deleted meanwhile; the foreign key refuses the row.
	if e = a.storeThumbnail(file.ID, source); e != nil {
		fail(w, 404, "file_not_found")
		return
	}
	a.thumbBlocked.Delete(file.ID)
	w.WriteHeader(204)
}

// Nearest-neighbour is deliberate: thumbnails are decorative 256 px previews, so
// the bounded decode allocation stays in charge instead of a resampling stack.
func scaleDown(source image.Image, maxEdge int) (*image.NRGBA, error) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, errNoThumbnail
	}
	targetWidth, targetHeight := width, height
	if width > maxEdge || height > maxEdge {
		// The longest edge is pinned to the cap so a bounding box never drifts a
		// pixel below it through float rounding.
		if width >= height {
			targetWidth = maxEdge
			targetHeight = max(1, int(math.Round(float64(height)*float64(maxEdge)/float64(width))))
		} else {
			targetHeight = maxEdge
			targetWidth = max(1, int(math.Round(float64(width)*float64(maxEdge)/float64(height))))
		}
	}
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sourceY := bounds.Min.Y + y*height/targetHeight
		for x := 0; x < targetWidth; x++ {
			target.Set(x, y, source.At(bounds.Min.X+x*width/targetWidth, sourceY))
		}
	}
	return target, nil
}

func opaque(img image.Image) bool {
	switch img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		return true
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := img.At(x, y).RGBA(); alpha != 0xffff {
				return false
			}
		}
	}
	return true
}

// Thumbnails follow the preview authorization path exactly: owner sessions for
// /api/shares, and published state, expiry, tombstone plus password grants for
// /api/public.
func (a *App) thumbnailWith(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool)) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// Authorization and any lazy generation happen before a download slot is
	// taken: a decorative image must never hold download capacity, and an
	// unauthorized request must not consume it either.
	a.mu.Lock()
	share, ok := authorize()
	fileID := ""
	if ok {
		for _, f := range share.Files {
			if f.ID == r.PathValue("file") {
				fileID = f.ID
				break
			}
		}
	}
	var data []byte
	var format string
	if fileID != "" {
		data, format = a.readThumbnail(fileID)
	}
	a.mu.Unlock()
	if !ok {
		return
	}
	if len(data) == 0 && fileID != "" {
		a.requestThumbnail(fileID)
		a.mu.Lock()
		// Lazy generation happened outside mu; a live link may have been revoked
		// or the share deleted while this request waited. Re-run authorization
		// and exact membership against the freshly loaded share before serving.
		var reauth bool
		if fresh, freshOK := authorize(); freshOK {
			for _, f := range fresh.Files {
				if f.ID == fileID {
					reauth = true
					break
				}
			}
		}
		if !reauth {
			a.mu.Unlock()
			fail(w, 404, "thumbnail_unavailable")
			return
		}
		data, format = a.readThumbnail(fileID)
		a.mu.Unlock()
	}
	if len(data) == 0 {
		// Never answer a thumbnail request with the full preview: the client
		// asked for a small decorative image and must fall back on its own.
		fail(w, 404, "thumbnail_unavailable")
		return
	}
	// A thumbnail is a few kilobytes already in memory: it takes a shared slot
	// but not one of the visitor's own, so a page with many pictures shows
	// them all.
	if !acquire(w, a.downloads) {
		return
	}
	release := func() { <-a.downloads }
	defer release()
	contentType := "image/jpeg"
	if format == "png" {
		contentType = "image/png"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "thumbnail." + format}))
	http.ServeContent(w, r, "thumbnail", time.Time{}, bytes.NewReader(data))
}

// Caller holds mu.
func (a *App) readThumbnail(fileID string) ([]byte, string) {
	var data []byte
	var format string
	if e := a.db.QueryRow("SELECT bytes,format FROM thumbnails WHERE file_id=?", fileID).Scan(&data, &format); e != nil {
		return nil, ""
	}
	return data, format
}

func (a *App) thumbnailRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /api/shares/{id}/files/{file}/thumbnail", a.putThumbnail)
	m.HandleFunc("GET /api/shares/{id}/files/{file}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		a.thumbnailWith(w, r, func() (Share, bool) { return a.ownerReadableShare(w, r) })
	})
	m.HandleFunc("GET /api/public/{id}/files/{file}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		a.thumbnailWith(w, r, func() (Share, bool) { return a.publicShare(w, r) })
	})
}

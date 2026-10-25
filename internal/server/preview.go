package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/webp"
)

const previewMaxBytes int64 = 20 << 20
const previewMaxPixels = 16_000_000

// Larger JPEGs, up to these limits, are previewed by their header only.
const (
	largeJPEGMaxBytes  int64 = 64 << 20
	largeJPEGMaxPixels       = 200_000_000
)

// largeJPEG returns the file for a JPEG too big to decode here but within the
// large limits, after checking its header: a JPEG start and a frame of a
// sensible size. Anything else goes the usual, fully decoded way.
func largeJPEG(file io.ReaderAt, size int64) (io.ReadSeeker, bool) {
	if size > largeJPEGMaxBytes {
		return nil, false
	}
	whole := io.NewSectionReader(file, 0, size)
	head := make([]byte, 3)
	if _, e := whole.ReadAt(head, 0); e != nil || !bytes.Equal(head, []byte{0xFF, 0xD8, 0xFF}) {
		return nil, false
	}
	config, format, e := image.DecodeConfig(io.NewSectionReader(file, 0, size))
	if e != nil || format != "jpeg" || config.Width <= 0 || config.Height <= 0 || config.Width > largeJPEGMaxPixels/config.Height {
		return nil, false
	}
	if size <= previewMaxBytes && config.Width <= previewMaxPixels/config.Height {
		return nil, false
	}
	return whole, true
}

const previewMaxGIFFrames = 32

// Zero is reserved for permanent shares, not sessions, grants or capabilities.
func shareExpired(expires, now int64) bool { return expires != 0 && expires <= now }

// A permanent share never extends the finite deadline of a capability.
func capShareExpiry(shareExpires, deadline int64) int64 {
	if shareExpires == 0 {
		return deadline
	}
	return min(shareExpires, deadline)
}

// Caller holds mu. Public grants are not owner credentials.
func (a *App) ownerReadableShare(w http.ResponseWriter, r *http.Request) (Share, bool) {
	s, ok := a.ownShare(w, r)
	if !ok {
		return s, false
	}
	if !s.Published {
		fail(w, 404, "share_not_found")
		return s, false
	}
	if shareExpired(s.Expires, time.Now().Unix()) {
		fail(w, 410, "share_expired")
		return s, false
	}
	return s, true
}

func (a *App) previewWith(w http.ResponseWriter, r *http.Request, authorize func() (Share, bool)) {
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
	// Authorization, offset, backend and actual storage are checked before
	// examining the content. Both bytes and decoding work stay off mu.
	// Phone photos can be far larger than what is decoded here (a 200 MP
	// camera), and AVIF and HEIC are not decoded here at all: those are
	// checked by their structure only and sent as they are, for the browser
	// to show, never decoded on the server.
	inline := mime.FormatMediaType("inline", map[string]string{"filename": selected.Name})
	if contentType, ok := headerPreview(file, selected.Size); ok {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", inline)
		http.ServeContent(w, r, selected.Name, time.Time{}, io.NewSectionReader(file, 0, selected.Size))
		return
	}
	limit := previewMaxBytes
	head := make([]byte, 4)
	if _, e := file.ReadAt(head, 0); e == nil && isTIFF(head) {
		limit = tiffPreviewMaxBytes
	}
	if selected.Size > limit {
		fail(w, 415, "preview_unsupported")
		return
	}
	data, e := io.ReadAll(io.LimitReader(file, limit+1))
	if e != nil || int64(len(data)) != selected.Size || int64(len(data)) > limit {
		fail(w, 415, "preview_unsupported")
		return
	}
	// TIFF is shown as PNG, the same pixels in a form every browser draws.
	if isTIFF(data) {
		converted, ok := a.convertPreview(r, func() ([]byte, bool) { return tiffPreview(data) })
		if !ok {
			fail(w, 415, "preview_unsupported")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", inline)
		http.ServeContent(w, r, selected.Name, time.Time{}, bytes.NewReader(converted))
		return
	}
	contentType, ok := a.decodePreview(r, data)
	if !ok && isICO(data) {
		// An icon's pictures are decoded too, so it takes a turn as well.
		if release, turn := a.decodeSlot(r.Context()); turn {
			contentType, ok = documentPreview(selected.Name, data)
			release()
		}
	} else if !ok {
		contentType, ok = documentPreview(selected.Name, data)
	}
	if !ok {
		fail(w, 415, "preview_unsupported")
		return
	}
	// Serve the validated original, not a lossy re-encoding or filename guess.
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", inline)
	http.ServeContent(w, r, selected.Name, time.Time{}, bytes.NewReader(data))
}

// decodePreview checks a picture by decoding it fully, which takes up to
// about 100 MB; only a few run at once, and a visitor waits for a turn
// while the request lasts. Anything that is not a picture does not wait.
func (a *App) decodePreview(r *http.Request, data []byte) (string, bool) {
	if !rasterSignature(data) {
		return "", false
	}
	release, ok := a.decodeSlot(r.Context())
	if !ok {
		return "", false
	}
	defer release()
	return rasterPreviewType(data)
}

// decodeSlot waits for one of the few picture decodings that may run at
// once: previews, thumbnails and profile pictures share them.
func (a *App) decodeSlot(ctx context.Context) (func(), bool) {
	select {
	case a.previewDecode <- struct{}{}:
		return func() { <-a.previewDecode }, true
	case <-ctx.Done():
		return nil, false
	}
}

// rasterSignature reports whether data starts like a picture
// rasterPreviewType reads.
func rasterSignature(data []byte) bool {
	for _, magic := range []string{"\x89PNG\r\n\x1a\n", "\xff\xd8", "GIF87a", "GIF89a", "BM"} {
		if bytes.HasPrefix(data, []byte(magic)) {
			return true
		}
	}
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

// isICO reports whether data starts like a Windows icon or cursor.
func isICO(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x00\x00\x01\x00")) || bytes.HasPrefix(data, []byte("\x00\x00\x02\x00"))
}

// convertPreview runs one picture conversion at a time, which can need a few
// hundred megabytes; a visitor waits for a turn while the request lasts.
func (a *App) convertPreview(r *http.Request, convert func() ([]byte, bool)) ([]byte, bool) {
	select {
	case a.previewConvert <- struct{}{}:
	case <-r.Context().Done():
		return nil, false
	}
	defer func() { <-a.previewConvert }()
	return convert()
}

func rasterPreviewType(data []byte) (string, bool) {
	_, kind, ok := rasterPreviewImage(data)
	return kind, ok
}

// decodeRaster is the picture rasterPreviewType checked, for thumbnails and
// profile pictures, so it is decoded only once.
func decodeRaster(data []byte) (image.Image, bool) {
	picture, _, ok := rasterPreviewImage(data)
	return picture, ok
}

func rasterPreviewImage(data []byte) (image.Image, string, bool) {
	// WebP is checked by its own decoder first, so its frame limits apply
	// before anything else reads it. Animated WebP is not supported and
	// refused.
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		config, e := webp.DecodeConfig(bytes.NewReader(data))
		if e != nil || config.Width <= 0 || config.Height <= 0 || config.Width > previewMaxPixels/config.Height {
			return nil, "", false
		}
		picture, e := webp.Decode(bytes.NewReader(data))
		return picture, "image/webp", e == nil
	}
	// BMP likewise, by its own decoder.
	if bytes.HasPrefix(data, []byte("BM")) {
		config, e := bmp.DecodeConfig(bytes.NewReader(data))
		if e != nil || config.Width <= 0 || config.Height <= 0 || config.Width > previewMaxPixels/config.Height {
			return nil, "", false
		}
		picture, e := bmp.Decode(bytes.NewReader(data))
		return picture, "image/bmp", e == nil
	}
	config, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width <= 0 || config.Height <= 0 || config.Width > previewMaxPixels/config.Height {
		return nil, "", false
	}
	// DecodeConfig alone cannot detect corrupt pixel data, truncation or CRC
	// failures. Fully decode every accepted raster before releasing any bytes.
	var picture image.Image
	switch format {
	case "png":
		if !boundedPNG(data) {
			return nil, "", false
		}
		picture, e = png.Decode(bytes.NewReader(data))
	case "jpeg":
		picture, e = jpeg.Decode(bytes.NewReader(data))
	case "gif":
		if !boundedGIF(data, config.Width, config.Height) {
			return nil, "", false
		}
		var all *gif.GIF
		if all, e = gif.DecodeAll(bytes.NewReader(data)); e == nil {
			if len(all.Image) == 0 {
				return nil, "", false
			}
			picture = all.Image[0]
		}
	default:
		return nil, "", false
	}
	return picture, "image/" + format, e == nil
}

// The static PNG decoder does not validate APNG frames. Walk chunk boundaries
// before decoding and reject animation rather than releasing unvalidated frames
// to browsers. The byte cap and minimum chunk size bound this walk's work.
func boundedPNG(data []byte) bool {
	if int64(len(data)) > previewMaxBytes || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	for i := 8; i < len(data); {
		if len(data)-i < 12 {
			return false
		}
		n := binary.BigEndian.Uint32(data[i : i+4])
		if uint64(n) > uint64(len(data)-i-12) {
			return false
		}
		kind := string(data[i+4 : i+8])
		switch kind {
		case "acTL", "fcTL", "fdAT":
			return false
		case "IEND":
			return n == 0 && len(data)-i == 12
		}
		i += 12 + int(n)
	}
	return false
}

// Inspect GIF structure without decompressing/retaining frames first. DecodeAll
// alone has no frame limit: a small compressed input can allocate many rasters.
// Bound both frame count and aggregate frame pixels, then let the standard
// decoder validate all compressed data and semantic details.
func boundedGIF(data []byte, width, height int) bool {
	if len(data) < 13 {
		return false
	}
	i := 13
	if data[10]&0x80 != 0 {
		i += 3 * (1 << (uint(data[10]&7) + 1))
	}
	frames, pixels := 0, 0
	skipBlocks := func() bool {
		for i < len(data) {
			n := int(data[i])
			i++
			if n == 0 {
				return true
			}
			if n > len(data)-i {
				return false
			}
			i += n
		}
		return false
	}
	for i < len(data) {
		block := data[i]
		i++
		switch block {
		case 0x3b: // Trailer: no incomplete or unvalidated extra frames.
			return frames > 0 && i == len(data)
		case 0x21: // Extension label followed by bounded data sub-blocks.
			if i >= len(data) {
				return false
			}
			i++
			if !skipBlocks() {
				return false
			}
		case 0x2c: // Image descriptor, optional local table, LZW data.
			if len(data)-i < 9 {
				return false
			}
			x := int(binary.LittleEndian.Uint16(data[i:]))
			y := int(binary.LittleEndian.Uint16(data[i+2:]))
			w := int(binary.LittleEndian.Uint16(data[i+4:]))
			h := int(binary.LittleEndian.Uint16(data[i+6:]))
			packed := data[i+8]
			i += 9
			frames++
			if frames > previewMaxGIFFrames || w <= 0 || h <= 0 || x+w > width || y+h > height || w > (previewMaxPixels-pixels)/h {
				return false
			}
			pixels += w * h
			if packed&0x80 != 0 {
				i += 3 * (1 << (uint(packed&7) + 1))
			}
			if i >= len(data) {
				return false
			}
			i++ // LZW minimum code size, validated by DecodeAll.
			if !skipBlocks() {
				return false
			}
		default:
			return false
		}
	}
	return false
}

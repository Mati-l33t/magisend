package server

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"time"
)

const previewMaxBytes int64 = 20 << 20
const previewMaxPixels = 16_000_000
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
	if selected.Size > previewMaxBytes {
		fail(w, 415, "preview_unsupported")
		return
	}
	data, e := io.ReadAll(io.LimitReader(file, previewMaxBytes+1))
	if e != nil || int64(len(data)) != selected.Size || int64(len(data)) > previewMaxBytes {
		fail(w, 415, "preview_unsupported")
		return
	}
	contentType, ok := rasterPreviewType(data)
	if !ok {
		fail(w, 415, "preview_unsupported")
		return
	}
	// Serve the validated original, not a lossy re-encoding or filename guess.
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": selected.Name}))
	http.ServeContent(w, r, selected.Name, time.Time{}, bytes.NewReader(data))
}

func rasterPreviewType(data []byte) (string, bool) {
	config, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width <= 0 || config.Height <= 0 || config.Width > previewMaxPixels/config.Height {
		return "", false
	}
	// DecodeConfig alone cannot detect corrupt pixel data, truncation or CRC
	// failures. Fully decode every accepted raster before releasing any bytes.
	switch format {
	case "png":
		if !boundedPNG(data) {
			return "", false
		}
		_, e = png.Decode(bytes.NewReader(data))
	case "jpeg":
		_, e = jpeg.Decode(bytes.NewReader(data))
	case "gif":
		if !boundedGIF(data, config.Width, config.Height) {
			return "", false
		}
		_, e = gif.DecodeAll(bytes.NewReader(data))
	default:
		return "", false
	}
	return "image/" + format, e == nil
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

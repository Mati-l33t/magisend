package server

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Photos sent from the Send page can lose where they were taken. The GPS
// block of their Exif data is emptied in place, together with the camera's
// and lens's serial numbers and the owner name, and GPS fields in XMP are
// blanked. The picture, its rotation and every other detail stay, and the
// file keeps its exact size, so nothing else about the upload changes.
// JPEG, HEIC/HEIF, WebP, TIFF and PNG are handled; other files are left alone.

var scrubExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".jfif": true, ".heic": true, ".heif": true, ".webp": true, ".tif": true, ".tiff": true, ".png": true, ".avif": true, ".dng": true}

// Metadata sits near the start of these files; the rest is the picture.
const scrubWindow = 8 << 20

var (
	exifMarker = []byte("Exif\x00\x00")
	xmpGPS     = regexp.MustCompile(`(?s)(exif:GPS[A-Za-z]*=")([^"]*)(")|(<exif:GPS[A-Za-z]*>)(.*?)(</exif:GPS[A-Za-z]*>)`)
	// Exif IFD tags that identify a camera or its owner.
	privateTags = map[uint16]bool{0xA430: true, 0xA431: true, 0xA435: true}
)

func migrateScrub(db *sql.DB) error {
	return addColumn(db, "shares", "strip_location", "INTEGER NOT NULL DEFAULT 0")
}

// Caller holds mu. Runs when a link that asked for it is published, before
// anyone can download its files.
func (a *App) scrubShare(s Share) error {
	var strip bool
	if e := a.db.QueryRow("SELECT strip_location FROM shares WHERE id=?", s.ID).Scan(&strip); e != nil || !strip {
		return e
	}
	for _, f := range s.Files {
		if !shouldScrub(f.Name) {
			continue
		}
		path, e := a.currentBlob(f.ID)
		if e != nil {
			return e
		}
		if _, e = scrubLocation(path); e != nil {
			return e
		}
	}
	return nil
}

func shouldScrub(name string) bool {
	return scrubExtensions[strings.ToLower(filepath.Ext(name))]
}

// scrubLocation cleans the file at path in place. It reports whether
// anything was changed.
func scrubLocation(path string) (bool, error) {
	file, e := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return false, e
	}
	defer file.Close()
	head := make([]byte, scrubWindow)
	n, e := io.ReadFull(file, head)
	if e != nil && e != io.ErrUnexpectedEOF && e != io.EOF {
		return false, e
	}
	head = head[:n]
	if bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")) {
		return scrubPNG(file)
	}
	changed := false
	if bytes.HasPrefix(head, []byte("II*\x00")) || bytes.HasPrefix(head, []byte("MM\x00*")) {
		changed = scrubTIFF(head) || changed
	}
	// JPEG APP1, HEIF Exif items and WebP EXIF chunks all carry the marker
	// before the TIFF header (WebP sometimes does not; then the chunk is used).
	for at := 0; ; {
		i := bytes.Index(head[at:], exifMarker)
		if i < 0 {
			break
		}
		start := at + i + len(exifMarker)
		changed = scrubTIFF(head[start:]) || changed
		at = start
	}
	if bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 && string(head[8:12]) == "WEBP" {
		for at := 12; at+8 <= len(head); {
			size := int(binary.LittleEndian.Uint32(head[at+4 : at+8]))
			if size < 0 || at+8+size > len(head) {
				break
			}
			if string(head[at:at+4]) == "EXIF" {
				changed = scrubTIFF(head[at+8:at+8+size]) || changed
			}
			at += 8 + size + size%2
		}
	}
	changed = scrubXMP(head) || changed
	if !changed {
		return false, nil
	}
	if _, e = file.WriteAt(head, 0); e != nil {
		return false, e
	}
	return true, file.Sync()
}

// scrubXMP blanks GPS values in XMP text with spaces of the same length.
func scrubXMP(b []byte) bool {
	changed := false
	for _, m := range xmpGPS.FindAllSubmatchIndex(b, -1) {
		for _, group := range []int{2, 5} {
			from, to := m[group*2], m[group*2+1]
			for i := from; i >= 0 && i < to; i++ {
				if b[i] != ' ' {
					b[i] = ' '
					changed = true
				}
			}
		}
	}
	return changed
}

var typeSizes = map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4}

// scrubTIFF works on a buffer that starts with a TIFF header. Every offset
// is checked against the buffer, so damaged data is skipped, never followed
// out of bounds.
func scrubTIFF(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	var order binary.ByteOrder
	switch string(b[:4]) {
	case "II*\x00":
		order = binary.LittleEndian
	case "MM\x00*":
		order = binary.BigEndian
	default:
		return false
	}
	type entry struct {
		at    int
		tag   uint16
		kind  uint16
		count uint32
		value uint32
	}
	read := func(offset uint32) []entry {
		at := int(offset)
		if at < 8 || at+2 > len(b) {
			return nil
		}
		n := int(order.Uint16(b[at:]))
		if n > 512 || at+2+n*12 > len(b) {
			return nil
		}
		list := make([]entry, n)
		for i := range list {
			p := at + 2 + i*12
			list[i] = entry{p, order.Uint16(b[p:]), order.Uint16(b[p+2:]), order.Uint32(b[p+4:]), order.Uint32(b[p+8:])}
		}
		return list
	}
	// blank zeroes an entry's value, wherever it is stored, and reports
	// whether there was anything to zero.
	blank := func(e entry) bool {
		size := typeSizes[e.kind] * int(e.count)
		value := b[e.at+8 : e.at+12]
		if size > 4 {
			from := int(e.value)
			if from < 8 || size > len(b) || from+size > len(b) {
				return false
			}
			value = b[from : from+size]
		}
		if !bytes.ContainsFunc(value, func(r rune) bool { return r != 0 }) {
			return false
		}
		clear(value)
		return true
	}
	changed := false
	for _, e := range read(order.Uint32(b[4:])) {
		switch e.tag {
		case 0x8825: // GPS: every field emptied, then the block marked empty.
			gps := read(e.value)
			for _, g := range gps {
				blank(g)
				copy(b[g.at:g.at+12], make([]byte, 12))
			}
			if len(gps) > 0 {
				order.PutUint16(b[int(e.value):], 0)
				changed = true
			}
		case 0x8769: // Exif: serial numbers and owner name.
			for _, x := range read(e.value) {
				if privateTags[x.tag] && blank(x) {
					changed = true
				}
			}
		}
	}
	return changed
}

// scrubPNG cleans the eXIf chunk and XMP text chunks and writes their
// checksums again.
func scrubPNG(file *os.File) (bool, error) {
	changed := false
	header := make([]byte, 8)
	for at := int64(8); ; {
		if _, e := file.ReadAt(header, at); e != nil {
			return changed, nil
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		kind := string(header[4:8])
		if kind == "IEND" || size > 64<<20 {
			break
		}
		if kind == "eXIf" || kind == "iTXt" || kind == "tEXt" {
			data := make([]byte, size)
			if _, e := file.ReadAt(data, at+8); e != nil {
				return changed, e
			}
			done := false
			if kind == "eXIf" {
				done = scrubTIFF(data)
			} else {
				done = scrubXMP(data)
			}
			if done {
				crc := crc32.NewIEEE()
				crc.Write(header[4:8])
				crc.Write(data)
				sum := make([]byte, 4)
				binary.BigEndian.PutUint32(sum, crc.Sum32())
				if _, e := file.WriteAt(data, at+8); e != nil {
					return changed, e
				}
				if _, e := file.WriteAt(sum, at+8+size); e != nil {
					return changed, e
				}
				changed = true
			}
		}
		at += 12 + size
	}
	if changed {
		return true, file.Sync()
	}
	return false, nil
}

package server

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
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

// maxTIFFValue is the largest single TIFF value (an XMP packet, say) read
// to clean it.
const maxTIFFValue = 16 << 20

var errLocationNotRemoved = errors.New("location data could not be removed")

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
	// BigTIFF uses 8-byte offsets this cleaner does not read: refused rather
	// than published with its location.
	if bytes.HasPrefix(head, []byte("II+\x00")) || bytes.HasPrefix(head, []byte("MM\x00+")) {
		return false, errLocationNotRemoved
	}
	// Many TIFF writers put the directories after the image data, so a TIFF
	// is cleaned where its directories point, in the file itself: only those
	// few bytes are read and written, however large the file is.
	if bytes.HasPrefix(head, []byte("II*\x00")) || bytes.HasPrefix(head, []byte("MM\x00*")) {
		info, e := file.Stat()
		if e != nil {
			return false, e
		}
		changed, e := scrubTIFFData(fileTIFF{file, info.Size()})
		if e != nil || !changed {
			return false, e
		}
		return true, file.Sync()
	}
	changed := false
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

// tiffData holds a TIFF structure, in memory or in a file; offsets count
// from its header.
type tiffData interface {
	io.ReaderAt
	io.WriterAt
	Size() int64
}

type memTIFF []byte

func (m memTIFF) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(m)) {
		return 0, io.EOF
	}
	return copy(p, m[off:]), nil
}

func (m memTIFF) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(m)) {
		return 0, io.ErrShortWrite
	}
	return copy(m[off:], p), nil
}

func (m memTIFF) Size() int64 { return int64(len(m)) }

type fileTIFF struct {
	*os.File
	size int64
}

func (f fileTIFF) Size() int64 { return f.size }

// scrubTIFF cleans a buffer that starts with a TIFF header.
func scrubTIFF(b []byte) bool {
	changed, _ := scrubTIFFData(memTIFF(b))
	return changed
}

// scrubTIFFData empties the GPS block and blanks serial numbers, owner names
// and XMP location fields, in every directory of the chain. Every offset is
// checked against the data, so damaged data is skipped, never followed out
// of bounds.
func scrubTIFFData(b tiffData) (bool, error) {
	size := b.Size()
	get := func(at int64, n int64) []byte {
		if at < 0 || n < 0 || n > maxTIFFValue || at+n > size {
			return nil
		}
		buf := make([]byte, n)
		if _, e := b.ReadAt(buf, at); e != nil {
			return nil
		}
		return buf
	}
	head := get(0, 8)
	if head == nil {
		return false, nil
	}
	var order binary.ByteOrder
	switch string(head[:4]) {
	case "II*\x00":
		order = binary.LittleEndian
	case "MM\x00*":
		order = binary.BigEndian
	default:
		return false, nil
	}
	type entry struct {
		at    int64
		tag   uint16
		kind  uint16
		count uint32
		value uint32
	}
	read := func(offset uint32) (list []entry, next uint32) {
		at := int64(offset)
		if at < 8 {
			return nil, 0
		}
		count := get(at, 2)
		if count == nil {
			return nil, 0
		}
		n := int64(order.Uint16(count))
		if n > 512 {
			return nil, 0
		}
		raw := get(at+2, n*12+4)
		if raw == nil {
			if raw = get(at+2, n*12); raw == nil {
				return nil, 0
			}
		}
		list = make([]entry, n)
		for i := range list {
			p := i * 12
			list[i] = entry{at + 2 + int64(p), order.Uint16(raw[p:]), order.Uint16(raw[p+2:]), order.Uint32(raw[p+4:]), order.Uint32(raw[p+8:])}
		}
		if int64(len(raw)) == n*12+4 {
			next = order.Uint32(raw[n*12:])
		}
		return list, next
	}
	var failure error
	write := func(p []byte, at int64) {
		if failure == nil {
			_, failure = b.WriteAt(p, at)
		}
	}
	// valueAt is where an entry's value is stored and how long it is.
	valueAt := func(e entry) (int64, int64, bool) {
		n := int64(typeSizes[e.kind]) * int64(e.count)
		if n <= 4 {
			return e.at + 8, 4, true
		}
		from := int64(e.value)
		if from < 8 || from+n > size {
			return 0, 0, false
		}
		return from, n, true
	}
	// blank zeroes an entry's value, wherever it is stored, and reports
	// whether there was anything to zero.
	blank := func(e entry) bool {
		at, n, ok := valueAt(e)
		value := get(at, n)
		if !ok || value == nil || !bytes.ContainsFunc(value, func(r rune) bool { return r != 0 }) {
			return false
		}
		write(make([]byte, n), at)
		return true
	}
	changed := false
	seen := map[uint32]bool{}
	for offset, directories := order.Uint32(head[4:]), 0; offset != 0 && directories < 16 && !seen[offset]; directories++ {
		seen[offset] = true
		list, next := read(offset)
		for _, e := range list {
			switch e.tag {
			case 0x8825: // GPS: every field emptied, then the block marked empty.
				gps, _ := read(e.value)
				for _, g := range gps {
					blank(g)
					write(make([]byte, 12), g.at)
				}
				if len(gps) > 0 {
					write(make([]byte, 2), int64(e.value))
					changed = true
				}
			case 0x8769: // Exif: serial numbers and owner name.
				exif, _ := read(e.value)
				for _, x := range exif {
					if privateTags[x.tag] && blank(x) {
						changed = true
					}
				}
			case 0x02bc: // XMP packet
				if at, n, ok := valueAt(e); ok {
					if packet := get(at, n); packet != nil && scrubXMP(packet) {
						write(packet, at)
						changed = true
					}
				}
			}
		}
		offset = next
	}
	return changed, failure
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
		if kind == "IEND" {
			break
		}
		metadata := kind == "eXIf" || kind == "iTXt" || kind == "tEXt" || kind == "zTXt"
		// A metadata chunk too large to read is refused rather than passed on
		// unread; large image data is skipped.
		if metadata && size > 64<<20 {
			return changed, errLocationNotRemoved
		}
		if metadata {
			data := make([]byte, size)
			if _, e := file.ReadAt(data, at+8); e != nil {
				return changed, e
			}
			done := false
			switch {
			case kind == "eXIf":
				done = scrubTIFF(data)
			case opaqueMetadata(kind, data):
				// Compressed XMP and Exif profiles (hex text, compressed or not)
				// cannot be searched; they are replaced by an empty comment of
				// the same size (the chunk becomes tEXt).
				comment := []byte("Comment\x00")
				if len(data) >= len(comment) {
					copy(data, comment)
					for i := len(comment); i < len(data); i++ {
						data[i] = ' '
					}
					copy(header[4:8], "tEXt")
					if _, e := file.WriteAt(header[4:8], at+4); e != nil {
						return changed, e
					}
					done = true
				}
			default:
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

// opaqueMetadata reports a text chunk whose location fields cannot be
// searched as text: any raw profile ("Raw profile type exif" and the like,
// hex-encoded whether compressed or not), and XMP in a zTXt chunk or a
// compressed iTXt chunk.
func opaqueMetadata(kind string, data []byte) bool {
	keyword, rest, found := bytes.Cut(data, []byte{0})
	if !found {
		return false
	}
	name := strings.ToLower(string(keyword))
	if strings.HasPrefix(name, "raw profile type") {
		return true
	}
	switch kind {
	case "zTXt":
		return name == "xml:com.adobe.xmp"
	case "iTXt":
		return name == "xml:com.adobe.xmp" && len(rest) > 0 && rest[0] == 1
	}
	return false
}

// scrubIfStripped removes location data from a stored object when the share
// it belongs to strips it. Caller holds mu.
func (a *App) scrubIfStripped(share, blob, loc string) error {
	var strip bool
	if e := a.db.QueryRow("SELECT strip_location FROM shares WHERE id=?", share).Scan(&strip); e != nil || !strip {
		return e
	}
	dir, e := a.blobDir(loc)
	if e != nil {
		return e
	}
	_, e = scrubLocation(filepath.Join(dir, blob))
	return e
}

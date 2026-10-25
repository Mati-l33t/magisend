package server

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"hash/crc32"
	"image/png"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/tiff"
)

// Pictures the server cannot decode itself, or not at their size, are
// previewed after a check of their structure and sent as they are for the
// browser to draw: large JPEG and PNG photos, AVIF and HEIC. They stay
// within these limits.
const (
	headerPreviewMaxBytes  int64 = 64 << 20
	headerPreviewMaxPixels       = 200_000_000
	// Browsers other than Safari cannot show TIFF; it is decoded here and
	// sent as PNG, so it may be larger than other decoded pictures.
	tiffPreviewMaxBytes int64 = 64 << 20
)

// headerPreview reports the type of a picture that is previewed by its
// structure only; anything else goes the usual, fully decoded way.
func headerPreview(file io.ReaderAt, size int64) (string, bool) {
	if size <= 0 || size > headerPreviewMaxBytes {
		return "", false
	}
	head := make([]byte, min(size, 4096))
	if _, e := file.ReadAt(head, 0); e != nil {
		return "", false
	}
	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		if _, ok := largeJPEG(file, size); ok {
			return "image/jpeg", true
		}
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		if largePNG(file, size) {
			return "image/png", true
		}
	default:
		return isoImage(file, size, head)
	}
	return "", false
}

// largePNG accepts a still PNG too big to decode here: chunks that add up to
// the file with correct checksums, a sensible size in its header, no
// animation. The file is read once, never decoded.
func largePNG(file io.ReaderAt, size int64) bool {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, 8, size-8), 64<<10)
	header := make([]byte, 8)
	sum := make([]byte, 4)
	for at, first := int64(8), true; ; first = false {
		if _, e := io.ReadFull(reader, header); e != nil {
			return false
		}
		n := int64(binary.BigEndian.Uint32(header[:4]))
		kind := string(header[4:8])
		if n > size-at-12 || first != (kind == "IHDR") || kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
			return false
		}
		crc := crc32.NewIEEE()
		crc.Write(header[4:8])
		var data bytes.Buffer
		if first {
			if n != 13 {
				return false
			}
			if _, e := io.CopyN(io.MultiWriter(crc, &data), reader, n); e != nil {
				return false
			}
		} else if _, e := io.CopyN(crc, reader, n); e != nil {
			return false
		}
		if _, e := io.ReadFull(reader, sum); e != nil || binary.BigEndian.Uint32(sum) != crc.Sum32() {
			return false
		}
		if first {
			ihdr := data.Bytes()
			width, height := int64(binary.BigEndian.Uint32(ihdr[:4])), int64(binary.BigEndian.Uint32(ihdr[4:8]))
			if width <= 0 || height <= 0 || width > headerPreviewMaxPixels/height {
				return false
			}
			// Small enough to decode: the usual way checks every pixel.
			if size <= previewMaxBytes && width <= previewMaxPixels/height {
				return false
			}
		}
		at += 12 + n
		if kind == "IEND" {
			return n == 0 && at == size
		}
	}
}

// isoImage recognises AVIF and HEIC/HEIF: an ftyp box naming an image brand,
// then boxes that add up to the file, with the image's description (meta,
// or moov for an image sequence) among them.
func isoImage(file io.ReaderAt, size int64, head []byte) (string, bool) {
	if len(head) < 16 || string(head[4:8]) != "ftyp" {
		return "", false
	}
	boxSize := int64(binary.BigEndian.Uint32(head[:4]))
	if boxSize < 16 || boxSize > int64(len(head)) || boxSize%4 != 0 {
		return "", false
	}
	brands := map[string]bool{string(head[8:12]): true}
	for at := int64(16); at+4 <= boxSize; at += 4 {
		brands[string(head[at:at+4])] = true
	}
	kind := ""
	switch {
	case brands["avif"] || brands["avis"]:
		kind = "image/avif"
	case brands["heic"] || brands["heix"] || brands["heim"] || brands["heis"] || brands["hevc"] || brands["hevx"]:
		kind = "image/heic"
	case brands["mif1"] || brands["msf1"]:
		kind = "image/heif"
	default:
		return "", false
	}
	described := false
	header := make([]byte, 16)
	at := int64(0)
	for boxes := 0; at < size; boxes++ {
		if boxes >= 1024 || size-at < 8 {
			return "", false
		}
		if _, e := file.ReadAt(header[:8], at); e != nil {
			return "", false
		}
		n := int64(binary.BigEndian.Uint32(header[:4]))
		switch n {
		case 0: // to the end of the file
			n = size - at
		case 1: // a 64-bit size follows
			if size-at < 16 {
				return "", false
			}
			if _, e := file.ReadAt(header[8:16], at+8); e != nil {
				return "", false
			}
			large := binary.BigEndian.Uint64(header[8:16])
			if large > uint64(size-at) {
				return "", false
			}
			n = int64(large)
		}
		if n < 8 || n > size-at {
			return "", false
		}
		if box := string(header[4:8]); box == "meta" || box == "moov" {
			described = true
		}
		at += n
	}
	return kind, described && at == size
}

// tiffPreview decodes a TIFF and returns it as PNG.
func tiffPreview(data []byte) ([]byte, bool) {
	config, e := tiff.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width <= 0 || config.Height <= 0 || config.Width > previewMaxPixels/config.Height {
		return nil, false
	}
	picture, e := tiff.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, false
	}
	var out bytes.Buffer
	if (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, picture) != nil {
		return nil, false
	}
	return out.Bytes(), true
}

func isTIFF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("II*\x00")) || bytes.HasPrefix(data, []byte("MM\x00*"))
}

// svgPreview accepts an SVG document: UTF-8 XML whose root element is svg,
// without entity declarations. It is drawn as a picture (<img>), where its
// scripts never run and it loads nothing else; the response is sandboxed in
// case it is opened directly.
func svgPreview(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(data) || bytes.Contains(bytes.ToUpper(data), []byte("<!ENTITY")) {
		return false
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for tokens := 0; tokens < 64; tokens++ {
		token, e := decoder.Token()
		if e != nil {
			return false
		}
		switch t := token.(type) {
		case xml.StartElement:
			return t.Name.Local == "svg" && (t.Name.Space == "" || t.Name.Space == "http://www.w3.org/2000/svg")
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return false
			}
		}
	}
	return false
}

// icoPreview accepts a Windows icon: a directory whose entries lie inside
// the file, each a PNG (decoded in full) or a bitmap with a sensible header.
func icoPreview(data []byte) bool {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:]) != 0 {
		return false
	}
	if kind := binary.LittleEndian.Uint16(data[2:]); kind != 1 && kind != 2 {
		return false
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	if count == 0 || count > 256 || len(data) < 6+16*count {
		return false
	}
	for i := 0; i < count; i++ {
		entry := data[6+16*i:]
		n, at := int64(binary.LittleEndian.Uint32(entry[8:])), int64(binary.LittleEndian.Uint32(entry[12:]))
		if n < 40 || at < int64(6+16*count) || at+n > int64(len(data)) {
			return false
		}
		picture := data[at : at+n]
		if bytes.HasPrefix(picture, []byte("\x89PNG\r\n\x1a\n")) {
			if kind, ok := rasterPreviewType(picture); !ok || kind != "image/png" {
				return false
			}
			continue
		}
		// BITMAPINFOHEADER or a later version; icons hold the image and its
		// mask, so the height is twice the picture's.
		header := int64(binary.LittleEndian.Uint32(picture[0:]))
		width, height := int64(int32(binary.LittleEndian.Uint32(picture[4:]))), int64(int32(binary.LittleEndian.Uint32(picture[8:])))
		if header != 40 && header != 108 && header != 124 || width <= 0 || width > 1024 || height <= 0 || height > 2048 {
			return false
		}
	}
	return true
}

// documentPreview is the type of a picture checked here by its structure and
// sent as it is (SVG, ICO), or false.
func documentPreview(name string, data []byte) (string, bool) {
	switch filepath.Ext(strings.ToLower(name)) {
	case ".ico", ".cur":
		if icoPreview(data) {
			return "image/x-icon", true
		}
	case ".svg":
		if svgPreview(data) {
			return "image/svg+xml", true
		}
	}
	return "", false
}

package server

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A little-endian Exif block: camera make, a GPS position and a body serial.
func exifBlock() []byte {
	b := make([]byte, 160)
	le := binary.LittleEndian
	copy(b, "II*\x00")
	le.PutUint32(b[4:], 8)
	entry := func(at int, tag, kind uint16, count, value uint32) {
		le.PutUint16(b[at:], tag)
		le.PutUint16(b[at+2:], kind)
		le.PutUint32(b[at+4:], count)
		le.PutUint32(b[at+8:], value)
	}
	// IFD0 at 8: Make, Exif pointer, GPS pointer.
	le.PutUint16(b[8:], 3)
	entry(10, 0x010F, 2, 6, 50)
	entry(22, 0x8769, 4, 1, 56)
	entry(34, 0x8825, 4, 1, 80)
	copy(b[50:], "Canon\x00")
	// Exif IFD at 56: body serial number.
	le.PutUint16(b[56:], 1)
	entry(58, 0xA431, 2, 8, 72)
	copy(b[72:], "SN12345\x00")
	// GPS IFD at 80: latitude reference (inline) and latitude (three rationals).
	le.PutUint16(b[80:], 2)
	entry(82, 0x0001, 2, 2, binary.LittleEndian.Uint32([]byte("N\x00\x00\x00")))
	entry(94, 0x0002, 5, 3, 110)
	for i, v := range []uint32{59, 1, 19, 1, 4512, 100} {
		le.PutUint32(b[110+i*4:], v)
	}
	return b
}

func photo(t *testing.T) []byte {
	var plain bytes.Buffer
	if e := jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 16, 8)), nil); e != nil {
		t.Fatal(e)
	}
	app1 := append([]byte("Exif\x00\x00"), exifBlock()...)
	xmp := []byte(`http://ns.adobe.com/xap/1.0/` + "\x00" + `<x:xmpmeta><rdf:Description exif:GPSLatitude="59,19.7N" exif:GPSLongitude="18,4.1E"><exif:GPSAltitude>27/1</exif:GPSAltitude></rdf:Description></x:xmpmeta>`)
	out := []byte{0xFF, 0xD8}
	for _, seg := range [][]byte{app1, xmp} {
		out = append(out, 0xFF, 0xE1, byte((len(seg)+2)>>8), byte(len(seg)+2))
		out = append(out, seg...)
	}
	return append(out, plain.Bytes()[2:]...)
}

func TestScrubLocationKeepsThePicture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	original := photo(t)
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if changed, e := scrubLocation(path); e != nil || !changed {
		t.Fatal(changed, e)
	}
	data, _ := os.ReadFile(path)
	latitude := make([]byte, 24)
	for i, v := range []uint32{59, 1, 19, 1, 4512, 100} {
		binary.LittleEndian.PutUint32(latitude[i*4:], v)
	}
	if len(data) != len(original) || bytes.Contains(data, latitude) || bytes.Contains(data, []byte("SN12345")) ||
		strings.Contains(string(data), "59,19.7N") || strings.Contains(string(data), "27/1") || !bytes.Contains(data, []byte("Canon")) {
		t.Fatal("scrub result")
	}
	if _, e := jpeg.Decode(bytes.NewReader(data)); e != nil {
		t.Fatal("picture damaged", e)
	}
	if changed, _ := scrubLocation(path); changed {
		t.Fatal("second pass changed it again")
	}

	// PNG: the eXIf chunk is cleaned and its checksum written again.
	var pic bytes.Buffer
	png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	raw := pic.Bytes()
	exif := exifBlock()
	chunk := make([]byte, 8+len(exif)+4)
	binary.BigEndian.PutUint32(chunk, uint32(len(exif)))
	copy(chunk[4:], "eXIf")
	copy(chunk[8:], exif)
	binary.BigEndian.PutUint32(chunk[8+len(exif):], crc32.ChecksumIEEE(chunk[4:8+len(exif)]))
	withExif := append(append(append([]byte{}, raw[:33]...), chunk...), raw[33:]...)
	pngPath := filepath.Join(t.TempDir(), "photo.png")
	os.WriteFile(pngPath, withExif, 0600)
	if changed, e := scrubLocation(pngPath); e != nil || !changed {
		t.Fatal(changed, e)
	}
	data, _ = os.ReadFile(pngPath)
	if bytes.Contains(data, []byte("SN12345")) {
		t.Fatal("png serial kept")
	}
	if _, e := png.Decode(bytes.NewReader(data)); e != nil {
		t.Fatal("png damaged", e)
	}
}

func TestSendLinksScrubPhotosWhenAsked(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	data := photo(t)
	send := func(strip bool) []byte {
		t.Helper()
		body := `{"days":1,"strip_location":` + strconv.FormatBool(strip) + `,"files":[{"name":"IMG_0001.JPG","size":` + strconv.Itoa(len(data)) + `}]}`
		w := call(a, "POST", "/api/shares", body, token)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		sh := decodeShare(t, w)
		if w = chunk(a, token, sh.Files[0].ID, 0, string(data)); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		folderCall(t, a, token, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
		return call(a, "GET", "/api/public/"+sh.ID+"/files/"+sh.Files[0].ID, "", "").Body.Bytes()
	}
	if got := send(true); bytes.Contains(got, []byte("SN12345")) || len(got) != len(data) {
		t.Fatal("photo not scrubbed")
	}
	if got := send(false); !bytes.Equal(got, data) {
		t.Fatal("photo changed without being asked")
	}
}

// A TIFF larger than the scan window, with its directory at the end (as
// libtiff writes it), still loses its location.
func TestScrubLargeTIFFWithDirectoryAtTheEnd(t *testing.T) {
	const at = 9 << 20
	file := make([]byte, at+160)
	copy(file[at:], exifBlock())
	le := binary.LittleEndian
	copy(file, "II*\x00")
	le.PutUint32(file[4:], at+8)
	for _, entry := range []int{10, 22, 34, 58, 94} {
		le.PutUint32(file[at+entry+8:], le.Uint32(file[at+entry+8:])+at)
	}
	path := filepath.Join(t.TempDir(), "scan.tif")
	os.WriteFile(path, file, 0600)
	if changed, e := scrubLocation(path); e != nil || !changed {
		t.Fatal(changed, e)
	}
	got, _ := os.ReadFile(path)
	if len(got) != len(file) || !bytes.Equal(got[at+110:at+134], make([]byte, 24)) {
		t.Fatal("the latitude is still there")
	}
}

// Compressed XMP in a PNG is replaced by an empty comment, and the picture
// still decodes.
func TestScrubCompressedPNGMetadata(t *testing.T) {
	var plain bytes.Buffer
	if e := png.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 4, 4))); e != nil {
		t.Fatal(e)
	}
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		out = append(out, kind...)
		out = append(out, data...)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		return binary.BigEndian.AppendUint32(out, crc.Sum32())
	}
	source := plain.Bytes()
	xmp := append([]byte("XML:com.adobe.xmp\x00\x00"), bytes.Repeat([]byte{0x78, 0x9c, 0x01}, 20)...)
	file := append(append(append([]byte{}, source[:33]...), chunk("zTXt", xmp)...), source[33:]...)
	path := filepath.Join(t.TempDir(), "photo.png")
	os.WriteFile(path, file, 0600)
	if changed, e := scrubLocation(path); e != nil || !changed {
		t.Fatal(changed, e)
	}
	got, _ := os.ReadFile(path)
	if bytes.Contains(got, []byte("zTXt")) || !bytes.Contains(got, []byte("tEXtComment\x00")) || len(got) != len(file) {
		t.Fatal("the compressed metadata is still there")
	}
	if _, e := png.Decode(bytes.NewReader(got)); e != nil {
		t.Fatal("the picture no longer decodes", e)
	}
}

// A raw Exif profile in plain PNG text is hex that cannot be searched, so it
// is replaced too; BigTIFF is refused; a GPS block in a second directory is
// found.
func TestScrubRawProfilesBigTIFFAndLaterDirectories(t *testing.T) {
	var plain bytes.Buffer
	if e := png.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 4, 4))); e != nil {
		t.Fatal(e)
	}
	source := plain.Bytes()
	data := []byte("Raw profile type exif\x00\nexif\n     160\n" + strings.Repeat("4949", 40))
	text := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	text = append(append(text, "tEXt"...), data...)
	crc := crc32.NewIEEE()
	crc.Write([]byte("tEXt"))
	crc.Write(data)
	text = binary.BigEndian.AppendUint32(text, crc.Sum32())
	file := append(append(append([]byte{}, source[:33]...), text...), source[33:]...)
	path := filepath.Join(t.TempDir(), "photo.png")
	os.WriteFile(path, file, 0600)
	if changed, e := scrubLocation(path); e != nil || !changed {
		t.Fatal(changed, e)
	}
	if got, _ := os.ReadFile(path); bytes.Contains(got, []byte("Raw profile type")) {
		t.Fatal("an uncompressed raw profile was kept")
	}

	big := filepath.Join(t.TempDir(), "photo.tif")
	os.WriteFile(big, append([]byte("II+\x00\x08\x00\x00\x00"), make([]byte, 64)...), 0600)
	if _, e := scrubLocation(big); e != errLocationNotRemoved {
		t.Fatal("BigTIFF was passed on unread", e)
	}

	// IFD0 at 8 holds nothing but points at a second directory: the block
	// from exifBlock, moved to 200.
	const at = 192
	second := make([]byte, at+160)
	le := binary.LittleEndian
	copy(second, "II*\x00")
	le.PutUint32(second[4:], 8)
	le.PutUint16(second[8:], 0)
	le.PutUint32(second[10:], at+8)
	copy(second[at:], exifBlock())
	for _, entry := range []int{10, 22, 34, 58, 94} {
		le.PutUint32(second[at+entry+8:], le.Uint32(second[at+entry+8:])+at)
	}
	tiff := filepath.Join(t.TempDir(), "two.tif")
	os.WriteFile(tiff, second, 0600)
	if changed, e := scrubLocation(tiff); e != nil || !changed {
		t.Fatal(changed, e)
	}
	if got, _ := os.ReadFile(tiff); !bytes.Equal(got[at+110:at+134], make([]byte, 24)) {
		t.Fatal("the latitude in the second directory is still there")
	}
}

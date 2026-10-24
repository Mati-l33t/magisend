package main

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

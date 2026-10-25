package server

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func box(kind string, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, kind...), body...)
}

func headerKind(t *testing.T, data []byte) (string, bool) {
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, data, 0600)
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	return headerPreview(f, int64(len(data)))
}

// AVIF and HEIC are recognised by their boxes; a video with the same layout
// and a file whose boxes do not add up are not.
func TestAVIFAndHEICArePreviewedByTheirBoxes(t *testing.T) {
	image := func(brands ...string) []byte {
		body := []byte(brands[0] + "\x00\x00\x00\x00")
		for _, b := range brands {
			body = append(body, b...)
		}
		return append(append(box("ftyp", body), box("meta", make([]byte, 24))...), box("mdat", make([]byte, 64))...)
	}
	for data, want := range map[string]string{
		string(image("avif", "mif1", "miaf")): "image/avif",
		string(image("heic", "mif1")):         "image/heic",
		string(image("mif1", "heic")):         "image/heic",
	} {
		if got, ok := headerKind(t, []byte(data)); !ok || got != want {
			t.Fatal(want, "not recognised:", got)
		}
	}
	video := append(box("ftyp", []byte("isom\x00\x00\x00\x00isommp41")), box("moov", make([]byte, 24))...)
	if _, ok := headerKind(t, video); ok {
		t.Fatal("an MP4 video was taken for a picture")
	}
	broken := image("avif", "mif1")
	broken = append(broken, 0, 0, 1, 0)
	if _, ok := headerKind(t, broken); ok {
		t.Fatal("boxes that do not add up were accepted")
	}
	noMeta := append(box("ftyp", []byte("avif\x00\x00\x00\x00avif")), box("mdat", make([]byte, 64))...)
	if _, ok := headerKind(t, noMeta); ok {
		t.Fatal("an AVIF without its description was accepted")
	}
}

// BMP is decoded in full; TIFF is decoded and sent as PNG.
func TestBMPAndTIFFPreviews(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 3, 2))
	picture.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b, tf bytes.Buffer
	if e := bmp.Encode(&b, picture); e != nil {
		t.Fatal(e)
	}
	if kind, ok := rasterPreviewType(b.Bytes()); !ok || kind != "image/bmp" {
		t.Fatal("BMP refused", kind)
	}
	if _, ok := rasterPreviewType(b.Bytes()[:b.Len()-5]); ok {
		t.Fatal("a truncated BMP was accepted")
	}
	if e := tiff.Encode(&tf, picture, nil); e != nil {
		t.Fatal(e)
	}
	converted, ok := tiffPreview(tf.Bytes())
	if !ok {
		t.Fatal("TIFF refused")
	}
	shown, e := png.Decode(bytes.NewReader(converted))
	if e != nil || shown.Bounds() != picture.Bounds() || color.RGBAModel.Convert(shown.At(1, 1)) != (color.RGBA{R: 200, A: 255}) {
		t.Fatal("the PNG does not show the TIFF's pixels", e)
	}

	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	f := libraryUpload(t, a, owner, "", "scan.tif", tf.String()).Files[0]
	w := call(a, "GET", "/api/library/files/"+f.ID+"/preview", "", owner)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), converted) {
		t.Fatal("TIFF preview", w.Code, w.Header().Get("Content-Type"))
	}
}

// SVG and ICO are shown when their structure is right and only under their
// own names.
func TestSVGAndICOPreviews(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><!-- drawn --><svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4"/></svg>`)
	if kind, ok := documentPreview("logo.svg", svg); !ok || kind != "image/svg+xml" {
		t.Fatal("SVG refused")
	}
	if _, ok := documentPreview("logo.png", svg); ok {
		t.Fatal("an SVG under another name was accepted")
	}
	for _, bad := range []string{
		`<html><svg></svg></html>`,
		`<!DOCTYPE svg [<!ENTITY a "aaaa">]><svg xmlns="http://www.w3.org/2000/svg">&a;</svg>`,
		`text before <svg></svg>`,
		"<svg>\xff</svg>",
	} {
		if _, ok := documentPreview("x.svg", []byte(bad)); ok {
			t.Fatal("accepted", bad)
		}
	}

	var icon bytes.Buffer
	png.Encode(&icon, image.NewGray(image.Rect(0, 0, 16, 16)))
	ico := []byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0}
	ico = binary.LittleEndian.AppendUint32(ico, uint32(icon.Len()))
	ico = binary.LittleEndian.AppendUint32(ico, 22)
	ico = append(ico, icon.Bytes()...)
	if kind, ok := documentPreview("favicon.ico", ico); !ok || kind != "image/x-icon" {
		t.Fatal("ICO refused")
	}
	short := append([]byte{}, ico[:len(ico)-10]...)
	if _, ok := documentPreview("favicon.ico", short); ok {
		t.Fatal("an icon whose entry runs past the file was accepted")
	}
}

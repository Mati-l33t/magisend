package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPreviewRejectsAPNGAnimationChunks(t *testing.T) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	original := buffer.Bytes()
	for _, kind := range []string{"acTL", "fcTL", "fdAT"} {
		t.Run(kind, func(t *testing.T) {
			payload := make([]byte, 26)
			binary.BigEndian.PutUint32(payload, 100000)
			chunk := make([]byte, 12+len(payload))
			binary.BigEndian.PutUint32(chunk, uint32(len(payload)))
			copy(chunk[4:8], kind)
			copy(chunk[8:], payload)
			binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
			data := append(append(append([]byte{}, original[:33]...), chunk...), original[33:]...)
			// The default raster remains valid to a static PNG decoder; browser animation
			// must not be released merely because this static image decodes successfully.
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatal("invalid static fixture", err)
			}
			if _, ok := rasterPreviewType(data); ok {
				t.Fatal("APNG animation chunk accepted as bounded static PNG")
			}
			a := testApp(t)
			setupAdmin(t, a)
			owner := login(t, a, "mati")
			sh := createShare(t, a, owner, "", int64(len(data)))
			if w := chunkUpload(a, owner, sh.Files[0].ID, data); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for _, scope := range []string{"shares", "public"} {
				path := "/api/" + scope + "/" + sh.ID + "/files/" + sh.Files[0].ID
				if w := call(a, "GET", path+"/preview", "", owner); w.Code != 415 {
					t.Errorf("%s preview: %d", scope, w.Code)
				}
				w := call(a, "GET", path, "", owner)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
					t.Error("original attachment changed")
				}
			}
		})
	}
}

// Keep the table's local PNG chunk variable separate from the existing helper.
func chunkUpload(a *App, owner, id string, data []byte) *httptest.ResponseRecorder {
	return chunk(a, owner, id, 0, string(data))
}

func TestPublishZeroByteRejectsSubstitutedStorage(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink-missing-target"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			setupAdmin(t, a)
			owner := login(t, a, "mati")
			sh := createShare(t, a, owner, "", 0)
			path := a.blob(sh.Files[0].ID)
			target := a.blob("missing-target")
			if kind == "fifo" {
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner) }()
			var result *httptest.ResponseRecorder
			select {
			case result = <-done:
			case <-time.After(time.Second):
				t.Error("publication blocked on nonregular storage while holding global mutex")
				// Release the old unsafe FIFO writer before fixture cleanup so RED itself
				// does not leak a blocked goroutine or hang the test process.
				reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				select {
				case result = <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("could not release unsafe publication fixture")
				}
			}
			if result.Code != 503 {
				t.Errorf("publication status %d want 503", result.Code)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Error("publication followed symlink and created its missing target")
			}
			// A subsequent owner metadata operation must still acquire the mutex.
			if w := call(a, "GET", "/api/shares", "", owner); w.Code != 200 {
				t.Error("global mutex unavailable after rejection", w.Code)
			}
		})
	}
}

// A WebP cut short is refused, never passed to the browser undecoded.
func TestPreviewRejectsTruncatedWebP(t *testing.T) {
	data, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if kind, ok := rasterPreviewType(data); !ok || kind != "image/webp" {
		t.Fatal("a valid WebP was refused", kind)
	}
	if _, ok := rasterPreviewType(data[:len(data)-4]); ok {
		t.Fatal("a truncated WebP was accepted")
	}
}

// A photo from a 200 MP camera is previewed by its header, without decoding;
// a large PNG is still refused.
func TestLargeJPEGIsPreviewedByItsHeader(t *testing.T) {
	var small bytes.Buffer
	if e := jpeg.Encode(&small, image.NewGray(image.Rect(0, 0, 8, 8)), nil); e != nil {
		t.Fatal(e)
	}
	data := small.Bytes()
	// Rewrite the frame size to 16320x12240 (200 MP, as a Galaxy S25 takes).
	for i := 0; i+9 < len(data); i++ {
		if data[i] == 0xFF && data[i+1] == 0xC0 {
			binary.BigEndian.PutUint16(data[i+5:], 12240)
			binary.BigEndian.PutUint16(data[i+7:], 16320)
			break
		}
	}
	path := filepath.Join(t.TempDir(), "photo.jpg")
	os.WriteFile(path, data, 0600)
	f, _ := os.Open(path)
	defer f.Close()
	if _, ok := largeJPEG(f, int64(len(data))); !ok {
		t.Fatal("a 200 MP JPEG was not accepted")
	}
	binary.BigEndian.PutUint16(data[bytes.Index(data, []byte{0xFF, 0xC0})+5:], 20000)
	os.WriteFile(path, data, 0600)
	g, _ := os.Open(path)
	defer g.Close()
	if _, ok := largeJPEG(g, int64(len(data))); ok {
		t.Fatal("a JPEG past 200 MP was accepted")
	}
	var png8 bytes.Buffer
	png.Encode(&png8, image.NewGray(image.Rect(0, 0, 8, 8)))
	os.WriteFile(path, png8.Bytes(), 0600)
	h, _ := os.Open(path)
	defer h.Close()
	if _, ok := largeJPEG(h, int64(png8.Len())); ok {
		t.Fatal("a PNG took the JPEG path")
	}
}

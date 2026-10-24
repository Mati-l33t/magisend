package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
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

package server

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"testing"
)

func TestPreviewAnimatedGIFWorkBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		side, frames  int
		truncate      bool
		wantSupported bool
	}{
		{"small-animation", 2, 2, false, true},
		{"frame-limit", 1, 32, false, true},
		{"too-many-frames", 1, 33, false, false},
		{"too-many-total-pixels", 2000, 5, false, false},
		{"truncated-animation", 2, 2, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := image.NewPaletted(image.Rect(0, 0, tc.side, tc.side), color.Palette{color.Black, color.White})
			animation := &gif.GIF{}
			for i := 0; i < tc.frames; i++ {
				animation.Image = append(animation.Image, frame)
				animation.Delay = append(animation.Delay, 1)
			}
			var encoded bytes.Buffer
			if e := gif.EncodeAll(&encoded, animation); e != nil {
				t.Fatal(e)
			}
			data := encoded.Bytes()
			if tc.truncate {
				data = data[:len(data)-2]
			}
			contentType, ok := rasterPreviewType(data)
			if ok != tc.wantSupported || (ok && contentType != "image/gif") {
				t.Fatalf("supported=%v MIME=%q, want supported=%v", ok, contentType, tc.wantSupported)
			}
		})
	}
}

func TestPreviewSharesDownloadSlotsWithoutExtendingBearerAccess(t *testing.T) {
	a, owner, sh := readyShare(t)
	for i := 0; i < cap(a.downloads); i++ {
		a.downloads <- struct{}{}
	}
	for _, path := range []string{
		"/api/shares/" + sh.ID + "/files/" + sh.Files[0].ID,
		"/api/shares/" + sh.ID + "/files/" + sh.Files[0].ID + "/preview",
		"/api/public/" + sh.ID + "/files/" + sh.Files[0].ID + "/preview",
	} {
		if w := call(a, "GET", path, "", owner); w.Code != 429 {
			t.Errorf("shared slots: %s returned %d", path, w.Code)
		}
	}
	for i := 0; i < cap(a.downloads); i++ {
		<-a.downloads
	}
	// Existing helper's file is plain abc: owners can download, not preview it.
	if w := call(a, "GET", "/api/shares/"+sh.ID+"/files/"+sh.Files[0].ID+"/preview", "", owner); w.Code != 415 {
		t.Errorf("slot not released or fake image accepted: %d", w.Code)
	}
	if len(a.downloads) != 0 {
		t.Error("preview leaked a download slot")
	}
}

func TestPreviewValidatesStorageBeforeContent(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	var encoded bytes.Buffer
	if e := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"missing", "truncated", "symlink", "directory", "backend"} {
		t.Run(kind, func(t *testing.T) {
			sh := createShare(t, a, owner, "", int64(encoded.Len()))
			if w := chunk(a, owner, sh.Files[0].ID, 0, encoded.String()); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			path := a.blob(sh.Files[0].ID)
			var e error
			switch kind {
			case "missing":
				e = os.Remove(path)
			case "truncated":
				e = os.Truncate(path, 1)
			case "symlink":
				if e = os.Rename(path, path+"-target"); e == nil {
					e = os.Symlink(path+"-target", path)
				}
			case "directory":
				if e = os.Remove(path); e == nil {
					e = os.Mkdir(path, 0700)
				}
			case "backend":
				e = exec(a, "UPDATE files SET backend='unavailable' WHERE id=?", sh.Files[0].ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			for _, scope := range []string{"shares", "public"} {
				for _, suffix := range []string{"", "/preview"} {
					w := call(a, "GET", "/api/"+scope+"/"+sh.ID+"/files/"+sh.Files[0].ID+suffix, "", owner)
					if w.Code != 503 {
						t.Errorf("%s%s: %d want storage failure 503", scope, suffix, w.Code)
					}
				}
			}
		})
	}
}

func readyShare(t *testing.T) (*App, string, Share) {
	t.Helper()
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	sh := createShare(t, a, owner, "share-password", 3)
	if w := chunk(a, owner, sh.Files[0].ID, 0, "abc"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	return a, owner, sh
}

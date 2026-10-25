package server

import (
	"encoding/binary"
	"runtime"
	"testing"
)

// Bytes a token upload already wrote count in the free disk space, so they
// are not counted a second time through its reservation.
func TestTokenUploadWrittenBytesCountOnce(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	var owner string
	a.db.QueryRow("SELECT id FROM users WHERE username='mati'").Scan(&owner)
	a.db.Exec("UPDATE users SET quota=? WHERE id=?", int64(1)<<50, owner)
	a.db.Exec("UPDATE settings SET storage_budget=?", int64(1)<<50)
	free, e := a.diskFree()
	if e != nil || free < 1<<30 {
		t.Skip("not enough free disk for the test", free, e)
	}
	// An upload that has written as much as the disk has free: counted
	// against the reservation as well, nothing more would fit.
	s := &tokenSpace{a: a, owner: owner, limit: int64(1) << 50, held: free, wrote: free}
	a.mu.Lock()
	a.tokenUploads.total += free
	a.tokenUploads.user[owner] += free
	a.mu.Unlock()
	if ok, e := s.grow(s.held + 1); !ok || e != nil {
		t.Fatal("refused although what was written is already in the free space", ok, e)
	}
	a.mu.Lock()
	if a.tokenUploads.written != free {
		t.Fatal("written bytes not counted", a.tokenUploads.written, free)
	}
	a.mu.Unlock()
	s.release()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokenUploads.total != 0 || a.tokenUploads.written != 0 || len(a.tokenUploads.user) != 0 {
		t.Fatal("reservations left behind", a.tokenUploads.total, a.tokenUploads.written, a.tokenUploads.user)
	}
}

func riffWebP(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
		if len(c)%2 == 1 {
			body = append(body, 0)
		}
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func webpChunk(id string, data []byte) []byte {
	return append(append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(data)))...), data...)
}

// A WebP whose picture is larger than the small canvas it declares is
// refused before anything that size is decoded.
func TestWebPFrameLargerThanCanvasIsRefused(t *testing.T) {
	canvas := webpChunk("VP8X", []byte{0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0}) // 1×1, with alpha
	lossless := webpChunk("VP8L", append([]byte{0x2f, 0xff, 0xff, 0xff, 0x0f}, make([]byte, 64)...))
	lossy := webpChunk("VP8 ", append([]byte{0x50, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0xff, 0x3f, 0xff, 0x3f}, make([]byte, 64)...))
	alpha := webpChunk("ALPH", []byte{0, 0xff})
	for name, data := range map[string][]byte{
		"lossless": riffWebP(webpChunk("VP8X", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}), lossless),
		"lossy":    riffWebP(canvas, alpha, lossy),
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, ok := rasterPreviewType(data); ok {
			t.Fatal(name, "accepted")
		}
		runtime.ReadMemStats(&after)
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
			t.Fatal(name, "allocated", grew)
		}
	}
}

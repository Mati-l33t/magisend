package server

import (
	"bytes"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidSlugShape(t *testing.T) {
	valid := []string{"abc", "a-b", "min-delning-2026", "abc123", "a1b2c3d4e5f6a7b8"}
	for _, slug := range valid {
		if !validSlug(slug) {
			t.Fatalf("rejected valid slug %q", slug)
		}
	}
	invalid := []string{"", "ab", "AB", "Abc", "abc-", "-abc", "a--b", "a b", "a/b", "api", "assets", "settings", "login", "admin", "s", "åäö", "abc_def", "abc.def", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4"}
	for _, slug := range invalid {
		if validSlug(slug) {
			t.Fatalf("accepted invalid slug %q", slug)
		}
	}
	// 48 hex characters is a share id and must never be claimed as a name.
	if validSlug("0123456789abcdef0123456789abcdef0123456789abcdef") {
		t.Fatal("accepted an id-shaped slug")
	}
}

func TestScaleDownPreservesAspectAndBounds(t *testing.T) {
	tall := image.NewRGBA(image.Rect(0, 0, 900, 300))
	small, e := scaleDown(tall, 256)
	if e != nil {
		t.Fatal(e)
	}
	if small.Bounds().Dx() != 256 || small.Bounds().Dy() != 85 {
		t.Fatalf("unexpected scaled size %dx%d", small.Bounds().Dx(), small.Bounds().Dy())
	}
	portrait := image.NewRGBA(image.Rect(0, 0, 300, 900))
	if small, e = scaleDown(portrait, 256); e != nil || small.Bounds().Dy() != 256 || small.Bounds().Dx() != 85 {
		t.Fatalf("portrait scaled to %v (%v)", small.Bounds(), e)
	}
	tiny := image.NewRGBA(image.Rect(0, 0, 12, 7))
	if small, e = scaleDown(tiny, 256); e != nil || small.Bounds().Dx() != 12 || small.Bounds().Dy() != 7 {
		t.Fatalf("small image was rescaled: %v (%v)", small.Bounds(), e)
	}
	// Pixel content survives the copy: the source is a gradient, not a blank.
	source := image.NewRGBA(image.Rect(0, 0, 4, 4))
	source.Set(3, 3, color.RGBA{0xff, 0, 0, 0xff})
	if small, e = scaleDown(source, 256); e != nil {
		t.Fatal(e)
	}
	if r, _, _, _ := small.At(3, 3).RGBA(); r != 0xffff {
		t.Fatalf("pixel content lost: %v", small.At(3, 3))
	}
	if _, e = scaleDown(image.NewRGBA(image.Rect(0, 0, 0, 0)), 256); !errors.Is(e, errNoThumbnail) {
		t.Fatalf("empty image accepted: %v", e)
	}
}

func TestOpaqueDetection(t *testing.T) {
	opaqueImage := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			opaqueImage.Set(x, y, color.RGBA{10, 20, 30, 0xff})
		}
	}
	if !opaque(opaqueImage) {
		t.Fatal("opaque image reported as transparent")
	}
	transparent := image.NewRGBA(image.Rect(0, 0, 4, 4))
	transparent.Set(1, 1, color.RGBA{10, 20, 30, 0x00})
	if opaque(transparent) {
		t.Fatal("transparent pixel ignored")
	}
	if !opaque(image.NewGray(image.Rect(0, 0, 4, 4))) {
		t.Fatal("gray image reported as transparent")
	}
}

func seedStoredFile(t *testing.T, a *App, payload []byte, declared int64) string {
	t.Helper()
	fileID, shareID := randomID(), randomID()
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, e := a.db.Exec("INSERT INTO shares(id,title,expires,created,published) VALUES(?,?,?,?,1)", shareID, "seed", 0, time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO files(id,share_id,name,size,offset,backend) VALUES(?,?,?,?,?,'local')", fileID, shareID, "seed.bin", declared, declared); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(a.blob(fileID), payload, 0600); e != nil {
		t.Fatal(e)
	}
	return fileID
}

func TestGenerateThumbnailOnlyForBoundedRasters(t *testing.T) {
	a := testApp(t)

	picture := pngFixture(t, 64, 48)
	fileID := seedStoredFile(t, a, picture, int64(len(picture)))
	if e := a.generateThumbnail(fileID, 0); e != nil {
		t.Fatal(e)
	}
	var format string
	var width, height int
	if e := a.db.QueryRow("SELECT format,width,height FROM thumbnails WHERE file_id=?", fileID).Scan(&format, &width, &height); e != nil {
		t.Fatal(e)
	}
	if format != "jpeg" && format != "png" {
		t.Fatalf("unexpected thumbnail format %q", format)
	}
	if width != 64 || height != 48 {
		t.Fatalf("unexpected thumbnail size %dx%d", width, height)
	}
	// Regeneration is idempotent and never replaces a good thumbnail.
	if e := a.generateThumbnail(fileID, 0); e != nil {
		t.Fatal(e)
	}

	text := seedStoredFile(t, a, []byte("not an image at all"), 18)
	if e := a.generateThumbnail(text, 0); !errors.Is(e, errNoThumbnail) {
		t.Fatalf("text produced a thumbnail: %v", e)
	}
	oversize := seedStoredFile(t, a, picture, previewMaxBytes+1)
	if e := a.generateThumbnail(oversize, 0); !errors.Is(e, errNoThumbnail) {
		t.Fatalf("oversize file produced a thumbnail: %v", e)
	}
	mismatch := seedStoredFile(t, a, picture, int64(len(picture))+5)
	if e := a.generateThumbnail(mismatch, 0); !errors.Is(e, errNoThumbnail) {
		t.Fatalf("size mismatch produced a thumbnail: %v", e)
	}
	var count int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM thumbnails").Scan(&count); e != nil || count != 1 {
		t.Fatalf("unexpected thumbnail rows: %d %v", count, e)
	}
}

func TestAnimatedGIFThumbnailUsesFirstFrame(t *testing.T) {
	a := testApp(t)
	palette := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(0, 0, 120, 90), palette)
	for x := 0; x < 120; x++ {
		frame.SetColorIndex(x, 10, 1)
	}
	second := image.NewPaletted(image.Rect(0, 0, 120, 90), palette)
	for x := 0; x < 120; x++ {
		second.SetColorIndex(x, 40, 1)
	}
	var encoded bytes.Buffer
	if e := gif.EncodeAll(&encoded, &gif.GIF{Image: []*image.Paletted{frame, second}, Delay: []int{10, 10}}); e != nil {
		t.Fatal(e)
	}
	fileID := seedStoredFile(t, a, encoded.Bytes(), int64(encoded.Len()))
	if e := a.generateThumbnail(fileID, 0); e != nil {
		t.Fatal(e)
	}
	var width, height int
	if e := a.db.QueryRow("SELECT width,height FROM thumbnails WHERE file_id=?", fileID).Scan(&width, &height); e != nil {
		t.Fatal(e)
	}
	if width != 120 || height != 90 {
		t.Fatalf("animated gif thumbnail %dx%d", width, height)
	}
}

func TestMigrationKeepsLegacySharesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "blobs"), 0700); e != nil {
		t.Fatal(e)
	}
	legacy, e := sql.Open("sqlite3", filepath.Join(root, "magisend.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = legacy.Exec(`CREATE TABLE shares(id TEXT PRIMARY KEY, owner TEXT, title TEXT NOT NULL, password TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL, created INTEGER NOT NULL, published INTEGER NOT NULL DEFAULT 0)`); e != nil {
		t.Fatal(e)
	}
	if _, e = legacy.Exec("INSERT INTO shares(id,owner,title,expires,created,published) VALUES('legacy-row','legacy-user','Gammal delning',0,1,1)"); e != nil {
		t.Fatal(e)
	}
	legacy.Close()

	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer a.db.Close()
	if _, e = a.db.Exec("INSERT INTO users(id,username,password,role,quota) VALUES('legacy-user','legacy','x','user',1)"); e != nil {
		t.Fatal(e)
	}
	share, _, _, e := a.loadShare("legacy-row")
	if e != nil || share.Title != "Gammal delning" || share.Description != "" || share.Slug != "" {
		t.Fatalf("legacy share damaged: %+v (%v)", share, e)
	}
	// A second start must not fail or repeat the schema change.
	if e = migrate(a.db); e != nil {
		t.Fatal(e)
	}
	rows, e := a.db.Query("PRAGMA table_info(shares)")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var fallback any
		if e = rows.Scan(&cid, &name, &kind, &notNull, &fallback, &primaryKey); e != nil {
			t.Fatal(e)
		}
		columns[name] = true
	}
	if !columns["description"] || !columns["slug"] {
		t.Fatalf("migration did not add columns: %v", columns)
	}
	// The unique index must not merge or reject the legacy empty slug rows.
	if _, e = a.db.Exec("INSERT INTO shares(id,title,description,slug,expires,created,published) VALUES('second','Andra','','',0,2,1)"); e != nil {
		t.Fatal(e)
	}
}

// The defensive mapping behind the 409 must recognise the error the driver really
// produces, not a guessed message.
func TestSlugConflictIsRecognisedFromTheDriverError(t *testing.T) {
	a := testApp(t)
	if _, e := a.db.Exec("INSERT INTO shares(id,title,slug,expires,created,published) VALUES('dup-one','Först','taget-namn',0,1,1)"); e != nil {
		t.Fatal(e)
	}
	_, e := a.db.Exec("INSERT INTO shares(id,title,slug,expires,created,published) VALUES('dup-two','Andra','taget-namn',0,1,1)")
	if e == nil {
		t.Fatal("the partial unique index did not fire")
	}
	if !slugConflictError(e) {
		t.Fatalf("driver error not recognised as a slug conflict: %v", e)
	}
	if slugConflictError(nil) || slugConflictError(errors.New("disk I/O error")) {
		t.Fatal("unrelated errors must not look like a slug conflict")
	}
	// Empty slugs share the same value on purpose and must stay allowed.
	if _, e = a.db.Exec("INSERT INTO shares(id,title,slug,expires,created,published) VALUES('empty-one','A','',0,1,1)"); e != nil {
		t.Fatal(e)
	}
	if _, e = a.db.Exec("INSERT INTO shares(id,title,slug,expires,created,published) VALUES('empty-two','B','',0,1,1)"); e != nil {
		t.Fatal(e)
	}
}

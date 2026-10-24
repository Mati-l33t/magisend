package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Black-box regressions for 0.3.1. They deliberately use only JSON maps and
// helpers that existed before the feature, so the same file compiles against the
// previous revision and fails there for behavioural reasons, not build reasons.

func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 0x40, 0xff})
		}
	}
	var buffer bytes.Buffer
	if e := png.Encode(&buffer, img); e != nil {
		t.Fatal(e)
	}
	return buffer.Bytes()
}

func decodeObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &value); e != nil {
		t.Fatalf("invalid JSON: %v (%s)", e, w.Body)
	}
	return value
}

// Returns the share id and the file ids by name.
func createUploadPublish(t *testing.T, a *App, session, body string, payloads map[string][]byte) (string, map[string]string) {
	t.Helper()
	w := call(a, "POST", "/api/shares", body, session)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := decodeObject(t, w)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no share id in %s", w.Body)
	}
	files, _ := created["files"].([]any)
	if len(files) != len(payloads) {
		t.Fatalf("unexpected file count: %s", w.Body)
	}
	ids := map[string]string{}
	for _, entry := range files {
		file, _ := entry.(map[string]any)
		name, _ := file["name"].(string)
		fileID, _ := file["id"].(string)
		data, ok := payloads[name]
		if !ok {
			t.Fatalf("no payload for %s", name)
		}
		if w = chunk(a, session, fileID, 0, string(data)); w.Code != 200 {
			t.Fatalf("chunk %s: %d %s", name, w.Code, w.Body)
		}
		ids[name] = fileID
	}
	if w = call(a, "POST", "/api/shares/"+id+"/publish", "{}", session); w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	return id, ids
}

func thumbnail(t *testing.T, a *App, path, session string, want int) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := call(a, "GET", path, "", session)
		if w.Code == want || time.Now().After(deadline) {
			return w
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestShareCarriesDescriptionAndCustomLink(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	payload := pngFixture(t, 40, 30)
	body := fmt.Sprintf(`{"title":"Bilder","description":"  Två rader\nrad två  ","slug":"bilder-2026","days":7,"files":[{"name":"a.png","size":%d}]}`, len(payload))
	id, _ := createUploadPublish(t, a, session, body, map[string][]byte{"a.png": payload})

	list := call(a, "GET", "/api/shares", "", session)
	if list.Code != 200 {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	var shares []map[string]any
	if e := json.Unmarshal(list.Body.Bytes(), &shares); e != nil || len(shares) != 1 {
		t.Fatalf("list: %v %s", e, list.Body)
	}
	if shares[0]["slug"] != "bilder-2026" || shares[0]["description"] != "Två rader\nrad två" {
		t.Fatalf("owner list lost metadata: %v", shares[0])
	}
	if shares[0]["slug"] == "" || shares[0]["slug"] == nil {
		t.Fatalf("owner list has no custom link: %v", shares[0])
	}
	// The custom link serves the share, and the generated id link keeps working.
	for _, ref := range []string{"bilder-2026", id} {
		w := call(a, "GET", "/api/public/"+ref, "", "")
		if w.Code != 200 {
			t.Fatalf("public %s: %d %s", ref, w.Code, w.Body)
		}
		if public := decodeObject(t, w); public["slug"] != "bilder-2026" || public["description"] != "Två rader\nrad två" {
			t.Fatalf("public %s lost metadata: %v", ref, public)
		}
	}
}

func TestCustomLinkValidationAndUniqueness(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	hex48 := strings.Repeat("ab12", 12)
	for _, slug := range []string{"ab", "Abc", "-abc", "abc-", "a--b", "api", "admin", "with space", "bad/name", hex48, "åäö"} {
		body := fmt.Sprintf(`{"title":"x","slug":%q,"days":7,"files":[{"name":"a.png","size":10}]}`, slug)
		w := call(a, "POST", "/api/shares", body, session)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "slug_invalid") {
			t.Fatalf("slug %q accepted: %d %s", slug, w.Code, w.Body)
		}
	}
	body := `{"title":"x","slug":"min-delning","days":7,"files":[{"name":"a.png","size":10}]}`
	if w := call(a, "POST", "/api/shares", body, session); w.Code != 201 {
		t.Fatalf("valid slug rejected: %d %s", w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares", body, session); w.Code != 409 || !strings.Contains(w.Body.String(), "slug_taken") {
		t.Fatalf("duplicate slug accepted: %d %s", w.Code, w.Body)
	}
	long := strings.Repeat("å", 1001)
	if w := call(a, "POST", "/api/shares", fmt.Sprintf(`{"title":"x","description":%q,"days":7,"files":[{"name":"a.png","size":10}]}`, long), session); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_description") {
		t.Fatalf("long description accepted: %d %s", w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares", `{"title":"x","slug":"","days":7,"files":[{"name":"a.png","size":10}]}`, session); w.Code != 201 {
		t.Fatalf("empty slug rejected: %d %s", w.Code, w.Body)
	}
}

func TestThumbnailIsBoundedAndAuthorized(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	picture := pngFixture(t, 900, 300)
	vector := []byte("<svg></svg>")
	body := fmt.Sprintf(`{"title":"Bilder","days":7,"files":[{"name":"a.png","size":%d},{"name":"b.svg","size":%d}]}`, len(picture), len(vector))
	id, ids := createUploadPublish(t, a, session, body, map[string][]byte{"a.png": picture, "b.svg": vector})

	owned := "/api/shares/" + id + "/files/"
	w := thumbnail(t, a, owned+ids["a.png"]+"/thumbnail", session, 200)
	if w.Code != 200 {
		t.Fatalf("owner thumbnail: %d %s", w.Code, w.Body)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "image/png" && contentType != "image/jpeg" {
		t.Fatalf("thumbnail content type %q", contentType)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("unsafe thumbnail headers: %v", w.Header())
	}
	config, format, e := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if e != nil || (format != "png" && format != "jpeg") {
		t.Fatalf("thumbnail is not a decodable raster: %v %q", e, format)
	}
	if config.Width > 256 || config.Height > 256 {
		t.Fatalf("thumbnail too large: %dx%d", config.Width, config.Height)
	}
	if config.Width != 256 || config.Height != 85 {
		t.Fatalf("thumbnail aspect not preserved: %dx%d", config.Width, config.Height)
	}
	if w = call(a, "GET", owned+ids["b.svg"]+"/thumbnail", "", session); w.Code != 404 || !strings.Contains(w.Body.String(), "thumbnail_unavailable") {
		t.Fatalf("non-raster thumbnail: %d %s", w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/public/"+id+"/files/"+ids["a.png"]+"/thumbnail", "", ""); w.Code != 200 {
		t.Fatalf("public thumbnail: %d %s", w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/public/"+id+"/files/"+strings.Repeat("0", 48)+"/thumbnail", "", ""); w.Code != 404 {
		t.Fatalf("unknown file thumbnail: %d %s", w.Code, w.Body)
	}
	if w = call(a, "GET", owned+ids["a.png"]+"/thumbnail", "", ""); w.Code != 401 {
		t.Fatalf("owner thumbnail without session: %d %s", w.Code, w.Body)
	}

	// A password protected share answers the owner but never an anonymous visitor.
	protected := fmt.Sprintf(`{"title":"Skyddad","password":"hemligt hemligt","days":7,"files":[{"name":"a.png","size":%d}]}`, len(picture))
	protectedID, protectedIDs := createUploadPublish(t, a, session, protected, map[string][]byte{"a.png": picture})
	if w = thumbnail(t, a, "/api/shares/"+protectedID+"/files/"+protectedIDs["a.png"]+"/thumbnail", session, 200); w.Code != 200 {
		t.Fatalf("owner thumbnail of protected share: %d %s", w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/public/"+protectedID+"/files/"+protectedIDs["a.png"]+"/thumbnail", "", ""); w.Code != 401 {
		t.Fatalf("protected thumbnail without grant: %d %s", w.Code, w.Body)
	}
}

func TestDeletingShareRemovesThumbnailMetadata(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	session := login(t, a, "mati")
	picture := pngFixture(t, 60, 60)
	id, ids := createUploadPublish(t, a, session, fmt.Sprintf(`{"title":"Bilder","days":7,"files":[{"name":"a.png","size":%d}]}`, len(picture)), map[string][]byte{"a.png": picture})
	if w := thumbnail(t, a, "/api/shares/"+id+"/files/"+ids["a.png"]+"/thumbnail", session, 200); w.Code != 200 {
		t.Fatalf("thumbnail before delete: %d %s", w.Code, w.Body)
	}
	if w := call(a, "DELETE", "/api/shares/"+id, "{}", session); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	var count int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM thumbnails").Scan(&count); e != nil || count != 0 {
		t.Fatalf("thumbnails survived deletion: %d %v", count, e)
	}
	if w := call(a, "GET", "/api/public/"+id+"/files/"+ids["a.png"]+"/thumbnail", "", ""); w.Code != 404 {
		t.Fatalf("thumbnail after delete: %d %s", w.Code, w.Body)
	}
}

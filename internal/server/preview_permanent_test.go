package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPermanentCreateAndDaysZeroCompatibility(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	if e := exec(a, "UPDATE settings SET default_days=3 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, fields string
		days         int
	}{
		{"permanent", `"permanent":true,"days":0,`, 0},
		{"legacy-zero", `"days":0,`, 3},
		{"explicit-false", `"permanent":false,"days":0,`, 3},
		{"omitted-days", "", 3},
		{"dated", `"days":2,`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			w := call(a, "POST", "/api/shares", `{`+tc.fields+`"files":[{"name":"empty.png","size":0}]}`, owner)
			after := time.Now()
			if w.Code != 201 {
				t.Fatalf("create: %d %s", w.Code, w.Body)
			}
			var sh Share
			if e := json.Unmarshal(w.Body.Bytes(), &sh); e != nil {
				t.Fatal(e)
			}
			var stored int64
			if e := a.db.QueryRow("SELECT expires FROM shares WHERE id=?", sh.ID).Scan(&stored); e != nil {
				t.Fatal(e)
			}
			if stored != sh.Expires {
				t.Fatalf("response expiry %d differs from database %d", sh.Expires, stored)
			}
			if tc.days == 0 {
				if stored != 0 {
					t.Fatalf("permanent expiry = %d, want 0", stored)
				}
			} else if stored < before.Add(time.Duration(tc.days)*24*time.Hour).Unix() || stored > after.Add(time.Duration(tc.days)*24*time.Hour).Unix() {
				t.Fatalf("expiry %d does not follow %d-day policy", stored, tc.days)
			}
		})
	}
}

func TestPermanentCleanupRetainsPublishedButNotStaleDraftsOrTombstones(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name      string
		published bool
		stale     bool
		tombstone bool
		dated     bool
		keep      bool
	}{
		{name: "published-permanent", published: true, stale: true, keep: true},
		{name: "fresh-permanent-draft", keep: true},
		{name: "stale-permanent-draft", stale: true},
		{name: "permanent-tombstone", published: true, tombstone: true},
		{name: "dated-expired", published: true, dated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			setupAdmin(t, a)
			owner := login(t, a, "mati")
			sh := createShare(t, a, owner, "", 0)
			if tc.published {
				if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			}
			created := now.Unix()
			if tc.stale {
				created = now.Add(-25 * time.Hour).Unix()
			}
			expires := int64(0)
			if tc.dated {
				expires = now.Add(-time.Hour).Unix()
			}
			if e := exec(a, "UPDATE shares SET expires=?,created=? WHERE id=?", expires, created, sh.ID); e != nil {
				t.Fatal(e)
			}
			if tc.tombstone {
				if e := exec(a, "INSERT INTO deleting(share_id) VALUES(?)", sh.ID); e != nil {
					t.Fatal(e)
				}
			}
			if e := a.Cleanup(now); e != nil {
				t.Fatal(e)
			}
			var count int
			if e := a.db.QueryRow("SELECT COUNT(*) FROM shares WHERE id=?", sh.ID).Scan(&count); e != nil {
				t.Fatal(e)
			}
			if (count == 1) != tc.keep {
				t.Errorf("retained=%v, want %v", count == 1, tc.keep)
			}
			if tc.keep && tc.published {
				if e := a.Cleanup(now.AddDate(10, 0, 0)); e != nil {
					t.Fatal(e)
				}
				if _, _, _, e := a.loadShare(sh.ID); e != nil {
					t.Errorf("future cleanup removed permanent share: %v", e)
				}
				if _, e := os.Stat(a.blob(sh.Files[0].ID)); e != nil {
					t.Errorf("future cleanup removed original blob: %v", e)
				}
			}
		})
	}
}

func TestPermanentUploadPublishPublicUnlockAndZIP(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	data := "original\x00bytes\xff"
	sh := createShare(t, a, owner, "share-password", int64(len(data)))
	// Seed the new persisted representation independently of the create test,
	// so each expiry guard is exercised even while permanent:true is missing.
	if e := exec(a, "UPDATE shares SET expires=0 WHERE id=?", sh.ID); e != nil {
		t.Fatal(e)
	}
	t.Run("upload", func(t *testing.T) {
		if w := chunk(a, owner, sh.Files[0].ID, 0, data); w.Code != 200 {
			t.Errorf("permanent upload: %d %s", w.Code, w.Body)
		}
	})
	// Restore a complete real blob if upload is RED, without concealing its failure.
	if e := os.WriteFile(a.blob(sh.Files[0].ID), []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	if e := exec(a, "UPDATE files SET offset=size WHERE id=?", sh.Files[0].ID); e != nil {
		t.Fatal(e)
	}
	t.Run("publish", func(t *testing.T) {
		if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
			t.Errorf("permanent publish: %d %s", w.Code, w.Body)
		}
	})
	if e := exec(a, "UPDATE shares SET published=1 WHERE id=?", sh.ID); e != nil {
		t.Fatal(e)
	}
	base := "/api/public/" + sh.ID
	var cookies []*http.Cookie
	t.Run("unlock", func(t *testing.T) {
		if w := call(a, "POST", base+"/unlock", `{"password":"wrong"}`, ""); w.Code != 401 {
			t.Errorf("wrong password: %d %s", w.Code, w.Body)
		}
		w := call(a, "POST", base+"/unlock", `{"password":"share-password"}`, "")
		if w.Code != 200 {
			t.Fatalf("permanent unlock: %d %s", w.Code, w.Body)
		}
		cookies = w.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("unlock did not issue grant")
		}
	})
	if len(cookies) == 0 {
		// A real persisted grant isolates publicShare's expiry guard from unlock.
		grant := randomID()
		if e := exec(a, "INSERT INTO grants(hash,share_id,expires) VALUES(?,?,?)", hash(grant), sh.ID, time.Now().Add(time.Hour).Unix()); e != nil {
			t.Fatal(e)
		}
		cookies = []*http.Cookie{{Name: "magisend_grant_" + sh.ID, Value: grant}}
	}
	for _, suffix := range []string{"", "/files/" + sh.Files[0].ID, "/zip"} {
		t.Run("public"+suffix, func(t *testing.T) {
			if w := call(a, "GET", base+suffix, "", ""); w.Code != 401 {
				t.Errorf("permanent protected share without grant: %d %s", w.Code, w.Body)
			}
			w := publicRequest(a, "GET", base+suffix, "", cookies)
			if w.Code != 200 {
				t.Fatalf("permanent public access: %d %s", w.Code, w.Body)
			}
			switch suffix {
			case "":
				var got Share
				if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil || got.Expires != 0 {
					t.Fatal("permanent public metadata", e, w.Body)
				}
				// Visitors are not told where a file is stored or how far its upload got.
				if body := w.Body.String(); strings.Contains(body, `"backend"`) || strings.Contains(body, `"offset"`) || len(got.Files) != 1 || got.Files[0].Name != sh.Files[0].Name {
					t.Fatal("public file view", body)
				}
			case "/zip":
				z, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
				if e != nil {
					t.Fatal(e)
				}
				if len(z.File) != 1 || z.File[0].Name != sh.Files[0].Name {
					t.Fatal("unexpected ZIP members")
				}
				f, e := z.File[0].Open()
				if e != nil {
					t.Fatal(e)
				}
				got, e := io.ReadAll(f)
				f.Close()
				if e != nil || string(got) != data {
					t.Fatal("ZIP changed original bytes", e)
				}
			default:
				if w.Body.String() != data || w.Header().Get("Content-Type") != "application/octet-stream" {
					t.Fatal("download changed bytes or MIME")
				}
			}
		})
	}
}

func TestPreviewRasterMIMEHeadersAndOriginalBytes(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			var encoded bytes.Buffer
			var e error
			switch format {
			case "png":
				e = png.Encode(&encoded, img)
			case "jpeg":
				e = jpeg.Encode(&encoded, img, nil)
			case "gif":
				e = gif.Encode(&encoded, img, nil)
			}
			if e != nil {
				t.Fatal(e)
			}
			sh := createShare(t, a, owner, "", int64(encoded.Len()))
			// The bytes, not this deliberately misleading extension, determine MIME.
			name := `not-an-image.html`
			if e := exec(a, "UPDATE files SET name=? WHERE id=?", name, sh.Files[0].ID); e != nil {
				t.Fatal(e)
			}
			if w := chunk(a, owner, sh.Files[0].ID, 0, encoded.String()); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for _, scope := range []string{"shares", "public"} {
				base := "/api/" + scope + "/" + sh.ID + "/files/" + sh.Files[0].ID
				for _, preview := range []bool{true, false} {
					t.Run(fmt.Sprintf("%s/preview=%v", scope, preview), func(t *testing.T) {
						path := base
						wantType, wantDisposition := "application/octet-stream", "attachment"
						if preview {
							path += "/preview"
							wantType, wantDisposition = "image/"+format, "inline"
						}
						w := call(a, "GET", path, "", owner)
						if w.Code != 200 {
							t.Fatalf("%s: %d %s", path, w.Code, w.Body)
						}
						if !bytes.Equal(w.Body.Bytes(), encoded.Bytes()) {
							t.Error("response changed original bytes")
						}
						if w.Header().Get("Content-Type") != wantType {
							t.Errorf("MIME=%q want %q", w.Header().Get("Content-Type"), wantType)
						}
						disposition, params, e := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
						if e != nil || disposition != wantDisposition || params["filename"] != name {
							t.Errorf("disposition=%q: %v", w.Header().Get("Content-Disposition"), e)
						}
						if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
							t.Error("missing no-store/nosniff")
						}
						if preview {
							found := false
							for _, directive := range strings.Split(w.Header().Get("Content-Security-Policy"), ";") {
								if strings.TrimSpace(directive) == "sandbox" {
									found = true
								}
							}
							if !found {
								t.Error("preview must use restrictive CSP sandbox")
							}
						}
					})
				}
			}
			stored, e := os.ReadFile(a.blob(sh.Files[0].ID))
			if e != nil || !bytes.Equal(stored, encoded.Bytes()) {
				t.Fatal("preview mutated stored original", e)
			}
		})
	}
}

func TestPreviewPNGAuthorizationIDORAndLifecycle(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	if _, e := a.addUser("bob", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	bob := login(t, a, "bob")
	var encoded bytes.Buffer
	if e := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); e != nil {
		t.Fatal(e)
	}
	sh := createShare(t, a, owner, "share-password", int64(encoded.Len()))
	if w := chunk(a, owner, sh.Files[0].ID, 0, encoded.String()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	foreign := createShare(t, a, bob, "", 0)
	w := call(a, "POST", "/api/public/"+sh.ID+"/unlock", `{"password":"share-password"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	grant := w.Result().Cookies()
	if len(grant) == 0 {
		t.Fatal("missing grant fixture")
	}
	ownerCookies := []*http.Cookie{{Name: "magisend_session", Value: owner}}
	bobCookies := []*http.Cookie{{Name: "magisend_session", Value: bob}}
	for _, tc := range []struct {
		name, scope, file string
		cookies           []*http.Cookie
		status            int
	}{
		{"owner-anonymous", "shares", sh.Files[0].ID, nil, 401},
		{"owner-no-public-password", "shares", sh.Files[0].ID, ownerCookies, 200},
		{"other-owner", "shares", sh.Files[0].ID, bobCookies, 404},
		{"grant-is-not-owner", "shares", sh.Files[0].ID, grant, 401},
		{"other-owner-with-grant", "shares", sh.Files[0].ID, append(append([]*http.Cookie{}, bobCookies...), grant...), 404},
		{"owner-foreign-file", "shares", foreign.Files[0].ID, ownerCookies, 404},
		{"public-no-grant", "public", sh.Files[0].ID, nil, 401},
		{"owner-session-not-public-grant", "public", sh.Files[0].ID, ownerCookies, 401},
		{"public-unlocked", "public", sh.Files[0].ID, grant, 200},
		{"public-foreign-file", "public", foreign.Files[0].ID, grant, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, suffix := range []string{"/preview", ""} {
				path := "/api/" + tc.scope + "/" + sh.ID + "/files/" + tc.file + suffix
				w := publicRequest(a, "GET", path, "", tc.cookies)
				if w.Code != tc.status {
					t.Errorf("%s: %d want %d; %s", path, w.Code, tc.status, w.Body)
				}
				if tc.status == 200 && !bytes.Equal(w.Body.Bytes(), encoded.Bytes()) {
					t.Error("authorized PNG differs from original")
				}
			}
		})
	}
	for _, state := range []string{"permanent", "expired-grant", "expired-share", "draft", "incomplete-file", "tombstone", "deleted"} {
		t.Run(state, func(t *testing.T) {
			if e := exec(a, "UPDATE shares SET expires=?,published=1 WHERE id=?", time.Now().Add(time.Hour).Unix(), sh.ID); e != nil {
				t.Fatal(e)
			}
			if e := exec(a, "UPDATE files SET offset=size WHERE id=?", sh.Files[0].ID); e != nil {
				t.Fatal(e)
			}
			if e := exec(a, "UPDATE grants SET expires=? WHERE share_id=?", time.Now().Add(time.Hour).Unix(), sh.ID); e != nil {
				t.Fatal(e)
			}
			ownerStatus, publicStatus := 200, 200
			var e error
			switch state {
			case "permanent":
				e = exec(a, "UPDATE shares SET expires=0 WHERE id=?", sh.ID)
			case "expired-grant":
				e = exec(a, "UPDATE grants SET expires=? WHERE share_id=?", time.Now().Add(-time.Minute).Unix(), sh.ID)
				publicStatus = 401
			case "expired-share":
				e = exec(a, "UPDATE shares SET expires=? WHERE id=?", time.Now().Add(-time.Minute).Unix(), sh.ID)
				ownerStatus, publicStatus = 410, 410
			case "draft":
				e = exec(a, "UPDATE shares SET published=0 WHERE id=?", sh.ID)
				ownerStatus, publicStatus = 404, 404
			case "incomplete-file":
				e = exec(a, "UPDATE files SET offset=size-1 WHERE id=?", sh.Files[0].ID)
				ownerStatus, publicStatus = 404, 404
			case "tombstone":
				e = exec(a, "INSERT INTO deleting(share_id) VALUES(?)", sh.ID)
				ownerStatus, publicStatus = 404, 404
			case "deleted":
				w := call(a, "DELETE", "/api/shares/"+sh.ID, "{}", owner)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
				ownerStatus, publicStatus = 404, 404
			}
			if e != nil {
				t.Fatal(e)
			}
			for _, scope := range []string{"shares", "public"} {
				cookies, status := ownerCookies, ownerStatus
				if scope == "public" {
					cookies, status = grant, publicStatus
				}
				for _, suffix := range []string{"/preview", ""} {
					path := "/api/" + scope + "/" + sh.ID + "/files/" + sh.Files[0].ID + suffix
					w := publicRequest(a, "GET", path, "", cookies)
					if w.Code != status {
						t.Errorf("%s: %d want %d; %s", path, w.Code, status, w.Body)
					}
				}
			}
		})
	}
}

func TestPreviewRejectsUnsafeMalformedAndOversizeImages(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	var small, huge bytes.Buffer
	if e := png.Encode(&small, image.NewGray(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	// A real, highly compressible raster above 16 MP; bounded to ~17 MB raw.
	if e := png.Encode(&huge, image.NewGray(image.Rect(0, 0, 4097, 4097))); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"svg.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"html.png", []byte(`<!doctype html><html><script>alert(1)</script></html>`)},
		{"fake.png", []byte("not a PNG despite its extension")},
		{"signature-only.png", []byte("\x89PNG\r\n\x1a\n")},
		{"truncated.png", small.Bytes()[:small.Len()/2]},
		{"bad-crc.png", append(append([]byte{}, small.Bytes()[:small.Len()-1]...), small.Bytes()[small.Len()-1]^0xff)},
		{"document.pdf", []byte("%PDF-1.7\nnot for inline preview")},
		{"program.png", []byte("MZ\x00\x00executable")},
		{"too-many-pixels.png", huge.Bytes()},
		// A valid PNG followed by padding exceeds the byte cap independently
		// of dimensions. Normal attachment download must still preserve it.
		{"too-many-bytes.png", append(append([]byte{}, small.Bytes()...), make([]byte, 20<<20)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh := createShare(t, a, owner, "", int64(len(tc.data)))
			if e := exec(a, "UPDATE files SET name=? WHERE id=?", tc.name, sh.Files[0].ID); e != nil {
				t.Fatal(e)
			}
			for offset := 0; offset < len(tc.data); {
				end := min(offset+int(chunkMax), len(tc.data))
				if w := chunk(a, owner, sh.Files[0].ID, int64(offset), string(tc.data[offset:end])); w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
				offset = end
			}
			if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for _, scope := range []string{"shares", "public"} {
				base := "/api/" + scope + "/" + sh.ID + "/files/" + sh.Files[0].ID
				w := call(a, "GET", base+"/preview", "", owner)
				if w.Code != 415 {
					t.Errorf("%s: %d want 415; %s", base, w.Code, w.Body)
				}
				var problem struct {
					Code string `json:"error_code"`
				}
				if e := json.Unmarshal(w.Body.Bytes(), &problem); e != nil || problem.Code != "preview_unsupported" {
					t.Errorf("unsafe preview must have stable preview_unsupported: %v %s", e, w.Body)
				}
				w = call(a, "GET", base, "", owner)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), tc.data) || w.Header().Get("Content-Type") != "application/octet-stream" {
					t.Errorf("attachment must preserve unsupported original: status=%d", w.Code)
				}
			}
		})
	}
}

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func pngBytes(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

func TestBranding(t *testing.T) {
	a, admin := emailAdmin(t)
	status := decodeObject(t, call(a, "GET", "/api/status", "", ""))["branding"].(map[string]any)
	if status["name"] != "" || status["logo"] != false {
		t.Fatal(status)
	}
	for _, bad := range []string{`{"name":"x","color":"red"}`, `{"name":"x","color":"#12345"}`, `{"name":"` + strings.Repeat("n", 41) + `"}`} {
		if w := call(a, "PUT", "/api/admin/branding", bad, admin); w.Code != 400 {
			t.Fatal("accepted", bad, w.Code)
		}
	}
	if w := call(a, "PUT", "/api/admin/branding", `{"name":"Acme <Files>","color":"#3E63DD"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	status = decodeObject(t, call(a, "GET", "/api/status", "", ""))["branding"].(map[string]any)
	if status["name"] != "Acme <Files>" || status["color"] != "#3e63dd" {
		t.Fatal(status)
	}
	// The default look: one of the known looks, kept when a client leaves it out.
	if w := call(a, "PUT", "/api/admin/branding", `{"name":"Acme <Files>","color":"#3E63DD","look":"neon"}`, admin); w.Code != 400 {
		t.Fatal("unknown look accepted", w.Code)
	}
	if w := call(a, "PUT", "/api/admin/branding", `{"name":"Acme <Files>","color":"#3E63DD","look":"aurora"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "PUT", "/api/admin/branding", `{"name":"Acme <Files>","color":"#3E63DD"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if look := decodeObject(t, call(a, "GET", "/api/status", "", ""))["branding"].(map[string]any)["look"]; look != "aurora" {
		t.Fatal("look not kept", look)
	}
	m := a.branded(inviteMail("x@example.com", "mati", "http://example.com/invite/abc", "en"))
	if !strings.Contains(m.subject, "invited you to Acme <Files>") || !strings.Contains(m.body, "ACME &lt;FILES&gt;") || strings.Contains(m.body, "<Files>") {
		t.Fatal(m.subject, m.body)
	}
	upload := func(data []byte, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/admin/branding/logo", bytes.NewReader(data))
		r.Header.Set("X-MagiSend", "1")
		r.Header.Set("Cookie", "magisend_session="+token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, bad := range [][]byte{[]byte("<svg onload=alert(1)>"), pngBytes(2000, 10), append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3)} {
		if w := upload(bad, admin); w.Code != 400 {
			t.Fatal("bad logo accepted", w.Code)
		}
	}
	logo := pngBytes(64, 64)
	if w := upload(logo, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w := call(a, "GET", "/api/branding/logo", "", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), logo) || w.Header().Get("Content-Type") != "image/png" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Code, w.Header())
	}
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), admin)
	bob := login(t, a, "bob")
	if w = call(a, "PUT", "/api/admin/branding", `{"name":"Mine"}`, bob); w.Code != 403 {
		t.Fatal("a user changed the branding", w.Code)
	}
	if w = upload(logo, bob); w.Code != 403 {
		t.Fatal("a user changed the logo", w.Code)
	}
	call(a, "DELETE", "/api/admin/branding/logo", "{}", admin)
	if w = call(a, "GET", "/api/branding/logo", "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

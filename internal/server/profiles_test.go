package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfiles(t *testing.T) {
	a, admin := emailAdmin(t)
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), admin)
	bob := login(t, a, "bob")
	me := func(token string) map[string]any {
		return decodeObject(t, call(a, "GET", "/api/me", "", token))["user"].(map[string]any)
	}
	if u := me(bob); u["display_name"] != "" || u["avatar"] != float64(0) {
		t.Fatal(u)
	}

	// Display name: trimmed, header-safe, at most 60 characters.
	if w := call(a, "PUT", "/api/account/profile", `{"display_name":"`+strings.Repeat("n", 61)+`"}`, bob); w.Code != 400 {
		t.Fatal("long name accepted", w.Code)
	}
	if w := call(a, "PUT", "/api/account/profile", `{"display_name":"  Bob Builder  "}`, bob); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if u := me(bob); u["display_name"] != "Bob Builder" {
		t.Fatal(u)
	}

	upload := func(data []byte, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/account/avatar", bytes.NewReader(data))
		r.Header.Set("X-MagiSend", "1")
		if token != "" {
			r.Header.Set("Cookie", "magisend_session="+token)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := upload(pngBytes(40, 40), ""); w.Code != 401 {
		t.Fatal("anonymous upload", w.Code)
	}
	for _, bad := range [][]byte{[]byte("<svg onload=alert(1)>"), pngBytes(20, 20), append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3)} {
		if w := upload(bad, bob); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_avatar") {
			t.Fatal("bad picture accepted", w.Code, w.Body)
		}
	}
	// A wide, partly transparent PNG becomes a centred square JPEG on white.
	source := image.NewNRGBA(image.Rect(0, 0, 600, 300))
	for x := 150; x < 450; x++ {
		for y := 0; y < 300; y++ {
			source.Set(x, y, color.NRGBA{255, 95, 0, 255})
		}
	}
	var encoded bytes.Buffer
	png.Encode(&encoded, source)
	w := upload(encoded.Bytes(), bob)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	u := me(bob)
	if u["avatar"] == float64(0) {
		t.Fatal("no avatar version", u)
	}
	id := u["id"].(string)
	if w = call(a, "GET", "/api/avatars/"+id, "", ""); w.Code != 401 {
		t.Fatal("picture served without sign-in", w.Code)
	}
	w = call(a, "GET", "/api/avatars/"+id, "", admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Code, w.Header())
	}
	picture, e := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
	if e != nil || picture.Bounds().Dx() != 256 || picture.Bounds().Dy() != 256 {
		t.Fatal("stored picture is not a 256 px square JPEG", e)
	}
	// The crop is the centre, which is orange.
	if r, g, _, _ := picture.At(128, 128).RGBA(); r>>8 < 200 || g>>8 > 140 {
		t.Fatal("centre not kept", r>>8, g>>8)
	}
	// The administrator's account list carries the same fields.
	var users []any
	if e := json.Unmarshal(call(a, "GET", "/api/admin/users", "", admin).Body.Bytes(), &users); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, item := range users {
		user := item.(map[string]any)
		if user["username"] == "bob" {
			found = user["display_name"] == "Bob Builder" && user["avatar"] == u["avatar"]
		}
	}
	if !found {
		t.Fatal("admin list misses the profile", users)
	}
	if w = call(a, "DELETE", "/api/account/avatar", "{}", bob); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if me(bob)["avatar"] != float64(0) {
		t.Fatal("picture not removed")
	}
	if w = call(a, "GET", "/api/avatars/"+id, "", admin); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

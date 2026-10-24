package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func tokenUpload(a *App, token, query string, files map[string]string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("note", "ignored")
	for name, data := range files {
		part, _ := form.CreateFormFile("sharex", name)
		part.Write([]byte(data))
	}
	form.Close()
	r := httptest.NewRequest("POST", "/api/v1/upload"+query, &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestAPITokenUpload(t *testing.T) {
	a, admin := emailAdmin(t)
	w := call(a, "POST", "/api/account/tokens", `{"name":"ShareX"}`, admin)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var created struct {
		Token string   `json:"token"`
		Info  APIToken `json:"info"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Token, "ms_") || strings.Contains(call(a, "GET", "/api/account/tokens", "", admin).Body.String(), created.Token) {
		t.Fatal("token must be shown once and never listed", created)
	}
	if w = tokenUpload(a, "ms_wrong", "", map[string]string{"a.png": "x"}); w.Code != 401 {
		t.Fatal("wrong token accepted", w.Code)
	}
	if w = tokenUpload(a, "", "", map[string]string{"a.png": "x"}); w.Code != 403 {
		t.Fatal("upload without a token or browser header", w.Code)
	}
	w = tokenUpload(a, created.Token, "?days=0&downloads=2&title=Screenshot", map[string]string{"shot.png": "pixels"})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	result := decodeObject(t, w)
	url := result["url"].(string)
	if !strings.HasPrefix(url, "http://example.com/s/") || result["expires"] != float64(0) {
		t.Fatal(result)
	}
	id := strings.TrimPrefix(url, "http://example.com/s/")
	public := decodeObject(t, call(a, "GET", "/api/public/"+id, "", ""))
	file := public["files"].([]any)[0].(map[string]any)
	if public["title"] != "Screenshot" || public["download_limit"] != float64(2) || file["name"] != "shot.png" {
		t.Fatal(public)
	}
	if w = call(a, "GET", "/api/public/"+id+"/files/"+file["id"].(string), "", ""); w.Body.String() != "pixels" {
		t.Fatal(w.Code, w.Body)
	}
	// The token is only an upload key: it does not open the account.
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.Header.Set("Authorization", "Bearer "+created.Token)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Fatal("token opened the account", rec.Code)
	}
	// Several files and repeated names.
	w = tokenUpload(a, created.Token, "?password=secret-share", map[string]string{"a.txt": "1", "b.txt": "2"})
	if w.Code != 201 || decodeObject(t, w)["files"] != float64(2) {
		t.Fatal(w.Code, w.Body)
	}
	// Directory parts of a name are dropped by the multipart reader.
	w = tokenUpload(a, created.Token, "", map[string]string{"../../evil.txt": "x"})
	evil := strings.TrimPrefix(decodeObject(t, w)["url"].(string), "http://example.com/s/")
	if name := decodeObject(t, call(a, "GET", "/api/public/"+evil, "", ""))["files"].([]any)[0].(map[string]any)["name"]; name != "evil.txt" {
		t.Fatal(name)
	}
	if w = tokenUpload(a, created.Token, "", map[string]string{}); w.Code != 400 {
		t.Fatal("upload without files", w.Code)
	}
	if w = tokenUpload(a, created.Token, "?days=999", map[string]string{"a.txt": "x"}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// Quota applies.
	body := fmt.Sprintf(`{"username":"bob","password":%q,"quota":3}`, testPassword)
	call(a, "POST", "/api/admin/users", body, admin)
	bob := login(t, a, "bob")
	var bobToken struct {
		Token string `json:"token"`
	}
	json.Unmarshal(call(a, "POST", "/api/account/tokens", `{"name":"cli"}`, bob).Body.Bytes(), &bobToken)
	if w = tokenUpload(a, bobToken.Token, "", map[string]string{"big.bin": "12345"}); w.Code != 413 {
		t.Fatal("quota not enforced", w.Code, w.Body)
	}
	// Revoked tokens stop working; others' tokens cannot be revoked.
	var list []APIToken
	json.Unmarshal(call(a, "GET", "/api/account/tokens", "", admin).Body.Bytes(), &list)
	if len(list) != 1 || list[0].LastUsed == 0 {
		t.Fatal(list)
	}
	if w = call(a, "DELETE", "/api/account/tokens/"+list[0].ID, "{}", bob); w.Code != 404 {
		t.Fatal("revoked someone else's token", w.Code)
	}
	call(a, "DELETE", "/api/account/tokens/"+list[0].ID, "{}", admin)
	if w = tokenUpload(a, created.Token, "", map[string]string{"a.txt": "x"}); w.Code != 401 {
		t.Fatal("revoked token still works", w.Code)
	}
	// No temporary files are left behind.
	entries, _ := a.blobEntries()
	for _, name := range entries {
		if strings.HasPrefix(name, partialUploadTo) {
			t.Fatal("leftover", name)
		}
	}
}

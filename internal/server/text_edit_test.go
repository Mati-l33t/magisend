package server

import (
	"encoding/json"

	"strings"
	"testing"
)

func textBody(version int, text string) string {
	b, _ := json.Marshal(map[string]any{"version": version, "text": text})
	return string(b)
}
func TestTextVersionsSafetyConflictAndRestore(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	if _, e := a.addUser("sender", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	token := login(t, a, "sender")
	sh := libraryUpload(t, a, token, "", "notes.md", "original")
	file := sh.Files[0].ID
	path := "/api/library/files/" + file + "/text"
	state := folderCall(t, a, token, "GET", path, "", 200)
	if state["text"] != "original" || state["version"] != float64(1) {
		t.Fatal(state)
	}
	folderCall(t, a, token, "PUT", path, textBody(1, "denied"), 403)
	folderCall(t, a, admin, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":true}`, 200)
	folderCall(t, a, token, "PUT", path, textBody(1, "edited"), 200)
	folderCall(t, a, token, "PUT", path, textBody(1, "stale"), 409)
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", token); w.Code != 200 || w.Body.String() != "edited" {
		t.Fatal(w.Code, w.Body)
	}
	history := call(a, "GET", "/api/library/files/"+file+"/versions", "", token)
	var versions []map[string]any
	if history.Code != 200 || json.Unmarshal(history.Body.Bytes(), &versions) != nil || len(versions) != 2 {
		t.Fatal(history.Code, history.Body)
	}
	folderCall(t, a, token, "POST", "/api/library/files/"+file+"/restore", `{"version":2,"restore":1}`, 200)
	state = folderCall(t, a, token, "GET", path, "", 200)
	if state["text"] != "original" || state["version"] != float64(3) {
		t.Fatal(state)
	}
	for _, bad := range []string{"binary\x00data", strings.Repeat("x", textMaxBytes+1)} {
		folderCall(t, a, token, "PUT", path, textBody(3, bad), 415)
	}
	for _, tc := range []struct{ name, data string }{{"document.pdf", "%PDF-1.7"}, {"bad.txt", "bad\x00bytes"}, {"page.html", "<script>alert(1)</script>"}, {"invalid.txt", string([]byte{255, 1, 2})}} {
		f := libraryUpload(t, a, token, "", tc.name, tc.data).Files[0].ID
		folderCall(t, a, token, "GET", "/api/library/files/"+f+"/text", "", 415)
		if tc.name == "document.pdf" {
			folderCall(t, a, token, "PATCH", "/api/library/files/"+f, `{"name":"renamed.txt"}`, 200)
			folderCall(t, a, token, "GET", "/api/library/files/"+f+"/text", "", 415)
		}
	}
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	other := login(t, a, "other")
	for _, suffix := range []string{"/text", "/versions"} {
		folderCall(t, a, other, "GET", "/api/library/files/"+file+suffix, "", 404)
	}
	folderCall(t, a, other, "PUT", path, textBody(3, "stolen"), 404)
	me := folderCall(t, a, token, "GET", "/api/me", "", 200)
	user := me["user"].(map[string]any)
	if user["used"].(float64) < 22 {
		t.Fatal("history not charged", user)
	}
}

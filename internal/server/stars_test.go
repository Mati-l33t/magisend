package server

import (
	"encoding/json"
	"testing"
	"time"
)

func starredFiles(t *testing.T, a *App, token string) map[string]bool {
	t.Helper()
	var files []LibraryFile
	if e := json.Unmarshal(call(a, "GET", "/api/library/files", "", token).Body.Bytes(), &files); e != nil {
		t.Fatal(e)
	}
	result := map[string]bool{}
	for _, f := range files {
		result[f.ID] = f.Starred
	}
	return result
}

func TestStarredFilesArePrivateBookmarks(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	other := login(t, a, "other")
	keep := libraryUpload(t, a, owner, "", "keep.txt", "keep").Files[0].ID
	gone := libraryUpload(t, a, owner, "", "gone.txt", "gone").Files[0].ID
	folderCall(t, a, owner, "PUT", "/api/library/files/"+keep+"/star", `{"starred":true}`, 200)
	folderCall(t, a, owner, "PUT", "/api/library/files/"+keep+"/star", `{"starred":true}`, 200)
	folderCall(t, a, owner, "PUT", "/api/library/files/"+gone+"/star", `{"starred":true}`, 200)
	folderCall(t, a, other, "PUT", "/api/library/files/"+keep+"/star", `{"starred":true}`, 404)
	if s := starredFiles(t, a, owner); !s[keep] || !s[gone] {
		t.Fatal(s)
	}
	folderCall(t, a, owner, "PUT", "/api/library/files/"+gone+"/star", `{"starred":false}`, 200)
	if s := starredFiles(t, a, owner); !s[keep] || s[gone] {
		t.Fatal(s)
	}
	folderCall(t, a, owner, "PUT", "/api/library/files/"+gone+"/star", `{"starred":true}`, 200)
	folderCall(t, a, owner, "DELETE", "/api/library/files/"+gone, `{}`, 200)
	folderCall(t, a, owner, "DELETE", "/api/trash/"+gone, `{}`, 200)
	a.Cleanup(time.Now())
	var n int
	a.db.QueryRow("SELECT COUNT(*) FROM stars WHERE file_id=?", gone).Scan(&n)
	if n != 0 {
		t.Fatal("bookmark outlived its file")
	}
}

func TestOptionalEmailAtSetupAndRegistration(t *testing.T) {
	a := testApp(t)
	if w := call(a, "POST", "/api/setup", `{"setup_code":"TEST-SETUP-CODE","username":"mati","password":"correct horse battery staple","email":"bad\r\nBcc: x@evil.com"}`, ""); w.Code != 400 {
		t.Fatal("invalid email accepted at setup", w.Code)
	}
	if w := call(a, "POST", "/api/setup", `{"setup_code":"TEST-SETUP-CODE","username":"mati","password":"correct horse battery staple","email":"Mati@Example.com"}`, ""); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	admin := login(t, a, "mati")
	var me struct {
		User User `json:"user"`
	}
	json.Unmarshal(call(a, "GET", "/api/me", "", admin).Body.Bytes(), &me)
	if me.User.Email == "" {
		t.Fatal("setup email not stored")
	}
	folderCall(t, a, admin, "PUT", "/api/admin/policy", `{"registration_open":true,"default_quota":1073741824,"max_file":1073741824,"max_share":2147483648,"default_days":7}`, 200)
	if w := call(a, "POST", "/api/register", `{"username":"newbie","password":"correct horse battery staple","email":"nope"}`, ""); w.Code != 400 {
		t.Fatal("invalid email accepted at registration", w.Code)
	}
	if w := call(a, "POST", "/api/register", `{"username":"newbie","password":"correct horse battery staple","email":"newbie@example.com"}`, ""); w.Code != 201 || !json.Valid(w.Body.Bytes()) || decodeObject(t, w)["email"] != "newbie@example.com" {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/register", `{"username":"noemail","password":"correct horse battery staple"}`, ""); w.Code != 201 {
		t.Fatal("email must stay optional", w.Code, w.Body)
	}
}

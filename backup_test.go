package main

import (
	"archive/tar"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitForBackup(t *testing.T, a *App) {
	t.Helper()
	for i := 0; i < 200; i++ {
		a.backup.Lock()
		running := a.backup.running
		a.backup.Unlock()
		if !running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("backup did not finish")
}

func TestBackupsHoldTheDatabaseAndFiles(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	file := libraryUpload(t, a, token, "", "keep.txt", "precious").Files[0].ID
	dir := filepath.Join(t.TempDir(), "elsewhere")
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"relative/path","every":0,"keep":2,"files":true}`, 400)
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":12,"keep":2,"files":true}`, 400)
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":24,"keep":2,"files":true}`, 200)

	folderCall(t, a, token, "POST", "/api/admin/backups", `{}`, 202)
	waitForBackup(t, a)
	var state struct {
		Backups []BackupFile `json:"backups"`
		Error   string       `json:"error"`
		Last    int64        `json:"last"`
	}
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	if len(state.Backups) != 1 || state.Error != "" || state.Last == 0 {
		t.Fatal(state)
	}
	name := state.Backups[0].Name
	archive, e := os.Open(filepath.Join(dir, name))
	if e != nil {
		t.Fatal(e)
	}
	defer archive.Close()
	contents := map[string][]byte{}
	reader := tar.NewReader(archive)
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(reader)
		contents[h.Name] = data
	}
	if string(contents["files/local/"+file]) != "precious" || len(contents["RESTORE.txt"]) == 0 {
		t.Fatal("backup contents", len(contents))
	}
	// The database copy opens and has the file in it.
	copyPath := filepath.Join(t.TempDir(), "copy.sqlite")
	os.WriteFile(copyPath, contents["magisend.sqlite"], 0600)
	db, e := sql.Open("sqlite3", copyPath)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM files WHERE id=?", file).Scan(&n); e != nil || n != 1 {
		t.Fatal("database copy", n, e)
	}

	// Downloadable by admins only; names outside the pattern are refused.
	if w := call(a, "GET", "/api/admin/backups/"+name, "", token); w.Code != 200 || w.Body.Len() < 1000 {
		t.Fatal(w.Code)
	}
	folderCall(t, a, token, "GET", "/api/admin/backups/..%2Fmagisend.sqlite", "", 404)
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, login(t, a, "other"), "GET", "/api/admin/backups/"+name, "", 403)

	// The schedule makes the next one; only the newest two are kept.
	for i := 1; i <= 2; i++ {
		a.dueBackup(time.Now().Add(time.Duration(i*25) * time.Hour))
		waitForBackup(t, a)
	}
	if list := listBackups(dir); len(list) != 2 || list[0].Name == name || list[1].Name == name {
		t.Fatal("kept", list)
	}
	folderCall(t, a, token, "DELETE", "/api/admin/backups/"+listBackups(dir)[0].Name, `{}`, 200)
	if len(listBackups(dir)) != 1 {
		t.Fatal("delete")
	}
}

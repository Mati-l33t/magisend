package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An earlier text version whose data is gone (after a database-only restore)
// does not stop MagiSend from starting.
func TestMissingEarlierVersionStillStarts(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	setupAdmin(t, a)
	token := login(t, a, "mati")
	id := libraryUpload(t, a, token, "", "notes.txt", "now").Files[0].ID
	now := time.Now().Unix()
	if _, e = a.db.Exec("INSERT INTO text_versions(file_id,number,blob,size,name,created,actor) VALUES(?,1,?,4,'notes.txt',?,''),(?,2,?,3,'notes.txt',?,'')", id, randomID(), now, id, id, now); e != nil {
		t.Fatal(e)
	}
	a.db.Close()
	b, e := NewApp(root, "")
	if e != nil {
		t.Fatal("a missing earlier version stopped the start:", e)
	}
	b.db.Close()
}

// Removing missing files never runs on a location that looks like the wrong
// disk, and only library files are offered and removed.
func TestMissingFilesRespectWrongDiskAndLinks(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	for i := range 3 {
		libraryUpload(t, a, token, "", fmt.Sprintf("f%d.txt", i), "data")
	}
	sh := createShare(t, a, token, "", 3)
	chunk(a, token, sh.Files[0].ID, 0, "abc")
	call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", token)
	blobs := filepath.Join(a.root, "blobs")
	entries, _ := os.ReadDir(blobs)
	for _, entry := range entries {
		os.RemoveAll(filepath.Join(blobs, entry.Name()))
	}
	listed := decodeObject(t, call(a, "GET", "/api/admin/storage/missing", "", token))
	if listed["count"] != float64(4) || listed["library"] != float64(3) || len(listed["wrong"].([]any)) != 1 {
		t.Fatal(listed)
	}
	if w := call(a, "DELETE", "/api/admin/storage/missing", `{}`, token); w.Code != 409 {
		t.Fatal("removed files from what looks like the wrong disk", w.Code)
	}
	// With the index back, the location counts as the right one: only the
	// library files go, the link's file stays listed.
	if e := a.writeStorageIndexes(time.Now()); e != nil {
		t.Fatal(e)
	}
	if got := folderCall(t, a, token, "DELETE", "/api/admin/storage/missing", `{}`, 200); got["removed"] != float64(3) {
		t.Fatal(got)
	}
	listed = decodeObject(t, call(a, "GET", "/api/admin/storage/missing", "", token))
	if listed["count"] != float64(1) || listed["library"] != float64(0) {
		t.Fatal(listed)
	}
}

// A new password, a reset or a disabled account ends the pass known
// browsers have past the account-wide login limit, and a device cookie the
// client made up is never taken over.
func TestKnownDevicesEndWithThePassword(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	devices := func() (n int) {
		a.db.QueryRow("SELECT COUNT(*) FROM known_devices").Scan(&n)
		return
	}
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(fmt.Sprintf(`{"username":"mati","password":%q}`, testPassword)))
	r.Header.Set("X-MagiSend", "1")
	chosen := randomID()
	r.AddCookie(&http.Cookie{Name: deviceCookie, Value: chosen})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	var session string
	for _, c := range w.Result().Cookies() {
		if c.Name == deviceCookie && c.Value == chosen {
			t.Fatal("a device value chosen by the client was kept")
		}
		if c.Name == "magisend_session" {
			session = c.Value
		}
	}
	if devices() != 1 || session == "" {
		t.Fatal("no known device after signing in")
	}
	folderCall(t, a, session, "POST", "/api/account/password", fmt.Sprintf(`{"current":%q,"password":"another long password 2"}`, testPassword), 200)
	if devices() != 0 {
		t.Fatal("a known device survived the password change")
	}
}

// A restored database from before installation IDs keeps this
// installation's ID, through the copy in the data folder.
func TestInstallIDSurvivesARestoredDatabase(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	id := a.installID()
	if !validInstallID(id) {
		t.Fatal(id)
	}
	a.db.Exec("UPDATE settings SET install_id=''")
	a.db.Close()
	b, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer b.db.Close()
	if b.installID() != id {
		t.Fatal("the installation ID changed with the database", b.installID(), id)
	}
	var stored string
	b.db.QueryRow("SELECT install_id FROM settings").Scan(&stored)
	if stored != id {
		t.Fatal("the database did not get the ID back", stored, id)
	}
}

// A database restored on a new machine brings the old installation's ID,
// so its backups are listed and pruned there.
func TestRestoredDatabaseBringsItsInstallID(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	old := "magisend-backup-aaaaaaaa-20261001-000000.tar"
	os.WriteFile(filepath.Join(dir, old), []byte("x"), 0600)
	a.db.Exec("UPDATE settings SET install_id='aaaaaaaa'")
	a.db.Close()
	b, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer b.db.Close()
	if b.installID() != "aaaaaaaa" {
		t.Fatal("the restored database's ID was not taken", b.installID())
	}
	if saved, _ := os.ReadFile(filepath.Join(root, "install-id")); strings.TrimSpace(string(saved)) != "aaaaaaaa" {
		t.Fatal("the ID file was not updated", string(saved))
	}
	if list := listBackups(dir, b.installID()); len(list) != 1 || list[0].Name != old {
		t.Fatal("the old installation's backups are not listed", list)
	}
}

// A token upload holds only about what it sent, so the same account can
// upload something else at the same time.
func TestTokenUploadsDoNotHoldTheWholeQuota(t *testing.T) {
	a, admin := emailAdmin(t)
	var created struct {
		Token string `json:"token"`
	}
	json.Unmarshal(call(a, "POST", "/api/account/tokens", `{"name":"cli"}`, admin).Body.Bytes(), &created)
	body, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	r := httptest.NewRequest("POST", "/api/v1/upload", body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+created.Token)
	slow := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		a.Handler().ServeHTTP(slow, r)
		close(finished)
	}()
	part, _ := form.CreateFormFile("file", "slow.bin")
	part.Write(bytes.Repeat([]byte("x"), 1<<20))
	if w := tokenUpload(a, created.Token, "", map[string]string{"quick.txt": "quick"}); w.Code != 201 {
		t.Fatal("a second upload was refused while the first ran", w.Code, w.Body)
	}
	form.Close()
	writer.Close()
	<-finished
	if slow.Code != 201 {
		t.Fatal(slow.Code, slow.Body)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokenUploads.total != 0 || len(a.tokenUploads.user) != 0 {
		t.Fatal("reservations left behind", a.tokenUploads.total, a.tokenUploads.user)
	}
}

// An archive from before installation IDs counts as this installation's
// own: listed, and pruned like the others.
func TestLegacyBackupsArePrunedAsOwn(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	dir := filepath.Join(t.TempDir(), "backups")
	os.MkdirAll(dir, 0700)
	legacy := filepath.Join(dir, "magisend-backup-20250101-000000.tar")
	os.WriteFile(legacy, []byte("old"), 0600)
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":0,"keep":1,"files":false}`, 200)
	folderCall(t, a, token, "POST", "/api/admin/backups", `{}`, 202)
	waitForBackup(t, a)
	if _, e := os.Stat(legacy); e == nil {
		t.Fatal("an archive from before installation IDs was never pruned")
	}
}

// A run the watchdog gave up stops at its next step and no longer moves the
// progress the next run shows.
func TestAbandonedBackupRunStops(t *testing.T) {
	a := testApp(t)
	a.backup.Lock()
	a.backup.running, a.backup.generation = true, 7
	a.backup.Unlock()
	if a.backupProgress(6, 100) {
		t.Fatal("an abandoned run went on")
	}
	if !a.backupProgress(7, 100) || a.backup.done != 100 {
		t.Fatal("the current run was stopped")
	}
	a.stopping.Store(true)
	if a.backupProgress(7, 1) {
		t.Fatal("a backup went on during shutdown")
	}
}

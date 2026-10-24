package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A new password ends upload tokens as well as sessions.
func TestPasswordChangesRevokeUploadTokens(t *testing.T) {
	a, admin := emailAdmin(t)
	token := func(session string) string {
		return decodeObject(t, call(a, "POST", "/api/account/tokens", `{"name":"ShareX"}`, session))["token"].(string)
	}
	upload := func(token string) int { return tokenUpload(a, token, "", map[string]string{"a.txt": "x"}).Code }
	mine := token(admin)
	if upload(mine) != 201 {
		t.Fatal("token does not work")
	}
	if w := call(a, "POST", "/api/account/password", `{"current":"`+testPassword+`","password":"a different long passphrase"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if upload(mine) != 401 {
		t.Fatal("a token survived changing the password")
	}
	bob, e := a.addUser("bob", testPassword, "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	bobs := token(login(t, a, "bob"))
	if w := call(a, "PATCH", "/api/admin/users/"+bob.ID, fmt.Sprintf(`{"password":%q}`, "reset by the admin here"), admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if upload(bobs) != 401 {
		t.Fatal("a token survived an administrator's reset")
	}
}

// Reading a file's text is a download, so limited links do not offer it.
func TestTextIsNotReadableOnLimitedLinks(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	sh := limitedShare(t, a, owner, 1)
	path := "/api/public/" + sh.ID + "/files/" + sh.Files[0].ID + "/text"
	for range 3 {
		if w := fetch(a, "GET", path, ""); w.Code != 403 {
			t.Fatal("text of a limited link was readable", w.Code)
		}
	}
}

func TestSlotsGroupIPv6ByHousehold(t *testing.T) {
	a := testApp(t)
	keys := map[string]bool{}
	for i := 0; i < 256; i += 37 {
		r := fetchRequest(fmt.Sprintf("[2001:db8:1:%x::1]:443", i))
		keys[slotClient(a.clientIP(r))] = true
	}
	if len(keys) != 1 {
		t.Fatal("one /56 counted as several clients", keys)
	}
	if got := slotClient("198.51.100.1"); got != "198.51.100.1" {
		t.Fatal(got)
	}
	if got := slotClient(a.clientIP(fetchRequest("[2001:db8:2::1]:443"))); !strings.HasSuffix(got, "/56") || keys[got] {
		t.Fatal("another household shares the key", got)
	}
}

// Restoring an older version never takes the place of the newest one, even
// right after the same person edited it.
func TestRestoreKeepsTheNewestVersion(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":true}`, 200)
	file := libraryUpload(t, a, token, "", "draft.txt", "first").Files[0].ID
	folderCall(t, a, token, "PUT", "/api/library/files/"+file+"/text", textBody(1, "second"), 200)
	folderCall(t, a, token, "PUT", "/api/library/files/"+file+"/text", textBody(2, "newest"), 200)
	folderCall(t, a, token, "POST", "/api/library/files/"+file+"/restore", `{"version":3,"restore":1}`, 200)
	rows, e := a.db.Query("SELECT blob FROM text_versions WHERE file_id=? ORDER BY number", file)
	if e != nil {
		t.Fatal(e)
	}
	var texts []string
	for rows.Next() {
		var blob string
		rows.Scan(&blob)
		data, _ := os.ReadFile(a.blob(blob))
		texts = append(texts, string(data))
	}
	rows.Close()
	if !slices.Contains(texts, "newest") || texts[len(texts)-1] != "first" {
		t.Fatal("history after restore", texts)
	}
}

// An older database restored after files were deleted still starts, and the
// administrator can see and remove what is missing. An empty folder that
// looks unmounted is still refused.
func TestRestoredDatabaseWithMissingFilesStarts(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	setupAdmin(t, a)
	token := login(t, a, "mati")
	kept := libraryUpload(t, a, token, "", "kept.txt", "still here").Files[0].ID
	gone := libraryUpload(t, a, token, "", "gone.txt", "deleted later").Files[0].ID
	older := filepath.Join(t.TempDir(), "older.sqlite")
	if _, e = a.db.Exec("VACUUM INTO ?", older); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, token, "DELETE", "/api/library/files/"+gone, `{}`, 200)
	folderCall(t, a, token, "DELETE", "/api/trash/"+gone, `{}`, 200)
	a.db.Close()
	restore := func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(filepath.Join(root, "magisend.sqlite"+suffix))
		}
		data, _ := os.ReadFile(older)
		os.WriteFile(filepath.Join(root, "magisend.sqlite"), data, 0600)
	}
	restore()
	if a, e = NewApp(root, ""); e != nil {
		t.Fatal("a restored backup with one deleted file does not start:", e)
	}
	token = login(t, a, "mati")
	listed := decodeObject(t, call(a, "GET", "/api/admin/storage/missing", "", token))
	files := listed["files"].([]any)
	if listed["count"] != float64(1) || files[0].(map[string]any)["name"] != "gone.txt" {
		t.Fatal(listed)
	}
	if got := folderCall(t, a, token, "DELETE", "/api/admin/storage/missing", `{}`, 200); got["removed"] != float64(1) {
		t.Fatal(got)
	}
	if w := call(a, "GET", "/api/library/files/"+kept+"/download", "", token); w.Body.String() != "still here" {
		t.Fatal(w.Code, w.Body)
	}
	if decodeObject(t, call(a, "GET", "/api/admin/storage/missing", "", token))["count"] != float64(0) {
		t.Fatal("still missing")
	}
	a.db.Close()

	// A data folder emptied of everything, index included, is refused.
	other := t.TempDir()
	b, e := NewApp(other, "")
	if e != nil {
		t.Fatal(e)
	}
	setupAdmin(t, b)
	owner := login(t, b, "mati")
	for i := range 3 {
		libraryUpload(t, b, owner, "", fmt.Sprintf("f%d.txt", i), "x")
	}
	b.Cleanup(time.Now())
	b.db.Close()
	entries, _ := os.ReadDir(filepath.Join(other, "blobs"))
	for _, entry := range entries {
		os.RemoveAll(filepath.Join(other, "blobs", entry.Name()))
	}
	if c, e := NewApp(other, ""); e == nil {
		c.db.Close()
		t.Fatal("an emptied storage folder was accepted")
	}
}

// A file moved to another location and back while a backup runs keeps its
// data when the deletions held for the backup are made.
func TestMoveAndBackDuringBackupKeepsTheFile(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	file := libraryUpload(t, a, token, "", "travel.txt", "there and back").Files[0].ID
	nas := addStorage(t, a, token, t.TempDir())
	a.backup.Lock()
	a.backup.running = true
	a.backup.Unlock()
	if e := a.moveFile(file, nas); e != nil {
		t.Fatal(e)
	}
	if e := a.moveFile(file, localLocation); e != nil {
		t.Fatal(e)
	}
	a.finishBackup()
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", token); w.Code != 200 || w.Body.String() != "there and back" {
		t.Fatal("the file lost its data", w.Code, w.Body)
	}
	nasDir, _ := a.blobDir(nas)
	if _, e := os.Stat(filepath.Join(nasDir, file)); !os.IsNotExist(e) {
		t.Fatal("the copy left on the other location was kept", e)
	}
}

// A token upload past the quota is refused before anything is written, and
// reservations are given back.
func TestTokenUploadReservesBeforeWriting(t *testing.T) {
	a, _ := emailAdmin(t)
	_, e := a.addUser("small", testPassword, "user", 4)
	if e != nil {
		t.Fatal(e)
	}
	token := decodeObject(t, call(a, "POST", "/api/account/tokens", `{"name":"x"}`, login(t, a, "small")))["token"].(string)
	if w := tokenUpload(a, token, "", map[string]string{"big.txt": "more than four bytes"}); w.Code != 413 && w.Code != 507 {
		t.Fatal("over quota accepted", w.Code, w.Body)
	}
	if w := tokenUpload(a, token, "", map[string]string{"ok.txt": "fit"}); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if a.tokenUploads.total != 0 || len(a.tokenUploads.user) != 0 {
		t.Fatal("a reservation was not given back", a.tokenUploads.total, a.tokenUploads.user)
	}
	entries, _ := os.ReadDir(filepath.Join(a.root, "blobs"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), partialUploadTo) {
			t.Fatal("a refused upload left a file", entry.Name())
		}
	}
}

// The files a ZIP is being unpacked into cannot be written, discarded or
// listed as an unfinished upload until the extraction is done.
func TestExtractionFilesAreGuarded(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	sh := createShare(t, a, token, "", 5)
	a.mu.Lock()
	a.extracting[sh.ID] = true
	a.mu.Unlock()
	if w := chunk(a, token, sh.Files[0].ID, 0, "abc"); w.Code != 404 {
		t.Fatal("a chunk was written into an extraction", w.Code)
	}
	if w := call(a, "DELETE", "/api/shares/"+sh.ID, `{}`, token); w.Code != 409 {
		t.Fatal("an extraction was discarded", w.Code)
	}
	a.mu.Lock()
	delete(a.extracting, sh.ID)
	a.mu.Unlock()
	if w := chunk(a, token, sh.Files[0].ID, 0, "abc"); w.Code != 200 {
		t.Fatal("ordinary uploads stopped working", w.Code, w.Body)
	}
}

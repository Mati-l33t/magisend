package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type unclaimedState struct {
	Count int             `json:"count"`
	Size  int64           `json:"size"`
	Files []UnclaimedFile `json:"files"`
}

func unclaimedNow(t *testing.T, a *App, token string) unclaimedState {
	t.Helper()
	var state unclaimedState
	w := call(a, "GET", "/api/admin/storage/unclaimed", "", token)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil {
		t.Fatal(w.Code, w.Body)
	}
	return state
}

// Restoring an older database leaves newer files unknown. They are set aside,
// not deleted, and come back with their names, owner and folder.
func TestFilesUnknownAfterARestoreComeBack(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	if _, e = a.addUser("anna", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	anna := login(t, a, "anna")
	libraryUpload(t, a, anna, "", "old.txt", "before the backup")
	older := filepath.Join(t.TempDir(), "older.sqlite")
	if _, e = a.db.Exec("VACUUM INTO ?", older); e != nil {
		t.Fatal(e)
	}
	work := folderCall(t, a, anna, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	plans := folderCall(t, a, anna, "POST", "/api/folders", `{"name":"Plans","parent_id":"`+work+`"}`, 201)["id"].(string)
	newer := libraryUpload(t, a, anna, plans, "after.txt", "made after the backup").Files[0].ID
	if e = a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	a.db.Close()

	// The older database comes back.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(root, "magisend.sqlite"+suffix))
	}
	data, _ := os.ReadFile(older)
	if e = os.WriteFile(filepath.Join(root, "magisend.sqlite"), data, 0600); e != nil {
		t.Fatal(e)
	}
	if a, e = NewApp(root, ""); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.db.Close() })
	admin = login(t, a, "mati")
	if _, e = os.Stat(filepath.Join(root, "blobs", unclaimedDir, newer)); e != nil {
		t.Fatal("the newer file was not set aside", e)
	}
	state := unclaimedNow(t, a, admin)
	if state.Count != 1 || !state.Files[0].Known || state.Files[0].Name != "after.txt" || state.Files[0].Owner != "anna" || state.Files[0].Folder != "Work/Plans" {
		t.Fatal(state)
	}
	// Ordinary users cannot see or restore them.
	anna = login(t, a, "anna")
	folderCall(t, a, anna, "GET", "/api/admin/storage/unclaimed", "", 403)

	got := folderCall(t, a, admin, "POST", "/api/admin/storage/unclaimed/restore", `{}`, 200)
	if got["restored"] != float64(1) || got["failed"] != float64(0) {
		t.Fatal(got)
	}
	var files []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", anna).Body.Bytes(), &files)
	var restored *LibraryFile
	for i := range files {
		if files[i].Name == "after.txt" {
			restored = &files[i]
		}
	}
	if restored == nil {
		t.Fatal("not back in anna's library", files)
	}
	var folderName, parentName string
	a.db.QueryRow("SELECT f.name,p.name FROM folders f JOIN folders p ON p.id=f.parent WHERE f.id=?", restored.FolderID).Scan(&folderName, &parentName)
	if folderName != "Plans" || parentName != "Work" {
		t.Fatal("not back in its folder", parentName, folderName)
	}
	if w := call(a, "GET", "/api/library/files/"+restored.ID+"/download", "", anna); w.Body.String() != "made after the backup" {
		t.Fatal(w.Code, w.Body)
	}
	if unclaimedNow(t, a, admin).Count != 0 {
		t.Fatal("still listed")
	}
}

// A file nothing knows about goes to the administrator's Recovered files.
func TestUnknownFileWithoutARecord(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	stray := randomID()
	if e := os.WriteFile(filepath.Join(a.root, "blobs", stray), []byte("\x89PNG\r\n\x1a\n0000"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	state := unclaimedNow(t, a, admin)
	if state.Count != 1 || state.Files[0].Known || state.Files[0].ID != stray {
		t.Fatal(state)
	}
	folderCall(t, a, admin, "POST", "/api/admin/storage/unclaimed/restore", `{}`, 200)
	var files []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", admin).Body.Bytes(), &files)
	if len(files) != 1 || !strings.HasPrefix(files[0].Name, "Recovered ") || !strings.HasSuffix(files[0].Name, ".png") {
		t.Fatal(files)
	}
	var folder string
	a.db.QueryRow("SELECT name FROM folders WHERE id=?", files[0].FolderID).Scan(&folder)
	if folder != recoveredFolder {
		t.Fatal("folder", folder)
	}
}

// Set-aside files go after 30 days, or at once when the administrator says so.
func TestUnclaimedFilesExpireOrAreDeleted(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	for range 2 {
		os.WriteFile(filepath.Join(a.root, "blobs", randomID()), []byte("x"), 0600)
	}
	a.Cleanup(time.Now())
	if unclaimedNow(t, a, admin).Count != 2 {
		t.Fatal("not set aside")
	}
	a.mu.Lock()
	a.expireUnclaimed(time.Now().Add(29 * 24 * time.Hour))
	a.mu.Unlock()
	if unclaimedNow(t, a, admin).Count != 2 {
		t.Fatal("expired too early")
	}
	a.mu.Lock()
	a.expireUnclaimed(time.Now().Add(31 * 24 * time.Hour))
	a.mu.Unlock()
	if unclaimedNow(t, a, admin).Count != 0 {
		t.Fatal("not expired")
	}
	os.WriteFile(filepath.Join(a.root, "blobs", randomID()), []byte("x"), 0600)
	a.Cleanup(time.Now())
	if got := folderCall(t, a, admin, "DELETE", "/api/admin/storage/unclaimed", `{}`, 200); got["deleted"] != float64(1) {
		t.Fatal(got)
	}
	if entries, _ := os.ReadDir(filepath.Join(a.root, "blobs", unclaimedDir)); len(entries) != 0 {
		t.Fatal("left", len(entries))
	}
}

// A copy is only deleted as a leftover when the location the database names
// is reachable and really holds the file; with that location offline, the
// copy here may be the only one and is set aside.
func TestCopyIsOnlyDeletedWhenTheOtherIsVerified(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	share := t.TempDir()
	nas := addStorage(t, a, admin, share)
	file := libraryUpload(t, a, admin, "", "moved.txt", "only copy?").Files[0].ID
	// The database says the file is on the NAS, but the bytes are only here.
	if _, e := a.db.Exec("UPDATE files SET backend=? WHERE id=?", nas, file); e != nil {
		t.Fatal(e)
	}
	a.stores.mu.Lock()
	a.stores.locations[nas].online = false
	a.stores.mu.Unlock()
	a.Cleanup(time.Now())
	if _, e := os.Stat(filepath.Join(a.root, "blobs", unclaimedDir, file)); e != nil {
		t.Fatal("the possibly only copy was not set aside", e)
	}
	// Back online without the file there: still kept aside.
	a.stores.mu.Lock()
	a.stores.locations[nas].online = true
	a.stores.mu.Unlock()
	a.Cleanup(time.Now())
	if _, e := os.Stat(filepath.Join(a.root, "blobs", unclaimedDir, file)); e != nil {
		t.Fatal("set-aside copy lost", e)
	}
	// A true leftover: the NAS holds the file at its size, so the local copy goes.
	other := libraryUpload(t, a, admin, "", "leftover.txt", "twice").Files[0].ID
	data, _ := os.ReadFile(filepath.Join(a.root, "blobs", other))
	os.WriteFile(filepath.Join(share, storageDirName, other), data, 0600)
	a.db.Exec("UPDATE files SET backend=? WHERE id=?", nas, other)
	a.Cleanup(time.Now())
	if _, e := os.Stat(filepath.Join(a.root, "blobs", other)); !os.IsNotExist(e) {
		t.Fatal("leftover copy kept", e)
	}
	if _, e := os.Stat(filepath.Join(a.root, "blobs", unclaimedDir, other)); !os.IsNotExist(e) {
		t.Fatal("leftover copy set aside", e)
	}
	// A copy of the wrong size elsewhere does not count.
	third := libraryUpload(t, a, admin, "", "third.txt", "the real one").Files[0].ID
	os.WriteFile(filepath.Join(share, storageDirName, third), []byte("short"), 0600)
	a.db.Exec("UPDATE files SET backend=? WHERE id=?", nas, third)
	a.Cleanup(time.Now())
	if _, e := os.Stat(filepath.Join(a.root, "blobs", unclaimedDir, third)); e != nil {
		t.Fatal("copy deleted although the other had the wrong size", e)
	}
}

// Files go back to the account that owned them, never to someone who has the
// name now.
func TestRestoredFilesFollowTheAccountNotTheName(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	gone := randomID()
	stray := randomID()
	os.WriteFile(filepath.Join(a.root, "blobs", stray), []byte("private"), 0600)
	index := storageIndex{Files: map[string]indexEntry{stray: {Name: "diary.txt", Owner: "anna", OwnerID: gone, Folder: "Private", Size: 7}}}
	data, _ := json.Marshal(index)
	os.WriteFile(filepath.Join(a.root, "blobs", storageIndexFile), data, 0600)
	if _, e := a.addUser("anna", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	a.Cleanup(time.Now())
	folderCall(t, a, admin, "POST", "/api/admin/storage/unclaimed/restore", `{}`, 200)
	var annas []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", login(t, a, "anna")).Body.Bytes(), &annas)
	if len(annas) != 0 {
		t.Fatal("the new anna got the old anna's file", annas)
	}
	var mine []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", admin).Body.Bytes(), &mine)
	if len(mine) != 1 || mine[0].Name != "diary.txt" {
		t.Fatal(mine)
	}
	var folder, parent, top string
	a.db.QueryRow("SELECT f.name,p.name,g.name FROM folders f JOIN folders p ON p.id=f.parent JOIN folders g ON g.id=p.parent WHERE f.id=?", mine[0].FolderID).Scan(&folder, &parent, &top)
	if top != recoveredFolder || parent != "anna" || folder != "Private" {
		t.Fatal("folder", top, parent, folder)
	}
}

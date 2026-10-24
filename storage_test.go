package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func addStorage(t *testing.T, a *App, admin, path string) string {
	t.Helper()
	w := call(a, "POST", "/api/admin/storage/locations", `{"path":"`+path+`","language":"en"}`, admin)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var b struct{ ID string }
	if e := json.Unmarshal(w.Body.Bytes(), &b); e != nil || b.ID == "" {
		t.Fatal(e, w.Body)
	}
	return b.ID
}

func waitForMove(t *testing.T, a *App) moveStatus {
	t.Helper()
	for range 200 {
		a.stores.mu.RLock()
		m := a.stores.move
		a.stores.mu.RUnlock()
		if !m.Running {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("move did not finish")
	return moveStatus{}
}

func TestStorageLocationHoldsNewFiles(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	share := t.TempDir()
	id := addStorage(t, a, admin, share)
	if w := call(a, "PUT", "/api/admin/storage/active", `{"id":"`+id+`"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	sh := libraryUpload(t, a, admin, "", "remote.txt", "on the share")
	file := sh.Files[0].ID
	data, e := os.ReadFile(filepath.Join(share, storageDirName, file))
	if e != nil || string(data) != "on the share" {
		t.Fatal("file not stored on the share", e, string(data))
	}
	if _, e = os.Stat(a.blob(file)); !os.IsNotExist(e) {
		t.Fatal("file also written to the data folder", e)
	}
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", admin); w.Code != 200 || w.Body.String() != "on the share" {
		t.Fatal(w.Code, w.Body)
	}
	// Text versions live next to the file.
	if w := call(a, "GET", "/api/admin/storage", "", admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"backend":"`+id+`"`) {
		t.Fatal(w.Code, w.Body)
	}
}

func TestOfflineStorageKeepsTheAppWorking(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	share := t.TempDir()
	id := addStorage(t, a, admin, share)
	call(a, "PUT", "/api/admin/storage/active", `{"id":"`+id+`"}`, admin)
	sh := libraryUpload(t, a, admin, "", "away.txt", "comes back")
	file := sh.Files[0].ID
	gone := libraryUpload(t, a, admin, "", "gone.txt", "deleted").Files[0].ID

	// Unmounting leaves an empty mount point without the files folder.
	away := filepath.Join(t.TempDir(), "away")
	if e := os.Rename(filepath.Join(share, storageDirName), away); e != nil {
		t.Fatal(e)
	}
	a.checkStorage(false)
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", admin); w.Code != 503 || !strings.Contains(w.Body.String(), "storage_offline") {
		t.Fatal("download from offline storage", w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/library/files", "", admin); w.Code != 200 || !strings.Contains(w.Body.String(), "away.txt") {
		t.Fatal("library listing needs the share", w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/library/uploads", `{"files":[{"name":"new.txt","size":3}]}`, admin); w.Code != 503 || !strings.Contains(w.Body.String(), "storage_offline") {
		t.Fatal("upload to offline storage", w.Code, w.Body)
	}
	if _, e := os.Stat(filepath.Join(share, storageDirName)); !os.IsNotExist(e) {
		t.Fatal("the empty mount point was written to", e)
	}
	// Deleting is recorded at once and finished when the share is back.
	if w := call(a, "DELETE", "/api/library/files/"+gone, "", admin); w.Code != 200 {
		t.Fatal("delete on offline storage", w.Code, w.Body)
	}
	if w := call(a, "DELETE", "/api/trash/"+gone, "", admin); w.Code != 200 {
		t.Fatal("delete for good on offline storage", w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/library/files", "", admin); strings.Contains(w.Body.String(), "gone.txt") {
		t.Fatal("deleted file still listed")
	}
	if e := a.Cleanup(time.Now()); e != nil && !strings.Contains(e.Error(), "offline") {
		t.Fatal(e)
	}
	// A restart while the share is away still starts.
	b, e := NewApp(a.root, "")
	if e != nil {
		t.Fatal("start with offline storage", e)
	}
	b.db.Close()

	if e := os.Rename(away, filepath.Join(share, storageDirName)); e != nil {
		t.Fatal(e)
	}
	a.checkStorage(false)
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", admin); w.Code != 200 || w.Body.String() != "comes back" {
		t.Fatal("download after the share returned", w.Code, w.Body)
	}
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(share, storageDirName, gone)); !os.IsNotExist(e) {
		t.Fatal("deleted file kept on the share", e)
	}
}

func TestStorageGoesOfflineAndBackByMail(t *testing.T) {
	a, admin := emailAdmin(t)
	configureEmail(t, a, admin)
	if w := call(a, "PUT", "/api/account/email", `{"email":"admin@example.com"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	share := t.TempDir()
	addStorage(t, a, admin, share)
	away := filepath.Join(t.TempDir(), "away")
	os.Rename(filepath.Join(share, storageDirName), away)
	a.checkStorage(true)
	if len(queued(a)) != 0 {
		t.Fatal("mailed before the share was away for a minute")
	}
	a.stores.mu.Lock()
	for _, l := range a.stores.locations {
		l.changed = time.Now().Add(-2 * storageNotifyAfter)
	}
	a.stores.mu.Unlock()
	a.checkStorage(true)
	mails := queued(a)
	if len(mails) != 1 || !strings.Contains(mails[0].subject, "offline") {
		t.Fatal(mails)
	}
	os.Rename(away, filepath.Join(share, storageDirName))
	a.checkStorage(true)
	if mails = queued(a); len(mails) != 1 || !strings.Contains(mails[0].subject, "back online") {
		t.Fatal(mails)
	}
}

func TestMoveFilesBetweenLocations(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	sh := libraryUpload(t, a, admin, "", "moving.txt", "moved intact")
	file := sh.Files[0].ID
	share := t.TempDir()
	id := addStorage(t, a, admin, share)
	if w := call(a, "POST", "/api/admin/storage/move", `{"to":"`+id+`"}`, admin); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	if m := waitForMove(t, a); m.Done != 1 || m.Failed != 0 || m.Error != "" {
		t.Fatalf("%+v", m)
	}
	if loc, _ := a.fileLocation(file); loc != id {
		t.Fatal("file not switched", loc)
	}
	if _, e := os.Stat(a.blob(file)); !os.IsNotExist(e) {
		t.Fatal("old copy left behind", e)
	}
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", admin); w.Body.String() != "moved intact" {
		t.Fatal(w.Code, w.Body)
	}
	// A location that holds files cannot be removed; once empty it can.
	if w := call(a, "DELETE", "/api/admin/storage/locations/"+id, "", admin); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	call(a, "POST", "/api/admin/storage/move", `{"to":"local"}`, admin)
	waitForMove(t, a)
	if data, e := os.ReadFile(a.blob(file)); e != nil || string(data) != "moved intact" {
		t.Fatal(e, string(data))
	}
	if w := call(a, "DELETE", "/api/admin/storage/locations/"+id, "", admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, e := os.Stat(filepath.Join(share, storageDirName, storageMarker)); !os.IsNotExist(e) {
		t.Fatal("marker left after removal", e)
	}
	// Removed, the folder can be added again.
	addStorage(t, a, admin, share)
}

func TestCleanupCollectsCopiesLeftOnAnotherLocation(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	sh := libraryUpload(t, a, admin, "", "left.txt", "x")
	share := t.TempDir()
	addStorage(t, a, admin, share)
	// A copy of the same object on a location the file does not use.
	stray := filepath.Join(share, storageDirName, sh.Files[0].ID)
	if e := os.WriteFile(stray, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(stray); !os.IsNotExist(e) {
		t.Fatal("stray copy kept", e)
	}
	if _, e := os.Stat(a.blob(sh.Files[0].ID)); e != nil {
		t.Fatal("the file's own copy was removed", e)
	}
}

func TestStorageFolderChecks(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	used := t.TempDir()
	os.MkdirAll(filepath.Join(used, storageDirName), 0700)
	os.WriteFile(filepath.Join(used, storageDirName, "something"), []byte("x"), 0600)
	for path, code := range map[string]string{
		"relative/path":                "storage_path_invalid",
		"/does/not/exist":              "storage_path_missing",
		"/etc":                         "storage_path_invalid",
		a.root:                         "storage_path_invalid",
		filepath.Join(a.root, "blobs"): "storage_path_invalid",
		filepath.Dir(a.root):           "storage_path_invalid",
		used:                           "storage_in_use",
	} {
		w := call(a, "POST", "/api/admin/storage/locations", `{"path":"`+path+`"}`, admin)
		if w.Code != 400 || !strings.Contains(w.Body.String(), code) {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	share := t.TempDir()
	addStorage(t, a, admin, share)
	if w := call(a, "POST", "/api/admin/storage/locations", `{"path":"`+share+`"}`, admin); w.Code != 400 || !strings.Contains(w.Body.String(), "storage_exists") {
		t.Fatal(w.Code, w.Body)
	}
	user := "alex"
	call(a, "POST", "/api/admin/users", `{"username":"`+user+`","password":"correct horse battery staple","quota":1073741824}`, admin)
	if w := call(a, "POST", "/api/admin/storage/locations", `{"path":"`+t.TempDir()+`"}`, login(t, a, user)); w.Code != 403 {
		t.Fatal("non-admin added storage", w.Code)
	}
}

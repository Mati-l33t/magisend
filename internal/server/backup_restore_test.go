package server

import (
	"archive/tar"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readBackup returns the newest backup's files by name.
func readBackup(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	var newest string
	var newestTime int64
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		info, e := entry.Info()
		if e == nil && backupName.MatchString(entry.Name()) && info.ModTime().UnixNano() >= newestTime {
			newest, newestTime = entry.Name(), info.ModTime().UnixNano()
		}
	}
	if newest == "" {
		t.Fatal("no backup was made")
	}
	archive, e := os.Open(filepath.Join(dir, newest))
	if e != nil {
		t.Fatal(e)
	}
	defer archive.Close()
	contents := map[string][]byte{}
	reader := tar.NewReader(archive)
	for {
		h, e := reader.Next()
		if e == io.EOF {
			return contents
		}
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(reader)
		contents[h.Name] = data
	}
}

func backUp(t *testing.T, a *App, token, dir string, storage ...bool) {
	t.Helper()
	settings := `{"dir":"` + dir + `","every":0,"keep":3,"files":true,"storage":false}`
	if len(storage) > 0 && storage[0] {
		settings = `{"dir":"` + dir + `","every":0,"keep":3,"files":true,"storage":true}`
	}
	folderCall(t, a, token, "PUT", "/api/admin/backups", settings, 200)
	folderCall(t, a, token, "POST", "/api/admin/backups", `{}`, 202)
	waitForBackup(t, a)
}

// A backup restored the way RESTORE.txt describes starts and serves the files.
func TestBackupRestoresIntoAWorkingApp(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	first := libraryUpload(t, a, token, "", "first.txt", "one").Files[0].ID
	second := libraryUpload(t, a, token, "", "second.txt", "two").Files[0].ID
	dir := filepath.Join(t.TempDir(), "backups")
	backUp(t, a, token, dir)
	contents := readBackup(t, dir)
	if _, incomplete := contents["MISSING.txt"]; incomplete {
		t.Fatal("a complete backup listed missing files")
	}

	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "blobs"), 0700); e != nil {
		t.Fatal(e)
	}
	for name, data := range contents {
		target := ""
		switch {
		case name == "magisend.sqlite":
			target = filepath.Join(root, name)
		case strings.HasPrefix(name, "files/local/"):
			target = filepath.Join(root, "blobs", strings.TrimPrefix(name, "files/local/"))
		default:
			continue
		}
		if e := os.WriteFile(target, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	restored, e := NewApp(root, "")
	if e != nil {
		t.Fatal("the restored backup does not start:", e)
	}
	t.Cleanup(func() { restored.db.Close() })
	again := login(t, restored, "mati")
	for id, want := range map[string]string{first: "one", second: "two"} {
		if w := call(restored, "GET", "/api/library/files/"+id+"/download", "", again); w.Code != 200 || w.Body.String() != want {
			t.Fatal("restored file", id, w.Code, w.Body)
		}
	}
}

// A file deleted while a backup runs stays until the backup is done, so the
// database copy and the archived files match.
func TestDeletionsWaitForARunningBackup(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	file := libraryUpload(t, a, token, "", "gone.txt", "bye").Files[0].ID
	blob := filepath.Join(a.root, "blobs", file)
	a.backup.Lock()
	a.backup.running = true
	a.backup.Unlock()
	folderCall(t, a, token, "DELETE", "/api/library/files/"+file, `{}`, 200)
	folderCall(t, a, token, "DELETE", "/api/trash/"+file, `{}`, 200)
	if _, e := os.Stat(blob); e != nil {
		t.Fatal("the file was removed during the backup", e)
	}
	a.finishBackup()
	if _, e := os.Stat(blob); !os.IsNotExist(e) {
		t.Fatal("the held file was not removed afterwards", e)
	}
}

// A storage location that is offline makes the backup say it is incomplete.
func TestIncompleteBackupSaysWhatIsMissing(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	libraryUpload(t, a, token, "", "here.txt", "local")
	share := t.TempDir()
	id := addStorage(t, a, token, share)
	a.stores.mu.Lock()
	a.stores.locations[id].online = false
	a.stores.mu.Unlock()
	dir := filepath.Join(t.TempDir(), "backups")
	backUp(t, a, token, dir, true)
	var state struct {
		Error   string `json:"error"`
		Warning string `json:"warning"`
	}
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	if state.Error != "" || !strings.Contains(state.Warning, share) {
		t.Fatal("the backup did not say a location was missing", state)
	}
	contents := readBackup(t, dir)
	if !strings.Contains(string(contents["MISSING.txt"]), share) {
		t.Fatal("MISSING.txt", string(contents["MISSING.txt"]))
	}
	// The next complete backup clears the warning.
	a.stores.mu.Lock()
	a.stores.locations[id].online = true
	a.stores.mu.Unlock()
	backUp(t, a, token, dir, true)
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	if state.Warning != "" {
		t.Fatal("warning kept", state.Warning)
	}
}

// By default only the data folder's files are backed up; files on added
// storage locations only when asked, and the page shows how large each is.
func TestBackupCanLeaveOutStorageLocations(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	local := libraryUpload(t, a, token, "", "local.txt", "on the data disk").Files[0].ID
	share := t.TempDir()
	id := addStorage(t, a, token, share)
	folderCall(t, a, token, "PUT", "/api/admin/storage/active", `{"id":"`+id+`"}`, 200)
	remote := libraryUpload(t, a, token, "", "nas.txt", "on the nas").Files[0].ID
	dir := filepath.Join(t.TempDir(), "backups")

	backUp(t, a, token, dir)
	contents := readBackup(t, dir)
	if _, ok := contents["files/local/"+local]; !ok {
		t.Fatal("local file missing")
	}
	if _, ok := contents["files/"+id+"/"+remote]; ok {
		t.Fatal("a storage location's file was included without being asked for")
	}
	var state struct {
		Settings BackupSettings `json:"settings"`
		Sizes    backupSize     `json:"sizes"`
	}
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	if state.Settings.Storage || state.Sizes.Local != int64(len("on the data disk")) || state.Sizes.Database == 0 ||
		len(state.Sizes.Locations) != 1 || state.Sizes.Locations[0].Path != share || state.Sizes.Locations[0].Size != int64(len("on the nas")) {
		t.Fatal(state)
	}

	backUp(t, a, token, dir, true)
	if string(readBackup(t, dir)["files/"+id+"/"+remote]) != "on the nas" {
		t.Fatal("the storage location's file was not included when asked for")
	}
	// Without files, storage locations are off too.
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":0,"keep":3,"files":false,"storage":true}`, 200)
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	if state.Settings.Storage {
		t.Fatal("storage kept without files")
	}
}

// An incomplete backup never pushes out the last complete one, and another
// installation's archives in a shared folder are neither listed nor pruned.
func TestBackupsKeepTheLastCompleteOne(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	libraryUpload(t, a, token, "", "here.txt", "local")
	share := t.TempDir()
	id := addStorage(t, a, token, share)
	dir := filepath.Join(t.TempDir(), "backups")
	os.MkdirAll(dir, 0700)
	foreign := "magisend-backup-deadbeef-20260101-000000.tar"
	os.WriteFile(filepath.Join(dir, foreign), []byte("someone else's"), 0600)
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":0,"keep":1,"files":true,"storage":true}`, 200)
	folderCall(t, a, token, "POST", "/api/admin/backups", `{}`, 202)
	waitForBackup(t, a)
	a.stores.mu.Lock()
	a.stores.locations[id].online = false
	a.stores.mu.Unlock()
	time.Sleep(1100 * time.Millisecond)
	folderCall(t, a, token, "POST", "/api/admin/backups", `{}`, 202)
	waitForBackup(t, a)
	list := listBackups(dir, a.ownBackup)
	if len(list) != 2 || list[0].Complete || !list[1].Complete {
		t.Fatal("the last complete backup was not kept", list)
	}
	if _, e := os.Stat(filepath.Join(dir, foreign)); e != nil {
		t.Fatal("another installation's backup was removed")
	}
	var state struct {
		Backups []BackupFile `json:"backups"`
	}
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	for _, b := range state.Backups {
		if b.Name == foreign {
			t.Fatal("another installation's backup was listed")
		}
	}
	if w := call(a, "GET", "/api/admin/backups/"+foreign, "", token); w.Code != 404 {
		t.Fatal("another installation's backup could be downloaded", w.Code)
	}
}

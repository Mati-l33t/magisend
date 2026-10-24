package server

import (
	"archive/tar"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readBackup returns the newest backup's files by name.
func readBackup(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	list := listBackups(dir)
	if len(list) == 0 {
		t.Fatal("no backup was made")
	}
	archive, e := os.Open(filepath.Join(dir, list[0].Name))
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

func backUp(t *testing.T, a *App, token, dir string) {
	t.Helper()
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":0,"keep":3,"files":true}`, 200)
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
	backUp(t, a, token, dir)
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
	backUp(t, a, token, dir)
	json.Unmarshal(call(a, "GET", "/api/admin/backups", "", token).Body.Bytes(), &state)
	if state.Warning != "" {
		t.Fatal("warning kept", state.Warning)
	}
}

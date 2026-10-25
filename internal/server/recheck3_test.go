package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func scanStateOf(a *App, file string) string {
	var state string
	a.db.QueryRow("SELECT state FROM file_scans WHERE file_id=? ORDER BY queued DESC,rowid DESC LIMIT 1", file).Scan(&state)
	return state
}

// An infected earlier version is not restored as text, and text written
// through the editor is scanned like any new contents.
func TestTextRestoreAndEditsAreScanned(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	f := libraryUpload(t, a, admin, "", "x.txt", eicar).Files[0]
	scanAllNow(a)
	if w := replaceUpload(t, a, admin, f.ID, "clean.txt", "clean"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", admin); w.Code != 423 {
		t.Fatal("a new version waiting for its scan was served", w.Code)
	}
	scanAllNow(a)
	if w := call(a, "POST", "/api/library/files/"+f.ID+"/restore", `{"version":2,"restore":1}`, admin); w.Code != 403 {
		t.Fatal("an infected version was restored", w.Code, w.Body)
	}
	if w := call(a, "PUT", "/api/library/files/"+f.ID+"/text", `{"version":2,"text":"edited"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", admin); w.Code != 423 {
		t.Fatal("edited text was not scanned", w.Code)
	}
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/text", "", admin); w.Code != 423 {
		t.Fatal("text waiting for its scan gave", w.Code)
	}
	// Saving again, and the list of versions, do not wait for the scan.
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/versions", "", admin); w.Code != 200 {
		t.Fatal("versions", w.Code, w.Body)
	}
	if w := call(a, "PUT", "/api/library/files/"+f.ID+"/text", `{"version":3,"text":"edited"}`, admin); w.Code != 200 {
		t.Fatal("a second save waited for the first one's scan", w.Code, w.Body)
	}
	scanAllNow(a)
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", admin); w.Code != 200 || w.Body.String() != "edited" {
		t.Fatal(w.Code, w.Body)
	}
}

// A file of an upload request is not served from the library before the
// upload is completed (and its scan queued).
func TestUnfinishedRequestUploadIsNotServed(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "received", address, 50<<20)
	q := folderCall(t, a, admin, "POST", "/api/requests", `{"title":"Inbox","days":7}`, 201)
	w := visitor(a, "POST", "/api/requests/"+q["id"].(string)+"/uploads", `{"sender":"Anna","files":[{"name":"evil.exe","size":4}]}`, nil)
	var up startedUpload
	json.Unmarshal(w.Body.Bytes(), &up)
	visitor(a, "PATCH", "/api/requests/"+q["id"].(string)+"/uploads/"+up.Upload+"/files/"+up.Files[0].ID, "evil", map[string]string{"Upload-Offset": "0", "X-Upload-Token": up.Token})
	for _, path := range []string{"/download", "/preview", "/media"} {
		if w := call(a, "GET", "/api/library/files/"+up.Files[0].ID+path, "", admin); w.Code != 409 {
			t.Fatal("an unfinished received file was served", path, w.Code)
		}
	}
}

// What comes out of the owner's own archive is scanned too when only
// received files are.
func TestUnpackedFilesAreScannedInReceivedMode(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "received", address, 50<<20)
	f := libraryUpload(t, a, admin, "", "a.zip", zipOf(t, map[string]string{"inside.txt": "in"})).Files[0]
	got := folderCall(t, a, admin, "POST", "/api/library/files/"+f.ID+"/extract", `{}`, 201)
	files := unpacked(t, a, admin, got["folder_id"].(string))
	if files["inside.txt"][1] == "in" {
		t.Fatal("an unpacked file was served before its scan")
	}
	scanAllNow(a)
	if files = unpacked(t, a, admin, got["folder_id"].(string)); files["inside.txt"][1] != "in" {
		t.Fatal(files)
	}
}

// Overlong tar names are skipped without being kept; a name that appears
// twice keeps its last copy, as tar does.
func TestTarLongNamesAndDuplicates(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	archive := tarOf(t, true, []tarPart{
		{name: strings.Repeat("n", 5000) + ".txt", data: "long"},
		{name: "a.txt", data: "old"},
		{name: "a.txt", data: "new"},
		{name: "link", kind: tar.TypeSymlink, link: "a.txt"},
	})
	id := libraryUpload(t, a, token, "", "t.tar.gz", archive).Files[0].ID
	got := folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 201)
	if got["files"] != float64(1) || got["skipped"] != float64(3) {
		t.Fatal(got)
	}
	if files := unpacked(t, a, token, got["folder_id"].(string)); files["a.txt"][1] != "new" {
		t.Fatal(files)
	}
}

// A file held back cannot become another file's contents; a clean one
// brings its scan along, and only the replaced file is queued.
func TestReplaceFollowsTheSourcesScan(t *testing.T) {
	a, admin := emailAdmin(t)
	address, scans := fakeClamd(t, 1<<20)
	body := `{"title":"Two","days":7,"files":[{"name":"one.txt","size":3},{"name":"two.txt","size":3}]}`
	_, ids := createUploadPublish(t, a, admin, body, map[string][]byte{"one.txt": []byte("one"), "two.txt": []byte("two")})
	setScanning(t, a, admin, "all", address, 50<<20)
	bad := libraryUpload(t, a, admin, "", "bad.txt", eicar).Files[0].ID
	scanAllNow(a)
	if w := call(a, "POST", "/api/library/files/"+ids["one.txt"]+"/replace", `{"source":"`+bad+`"}`, admin); w.Code != 403 {
		t.Fatal("an infected source was used", w.Code)
	}
	clean := libraryUpload(t, a, admin, "", "good.txt", "good").Files[0].ID
	scanAllNow(a)
	before := scans.Load()
	if w := call(a, "POST", "/api/library/files/"+ids["one.txt"]+"/replace", `{"source":"`+clean+`"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	scanAllNow(a)
	if scans.Load() != before {
		t.Fatal("a scanned source was scanned again")
	}
	if w := call(a, "GET", "/api/library/files/"+ids["one.txt"]+"/download", "", admin); w.Code != 200 || w.Body.String() != "good" {
		t.Fatal(w.Code, w.Body)
	}
	if state := scanStateOf(a, ids["two.txt"]); state != "" {
		t.Fatal("an unchanged file of the same share was queued", state)
	}
}

// A file clamd could not scan whole is marked unscanned, not clean.
func TestScanLimitsExceededIsUnscanned(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	f := libraryUpload(t, a, admin, "", "deep.zip", "LIMITS-EXCEEDED").Files[0]
	scanAllNow(a)
	if state := scanStateOf(a, f.ID); state != "unscanned" {
		t.Fatal(state)
	}
}

// An install request or a running install that is not heard of for long is
// given up, so the admin page offers the button again.
func TestStaleClamAVInstallIsGivenUp(t *testing.T) {
	a, admin := emailAdmin(t)
	trigger := filepath.Join(a.root, clamavTrigger)
	os.WriteFile(trigger, []byte("x"), 0600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(trigger, old, old)
	clamav := decodeObject(t, call(a, "GET", "/api/admin/antivirus", "", admin))["clamav"].(map[string]any)
	if clamav["requested"] == true {
		t.Fatal("an old request still counts", clamav)
	}
}

// IDs the machine had before stay its own: their backups are listed.
func TestFormerInstallIDsKeepTheirBackups(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	mine := a.installID()
	a.db.Exec("UPDATE settings SET install_id='aaaaaaaa'")
	a.db.Close()
	b, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer b.db.Close()
	dir := t.TempDir()
	for _, id := range []string{mine, "aaaaaaaa", "cccccccc"} {
		os.WriteFile(filepath.Join(dir, "magisend-backup-"+id+"-20261001-000000.tar"), []byte("x"), 0600)
	}
	if list := listBackups(dir, b.ownBackup); len(list) != 2 {
		t.Fatal("this machine's backups under both IDs are not listed", list)
	}

	b.db.Close()
	c, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer c.db.Close()
	if !c.ownBackup(mine) || c.ownBackup("cccccccc") {
		t.Fatal("the former ID was not kept", c.formerInstalls)
	}
}

// A note is made and saved again at once while scanning everything; the
// editor gets the new note's version without reading it.
func TestNotesSaveWhileTheirScanWaits(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	n := folderCall(t, a, admin, "POST", "/api/library/notes", `{"title":"Plan","text":"one","language":"en"}`, 201)
	if n["version"] != float64(1) {
		t.Fatal("no version", n)
	}
	id := n["id"].(string)
	if state := scanStateOf(a, id); state != "pending" {
		t.Fatal("a new note was not queued", state)
	}
	folderCall(t, a, admin, "PUT", "/api/library/notes/"+id, `{"version":1,"text":"two","title":"Plan","language":"en"}`, 200)
	var list []Note
	json.Unmarshal(call(a, "GET", "/api/library/notes", "", admin).Body.Bytes(), &list)
	if len(list) != 1 || list[0].Excerpt != "" {
		t.Fatal("a note waiting for its scan showed its text", list)
	}
}

// A ZIP that leaves files out says which.
func TestZipNamesFilesLeftOut(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	body := `{"title":"Mixed","days":7,"files":[{"name":"clean.txt","size":5},{"name":"virus.com","size":68}]}`
	id, _ := createUploadPublish(t, a, admin, body, map[string][]byte{"clean.txt": []byte("hello"), "virus.com": []byte(eicar)})
	scanAllNow(a)
	w := call(a, "GET", "/api/public/"+id+"/zip", "", "")
	reader, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil {
		t.Fatal(w.Code, e)
	}
	names := map[string]string{}
	for _, f := range reader.File {
		r, _ := f.Open()
		data, _ := io.ReadAll(r)
		names[f.Name] = string(data)
	}
	if _, ok := names["clean.txt"]; !ok || !strings.Contains(names["Not included.txt"], "virus.com") {
		t.Fatal(names)
	}
}

// Names from old tar files that are not UTF-8 are read as Latin-1.
func TestTarLatin1Names(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	archive := tarOf(t, true, []tarPart{{name: "r\xe4kning.txt", data: "kvitto"}})
	id := libraryUpload(t, a, token, "", "old.tar.gz", archive).Files[0].ID
	got := folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 201)
	if files := unpacked(t, a, token, got["folder_id"].(string)); files["räkning.txt"][1] != "kvitto" {
		t.Fatal(files)
	}
}

// The admin page tells when ClamAV here lacks the settings it needs.
func TestClamdSettingsAreChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clamd.conf")
	original := clamdConfPath
	clamdConfPath = path
	defer func() { clamdConfPath = original }()
	for conf, needs := range map[string]bool{
		"LocalSocket /run/clamav/clamd.ctl\nMaxFileSize 25M\nStreamMaxLength 25M\n":                  true,
		"MaxFileSize 100M\nStreamMaxLength 100M\nAlertExceedsMax false\n":                            true,
		"MaxFileSize 25M\nStreamMaxLength 100M\nAlertExceedsMax true\n":                              true,
		"MaxFileSize 100M\nStreamMaxLength 100M\nMaxScanSize 400M\nAlertExceedsMax true\n":           false,
		"# AlertExceedsMax true\nMaxFileSize 104857600\nStreamMaxLength 100M\nAlertExceedsMax yes\n": false,
	} {
		os.WriteFile(path, []byte(conf), 0600)
		if got := clamdNeedsSettings(defaultClamdAddress, defaultScanMaxSize); got != needs {
			t.Fatal(conf, got)
		}
	}
}

// Only a connection clamd closed counts as a stream it refused; a timeout or
// a file that could not be read is tried again.
func TestCutOffStreams(t *testing.T) {
	timeout := &net.OpError{Op: "write", Err: os.ErrDeadlineExceeded}
	for _, c := range []struct {
		sent, read error
		cut        bool
	}{
		{syscall.EPIPE, io.EOF, true},
		{&net.OpError{Op: "write", Err: syscall.ECONNRESET}, io.EOF, true},
		{nil, syscall.ECONNRESET, true},
		{timeout, timeout, false},
		{nil, io.EOF, false},
	} {
		if got := cutOff(c.sent, c.read); got != c.cut {
			t.Fatal(c, got)
		}
	}
}

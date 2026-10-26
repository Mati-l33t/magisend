package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Backups made on schedule prune only the current ID's archives; one under
// an earlier ID stays.
func TestBackupsDoNotPruneEarlierIDs(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	dir := filepath.Join(t.TempDir(), "backups")
	os.MkdirAll(dir, 0700)
	a.formerInstalls = []string{"bbbbbbbb"}
	former := filepath.Join(dir, "magisend-backup-bbbbbbbb-20200101-000000.tar")
	os.WriteFile(former, []byte("x"), 0600)
	folderCall(t, a, token, "PUT", "/api/admin/backups", `{"dir":"`+dir+`","every":24,"keep":1,"files":false}`, 200)
	for i := 1; i <= 3; i++ {
		a.dueBackup(time.Now().Add(time.Duration(i*25) * time.Hour))
		waitForBackup(t, a)
	}
	if _, e := os.Stat(former); e != nil {
		t.Fatal("an archive under an earlier ID was pruned")
	}
	if list := listBackups(dir, a.ownBackup); len(list) != 2 {
		t.Fatal("listed", list)
	}
}

// A replacement waiting for its scan that turns out infected blocks the
// file it became, in its history too.
func TestPendingSourceFoundInfectedBlocksTheTarget(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	f := libraryUpload(t, a, admin, "", "doc.txt", "fine").Files[0]
	scanAllNow(a)
	if w := replaceUpload(t, a, admin, f.ID, "doc2.txt", eicar); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	scanAllNow(a)
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", admin); w.Code != 403 {
		t.Fatal("an infected new version was served", w.Code)
	}
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/history/2", "", admin); w.Code != 403 {
		t.Fatal("an infected version in the history was served", w.Code)
	}
}

// Autosaves merged while their scan waits leave no scan behind, and a
// version in which a virus was found is not merged away.
func TestMergedSavesTakeTheirScanAlong(t *testing.T) {
	a, admin := emailAdmin(t)
	setScanning(t, a, admin, "all", "unix:"+filepath.Join(t.TempDir(), "down.sock"), 50<<20)
	f := libraryUpload(t, a, admin, "", "draft.txt", "zero").Files[0]
	version := 1
	for _, text := range []string{"one", "two", "three"} {
		got := folderCall(t, a, admin, "PUT", "/api/library/files/"+f.ID+"/text", `{"version":`+itoa(version)+`,"text":"`+text+`"}`, 200)
		version = int(got["version"].(float64))
	}
	var waiting int
	a.db.QueryRow("SELECT COUNT(*) FROM file_scans WHERE file_id=? AND state='pending'", f.ID).Scan(&waiting)
	// The original and the one merged version left.
	if waiting != 2 {
		t.Fatal("scans left behind", waiting)
	}

	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	g := libraryUpload(t, a, admin, "", "x.txt", "start").Files[0]
	scanAllNow(a)
	got := folderCall(t, a, admin, "PUT", "/api/library/files/"+g.ID+"/text", `{"version":1,"text":"`+strings.ReplaceAll(eicar, `\`, `\\`)+`"}`, 200)
	scanAllNow(a)
	folderCall(t, a, admin, "PUT", "/api/library/files/"+g.ID+"/text", `{"version":`+itoa(int(got["version"].(float64)))+`,"text":"clean"}`, 200)
	var infected int
	a.db.QueryRow("SELECT COUNT(*) FROM file_scans WHERE file_id=? AND state='infected'", g.ID).Scan(&infected)
	if infected != 1 {
		t.Fatal("the infected version was merged away")
	}
	// It is still in the history, and blocked there.
	var number int
	a.db.QueryRow("SELECT v.number FROM text_versions v JOIN file_scans s ON s.blob=v.blob WHERE v.file_id=? AND s.state='infected'", g.ID).Scan(&number)
	if w := call(a, "GET", "/api/library/files/"+g.ID+"/history/"+itoa(number), "", admin); number == 0 || w.Code != 403 {
		t.Fatal("the infected version is not blocked in the history", number, w.Code)
	}
}

// A file opened in the editor can still be another file's new version.
func TestReplaceWithAFileOpenedInTheEditor(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	f := libraryUpload(t, a, token, "", "a.txt", "old").Files[0]
	source := libraryUpload(t, a, token, "", "b.txt", "new").Files[0]
	folderCall(t, a, token, "GET", "/api/library/files/"+source.ID+"/text", "", 200)
	folderCall(t, a, token, "POST", "/api/library/files/"+f.ID+"/replace", `{"source":"`+source.ID+`"}`, 200)
	edited := libraryUpload(t, a, token, "", "c.txt", "c").Files[0]
	folderCall(t, a, token, "PUT", "/api/library/files/"+edited.ID+"/text", `{"version":1,"text":"changed"}`, 200)
	folderCall(t, a, token, "POST", "/api/library/files/"+f.ID+"/replace", `{"source":"`+edited.ID+`"}`, 409)
}

// A file held back whose contents are not text says it is held back.
func TestHeldNonTextSaysHeld(t *testing.T) {
	a, admin := emailAdmin(t)
	setScanning(t, a, admin, "all", "unix:"+filepath.Join(t.TempDir(), "down.sock"), 50<<20)
	f := libraryUpload(t, a, admin, "", "bin.txt", "a\x00b").Files[0]
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/versions", "", admin); w.Code != 423 {
		t.Fatal(w.Code)
	}
}

// Windows-1252 names keep their signs, C1 controls in UTF-8 names become
// them too, and the undefined bytes stay distinct.
func TestCP1252Names(t *testing.T) {
	for in, want := range map[string]string{
		"pris \x80 \x93ok\x94": "pris € “ok”",
		"a\u0085b.txt":         "a…b.txt",
		"M\x81ller":            "M%81ller",
		"M\x8dller":            "M%8Dller",
		"plain.txt":            "plain.txt",
	} {
		if got, _ := tarName(in); got != want {
			t.Fatal(in, got)
		}
	}
}

// A waiting scan of a stored object that is no longer one of the file's
// versions is dropped, not tried for ever.
func TestScannerDropsScansOfGoneVersions(t *testing.T) {
	a, admin := emailAdmin(t)
	address, scans := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	f := libraryUpload(t, a, admin, "", "a.txt", "a").Files[0]
	scanAllNow(a)
	a.db.Exec("INSERT INTO file_scans(file_id,blob,state,queued) VALUES(?,?,'pending',1)", f.ID, randomID())
	before := scans.Load()
	scanAllNow(a)
	var left int
	a.db.QueryRow("SELECT COUNT(*) FROM file_scans WHERE file_id=? AND state='pending'", f.ID).Scan(&left)
	if left != 0 || scans.Load() != before {
		t.Fatal("a gone version's scan was kept", left)
	}
}

// ClamAV on this server is recognised at its own socket or TCP port.
func TestLocalClamdAddresses(t *testing.T) {
	conf := map[string]string{"LocalSocket": "/var/run/clamav/clamd.ctl", "TCPSocket": "3310"}
	for address, local := range map[string]bool{
		"unix:/run/clamav/clamd.ctl":     true,
		"unix:/var/run/clamav/clamd.ctl": true,
		"127.0.0.1:3310":                 true,
		"localhost:3311":                 false,
		"clamav:3310":                    false,
	} {
		if localClamd(address, conf) != local {
			t.Fatal(address)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

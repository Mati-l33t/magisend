package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// The standard harmless test file every virus scanner reports.
const eicar = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

// fakeClamd answers like clamd: EICAR is found, streams over limit bytes are
// refused. It returns the address and a count of scans.
func fakeClamd(t *testing.T, limit int) (string, *atomic.Int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clamd.sock")
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	scans := &atomic.Int64{}
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				command := make([]byte, 0, 16)
				one := make([]byte, 1)
				for {
					if _, e := conn.Read(one); e != nil {
						return
					}
					if one[0] == 0 {
						break
					}
					command = append(command, one[0])
				}
				switch string(command) {
				case "zVERSION":
					conn.Write([]byte("ClamAV 1.4.3/27800/Sat Oct 25 08:00:00 2026\x00"))
				case "zINSTREAM":
					scans.Add(1)
					var data bytes.Buffer
					size := make([]byte, 4)
					for {
						if _, e := io.ReadFull(conn, size); e != nil {
							return
						}
						n := binary.BigEndian.Uint32(size)
						if n == 0 {
							break
						}
						if data.Len()+int(n) > limit {
							conn.Write([]byte("INSTREAM size limit exceeded. ERROR\x00"))
							return
						}
						if _, e := io.CopyN(&data, conn, int64(n)); e != nil {
							return
						}
					}
					if strings.Contains(data.String(), "LIMITS-EXCEEDED") {
						conn.Write([]byte("stream: Heuristics.Limits.Exceeded.MaxFileSize FOUND\x00"))
					} else if strings.Contains(data.String(), "EICAR-STANDARD-ANTIVIRUS-TEST-FILE") {
						conn.Write([]byte("stream: Eicar-Test-Signature FOUND\x00"))
					} else {
						conn.Write([]byte("stream: OK\x00"))
					}
				}
			}()
		}
	}()
	return "unix:" + path, scans
}

func setScanning(t *testing.T, a *App, admin, mode, address string, maxSize int64) {
	t.Helper()
	body := fmt.Sprintf(`{"mode":%q,"address":%q,"max_size":%d}`, mode, address, maxSize)
	if w := call(a, "PUT", "/api/admin/antivirus", body, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}

func scanAllNow(a *App) {
	for a.scanNext(context.Background()) {
	}
}

func TestScanningHoldsFilesUntilCleanAndBlocksInfected(t *testing.T) {
	a, admin := emailAdmin(t)
	address, scans := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	body := `{"title":"Mixed","days":7,"files":[{"name":"clean.txt","size":5},{"name":"virus.com","size":68}]}`
	id, ids := createUploadPublish(t, a, admin, body, map[string][]byte{"clean.txt": []byte("hello"), "virus.com": []byte(eicar)})

	// Waiting for the scan: nobody gets the file, and the link shows why.
	if w := call(a, "GET", "/api/public/"+id+"/files/"+ids["clean.txt"], "", ""); w.Code != 423 {
		t.Fatal("a file waiting for its scan was sent", w.Code)
	}
	var view struct {
		Files []publicFile `json:"files"`
	}
	json.Unmarshal(call(a, "GET", "/api/public/"+id, "", "").Body.Bytes(), &view)
	if len(view.Files) != 2 || view.Files[0].Scan != "pending" || view.Files[1].Scan != "pending" {
		t.Fatal("waiting files not marked", view.Files)
	}
	if w := call(a, "GET", "/api/public/"+id+"/zip", "", ""); w.Code != 423 {
		t.Fatal("a ZIP was made of files waiting for their scan", w.Code)
	}

	scanAllNow(a)
	if scans.Load() != 2 {
		t.Fatal("scans", scans.Load())
	}
	if w := call(a, "GET", "/api/public/"+id+"/files/"+ids["clean.txt"], "", ""); w.Code != 200 || w.Body.String() != "hello" {
		t.Fatal("a clean file was held back", w.Code)
	}
	for _, path := range []string{
		"/api/public/" + id + "/files/" + ids["virus.com"],
		"/api/public/" + id + "/files/" + ids["virus.com"] + "/media",
		"/api/shares/" + id + "/files/" + ids["virus.com"],
	} {
		if w := call(a, "GET", path, "", admin); w.Code != 403 || !strings.Contains(w.Body.String(), "file_infected") {
			t.Fatal("an infected file was sent", path, w.Code, w.Body)
		}
	}
	// The ZIP holds only the clean file.
	if w := call(a, "GET", "/api/public/"+id+"/zip", "", ""); w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("EICAR")) || !bytes.Contains(w.Body.Bytes(), []byte("clean.txt")) {
		t.Fatal("ZIP", w.Code)
	}

	// The admin page lists it; switching scanning off does not release it.
	got := decodeObject(t, call(a, "GET", "/api/admin/antivirus", "", admin))
	infected := got["infected"].([]any)
	if len(infected) != 1 || infected[0].(map[string]any)["signature"] != "Eicar-Test-Signature" || infected[0].(map[string]any)["name"] != "virus.com" {
		t.Fatal(got)
	}
	setScanning(t, a, admin, "off", address, 50<<20)
	if w := call(a, "GET", "/api/public/"+id+"/files/"+ids["virus.com"], "", ""); w.Code != 403 {
		t.Fatal("an infected file was released with scanning off", w.Code)
	}
	var logged int
	a.db.QueryRow("SELECT COUNT(*) FROM audit WHERE action='file.infected'").Scan(&logged)
	if logged != 1 {
		t.Fatal("not in the activity log", logged)
	}
}

func TestScanningOnlyReceivedFiles(t *testing.T) {
	a, admin := emailAdmin(t)
	address, scans := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "received", address, 50<<20)
	own := libraryUpload(t, a, admin, "", "mine.txt", "mine")
	if w := call(a, "GET", "/api/library/files/"+own.Files[0].ID+"/download", "", admin); w.Code != 200 {
		t.Fatal("the owner's own upload was held back", w.Code)
	}
	q := folderCall(t, a, admin, "POST", "/api/requests", `{"title":"Inbox","days":7}`, 201)
	w := visitor(a, "POST", "/api/requests/"+q["id"].(string)+"/uploads", `{"sender":"Anna","files":[{"name":"from-anna.txt","size":4}]}`, nil)
	var up startedUpload
	json.Unmarshal(w.Body.Bytes(), &up)
	visitor(a, "PATCH", "/api/requests/"+q["id"].(string)+"/uploads/"+up.Upload+"/files/"+up.Files[0].ID, "anna", map[string]string{"Upload-Offset": "0", "X-Upload-Token": up.Token})
	if w = visitor(a, "POST", "/api/requests/"+q["id"].(string)+"/uploads/"+up.Upload+"/complete", "{}", map[string]string{"X-Upload-Token": up.Token}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var files []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", admin).Body.Bytes(), &files)
	var received LibraryFile
	for _, f := range files {
		if f.Name == "from-anna.txt" {
			received = f
		}
	}
	if received.Scan != "pending" {
		t.Fatal("a received file is not waiting for its scan", received)
	}
	if w = call(a, "GET", "/api/library/files/"+received.ID+"/download", "", admin); w.Code != 423 {
		t.Fatal(w.Code)
	}
	scanAllNow(a)
	if scans.Load() != 1 {
		t.Fatal("only the received file is scanned", scans.Load())
	}
	if w = call(a, "GET", "/api/library/files/"+received.ID+"/download", "", admin); w.Code != 200 || w.Body.String() != "anna" {
		t.Fatal(w.Code)
	}
}

// When clamd does not answer, files keep waiting and the admin page says
// why; switching scanning off releases them.
func TestScannerDownKeepsFilesWaiting(t *testing.T) {
	a, admin := emailAdmin(t)
	setScanning(t, a, admin, "all", "unix:"+filepath.Join(t.TempDir(), "nothing.sock"), 50<<20)
	f := libraryUpload(t, a, admin, "", "a.txt", "a").Files[0]
	if a.scanNext(context.Background()) {
		t.Fatal("went on after the scanner failed")
	}
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", admin); w.Code != 423 {
		t.Fatal(w.Code)
	}
	got := decodeObject(t, call(a, "GET", "/api/admin/antivirus", "", admin))
	if status, _ := got["status"].(map[string]any); status["error"] == nil {
		t.Fatal("no error shown", got)
	}
	var retry int64
	a.db.QueryRow("SELECT retry_at FROM file_scans WHERE file_id=?", f.ID).Scan(&retry)
	if retry == 0 {
		t.Fatal("not put off")
	}
	setScanning(t, a, admin, "off", "", 50<<20)
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/download", "", admin); w.Code != 200 {
		t.Fatal("not released with scanning off", w.Code)
	}
}

func TestScanSkipsFilesTooLargeToScan(t *testing.T) {
	a, admin := emailAdmin(t)
	address, scans := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 1<<20)
	big := libraryUpload(t, a, admin, "", "big.bin", strings.Repeat("x", 2<<20)).Files[0]
	scanAllNow(a)
	if scans.Load() != 0 {
		t.Fatal("a file over the limit was sent to the scanner")
	}
	// clamd's own limit lower than MagiSend's.
	setScanning(t, a, admin, "all", address, 10<<20)
	over := libraryUpload(t, a, admin, "", "over.bin", strings.Repeat("y", 3<<20/2)).Files[0]
	scanAllNow(a)
	for _, id := range []string{big.ID, over.ID} {
		var state string
		a.db.QueryRow("SELECT state FROM file_scans WHERE file_id=?", id).Scan(&state)
		if state != "unscanned" {
			t.Fatal(id, state)
		}
		if w := call(a, "GET", "/api/library/files/"+id+"/download", "", admin); w.Code != 200 {
			t.Fatal("a file too large to scan was held back", w.Code)
		}
	}
}

// A new version is scanned again; an infected one cannot be unpacked,
// downloaded from the history or restored.
func TestScanFollowsVersionsAndBlocksExtracting(t *testing.T) {
	a, admin := emailAdmin(t)
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "all", address, 50<<20)
	archive := zipOf(t, map[string]string{"virus.com": eicar})
	f := libraryUpload(t, a, admin, "", "bad.zip", archive).Files[0]
	folderCall(t, a, admin, "POST", "/api/library/files/"+f.ID+"/extract", `{}`, 423)
	scanAllNow(a)
	folderCall(t, a, admin, "POST", "/api/library/files/"+f.ID+"/extract", `{}`, 403)

	// Replaced by a new version still waiting for its scan: the file waits
	// with it, then is usable; the infected first version stays blocked in
	// the history.
	if w := replaceUpload(t, a, admin, f.ID, "clean.zip", zipOf(t, map[string]string{"ok.txt": "ok"})); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, admin, "POST", "/api/library/files/"+f.ID+"/extract", `{}`, 423)
	scanAllNow(a)
	folderCall(t, a, admin, "POST", "/api/library/files/"+f.ID+"/extract", `{}`, 201)
	if w := call(a, "GET", "/api/library/files/"+f.ID+"/history/1", "", admin); w.Code != 403 {
		t.Fatal("an infected earlier version was sent", w.Code)
	}
	if w := call(a, "POST", "/api/library/files/"+f.ID+"/history/1/restore", "{}", admin); w.Code != 403 {
		t.Fatal("an infected earlier version was restored", w.Code)
	}
}

// replaceUpload uploads data and makes it the new version of file at once,
// as the library does.
func replaceUpload(t *testing.T, a *App, token, file, name, data string) *httptest.ResponseRecorder {
	t.Helper()
	source := libraryUpload(t, a, token, "", name, data).Files[0].ID
	return call(a, "POST", "/api/library/files/"+file+"/replace", `{"source":"`+source+`"}`, token)
}

func TestAntivirusSettingsAreChecked(t *testing.T) {
	a, admin := emailAdmin(t)
	for _, body := range []string{
		`{"mode":"sometimes","address":"","max_size":104857600}`,
		`{"mode":"all","address":"unix:relative.sock","max_size":104857600}`,
		`{"mode":"all","address":"no-port","max_size":104857600}`,
		`{"mode":"all","address":"","max_size":10}`,
	} {
		if w := call(a, "PUT", "/api/admin/antivirus", body, admin); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	address, _ := fakeClamd(t, 1<<20)
	if got := decodeObject(t, call(a, "POST", "/api/admin/antivirus/test", fmt.Sprintf(`{"address":%q}`, address), admin)); !strings.HasPrefix(fmt.Sprint(got["version"]), "ClamAV 1.4.3") {
		t.Fatal(got)
	}
	if w := call(a, "POST", "/api/admin/antivirus/test", `{"address":"unix:/nonexistent/clamd.sock"}`, admin); w.Code != 502 {
		t.Fatal(w.Code)
	}
}

// The admin page learns whether ClamAV answers; it can only be installed
// from there on a native installation with the updater.
func TestAdminSeesWhetherClamAVRuns(t *testing.T) {
	a, admin := emailAdmin(t)
	setScanning(t, a, admin, "off", "unix:"+filepath.Join(t.TempDir(), "nothing.sock"), 50<<20)
	clamav := decodeObject(t, call(a, "GET", "/api/admin/antivirus", "", admin))["clamav"].(map[string]any)
	if clamav["running"] != false || clamav["install"] != "manual" || clamav["can_install"] != false {
		t.Fatal(clamav)
	}
	if w := call(a, "POST", "/api/admin/antivirus/install", "{}", admin); w.Code != 409 || !strings.Contains(w.Body.String(), "antivirus_cannot_install") {
		t.Fatal(w.Code, w.Body)
	}
	address, _ := fakeClamd(t, 1<<20)
	setScanning(t, a, admin, "off", address, 50<<20)
	clamav = decodeObject(t, call(a, "GET", "/api/admin/antivirus", "", admin))["clamav"].(map[string]any)
	if clamav["running"] != true || !strings.HasPrefix(fmt.Sprint(clamav["version"]), "ClamAV ") {
		t.Fatal(clamav)
	}
}

// On a native installation the page asks the updater: it leaves the ClamAV
// request next to the update request that starts the updater.
func TestInstallingClamAVAsksTheUpdater(t *testing.T) {
	a, admin := emailAdmin(t)
	script := filepath.Join(t.TempDir(), "update.sh")
	os.WriteFile(script, []byte("#!/bin/sh\n# update.sh --clamav installs ClamAV\n"), 0755)
	original := updaterScript
	updaterScript = func() string { return script }
	defer func() { updaterScript = original }()
	a.updates.Mode, a.updates.Updater = "native", true
	if memoryMB() > 0 && memoryMB() < clamavMemory {
		t.Skip("too little memory here for the install to be allowed")
	}
	if w := call(a, "POST", "/api/admin/antivirus/install", "{}", admin); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	for _, name := range []string{clamavTrigger, updateTrigger} {
		if _, e := os.Stat(filepath.Join(a.root, name)); e != nil {
			t.Fatal(name, e)
		}
	}
	clamav := decodeObject(t, call(a, "GET", "/api/admin/antivirus", "", admin))["clamav"].(map[string]any)
	if clamav["requested"] != true || clamav["can_install"] != true {
		t.Fatal(clamav)
	}
}

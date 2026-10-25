package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"testing"
)

type tarPart struct {
	name, data, link string
	kind             byte
	size             int64
}

func tarOf(t *testing.T, gz bool, parts []tarPart) string {
	var b bytes.Buffer
	var z *gzip.Writer
	var tw *tar.Writer
	if gz {
		z = gzip.NewWriter(&b)
		tw = tar.NewWriter(z)
	} else {
		tw = tar.NewWriter(&b)
	}
	for _, p := range parts {
		h := &tar.Header{Name: p.name, Typeflag: p.kind, Linkname: p.link, Mode: 0644, Size: int64(len(p.data))}
		if p.kind == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if p.size > 0 {
			h.Size = p.size
		}
		if e := tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if p.size > 0 {
			chunk := make([]byte, 1<<20)
			for left := p.size; left > 0; left -= int64(len(chunk)) {
				tw.Write(chunk[:min(left, int64(len(chunk)))])
			}
		} else {
			tw.Write([]byte(p.data))
		}
	}
	tw.Close()
	if gz {
		z.Close()
	}
	return b.String()
}

// Unpacked files by name, with the folder path each landed in (below the
// new folder) and its content.
func unpacked(t *testing.T, a *App, token, top string) map[string][2]string {
	var folders []Folder
	json.Unmarshal(call(a, "GET", "/api/folders", "", token).Body.Bytes(), &folders)
	byID := map[string]Folder{}
	for _, f := range folders {
		byID[f.ID] = f
	}
	var files []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", token).Body.Bytes(), &files)
	got := map[string][2]string{}
	for _, f := range files {
		where, inside := "", false
		for p := f.FolderID; p != ""; p = byID[p].ParentID {
			if p == top {
				inside = true
				break
			}
			where = byID[p].Name + "/" + where
		}
		if inside {
			got[f.Name] = [2]string{where, call(a, "GET", "/api/library/files/"+f.ID+"/download", "", token).Body.String()}
		}
	}
	return got
}

func TestExtractTarGzKeepsOnlyPlainFiles(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	archive := tarOf(t, true, []tarPart{
		{name: "./", kind: tar.TypeDir},
		{name: "./readme.txt", data: "top"},
		{name: "./photos/", kind: tar.TypeDir},
		{name: "./photos/a.txt", data: "deep"},
		{name: "./evil", kind: tar.TypeSymlink, link: "/etc/passwd"},
		{name: "./hard", kind: tar.TypeLink, link: "readme.txt"},
		{name: "./pipe", kind: tar.TypeFifo},
		{name: "../escape.txt", data: "no"},
		{name: "/absolute.txt", data: "no"},
		{name: "./photos/.DS_Store", data: "no"},
	})
	id := libraryUpload(t, a, token, "", "Holiday.tar.gz", archive).Files[0].ID
	got := folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 201)
	if got["name"] != "Holiday" || got["files"] != float64(2) || got["skipped"] != float64(6) {
		t.Fatal(got)
	}
	files := unpacked(t, a, token, got["folder_id"].(string))
	if len(files) != 2 || files["readme.txt"] != [2]string{"", "top"} || files["a.txt"] != [2]string{"photos/", "deep"} {
		t.Fatal(files)
	}
}

// Made with: tar -cjf fixture.tar.bz2 . (docs/a.txt "alpha", b.txt "beta"
// and link.txt, a link to b.txt).
const tarBz2Fixture = "QlpoOTFBWSZTWZ/bcPMAAIx7hMmACEBAAf+ABSl+bd5AAACAKDAA2hCKIRTaRk0GI9NINDyIYAZNNBkMENMRowJJUZMmEwmRgQwTR6O+gRM05Ic0PFrCE3QEhTE9JSdTvIIjR2LWmiahNyQoEqwMp+pKbuvaDb05mnGIDA1oOxIATljznLaCemReG+PVLJUrA65qnBJ4+ByhKFUuLi16XpgXYV7qUwIsSRMqY9MuFEnCmZQ36Z6Vx0zPkTUJfZNifiZnVLk7AyHZozD+4PARpAcC7kinChIT+24eYA=="

func TestExtractTarBz2AndPlainTar(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	data, _ := base64.StdEncoding.DecodeString(tarBz2Fixture)
	id := libraryUpload(t, a, token, "", "Docs.tbz2", string(data)).Files[0].ID
	got := folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 201)
	if got["name"] != "Docs" || got["files"] != float64(2) || got["skipped"] != float64(1) {
		t.Fatal(got)
	}
	if files := unpacked(t, a, token, got["folder_id"].(string)); files["a.txt"] != [2]string{"docs/", "alpha"} || files["b.txt"] != [2]string{"", "beta"} {
		t.Fatal(files)
	}
	plain := tarOf(t, false, []tarPart{{name: "notes/one.txt", data: "1"}})
	id = libraryUpload(t, a, token, "", "Backup.tar", plain).Files[0].ID
	got = folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 201)
	if got["name"] != "Backup" || got["files"] != float64(1) {
		t.Fatal(got)
	}
}

func TestExtractTarRefusesBadArchives(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	id := libraryUpload(t, a, token, "", "fake.tar.gz", "not gzip").Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 400)
	id = libraryUpload(t, a, token, "", "fake.tar", "not a tar file at all").Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 400)

	// A small file that unpacks to far more than the owner's quota is not
	// read through, even when that is data of an entry that would be left out.
	a.db.Exec("UPDATE users SET quota=? WHERE username='mati'", 1<<20)
	bomb := tarOf(t, true, []tarPart{{name: "../zeros.bin", size: tarSlack + 16<<20}, {name: "after.txt", data: "x"}})
	if len(bomb) > 1<<20 {
		t.Fatal("the test archive is too large", len(bomb))
	}
	id = libraryUpload(t, a, token, "", "bomb.tgz", bomb).Files[0].ID
	folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 413)
	// The same when the quota is large but the storage budget is not.
	a.db.Exec("UPDATE users SET quota=? WHERE username='mati'", int64(1)<<50)
	a.db.Exec("UPDATE settings SET storage_budget=?", 4<<20)
	folderCall(t, a, token, "POST", "/api/library/files/"+id+"/extract", `{}`, 413)
	var folders int
	a.db.QueryRow("SELECT COUNT(*) FROM folders").Scan(&folders)
	if folders != 0 {
		t.Fatal("a refused archive left folders behind", folders)
	}
}

func TestArchiveFormat(t *testing.T) {
	for name, want := range map[string][2]string{
		"a.zip": {"zip", "a"}, "B.TAR.GZ": {"tar.gz", "B"}, "c.tgz": {"tar.gz", "c"},
		"d.tar.bz2": {"tar.bz2", "d"}, "e.tbz": {"tar.bz2", "e"}, "f.tar": {"tar", "f"},
		"g.rar": {"", ""}, ".tar": {"", ""}, "h.tar.xz": {"", ""},
	} {
		if format, base := archiveFormat(name); format != want[0] || base != want[1] {
			t.Error(name, format, base)
		}
	}
}

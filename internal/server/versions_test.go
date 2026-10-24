package server

import (
	"encoding/json"
	"os"
	"testing"
)

func TestReplaceKeepsLinksAndHistory(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	file := libraryUpload(t, a, token, "", "report.pdf", "one").Files[0].ID
	link := folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+file+`"]}`, 201)["id"].(string)
	update := libraryUpload(t, a, token, "", "report-v2.pdf", "two-two").Files[0].ID

	folderCall(t, a, token, "POST", "/api/library/files/"+file+"/replace", `{"source":"`+file+`"}`, 404)
	folderCall(t, a, token, "POST", "/api/library/files/"+file+"/replace", `{"source":"`+update+`"}`, 200)
	if w := call(a, "GET", "/api/public/"+link+"/files/"+file, "", ""); w.Code != 200 || w.Body.String() != "two-two" {
		t.Fatal("link did not get the new version", w.Code, w.Body)
	}
	folderCall(t, a, token, "GET", "/api/library/files/"+update+"/download", "", 404)
	var files []LibraryFile
	if w := call(a, "GET", "/api/library/files", "", token); json.Unmarshal(w.Body.Bytes(), &files) != nil || len(files) != 1 || files[0].Name != "report.pdf" || files[0].Size != 7 {
		t.Fatal("replaced file", files)
	}

	history := func() []FileVersion {
		t.Helper()
		var list []FileVersion
		if w := call(a, "GET", "/api/library/files/"+file+"/history", "", token); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return list
	}
	list := history()
	if len(list) != 2 || !list[0].Current || list[0].Name != "report-v2.pdf" || list[0].By != "mati" || list[1].By != "" {
		t.Fatal(list)
	}
	if w := call(a, "GET", "/api/library/files/"+file+"/history/1", "", token); w.Code != 200 || w.Body.String() != "one" {
		t.Fatal("old version", w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/library/files/"+file+"/history/2/restore", `{}`, 409)
	folderCall(t, a, token, "POST", "/api/library/files/"+file+"/history/1/restore", `{}`, 200)
	if w := call(a, "GET", "/api/public/"+link+"/files/"+file, "", ""); w.Body.String() != "one" {
		t.Fatal("restore", w.Body)
	}
	if list = history(); len(list) != 3 || list[0].Version != 3 || !list[0].Current {
		t.Fatal(list)
	}
	// Every version counts: 3 (current) + 3 (original) + 7 (second).
	me := folderCall(t, a, token, "GET", "/api/me", "", 200)
	if used := me["user"].(map[string]any)["used"]; used != float64(13) {
		t.Fatal("storage used", used)
	}
	// Another user can neither see nor change it.
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	other := login(t, a, "other")
	folderCall(t, a, other, "GET", "/api/library/files/"+file+"/history", "", 404)
	folderCall(t, a, other, "GET", "/api/library/files/"+file+"/history/1", "", 404)

	// Deleting the file for good removes every version's bytes.
	var blobs []string
	rows, _ := a.db.Query("SELECT blob FROM text_versions WHERE file_id=?", file)
	for rows.Next() {
		var b string
		rows.Scan(&b)
		blobs = append(blobs, b)
	}
	rows.Close()
	folderCall(t, a, token, "DELETE", "/api/library/files/"+file, `{}`, 200)
	folderCall(t, a, token, "DELETE", "/api/trash/"+file, `{}`, 200)
	for _, b := range blobs {
		if _, e := os.Stat(a.blob(b)); !os.IsNotExist(e) {
			t.Fatal("version left behind", b)
		}
	}
}

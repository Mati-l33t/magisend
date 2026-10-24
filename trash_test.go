package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func trashList(t *testing.T, a *App, token string) []TrashedFile {
	t.Helper()
	w := call(a, "GET", "/api/trash", "", token)
	var list []TrashedFile
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
		t.Fatal(w.Code, w.Body)
	}
	return list
}

func TestTrashHidesRestoresAndPurges(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	u, e := a.addUser("collab", "correct horse battery staple", "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	collab := login(t, a, "collab")
	fid := folderCall(t, a, owner, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	file := libraryUpload(t, a, owner, fid, "plan.txt", "plan").Files[0].ID
	link := folderCall(t, a, owner, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	download := "/api/public/" + link + "/files/" + file

	// Deleted: gone from the library and the link, but in the trash.
	folderCall(t, a, owner, "DELETE", "/api/library/files/"+file, `{}`, 200)
	folderCall(t, a, owner, "GET", "/api/library/files/"+file+"/download", "", 404)
	folderCall(t, a, "", "GET", download, "", 404)
	list := trashList(t, a, owner)
	if len(list) != 1 || list[0].ID != file || list[0].FolderID != fid || list[0].DeletedBy != "" ||
		list[0].PurgeAfter < time.Now().Add(29*24*time.Hour).Unix() {
		t.Fatal(list)
	}
	if len(trashList(t, a, collab)) != 0 {
		t.Fatal("trash is not private")
	}
	folderCall(t, a, collab, "POST", "/api/trash/"+file+"/restore", `{}`, 404)
	folderCall(t, a, collab, "DELETE", "/api/trash/"+file, `{}`, 404)

	// Restored: back in its folder and in the link.
	folderCall(t, a, owner, "POST", "/api/trash/"+file+"/restore", `{}`, 200)
	if w := call(a, "GET", download, "", ""); w.Code != 200 || w.Body.String() != "plan" {
		t.Fatal(w.Code, w.Body)
	}
	if len(trashList(t, a, owner)) != 0 {
		t.Fatal("restored file still in the trash")
	}

	// A recipient's delete lands in the owner's trash, naming them.
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/defaults", `{"grant_edit":true}`, 200)
	folderCall(t, a, owner, "PATCH", "/api/library/shares/"+link, `{"recipients":["collab"],"delete":true}`, 200)
	folderCall(t, a, owner, "PUT", "/api/admin/capabilities/users/"+u.ID, `{"delete_shared":true}`, 200)
	folderCall(t, a, collab, "DELETE", "/api/public/"+link+"/files/"+file, `{}`, 200)
	if list = trashList(t, a, owner); len(list) != 1 || list[0].DeletedBy != "collab" {
		t.Fatal(list)
	}

	// The emptied folder can be deleted; the file then comes back unfiled.
	folderCall(t, a, owner, "DELETE", "/api/folders/"+fid, `{}`, 200)
	folderCall(t, a, owner, "POST", "/api/trash/"+file+"/restore", `{}`, 200)
	var folder *string
	if e = a.db.QueryRow("SELECT folder_id FROM library_files WHERE file_id=?", file).Scan(&folder); e != nil || folder != nil {
		t.Fatal("restored file not unfiled", folder, e)
	}

	// The cleanup waits 30 days; then the bytes are gone.
	folderCall(t, a, owner, "DELETE", "/api/library/files/"+file, `{}`, 200)
	if e = a.Cleanup(time.Now().Add(29 * 24 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(a.blob(file)); e != nil {
		t.Fatal("purged before its day", e)
	}
	if e = a.Cleanup(time.Now().Add(31 * 24 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(a.blob(file)); !os.IsNotExist(e) {
		t.Fatal("trash never emptied", e)
	}

	// Emptying the trash removes everything at once. (The cleanup above also
	// ended the session.)
	owner = login(t, a, "mati")
	other := libraryUpload(t, a, owner, "", "other.txt", "other").Files[0].ID
	folderCall(t, a, owner, "DELETE", "/api/library/files/"+other, `{}`, 200)
	folderCall(t, a, owner, "DELETE", "/api/trash", `{}`, 200)
	if _, e = os.Stat(a.blob(other)); !os.IsNotExist(e) || len(trashList(t, a, owner)) != 0 {
		t.Fatal("empty trash left the file", e)
	}
}

// A file sent on a link that does not keep its files stays restorable even
// after that link expires.
func TestTrashedSentFileOutlivesItsLink(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	sh := createShare(t, a, token, "", 4)
	file := sh.Files[0].ID
	if w := chunk(a, token, file, 0, "sent"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
	folderCall(t, a, token, "DELETE", "/api/library/files/"+file, `{}`, 200)
	folderCall(t, a, token, "DELETE", "/api/shares/"+sh.ID, `{}`, 200)
	folderCall(t, a, token, "POST", "/api/trash/"+file+"/restore", `{}`, 200)
	if w := call(a, "GET", "/api/library/files/"+file+"/download", "", token); w.Code != 200 || w.Body.String() != "sent" {
		t.Fatal(w.Code, w.Body)
	}
}

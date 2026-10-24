package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func visitor(a *App, method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-MagiSend", "1")
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

type startedUpload struct {
	Upload string `json:"upload"`
	Token  string `json:"token"`
	Files  []File `json:"files"`
}

func TestUploadRequestReceivesFilesIntoTheLibrary(t *testing.T) {
	a, admin := emailAdmin(t)
	configureEmail(t, a, admin)
	call(a, "PUT", "/api/account/email", `{"email":"mati@example.com"}`, admin)
	folder := folderCall(t, a, admin, "POST", "/api/folders", `{"name":"Inbox"}`, 201)["id"].(string)
	body := fmt.Sprintf(`{"title":"Tax papers","message":"Please upload your receipts","folder_id":%q,"days":7,"notify":true}`, folder)
	q := folderCall(t, a, admin, "POST", "/api/requests", body, 201)
	id := q["id"].(string)

	info := decodeObject(t, visitor(a, "GET", "/api/requests/"+id, "", nil))
	request := info["request"].(map[string]any)
	if info["owner"] != "mati" || request["title"] != "Tax papers" || request["folder_id"] != "" || info["unlocked"] != true {
		t.Fatal("visitor view", info)
	}
	w := visitor(a, "POST", "/api/requests/"+id+"/uploads", `{"sender":"Anna","files":[{"name":"receipt.pdf","size":5},{"name":"note.txt","size":2}]}`, nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var up startedUpload
	json.Unmarshal(w.Body.Bytes(), &up)
	chunkTo := func(file File, offset int64, data, token string) *httptest.ResponseRecorder {
		return visitor(a, "PATCH", "/api/requests/"+id+"/uploads/"+up.Upload+"/files/"+file.ID, data, map[string]string{"Upload-Offset": fmt.Sprint(offset), "X-Upload-Token": token})
	}
	if w = chunkTo(up.Files[0], 0, "hello", "wrong-token"); w.Code != 404 {
		t.Fatal("chunk accepted without the batch token", w.Code)
	}
	if w = visitor(a, "POST", "/api/requests/"+id+"/uploads/"+up.Upload+"/complete", "{}", map[string]string{"X-Upload-Token": up.Token}); w.Code != 409 {
		t.Fatal("completed before all bytes arrived", w.Code, w.Body)
	}
	if w = chunkTo(up.Files[0], 0, "hel", up.Token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = chunkTo(up.Files[0], 0, "hel", up.Token); w.Code != 409 {
		t.Fatal("wrong offset accepted", w.Code)
	}
	chunkTo(up.Files[0], 3, "lo", up.Token)
	chunkTo(up.Files[1], 0, "hi", up.Token)
	if w = visitor(a, "POST", "/api/requests/"+id+"/uploads/"+up.Upload+"/complete", "{}", map[string]string{"X-Upload-Token": up.Token}); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = chunkTo(up.Files[1], 2, "x", up.Token); w.Code != 409 {
		t.Fatal("wrote to a completed upload", w.Code)
	}
	var files []LibraryFile
	json.Unmarshal(call(a, "GET", "/api/library/files", "", admin).Body.Bytes(), &files)
	received := 0
	for _, f := range files {
		if f.Received {
			received++
			if f.FolderID != folder || f.ReceivedFrom != "Anna" || f.ReceivedVia != "Tax papers" || f.Offset != f.Size {
				t.Fatal("received file", f)
			}
		}
	}
	if received != 2 {
		t.Fatal("files not in the library", files)
	}
	if w = call(a, "GET", "/api/library/files/"+up.Files[0].ID+"/download", "", admin); w.Body.String() != "hello" {
		t.Fatal("content", w.Code, w.Body)
	}
	var list []UploadRequest
	json.Unmarshal(call(a, "GET", "/api/requests", "", admin).Body.Bytes(), &list)
	if len(list) != 1 || list[0].Uploads != 1 || list[0].Files != 2 || list[0].Bytes != 7 || list[0].LastUpload == 0 {
		t.Fatal("owner statistics", list)
	}
	a.flushRequestNotifications(time.Now())
	if mails := queued(a); len(mails) != 1 || !strings.Contains(mails[0].text, "2 files were uploaded to “Tax papers”") {
		t.Fatal("notification", mails)
	}
	// Closing the request stops new uploads; files stay.
	folderCall(t, a, admin, "PATCH", "/api/requests/"+id, `{"closed":true}`, 200)
	if w = visitor(a, "POST", "/api/requests/"+id+"/uploads", `{"files":[{"name":"late.txt","size":1}]}`, nil); w.Code != 410 {
		t.Fatal("closed request accepted an upload", w.Code)
	}
	folderCall(t, a, admin, "DELETE", "/api/requests/"+id, `{}`, 200)
	if w = visitor(a, "GET", "/api/requests/"+id, "", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	json.Unmarshal(call(a, "GET", "/api/library/files", "", admin).Body.Bytes(), &files)
	if len(files) != 2 {
		t.Fatal("deleting the request must keep received files", len(files))
	}
}

func TestProtectedUploadRequestAndLimits(t *testing.T) {
	a, admin := emailAdmin(t)
	id := folderCall(t, a, admin, "POST", "/api/requests", `{"title":"Private","password":"request-password"}`, 201)["id"].(string)
	info := decodeObject(t, visitor(a, "GET", "/api/requests/"+id, "", nil))
	if info["unlocked"] != false || info["request"].(map[string]any)["protected"] != true {
		t.Fatal(info)
	}
	start := `{"files":[{"name":"a.txt","size":1}]}`
	if w := visitor(a, "POST", "/api/requests/"+id+"/uploads", start, nil); w.Code != 401 {
		t.Fatal("uploaded without the password", w.Code)
	}
	if w := visitor(a, "POST", "/api/requests/"+id+"/unlock", `{"password":"nope"}`, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := visitor(a, "POST", "/api/requests/"+id+"/unlock", `{"password":"request-password"}`, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	grant := w.Result().Cookies()[0]
	for body, status := range map[string]int{
		`{"files":[{"name":"../x","size":1}]}`: 400,
		`{"files":[]}`:                         400,
		`{"files":[{"name":"big.bin","size":` + fmt.Sprint(2*GiB) + `}]}`:              400,
		`{"sender":"` + strings.Repeat("n", 81) + `","files":[{"name":"a","size":1}]}`: 400,
	} {
		if w = visitor(a, "POST", "/api/requests/"+id+"/uploads", body, nil, grant); w.Code != status {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	// At most a few unfinished uploads may hold reservations at once.
	codes := []int{}
	for i := 0; i < maxOpenRequestBatches+1; i++ {
		codes = append(codes, visitor(a, "POST", "/api/requests/"+id+"/uploads", start, nil, grant).Code)
	}
	if codes[0] != 201 || codes[len(codes)-1] != 429 {
		t.Fatal("open uploads not bounded", codes)
	}
	// Another user cannot manage the request.
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), admin)
	bob := login(t, a, "bob")
	folderCall(t, a, bob, "PATCH", "/api/requests/"+id, `{"closed":true}`, 404)
	folderCall(t, a, bob, "DELETE", "/api/requests/"+id, `{}`, 404)
	// Expired requests refuse visitors.
	folderCall(t, a, admin, "PATCH", "/api/requests/"+id, `{"days":1}`, 200)
	a.db.Exec("UPDATE upload_requests SET expires=? WHERE id=?", time.Now().Unix()-1, id)
	if w = visitor(a, "GET", "/api/requests/"+id, "", nil); w.Code != 410 {
		t.Fatal(w.Code)
	}
}

func TestUploadRequestRespectsOwnerQuota(t *testing.T) {
	a, admin := emailAdmin(t)
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":10}`, testPassword), admin)
	bob := login(t, a, "bob")
	id := folderCall(t, a, bob, "POST", "/api/requests", `{"title":"Small"}`, 201)["id"].(string)
	if w := visitor(a, "POST", "/api/requests/"+id+"/uploads", `{"files":[{"name":"a.bin","size":11}]}`, nil); w.Code != 413 {
		t.Fatal("quota not enforced", w.Code, w.Body)
	}
	if w := visitor(a, "POST", "/api/requests/"+id+"/uploads", `{"files":[{"name":"a.bin","size":10}]}`, nil); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
}

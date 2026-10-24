package server

import (
	"encoding/json"
	"testing"
)

func TestPendingReservationsAndBoundary(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	p, e := a.policy()
	if e != nil {
		t.Fatal(e)
	}
	p.MaxFile = 5
	p.MaxShare = 5
	raw, _ := json.Marshal(p)
	folderCall(t, a, token, "PUT", "/api/admin/policy", string(raw), 200)
	folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":true}`, 200)
	fid := folderCall(t, a, token, "POST", "/api/folders", `{"name":"limits"}`, 201)["id"].(string)
	first := libraryUpload(t, a, token, fid, "first.txt", "abc").Files[0].ID
	sid := folderCall(t, a, token, "POST", "/api/library/shares", `{"folder_id":"`+fid+`"}`, 201)["id"].(string)
	// Exact boundary accepted, even before the second file has uploaded bytes.
	pending := folderCall(t, a, token, "POST", "/api/library/uploads", `{"folder_id":"`+fid+`","files":[{"name":"pending.txt","size":2}]}`, 201)
	var pendingShare Share
	b, _ := json.Marshal(pending)
	if e = json.Unmarshal(b, &pendingShare); e != nil {
		t.Fatal(e)
	}
	folderCall(t, a, token, "POST", "/api/library/uploads", `{"folder_id":"`+fid+`","files":[{"name":"overflow.txt","size":1}]}`, 413)
	// Editing a visible member must count those pending reservations too.
	folderCall(t, a, token, "PUT", "/api/library/files/"+first+"/text", textBody(1, "abcd"), 413)
	var state map[string]any
	w := call(a, "GET", "/api/library/files/"+first+"/text", "", token)
	if e = json.Unmarshal(w.Body.Bytes(), &state); e != nil || state["text"] != "abc" {
		t.Fatal("denied edit changed content", state, e)
	}
	folderCall(t, a, token, "DELETE", "/api/library/files/"+pendingShare.Files[0].ID, `{}`, 200)
	// Once reservation is released, growth to the exact maximum succeeds.
	folderCall(t, a, token, "PUT", "/api/library/files/"+first+"/text", textBody(1, "abcde"), 200)
	// A lowered policy may not trap users: shrinking and moving out remain possible.
	p.MaxFile = 2
	p.MaxShare = 2
	raw, _ = json.Marshal(p)
	folderCall(t, a, token, "PUT", "/api/admin/policy", string(raw), 200)
	folderCall(t, a, token, "PUT", "/api/library/files/"+first+"/text", textBody(2, "ab"), 200)
	folderCall(t, a, token, "PATCH", "/api/library/files/"+first, `{"folder_id":""}`, 200)
	public := folderCall(t, a, "", "GET", "/api/public/"+sid, "", 200)
	if len(public["files"].([]any)) != 0 {
		t.Fatal("move-out left live membership")
	}
}

package server

import "testing"

func TestLibraryDraftCannotBeRenamedOrMoved(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	sh := createShare(t, a, token, "", 4)
	id := sh.Files[0].ID
	folderCall(t, a, token, "PATCH", "/api/library/files/"+id, `{"name":"renamed.txt"}`, 409)
	folderCall(t, a, token, "PATCH", "/api/library/files/"+id, `{"folder_id":""}`, 409)
	if w := chunk(a, token, id, 0, "data"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "PATCH", "/api/library/files/"+id, `{"name":"renamed.txt"}`, 409)
}

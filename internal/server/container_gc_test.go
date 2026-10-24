package server

import "testing"

// Isolation regression for gcEmptyContainers (L1): a successful single-file
// delete must garbage-collect only its own fully-deleted container. Files of
// other containers, and any file whose tombstone is still pending because its
// blob could not be removed, must remain intact with their quota reservation.
func TestContainerGCNeverDeletesPendingOrUnrelatedFiles(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	folderCall(t, a, token, "PUT", "/api/admin/capabilities/defaults", `{"edit_own":true}`, 200)
	pending := libraryUpload(t, a, token, "", "pending.txt", "pending").Files[0].ID
	healthy := libraryUpload(t, a, token, "", "healthy.txt", "healthy").Files[0].ID
	removed := libraryUpload(t, a, token, "", "removed.txt", "removed").Files[0].ID
	// A previously failed deletion (blob removal failed after the tombstone
	// committed) must retain metadata and quota reservation until the storage
	// operation actually succeeds; GC of an unrelated delete must not bypass it.
	if e := exec(a, "INSERT INTO file_deleting(file_id) VALUES(?)", pending); e != nil {
		t.Fatal(e)
	}
	if w := call(a, "DELETE", "/api/library/files/"+removed, `{}`, token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "DELETE", "/api/trash/"+removed, `{}`, token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for _, id := range []string{pending, healthy} {
		var n int
		if e := a.db.QueryRow("SELECT COUNT(*) FROM files WHERE id=?", id).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n != 1 {
			t.Fatalf("GC deleted unrelated or pending file %s before its blob removal; data/quota lost", id)
		}
	}
	var tombstones int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM file_deleting WHERE file_id=?", pending).Scan(&tombstones); e != nil || tombstones != 1 {
		t.Fatalf("pending tombstone must survive unrelated GC: %d %v", tombstones, e)
	}
	var remaining int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM files WHERE id=?", removed).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatalf("the successfully deleted file must be gone: %d %v", remaining, e)
	}
}

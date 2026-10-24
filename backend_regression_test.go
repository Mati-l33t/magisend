package main

import (
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStrictEOF(t *testing.T) {
	for _, tail := range []string{"!", " {", " null"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"x"}`+tail))
		var b struct {
			Name string `json:"name"`
		}
		if decode(w, r, &b) {
			t.Errorf("accepted trailing %q", tail)
		}
	}
}

type observedBody struct{ reads int }

func (b *observedBody) Read(p []byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *observedBody) Close() error               { return nil }
func TestUploadAuthBeforeRead(t *testing.T) {
	a := testApp(t)
	b := &observedBody{}
	r := httptest.NewRequest("PATCH", "/api/uploads/no-file", b)
	r.Header.Set("X-MagiSend", "1")
	r.Header.Set("Upload-Offset", "0")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 || b.reads != 0 {
		t.Fatalf("status=%d body reads=%d", w.Code, b.reads)
	}
}

func TestResumeFailsClosed(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			a := testApp(t)
			setupAdmin(t, a)
			token := login(t, a, "mati")
			s := createShare(t, a, token, "", 6)
			id := s.Files[0].ID
			if w := chunk(a, token, id, 0, "abc"); w.Code != 200 {
				t.Fatal(w.Code)
			}
			var e error
			if missing {
				e = os.Remove(a.blob(id))
			} else {
				e = os.Truncate(a.blob(id), 1)
			}
			if e != nil {
				t.Fatal(e)
			}
			if w := chunk(a, token, id, 3, "def"); w.Code != 503 {
				t.Fatalf("unsafe resume: %d %s", w.Code, w.Body)
			}
			var offset int64
			if e = a.db.QueryRow("SELECT offset FROM files WHERE id=?", id).Scan(&offset); e != nil || offset != 3 {
				t.Fatal(offset, e)
			}
			info, e := os.Stat(a.blob(id))
			if missing {
				if !os.IsNotExist(e) {
					t.Fatal("recreated missing blob")
				}
			} else if e != nil || info.Size() != 1 {
				t.Fatal("extended truncated blob", e)
			}
		})
	}
}
func TestExistingDBMissingRoot(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	a.db.Close()
	if e = os.Remove(filepath.Join(root, "blobs")); e != nil {
		t.Fatal(e)
	}
	b, e := NewApp(root, "")
	if e == nil {
		b.db.Close()
		t.Fatal("existing db recreated unavailable storage")
	}
	if _, e = os.Stat(filepath.Join(root, "blobs")); !os.IsNotExist(e) {
		t.Fatal("storage recreated")
	}
}
func TestDeletionAndIndependentCleanup(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	bad := createShare(t, a, token, "", 0)
	good := createShare(t, a, token, "", 0)
	for _, s := range []Share{bad, good} {
		if w := call(a, "POST", "/api/shares/"+s.ID+"/publish", "{}", token); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	path := a.blob(bad.Files[0].ID)
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "blocked"), []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if w := call(a, "DELETE", "/api/shares/"+bad.ID, "", token); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if w := call(a, "GET", "/api/public/"+bad.ID, "", ""); w.Code != 404 {
		t.Fatal("failed deletion still public", w.Code)
	}
	// Zero now means permanent; retain this test's genuinely expired fixture.
	if e := exec(a, "UPDATE shares SET expires=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), good.ID); e != nil {
		t.Fatal(e)
	}
	a.db.Exec("INSERT INTO sessions(hash,user_id,expires) SELECT 'expired',id,0 FROM users LIMIT 1")
	if e := a.Cleanup(time.Now()); e == nil {
		t.Fatal("missing deletion error")
	}
	if _, _, _, e := a.loadShare(good.ID); e == nil {
		t.Fatal("independent share not cleaned")
	}
	var n int
	a.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE hash='expired'").Scan(&n)
	if n != 0 {
		t.Fatal("session pruning blocked")
	}
	if e := os.RemoveAll(path); e != nil {
		t.Fatal(e)
	}
	if e := a.Cleanup(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := a.loadShare(bad.ID); e == nil {
		t.Fatal("tombstone not retried")
	}
}

type blockedBody struct{ entered, release chan struct{} }

func (b *blockedBody) Read(p []byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return 0, io.EOF
}
func (b *blockedBody) Close() error { return nil }
func TestSlowJSONDoesNotLock(t *testing.T) {
	for _, path := range []string{"/api/setup", "/api/login", "/api/register", "/api/public/test/unlock"} {
		t.Run(path, func(t *testing.T) {
			a := testApp(t)
			a.db.Exec("INSERT INTO users VALUES('u','user','invalid','admin',0)")
			a.db.Exec("UPDATE settings SET registration_open=1")
			a.db.Exec("INSERT INTO shares VALUES('test','u','test','',?, ?,1)", time.Now().Add(time.Hour).Unix(), time.Now().Unix())
			b := &blockedBody{make(chan struct{}, 1), make(chan struct{})}
			r := httptest.NewRequest("POST", path, b)
			r.Header.Set("X-MagiSend", "1")
			done := make(chan struct{})
			go func() { a.Handler().ServeHTTP(httptest.NewRecorder(), r); close(done) }()
			select {
			case <-b.entered:
			case <-done:
				return
			case <-time.After(time.Second):
				close(b.release)
				t.Fatal("body not reached")
			}
			locked := make(chan struct{})
			go func() { a.mu.Lock(); a.mu.Unlock(); close(locked) }()
			select {
			case <-locked:
			case <-time.After(200 * time.Millisecond):
				t.Error("slow body holds mutex")
			}
			close(b.release)
			<-done
			<-locked
		})
	}
}
func TestMetadataCaps(t *testing.T) {
	for _, kind := range []string{"shares", "files"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			setupAdmin(t, a)
			token := login(t, a, "mati")
			var owner string
			a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&owner)
			tx, e := a.db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			count := totalShareLimit
			if kind != "shares" {
				count = defaultFilesPerUser
			}
			for i := 0; i < count; i++ {
				id := fmt.Sprint(i)
				if kind == "shares" {
					_, e = tx.Exec("INSERT INTO shares(id,owner,title,expires,created) VALUES(?,?, '',9999999999,0)", id, owner)
				} else {
					if i == 0 {
						_, e = tx.Exec("INSERT INTO shares(id,owner,title,expires,created) VALUES('s',?,'',9999999999,0)", owner)
					}
					if e == nil {
						_, e = tx.Exec("INSERT INTO files(id,share_id,name,size) VALUES(?,'s',?,0)", id, id)
					}
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			w := call(a, "POST", "/api/shares", `{"files":[{"name":"empty","size":0}]}`, token)
			if w.Code != 429 {
				t.Fatalf("unbounded %s: %d", kind, w.Code)
			}
		})
	}
}

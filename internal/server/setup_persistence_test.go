package server

import (
	"encoding/json"
	"testing"
)

func TestSetupCompletionSurvivesRestartAndEmptyUserTable(t *testing.T) {
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	w := call(a, "POST", "/api/setup", `{"username":"first","password":"correct horse battery staple"}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	// Simulate a later recovery/account-removal operation: never reopen bootstrap.
	if _, e = a.db.Exec("DELETE FROM users"); e != nil {
		t.Fatal(e)
	}
	a.db.Close()
	a, e = NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	defer a.db.Close()
	w = call(a, "GET", "/api/status", "", "")
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if status["setup_required"] != false {
		t.Fatalf("setup reopened after restart: %s", w.Body)
	}
	if w = call(a, "POST", "/api/setup", `{"username":"replacement","password":"correct horse battery staple"}`, ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}

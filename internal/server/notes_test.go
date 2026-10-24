package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNoteTitles(t *testing.T) {
	for _, c := range []struct{ title, text, name string }{
		{"", "# Shopping list\n- [ ] milk", "Shopping list.md"},
		{"", "\n\n- [x] **Call** the bank", "Call the bank.md"},
		{"Trip / plans", "anything", "Trip - plans.md"},
		{"  report.md ", "x", "report.md"},
		{"", "A very long first line that goes on and on about many different things to remember", "A very long first line that goes on and on about many….md"},
	} {
		if name, _ := noteName(c.title, c.text, "en"); name != c.name {
			t.Fatalf("%q %q → %q, want %q", c.title, c.text, name, c.name)
		}
	}
	if name, auto := noteName("", "  ", "sv"); !strings.HasPrefix(name, "Anteckning ") || !auto {
		t.Fatal(name)
	}
}

func TestNotesCreateSaveAndShare(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	n := folderCall(t, a, token, "POST", "/api/library/notes", `{"title":"","text":"# Shopping\n- [ ] milk","language":"sv"}`, 201)
	id := n["id"].(string)
	if n["name"] != "Shopping.md" || n["auto"] != true {
		t.Fatal(n)
	}
	if n["folder_id"] != "" {
		t.Fatal("a note written outside a folder is unfiled", n)
	}
	work := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	if in := folderCall(t, a, token, "POST", "/api/library/notes", `{"title":"Plan","text":"","folder_id":"`+work+`"}`, 201); in["folder_id"] != work || in["name"] != "Plan.md" {
		t.Fatal(in)
	}
	state := folderCall(t, a, token, "GET", "/api/library/files/"+id+"/text", "", 200)
	version := int(state["version"].(float64))
	save := func(version int, title, text string, status int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"version": version, "title": title, "text": text})
		return folderCall(t, a, token, "PUT", "/api/library/notes/"+id, string(body), status)
	}
	// An automatic title follows the first line.
	got := save(version, "", "# Groceries\n- [ ] milk", 200)
	if got["name"] != "Groceries.md" || got["auto"] != true {
		t.Fatal(got)
	}
	version = int(got["version"].(float64))
	save(version-1, "", "stale", 409)
	// A typed title stays when the text changes.
	got = save(version, "Weekly shop", "# Groceries\n- [x] milk", 200)
	version = int(got["version"].(float64))
	got = save(version, "Weekly shop", "# Something else", 200)
	if got["name"] != "Weekly shop.md" || got["auto"] != false {
		t.Fatal(got)
	}
	// Quick saves are one version after the original.
	var history []FileVersion
	json.Unmarshal(call(a, "GET", "/api/library/files/"+id+"/history", "", token).Body.Bytes(), &history)
	if len(history) != 2 || !history[0].Current {
		t.Fatal("autosave filled the history", history)
	}
	var notes []Note
	json.Unmarshal(call(a, "GET", "/api/library/notes", "", token).Body.Bytes(), &notes)
	if len(notes) != 2 || notes[0].Excerpt != "# Something else" || notes[0].Auto {
		t.Fatal(notes)
	}
	// Shared like any file.
	link := folderCall(t, a, token, "POST", "/api/library/shares", `{"files":["`+id+`"]}`, 201)["id"].(string)
	if w := call(a, "GET", "/api/public/"+link+"/files/"+id+"/media", "", ""); w.Code != 200 || w.Body.String() != "# Something else" {
		t.Fatal(w.Code, w.Body)
	}
	// Only the owner saves it.
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]any{"version": int(got["version"].(float64)), "title": "", "text": "mine"})
	folderCall(t, a, login(t, a, "other"), "PUT", "/api/library/notes/"+id, string(body), 404)
	// Not a note.
	plain := libraryUpload(t, a, token, "", "a.txt", "text").Files[0].ID
	folderCall(t, a, token, "PUT", "/api/library/notes/"+plain, `{"version":1,"text":"x","title":""}`, 415)
}

func TestNoteMoveAndRename(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	id := folderCall(t, a, token, "POST", "/api/library/notes", `{"title":"","text":"first line"}`, 201)["id"].(string)
	work := folderCall(t, a, token, "POST", "/api/folders", `{"name":"Work"}`, 201)["id"].(string)
	folderCall(t, a, token, "PATCH", "/api/library/files/"+id, `{"folder_id":"`+work+`"}`, 200)
	var notes []Note
	json.Unmarshal(call(a, "GET", "/api/library/notes", "", token).Body.Bytes(), &notes)
	if len(notes) != 1 || notes[0].FolderID != work || !notes[0].Auto {
		t.Fatal("moving keeps the automatic title", notes)
	}
	// A note renamed by hand keeps that name when its text changes.
	folderCall(t, a, token, "PATCH", "/api/library/files/"+id, `{"name":"Kept.md"}`, 200)
	state := folderCall(t, a, token, "GET", "/api/library/files/"+id+"/text", "", 200)
	body, _ := json.Marshal(map[string]any{"version": int(state["version"].(float64)), "title": "Kept", "text": "other line"})
	if got := folderCall(t, a, token, "PUT", "/api/library/notes/"+id, string(body), 200); got["name"] != "Kept.md" || got["auto"] != false {
		t.Fatal(got)
	}
}

// Long titles in scripts with multibyte characters still make a valid name.
func TestLongMultibyteTitles(t *testing.T) {
	for _, title := range []string{strings.Repeat("日本語のタイトル", 20), strings.Repeat("😀", 120)} {
		name, _ := noteName(title, "", "en")
		if !validFilename(name) {
			t.Fatal("invalid name for", len(title), "bytes:", len(name))
		}
		name, _ = noteName("", title+"\nbody", "en")
		if !validFilename(name) {
			t.Fatal("invalid derived name", len(name))
		}
	}
}

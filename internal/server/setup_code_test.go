package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupNeedsTheSetupCode(t *testing.T) {
	t.Setenv("MAGISEND_SETUP_CODE", "")
	root := t.TempDir()
	a, e := NewApp(root, "")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, setupCodeFile)
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("the setup code file is missing or readable by others", e)
	}
	data, _ := os.ReadFile(path)
	code := strings.TrimSpace(string(data))
	if len(normalSetupCode(code)) != 12 || strings.Count(code, "-") != 2 {
		t.Fatal("unexpected code", code)
	}
	body := func(code string) string {
		return `{"setup_code":"` + code + `","username":"first","password":"correct horse battery staple"}`
	}
	for _, wrong := range []string{"", "AAAA-BBBB-CCCC"} {
		if w := call(a, "POST", "/api/setup", body(wrong), ""); w.Code != 403 || !strings.Contains(w.Body.String(), "setup_code_invalid") {
			t.Fatal(w.Code, w.Body)
		}
	}
	// A restart keeps the same code.
	a.db.Close()
	if a, e = NewApp(root, ""); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.db.Close() })
	if a.setupCode != normalSetupCode(code) {
		t.Fatal("the code changed on restart")
	}
	// Typed in lower case with spaces it still matches; afterwards the file is gone.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	if w := call(a, "POST", "/api/setup", body(typed), ""); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("the setup code file was kept", e)
	}
}

func TestSetupCodeGuessesAreLimited(t *testing.T) {
	a := testApp(t)
	wrong := `{"setup_code":"AAAA-BBBB-CCCC","username":"first","password":"correct horse battery staple"}`
	for i := 0; i < setupFailures; i++ {
		if w := call(a, "POST", "/api/setup", wrong, ""); w.Code != 403 {
			t.Fatal(i, w.Code, w.Body)
		}
	}
	right := `{"setup_code":"TEST-SETUP-CODE","username":"first","password":"correct horse battery staple"}`
	if w := call(a, "POST", "/api/setup", right, ""); w.Code != 429 {
		t.Fatal("guessing was not slowed down", w.Code, w.Body)
	}
}

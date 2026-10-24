package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestAPIErrorIsEnglishWithStableTranslationCode(t *testing.T) {
	a := testApp(t)
	w := call(a, "GET", "/api/me", "", "")
	var body map[string]string
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if body["error_code"] != "login_required" || body["error"] != "Log in to continue" {
		t.Fatalf("untranslated/uncoded API error: %v", body)
	}
	w = call(a, "POST", "/api/setup", `{"username":"A","password":"short"}`, "")
	json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 400 || body["error_code"] != "invalid_credentials" {
		t.Fatal(w.Code, body)
	}
}

func TestUnknownServerErrorDoesNotLeakPrivateDetails(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, 503, "sqlite error /private/path")
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error_code"] != "operation_failed" || body["error"] != "The operation could not be completed" {
		t.Fatal(body)
	}
}

package server

import "testing"

func TestShortPasswordIsRejectedForAppAdmin(t *testing.T) {
	a := testApp(t)
	w := call(a, "POST", "/api/setup", `{"setup_code":"TEST-SETUP-CODE","username":"root","password":"test123456"}`, "")
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}

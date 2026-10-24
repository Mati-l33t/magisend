package server

import (
	"fmt"
	"strings"
	"testing"
)

// A new password ends upload tokens as well as sessions.
func TestPasswordChangesRevokeUploadTokens(t *testing.T) {
	a, admin := emailAdmin(t)
	token := func(session string) string {
		return decodeObject(t, call(a, "POST", "/api/account/tokens", `{"name":"ShareX"}`, session))["token"].(string)
	}
	upload := func(token string) int { return tokenUpload(a, token, "", map[string]string{"a.txt": "x"}).Code }
	mine := token(admin)
	if upload(mine) != 201 {
		t.Fatal("token does not work")
	}
	if w := call(a, "POST", "/api/account/password", `{"current":"`+testPassword+`","password":"a different long passphrase"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if upload(mine) != 401 {
		t.Fatal("a token survived changing the password")
	}
	bob, e := a.addUser("bob", testPassword, "user", GiB)
	if e != nil {
		t.Fatal(e)
	}
	bobs := token(login(t, a, "bob"))
	if w := call(a, "PATCH", "/api/admin/users/"+bob.ID, fmt.Sprintf(`{"password":%q}`, "reset by the admin here"), admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if upload(bobs) != 401 {
		t.Fatal("a token survived an administrator's reset")
	}
}

// Reading a file's text is a download, so limited links do not offer it.
func TestTextIsNotReadableOnLimitedLinks(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	owner := login(t, a, "mati")
	sh := limitedShare(t, a, owner, 1)
	path := "/api/public/" + sh.ID + "/files/" + sh.Files[0].ID + "/text"
	for range 3 {
		if w := fetch(a, "GET", path, ""); w.Code != 403 {
			t.Fatal("text of a limited link was readable", w.Code)
		}
	}
}

func TestSlotsGroupIPv6ByHousehold(t *testing.T) {
	a := testApp(t)
	keys := map[string]bool{}
	for i := 0; i < 256; i += 37 {
		r := fetchRequest(fmt.Sprintf("[2001:db8:1:%x::1]:443", i))
		keys[slotClient(a.clientIP(r))] = true
	}
	if len(keys) != 1 {
		t.Fatal("one /56 counted as several clients", keys)
	}
	if got := slotClient("198.51.100.1"); got != "198.51.100.1" {
		t.Fatal(got)
	}
	if got := slotClient(a.clientIP(fetchRequest("[2001:db8:2::1]:443"))); !strings.HasSuffix(got, "/56") || keys[got] {
		t.Fatal("another household shares the key", got)
	}
}

package main

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var tokenInMail = regexp.MustCompile(`/(invite|reset)/([a-f0-9]{48})`)

func mailToken(t *testing.T, m outgoingMail) (string, string) {
	t.Helper()
	found := tokenInMail.FindStringSubmatch(m.text)
	if found == nil {
		t.Fatal("no link in", m.text)
	}
	return found[0], found[2]
}

func TestSiteAddressForEmailLinks(t *testing.T) {
	a, admin := emailAdmin(t)
	for _, bad := range []string{"ftp://x.se", "https://user@x.se", "javascript:alert(1)", "https://x_y.se", "https://x.se:port"} {
		body := fmt.Sprintf(`{"host":"smtp.example.com","port":587,"security":"starttls","from":"share@example.com","site_url":%q}`, bad)
		if w := call(a, "PUT", "/api/admin/email", body, admin); w.Code != 400 {
			t.Fatal("accepted", bad, w.Code)
		}
	}
	configureEmail(t, a, admin)
	if s, _ := a.emailSettings(); s.SiteURL != "http://example.com" {
		t.Fatal("the admin's own address must be the default", s.SiteURL)
	}
	body := `{"host":"smtp.example.com","port":587,"security":"starttls","from":"share@example.com","site_url":"https://share.example.com/"}`
	call(a, "PUT", "/api/admin/email", body, admin)
	if a.linkOrigin() != "https://share.example.com" {
		t.Fatal(a.linkOrigin())
	}
}

func TestInvitationCreatesAnAccount(t *testing.T) {
	a, admin := emailAdmin(t)
	if w := call(a, "POST", "/api/admin/invites", `{"email":"new@example.com"}`, admin); w.Code != 409 {
		t.Fatal("invited without email settings", w.Code)
	}
	configureEmail(t, a, admin)
	if w := call(a, "POST", "/api/admin/invites", `{"email":"new@example.com","quota":1048576,"language":"sv"}`, admin); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	mails := queued(a)
	if len(mails) != 1 || mails[0].to != "new@example.com" || !strings.Contains(mails[0].subject, "mati har bjudit in dig") {
		t.Fatal(mails)
	}
	link, token := mailToken(t, mails[0])
	if !strings.HasPrefix(link, "/invite/") || !strings.Contains(mails[0].text, "http://example.com/invite/") {
		t.Fatal(mails[0].text)
	}
	if list := decodeList(t, call(a, "GET", "/api/admin/invites", "", admin)); len(list) != 1 || list[0]["email"] != "new@example.com" {
		t.Fatal(list)
	}
	if w := call(a, "GET", "/api/invites/"+token, "", ""); w.Code != 200 || decodeObject(t, w)["email"] != "new@example.com" {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/invites/"+token, `{"username":"newbie","password":"short"}`, ""); w.Code != 400 {
		t.Fatal("weak password accepted", w.Code)
	}
	if w := call(a, "POST", "/api/invites/"+token, fmt.Sprintf(`{"username":"newbie","password":%q}`, testPassword), ""); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/invites/"+token, fmt.Sprintf(`{"username":"second","password":%q}`, testPassword), ""); w.Code != 404 {
		t.Fatal("invitation used twice", w.Code)
	}
	newbie := login(t, a, "newbie")
	user := decodeObject(t, call(a, "GET", "/api/me", "", newbie))["user"].(map[string]any)
	if user["email"] != "new@example.com" || user["quota"] != float64(1048576) || user["role"] != "user" {
		t.Fatal(user)
	}
	if w := call(a, "POST", "/api/admin/invites", `{"email":"x@example.com"}`, newbie); w.Code != 403 {
		t.Fatal("a user sent an invitation", w.Code)
	}
}

func TestPasswordResetByEmail(t *testing.T) {
	a, admin := emailAdmin(t)
	if decodeObject(t, call(a, "GET", "/api/status", "", ""))["password_reset"] != false {
		t.Fatal("reset offered without email")
	}
	configureEmail(t, a, admin)
	call(a, "PUT", "/api/account/email", `{"email":"mati@example.com"}`, admin)
	if decodeObject(t, call(a, "GET", "/api/status", "", ""))["password_reset"] != true {
		t.Fatal("reset not offered")
	}
	// Unknown accounts get the same answer and no email.
	if w := call(a, "POST", "/api/password/forgot", `{"login":"nobody"}`, ""); w.Code != 200 || len(queued(a)) != 0 {
		t.Fatal(w.Code)
	}
	// A forged Host header never ends up in the link.
	r := httptest.NewRequest("POST", "/api/password/forgot", strings.NewReader(`{"login":"MATI@example.com"}`))
	r.Host = "evil.example.net"
	r.Header.Set("X-MagiSend", "1")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	mails := queued(a)
	if w.Code != 200 || len(mails) != 1 || strings.Contains(mails[0].text, "evil") || !strings.Contains(mails[0].text, "http://example.com/reset/") {
		t.Fatal(w.Code, mails)
	}
	_, token := mailToken(t, mails[0])
	if w = call(a, "GET", "/api/password/reset/"+token, "", ""); decodeObject(t, w)["username"] != "mati" {
		t.Fatal(w.Body)
	}
	if w = call(a, "POST", "/api/password/reset/"+token, `{"password":"short"}`, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = call(a, "POST", "/api/password/reset/"+token, `{"password":"a brand new long passphrase"}`, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = call(a, "GET", "/api/me", "", admin); w.Code != 401 {
		t.Fatal("old session survived the reset", w.Code)
	}
	if w = call(a, "POST", "/api/password/reset/"+token, `{"password":"another long passphrase"}`, ""); w.Code != 404 {
		t.Fatal("reset link used twice", w.Code)
	}
	if w = call(a, "POST", "/api/login", `{"username":"mati","password":"a brand new long passphrase"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// At most a few reset emails per account and hour.
	for i := 0; i < resetsPerAccountHour+2; i++ {
		call(a, "POST", "/api/password/forgot", `{"login":"mati"}`, "")
	}
	if n := len(queued(a)); n != resetsPerAccountHour {
		t.Fatal("reset emails not limited", n)
	}
}

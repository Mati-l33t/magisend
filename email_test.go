package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidEmail(t *testing.T) {
	for _, good := range []string{"mati@example.com", "a.b+tag@sub.example.se"} {
		if _, ok := validEmail(good); !ok {
			t.Fatal("rejected", good)
		}
	}
	for _, bad := range []string{"", "nobody", "a@b", "Mati <mati@example.com>", "a@example.com\r\nBcc: x@evil.com",
		"a@example.com, b@example.com", "a@example.com;b@example.com", strings.Repeat("a", 250) + "@example.com"} {
		if _, ok := validEmail(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// fakeSMTP accepts one plain SMTP session and returns what it received.
func fakeSMTP(t *testing.T) (int, <-chan string) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	received := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		reply := func(line string) { fmt.Fprintf(conn, "%s\r\n", line) }
		reply("220 test ESMTP")
		var transcript strings.Builder
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			transcript.WriteString(line)
			switch command := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				reply("250-test\r\n250 8BITMIME")
			case strings.HasPrefix(command, "DATA"):
				reply("354 go ahead")
				for {
					data, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					if data == ".\r\n" {
						break
					}
					transcript.WriteString(data)
				}
				reply("250 queued")
			case strings.HasPrefix(command, "QUIT"):
				reply("221 bye")
				received <- transcript.String()
				return
			default:
				reply("250 ok")
			}
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, received
}

func emailAdmin(t *testing.T) (*App, string) {
	a := testApp(t)
	setupAdmin(t, a)
	return a, login(t, a, "mati")
}

func TestEmailSettingsAreAdminOnlyAndHidePassword(t *testing.T) {
	a, admin := emailAdmin(t)
	body := `{"host":"smtp.example.com","port":587,"security":"starttls","username":"mailer","password":"s3cret","from":"share@example.com","from_name":"MagiSend"}`
	if w := call(a, "PUT", "/api/admin/email", body, admin); w.Code != 200 || strings.Contains(w.Body.String(), "s3cret") {
		t.Fatal(w.Code, w.Body)
	}
	w := call(a, "GET", "/api/admin/email", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "s3cret") || decodeObject(t, w)["password_set"] != true {
		t.Fatal(w.Code, w.Body)
	}
	keep := strings.Replace(body, `"password":"s3cret",`, "", 1)
	call(a, "PUT", "/api/admin/email", keep, admin)
	if s, _ := a.emailSettings(); s.Password != "s3cret" {
		t.Fatal("saving without a password must keep the stored one")
	}
	for _, bad := range []string{
		strings.Replace(body, "smtp.example.com", "smtp.example.com\r\nX: y", 1),
		strings.Replace(body, "starttls", "ssl3", 1),
		strings.Replace(body, "share@example.com", "Share <share@example.com>", 1),
	} {
		if w = call(a, "PUT", "/api/admin/email", bad, admin); w.Code != 400 {
			t.Fatal("accepted invalid settings", w.Code, bad)
		}
	}
	body2 := fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword)
	call(a, "POST", "/api/admin/users", body2, admin)
	bob := login(t, a, "bob")
	for _, request := range [][2]string{{"GET", ""}, {"PUT", body}} {
		if w = call(a, request[0], "/api/admin/email", request[1], bob); w.Code != 403 {
			t.Fatal("non-administrator reached email settings", request[0], w.Code)
		}
	}
}

func TestTestEmailSpeaksSMTP(t *testing.T) {
	a, admin := emailAdmin(t)
	port, received := fakeSMTP(t)
	body := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"security":"none","from":"share@example.com","from_name":"Mati's Files"}`, port)
	if w := call(a, "PUT", "/api/admin/email", body, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/admin/email/test", `{"to":"mati@example.com","language":"sv"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	select {
	case transcript := <-received:
		for _, want := range []string{"MAIL FROM:<share@example.com>", "RCPT TO:<mati@example.com>", "Subject: =?utf-8?q?Testmejl_fr=C3=A5n_MagiSend?=", "Content-Type: multipart/alternative"} {
			if !strings.Contains(transcript, want) {
				t.Fatalf("missing %q in\n%s", want, transcript)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no SMTP session")
	}
}

func emailReadyShare(t *testing.T, a *App, owner string, password string) Share {
	t.Helper()
	sh := createShare(t, a, owner, password, 5)
	if w := chunk(a, owner, sh.Files[0].ID, 0, "hello"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/publish", "{}", owner); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	return sh
}

func configureEmail(t *testing.T, a *App, admin string) {
	t.Helper()
	body := `{"host":"smtp.example.com","port":587,"security":"starttls","from":"share@example.com","from_name":"MagiSend"}`
	if w := call(a, "PUT", "/api/admin/email", body, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}

func queued(a *App) []outgoingMail {
	var list []outgoingMail
	for {
		select {
		case m := <-a.mail:
			list = append(list, m)
		default:
			return list
		}
	}
}

func TestEmailShareLink(t *testing.T) {
	a, admin := emailAdmin(t)
	sh := emailReadyShare(t, a, admin, "share-secret-password")
	if w := call(a, "POST", "/api/shares/"+sh.ID+"/email", `{"to":["a@example.com"]}`, admin); w.Code != 409 {
		t.Fatal("sent without email settings", w.Code)
	}
	configureEmail(t, a, admin)
	w := call(a, "POST", "/api/shares/"+sh.ID+"/email", `{"to":["a@example.com","b@example.com","A@example.com"],"message":"Hej <b>där</b>","language":"en"}`, admin)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	mails := queued(a)
	if len(mails) != 2 || mails[0].to != "a@example.com" || mails[1].to != "b@example.com" {
		t.Fatal("expected one message per distinct recipient", mails)
	}
	m := mails[0]
	if !strings.Contains(m.text, "http://example.com/s/"+sh.ID) || !strings.Contains(m.text, "Ask mati") ||
		strings.Contains(m.text, "share-secret-password") || strings.Contains(m.body, "<b>där</b>") || !strings.Contains(m.body, "&lt;b&gt;där&lt;/b&gt;") {
		t.Fatal("unexpected message", m.text, m.body)
	}
	for body, status := range map[string]int{
		`{"to":["x@example.com\r\nBcc: evil@example.com"]}`: 400,
		`{"to":[]}`: 400,
		`{"to":["1@e.se","2@e.se","3@e.se","4@e.se","5@e.se","6@e.se","7@e.se","8@e.se","9@e.se","10@e.se","11@e.se"]}`: 400,
	} {
		if w = call(a, "POST", "/api/shares/"+sh.ID+"/email", body, admin); w.Code != status {
			t.Fatal(body, w.Code)
		}
	}
	body2 := fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword)
	call(a, "POST", "/api/admin/users", body2, admin)
	if w = call(a, "POST", "/api/shares/"+sh.ID+"/email", `{"to":["a@example.com"]}`, login(t, a, "bob")); w.Code != 404 {
		t.Fatal("another user emailed someone else's share", w.Code)
	}
	for i := 0; ; i++ {
		w = call(a, "POST", "/api/shares/"+sh.ID+"/email", `{"to":["1@e.se","2@e.se","3@e.se","4@e.se","5@e.se","6@e.se","7@e.se","8@e.se","9@e.se","10@e.se"]}`, admin)
		queued(a)
		if w.Code == 429 {
			if i > 3 {
				t.Fatal("hourly limit not enforced", i)
			}
			break
		}
		if i > 5 {
			t.Fatal("never limited")
		}
	}
}

func TestDownloadNotificationsAreCountedAndBundled(t *testing.T) {
	a, admin := emailAdmin(t)
	configureEmail(t, a, admin)
	sh := emailReadyShare(t, a, admin, "")
	if w := call(a, "PUT", "/api/shares/"+sh.ID+"/notify", `{"enabled":true}`, admin); w.Code != 409 {
		t.Fatal("notifications enabled without an account email", w.Code)
	}
	if w := call(a, "PUT", "/api/account/email", `{"email":"mati@example.com"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(a, "PUT", "/api/shares/"+sh.ID+"/notify", `{"enabled":true,"language":"sv"}`, admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	download := func(span string) {
		r := httptest.NewRequest("GET", "/api/public/"+sh.ID+"/files/"+sh.Files[0].ID, nil)
		if span != "" {
			r.Header.Set("Range", span)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 && w.Code != 206 {
			t.Fatal(w.Code, w.Body)
		}
		io.Copy(io.Discard, w.Body)
	}
	download("")
	download("bytes=2-") // a resumed download is not a new one
	now := time.Now()
	a.flushNotifications(now)
	mails := queued(a)
	if len(mails) != 1 || mails[0].to != "mati@example.com" || !strings.Contains(mails[0].subject, "har laddats ner") {
		t.Fatal(mails)
	}
	download("")
	download("bytes=0-")
	a.flushNotifications(now.Add(time.Minute))
	if mails = queued(a); len(mails) != 0 {
		t.Fatal("notified again within the bundling interval", mails)
	}
	a.flushNotifications(now.Add(notifyInterval + time.Minute))
	if mails = queued(a); len(mails) != 1 || !strings.Contains(mails[0].text, "2 gånger") {
		t.Fatal("bundled notification missing", mails)
	}
	var owner map[string]any
	for _, s := range decodeList(t, call(a, "GET", "/api/shares", "", admin)) {
		if s["id"] == sh.ID {
			owner = s
		}
	}
	if owner["downloads"] != float64(3) || owner["notify"] != true {
		t.Fatal("owner view", owner)
	}
	public := decodeObject(t, call(a, "GET", "/api/public/"+sh.ID, "", ""))
	if public["downloads"] != float64(0) || public["notify"] != false {
		t.Fatal("public view exposes owner statistics", public)
	}
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var list []map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &list); e != nil {
		t.Fatal(e, w.Body)
	}
	return list
}

func TestSiteAddressWithoutScheme(t *testing.T) {
	for raw, want := range map[string]string{
		"http://192.0.2.10:8081/":            "http://192.0.2.10:8081",
		"192.0.2.10:8081":                    "http://192.0.2.10:8081",
		"localhost:8080":                     "http://localhost:8080",
		"share.example.com":                  "https://share.example.com",
		" https://share.example.com/":        "https://share.example.com",
		"http://192.0.2.10:8081/library?x=1": "http://192.0.2.10:8081",
		"http://192.0.2.10:8081\u200b":       "http://192.0.2.10:8081",
		"\ufeffhttp://192.0.2.10:8081\u00a0": "http://192.0.2.10:8081",
		"share.example.com/library":          "https://share.example.com",
	} {
		if got, ok := validSiteURL(raw); !ok || got != want {
			t.Errorf("%q: got %q %v, want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"ftp://share.example.com", "user@share.example.com", "bad_host"} {
		if _, ok := validSiteURL(raw); ok {
			t.Errorf("%q was accepted", raw)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPushChannelsSendAndStayOffTheLocalNetwork(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	token := login(t, a, "mati")
	got := make(chan map[string]any, 4)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		body["_path"] = r.URL.Path
		body["_auth"] = r.Header.Get("Authorization")
		got <- body
	}))
	defer service.Close()

	for _, bad := range []string{
		`{"kind":"ntfy","url":"` + service.URL + `","events":["downloaded"]}`,
		`{"kind":"gotify","url":"https://gotify.example","events":["downloaded"]}`,
		`{"kind":"discord","url":"https://evil.example/api/webhooks/1/x","events":["downloaded"]}`,
		`{"kind":"webhook","url":"ftp://example.com/x","events":["downloaded"]}`,
		`{"kind":"ntfy","url":"https://ntfy.sh/topic","events":["everything"]}`,
		`{"kind":"ntfy","url":"https://ntfy.sh/topic","events":[]}`,
	} {
		folderCall(t, a, token, "POST", "/api/account/push", bad, 400)
	}
	ch := folderCall(t, a, token, "POST", "/api/account/push", `{"kind":"ntfy","url":"`+service.URL+`/alerts","token":"tk_secret","events":["downloaded","expiring"],"language":"sv"}`, 201)
	id := ch["id"].(string)
	list := folderCall(t, a, token, "GET", "/api/account/push", "", 200)
	if strings.Contains(mustJSON(list), "tk_secret") || list["allow_local"] != false {
		t.Fatal("token shown or wrong setting", list)
	}

	// The test server is on this machine: refused until the admin allows it.
	if w := call(a, "POST", "/api/account/push/"+id+"/test", `{}`, token); w.Code != 502 || !strings.Contains(w.Body.String(), `"reason":"local"`) {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "PUT", "/api/admin/push", `{"allow_local":true}`, 200)
	folderCall(t, a, token, "POST", "/api/account/push/"+id+"/test", `{}`, 200)
	body := <-got
	if body["_path"] != "/" || body["topic"] != "alerts" || body["_auth"] != "Bearer tk_secret" || !strings.Contains(body["message"].(string), "fungerar") {
		t.Fatal("ntfy request", body)
	}

	// A download is bundled into one queued message in the channel's language.
	sh := createShare(t, a, token, "", 4)
	if w := chunk(a, token, sh.Files[0].ID, 0, "data"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	folderCall(t, a, token, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
	for i := 0; i < 2; i++ {
		if w := call(a, "GET", "/api/public/"+sh.ID+"/files/"+sh.Files[0].ID, "", ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	a.flushPush(time.Now())
	select {
	case job := <-a.push:
		if job.message.title != "”Testdelning” har laddats ner 2 gånger" || job.message.event != pushDownloaded {
			t.Fatal(job.message)
		}
	default:
		t.Fatal("no download notification")
	}
	a.flushPush(time.Now())
	if len(a.push) != 0 {
		t.Fatal("sent twice")
	}

	// Someone else cannot test or remove the channel.
	if _, e := a.addUser("other", "correct horse battery staple", "user", GiB); e != nil {
		t.Fatal(e)
	}
	other := login(t, a, "other")
	folderCall(t, a, other, "POST", "/api/account/push/"+id+"/test", `{}`, 404)
	folderCall(t, a, other, "DELETE", "/api/account/push/"+id, `{}`, 200)
	if len(a.channels(a.userID(t, "mati"), "")) != 1 {
		t.Fatal("another account removed the channel")
	}
	folderCall(t, a, other, "PUT", "/api/admin/push", `{"allow_local":false}`, 403)
}

func TestPushRequestsForEachService(t *testing.T) {
	ctx := context.Background()
	gotify, _ := pushRequest(ctx, PushChannel{Kind: "gotify", URL: "https://push.example/", token: "app"}, pushMessage{title: "T", link: "https://x/s"}, "MagiSend")
	if gotify.URL.String() != "https://push.example/message" || gotify.Header.Get("X-Gotify-Key") != "app" {
		t.Fatal(gotify.URL, gotify.Header)
	}
	discord, _ := pushRequest(ctx, PushChannel{Kind: "discord", URL: "https://discord.com/api/webhooks/1/x"}, pushMessage{title: "@everyone *bold*"}, "MagiSend")
	data, _ := io.ReadAll(discord.Body)
	if strings.Contains(string(data), "@everyone") || !strings.Contains(string(data), `"parse":[]`) {
		t.Fatal(string(data))
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.5", "172.16.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "0.0.0.0"} {
		if publicAddress(net.ParseIP(ip)) {
			t.Fatal("treated as public", ip)
		}
	}
	if !publicAddress(net.ParseIP("1.1.1.1")) {
		t.Fatal("1.1.1.1")
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (a *App) userID(t *testing.T, name string) string {
	t.Helper()
	var id string
	if e := a.db.QueryRow("SELECT id FROM users WHERE username=?", name).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}

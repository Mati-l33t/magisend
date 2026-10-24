package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Push notifications go to services people already use on their phones:
// ntfy, Gotify, a Discord channel or any webhook. Each person adds their own
// channels and picks the events each one gets. Messages carry only a link's
// title and counts, never file contents or passwords.
//
// The server makes these requests, so by default it refuses addresses on the
// local network (an account must not be able to reach other machines
// through MagiSend); an administrator can allow them for a self-hosted ntfy
// or Gotify. The check runs on the address actually connected to, after DNS.

const (
	pushDownloaded = "downloaded"
	pushReceived   = "received"
	pushExpiring   = "expiring"
	pushClosed     = "closed"
	maxPushPerUser = 10
)

var pushKinds = map[string]bool{"ntfy": true, "gotify": true, "discord": true, "webhook": true}
var pushEvents = []string{pushDownloaded, pushReceived, pushExpiring, pushClosed}

func migratePush(db *sql.DB) error {
	if _, e := db.Exec(`CREATE TABLE IF NOT EXISTS push_channels(id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, kind TEXT NOT NULL, url TEXT NOT NULL, token TEXT NOT NULL DEFAULT '', events TEXT NOT NULL, language TEXT NOT NULL DEFAULT 'en', created INTEGER NOT NULL, last_sent INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '')`); e != nil {
		return e
	}
	for _, c := range [][3]string{
		{"settings", "push_local", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "push_pending", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "push_last", "INTEGER NOT NULL DEFAULT 0"},
		{"upload_requests", "push_pending", "INTEGER NOT NULL DEFAULT 0"},
		{"upload_requests", "push_last", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if e := addColumn(db, c[0], c[1], c[2]); e != nil {
			return e
		}
	}
	return nil
}

type PushChannel struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	URL      string   `json:"url"`
	HasToken bool     `json:"has_token"`
	Events   []string `json:"events"`
	Created  int64    `json:"created"`
	LastSent int64    `json:"last_sent"`
	// The last failure, cleared by the next success.
	LastError string `json:"last_error"`
	token     string
	language  string
}

type pushMessage struct {
	event, title, text, link string
}

type pushJob struct {
	channel PushChannel
	message pushMessage
}

var pushText = map[string]map[string]string{
	"en": {
		"downloadedOne": "“%s” was downloaded",
		"downloaded":    "“%s” was downloaded %d times",
		"receivedOne":   "A file was uploaded to “%s”",
		"received":      "%d files were uploaded to “%s”",
		"expiring":      "“%s” expires tomorrow (%s)",
		"closed":        "“%s” reached its download limit and is closed",
		"test":          "Notifications from {brand} work",
	},
	"sv": {
		"downloadedOne": "”%s” har laddats ner",
		"downloaded":    "”%s” har laddats ner %d gånger",
		"receivedOne":   "En fil har laddats upp till ”%s”",
		"received":      "%d filer har laddats upp till ”%s”",
		"expiring":      "”%s” går ut i morgon (%s)",
		"closed":        "”%s” har nått nedladdningsgränsen och är stängd",
		"test":          "Aviseringar från {brand} fungerar",
	},
}

func (a *App) pushAllowsLocal() bool {
	var allow bool
	_ = a.db.QueryRow("SELECT push_local FROM settings WHERE id=1").Scan(&allow)
	return allow
}

var errPushAddress = errors.New("address not allowed")

// publicAddress is false for loopback, private, link-local, carrier-grade NAT
// and other addresses that are not on the internet.
func publicAddress(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && (v4[0] == 100 && v4[1]&0xc0 == 64 || v4[0] == 0) {
		return false
	}
	return true
}

func pushClient(allowLocal bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, e := net.SplitHostPort(address)
			ip := net.ParseIP(host)
			if e != nil || ip == nil || (!allowLocal && !publicAddress(ip)) {
				return errPushAddress
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
		// A redirect could lead anywhere; services that need one are not supported.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// pushRequest builds the request each service expects.
func pushRequest(ctx context.Context, c PushChannel, m pushMessage, brand string) (*http.Request, error) {
	var body any
	target := c.URL
	headers := map[string]string{}
	switch c.Kind {
	case "ntfy":
		// JSON publishing keeps non-ASCII titles intact: the topic goes in
		// the body and the request to the server's root.
		u, e := url.Parse(c.URL)
		if e != nil {
			return nil, e
		}
		topic := strings.Trim(u.Path, "/")
		u.Path = "/"
		target = u.String()
		message := map[string]any{"topic": topic, "title": brand, "message": m.title, "tags": []string{"package"}}
		if m.text != "" {
			message["message"] = m.title + "\n" + m.text
		}
		if m.link != "" {
			message["click"] = m.link
		}
		body = message
		if c.token != "" {
			headers["Authorization"] = "Bearer " + c.token
		}
	case "gotify":
		target = strings.TrimRight(c.URL, "/") + "/message"
		message := map[string]any{"title": brand, "message": strings.TrimSpace(m.title + "\n" + m.text), "priority": 5}
		if m.link != "" {
			message["extras"] = map[string]any{"client::notification": map[string]any{"click": map[string]string{"url": m.link}}}
		}
		body = message
		headers["X-Gotify-Key"] = c.token
	case "discord":
		content := "**" + discordEscape(m.title) + "**"
		if m.text != "" {
			content += "\n" + discordEscape(m.text)
		}
		if m.link != "" {
			content += "\n<" + m.link + ">"
		}
		if len([]rune(content)) > 1900 {
			content = string([]rune(content)[:1900])
		}
		body = map[string]any{"username": brand, "content": content, "allowed_mentions": map[string]any{"parse": []string{}}}
	default:
		body = map[string]any{"event": m.event, "title": m.title, "message": m.text, "url": m.link, "time": time.Now().Unix(), "app": brand}
	}
	data, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "MagiSend")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r, nil
}

// Titles are the owner's own words, but must not ping a whole channel or
// format a message by accident.
func discordEscape(s string) string {
	return strings.NewReplacer("*", "\\*", "_", "\\_", "~", "\\~", "`", "\\`", "|", "\\|", ">", "\\>", "@", "@​").Replace(s)
}

// deliver sends one message and records the outcome on the channel. It must
// not be called with mu held: the request can take seconds.
func (a *App) deliver(c PushChannel, m pushMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, e := pushRequest(ctx, c, m, a.brandName())
	if e == nil {
		var response *http.Response
		if response, e = pushClient(a.pushAllowsLocal()).Do(r); e == nil {
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode > 299 {
				e = fmt.Errorf("status %d", response.StatusCode)
			}
		}
	}
	// The reason is a short code the page words in the reader's language:
	// local, timeout, connect or "status <code>".
	if e != nil {
		reason := e.Error()
		if errors.Is(e, errPushAddress) {
			reason = "local"
		} else if errors.Is(e, context.DeadlineExceeded) {
			reason = "timeout"
		} else if !strings.HasPrefix(reason, "status ") {
			reason = "connect"
		}
		_, _ = a.db.Exec("UPDATE push_channels SET last_error=? WHERE id=?", reason, c.ID)
		return errors.New(reason)
	}
	_, _ = a.db.Exec("UPDATE push_channels SET last_sent=?, last_error='' WHERE id=?", time.Now().Unix(), c.ID)
	return nil
}

func (a *App) pushWorker() {
	for job := range a.push {
		if e := a.deliver(job.channel, job.message); e != nil {
			log.Print("push notification failed")
		}
	}
}

// channels returns a person's channels that want event; caller holds mu or
// runs in a worker.
func (a *App) channels(user, event string) []PushChannel {
	rows, e := a.db.Query("SELECT id,kind,url,token,events,language,created,last_sent,last_error FROM push_channels WHERE user_id=? ORDER BY created", user)
	if e != nil {
		return nil
	}
	defer rows.Close()
	var list []PushChannel
	for rows.Next() {
		var c PushChannel
		var events string
		if rows.Scan(&c.ID, &c.Kind, &c.URL, &c.token, &events, &c.language, &c.Created, &c.LastSent, &c.LastError) != nil {
			continue
		}
		c.Events = strings.Split(events, ",")
		c.HasToken = c.token != ""
		if event == "" || containsString(c.Events, event) {
			list = append(list, c)
		}
	}
	return list
}

func containsString(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// notify queues one message, worded in each channel's language, for every
// channel of user that wants event.
func (a *App) notify(user, event string, words func(t map[string]string) (string, string), link string) {
	for _, c := range a.channels(user, event) {
		title, text := words(pushText[emailLanguage(c.language)])
		select {
		case a.push <- pushJob{c, pushMessage{event: event, title: title, text: text, link: link}}:
		default:
		}
	}
}

// flushPush sends bundled download and upload notifications: the first soon
// after it happened, then at most one per link or request every 15 minutes.
func (a *App) flushPush(now time.Time) {
	origin := a.linkOrigin()
	type due struct {
		id, title, owner, origin string
		count                    int64
	}
	collect := func(query string) []due {
		rows, e := a.db.Query(query, now.Add(-notifyInterval).Unix())
		if e != nil {
			return nil
		}
		defer rows.Close()
		var list []due
		for rows.Next() {
			var d due
			if rows.Scan(&d.id, &d.title, &d.owner, &d.origin, &d.count) == nil {
				if origin != "" {
					d.origin = origin
				}
				list = append(list, d)
			}
		}
		return list
	}
	for _, d := range collect("SELECT id,title,owner,notify_origin,push_pending FROM shares WHERE push_pending>0 AND push_last<=?") {
		a.notify(d.owner, pushDownloaded, func(t map[string]string) (string, string) {
			if d.count == 1 {
				return fmt.Sprintf(t["downloadedOne"], d.title), ""
			}
			return fmt.Sprintf(t["downloaded"], d.title, d.count), ""
		}, linkTo(d.origin, "/shares"))
		_, _ = a.db.Exec("UPDATE shares SET push_pending=push_pending-?, push_last=? WHERE id=?", d.count, now.Unix(), d.id)
	}
	for _, d := range collect("SELECT id,title,owner,notify_origin,push_pending FROM upload_requests WHERE push_pending>0 AND push_last<=?") {
		a.notify(d.owner, pushReceived, func(t map[string]string) (string, string) {
			if d.count == 1 {
				return fmt.Sprintf(t["receivedOne"], d.title), ""
			}
			return fmt.Sprintf(t["received"], d.count, d.title), ""
		}, linkTo(d.origin, "/library"))
		_, _ = a.db.Exec("UPDATE upload_requests SET push_pending=push_pending-?, push_last=? WHERE id=?", d.count, now.Unix(), d.id)
	}
}

func linkTo(origin, path string) string {
	if origin == "" {
		return ""
	}
	return origin + path
}

// validPushChannel checks what a person typed; it returns the cleaned URL.
func validPushChannel(kind, raw, token string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, e := url.Parse(raw)
	if !pushKinds[kind] || e != nil || len(raw) > 500 || len(token) > 300 || u.Host == "" || u.User != nil ||
		(u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(token, "\r\n") {
		return "", false
	}
	switch kind {
	case "ntfy":
		topic := strings.Trim(u.Path, "/")
		if topic == "" || strings.Contains(topic, "/") || u.RawQuery != "" {
			return "", false
		}
	case "gotify":
		if token == "" || u.RawQuery != "" {
			return "", false
		}
	case "discord":
		host := strings.ToLower(u.Hostname())
		if u.Scheme != "https" || (host != "discord.com" && host != "discordapp.com" && !strings.HasSuffix(host, ".discord.com")) || !strings.HasPrefix(u.Path, "/api/webhooks/") {
			return "", false
		}
	}
	return u.String(), true
}

func (a *App) pushRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/account/push", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		list := a.channels(u.ID, "")
		if list == nil {
			list = []PushChannel{}
		}
		respond(w, 200, map[string]any{"channels": list, "allow_local": a.pushAllowsLocal()})
	})
	m.HandleFunc("POST /api/account/push", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Kind     string   `json:"kind"`
			URL      string   `json:"url"`
			Token    string   `json:"token"`
			Events   []string `json:"events"`
			Language string   `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		target, valid := validPushChannel(b.Kind, b.URL, strings.TrimSpace(b.Token))
		events := []string{}
		for _, e := range pushEvents {
			if containsString(b.Events, e) {
				events = append(events, e)
			}
		}
		if !valid || len(events) == 0 || len(events) != len(b.Events) {
			fail(w, 400, "push_invalid")
			return
		}
		var n int
		if internal(w, a.db.QueryRow("SELECT COUNT(*) FROM push_channels WHERE user_id=?", u.ID).Scan(&n)) {
			return
		}
		if n >= maxPushPerUser {
			fail(w, 429, "metadata_limit")
			return
		}
		c := PushChannel{ID: randomID(), Kind: b.Kind, URL: target, Events: events, Created: time.Now().Unix(), token: strings.TrimSpace(b.Token)}
		c.HasToken = c.token != ""
		if internal(w, exec(a, "INSERT INTO push_channels(id,user_id,kind,url,token,events,language,created) VALUES(?,?,?,?,?,?,?,?)", c.ID, u.ID, c.Kind, c.URL, c.token, strings.Join(events, ","), emailLanguage(b.Language), c.Created)) {
			return
		}
		respond(w, 201, c)
	})
	m.HandleFunc("DELETE /api/account/push/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		if internal(w, exec(a, "DELETE FROM push_channels WHERE id=? AND user_id=?", r.PathValue("id"), u.ID)) {
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	// The test is sent at once, so the person sees whether it worked.
	m.HandleFunc("POST /api/account/push/{id}/test", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		u, ok := a.require(w, r, false)
		var found *PushChannel
		if ok {
			for _, c := range a.channels(u.ID, "") {
				if c.ID == r.PathValue("id") {
					found = &c
				}
			}
		}
		a.mu.Unlock()
		if !ok {
			return
		}
		if found == nil {
			fail(w, 404, "not_found")
			return
		}
		title := strings.ReplaceAll(pushText[emailLanguage(found.language)]["test"], "{brand}", a.brandName())
		if e := a.deliver(*found, pushMessage{event: "test", title: title, link: linkTo(a.requestOrigin(r), "/")}); e != nil {
			respond(w, 502, map[string]string{"error": "The test notification could not be delivered", "error_code": "push_failed", "reason": e.Error()})
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/admin/push", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		respond(w, 200, map[string]bool{"allow_local": a.pushAllowsLocal()})
	})
	m.HandleFunc("PUT /api/admin/push", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			AllowLocal bool `json:"allow_local"`
		}
		if !decode(w, r, &b) {
			return
		}
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		if internal(w, exec(a, "UPDATE settings SET push_local=? WHERE id=1", b.AllowLocal)) {
			return
		}
		a.audit(r, admin.Username, "settings.push", "")
		respond(w, 200, map[string]bool{"allow_local": b.AllowLocal})
	})
}

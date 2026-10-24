package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExpiryReminderOncePerExpiry(t *testing.T) {
	a, admin := emailAdmin(t)
	configureEmail(t, a, admin)
	folderCall(t, a, admin, "PUT", "/api/account/email", `{"email":"owner@example.com"}`, 200)
	send := func(days int) Share {
		t.Helper()
		w := call(a, "POST", "/api/shares", `{"title":"Holiday","days":`+strconv.Itoa(days)+`,"language":"sv","files":[{"name":"a.txt","size":4}]}`, admin)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		sh := decodeShare(t, w)
		if w = chunk(a, admin, sh.Files[0].ID, 0, "data"); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		folderCall(t, a, admin, "POST", "/api/shares/"+sh.ID+"/publish", `{}`, 200)
		return sh
	}
	week := send(7)
	send(1)
	queued(a)

	// Not yet due, and a one-day link (due now) never gets one.
	a.sendExpiryReminders(time.Now())
	if m := queued(a); len(m) != 0 {
		t.Fatal("too early", m)
	}
	// The day before: one email, in the language the link was made in.
	later := time.Now().Add(6*24*time.Hour + time.Hour)
	a.sendExpiryReminders(later)
	m := queued(a)
	if len(m) != 1 || m[0].to != "owner@example.com" || !strings.Contains(m[0].subject, "Holiday") ||
		!strings.Contains(m[0].text, "/shares?extend="+week.ID) || !strings.Contains(m[0].subject, "går ut i morgon") {
		t.Fatal("reminder", m)
	}
	a.sendExpiryReminders(later)
	if len(queued(a)) != 0 {
		t.Fatal("reminded twice")
	}
	// A new expiry gets a new reminder.
	extend := func() {
		t.Helper()
		if e := exec(a, "UPDATE shares SET expires=expires-60 WHERE id=?", week.ID); e != nil {
			t.Fatal(e)
		}
		folderCall(t, a, admin, "PATCH", "/api/shares/"+week.ID, `{"days":7}`, 200)
	}
	extend()
	a.sendExpiryReminders(time.Now().Add(6*24*time.Hour + time.Hour))
	if len(queued(a)) != 1 {
		t.Fatal("extended link not reminded again")
	}
	// Turned off: nothing.
	folderCall(t, a, admin, "PUT", "/api/account/email", `{"email":"owner@example.com","remind_expiry":false}`, 200)
	if me := folderCall(t, a, admin, "GET", "/api/me", "", 200); me["user"].(map[string]any)["remind_expiry"] != false {
		t.Fatal(me)
	}
	extend()
	a.sendExpiryReminders(time.Now().Add(6*24*time.Hour + time.Hour))
	if len(queued(a)) != 0 {
		t.Fatal("reminder after turning them off")
	}
}

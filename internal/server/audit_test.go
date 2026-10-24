package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func activity(t *testing.T, a *App, admin string) []AuditEvent {
	t.Helper()
	var list []AuditEvent
	if e := json.Unmarshal(call(a, "GET", "/api/admin/activity", "", admin).Body.Bytes(), &list); e != nil {
		t.Fatal(e)
	}
	return list
}

func TestActivityLogAndOverview(t *testing.T) {
	a, admin := emailAdmin(t)
	call(a, "POST", "/api/login", `{"username":"mati","password":"wrong password here"}`, "")
	call(a, "POST", "/api/admin/users", fmt.Sprintf(`{"username":"bob","password":%q,"quota":0}`, testPassword), admin)
	bob := login(t, a, "bob")
	if w := call(a, "GET", "/api/admin/activity", "", bob); w.Code != 403 {
		t.Fatal("a user read the activity log", w.Code)
	}
	emailReadyShare(t, a, admin, "")
	seen := map[string]AuditEvent{}
	for _, ev := range activity(t, a, admin) {
		if _, dup := seen[ev.Action]; !dup {
			seen[ev.Action] = ev
		}
	}
	for _, want := range []string{"account.setup", "login", "login.failed", "account.created", "share.created"} {
		if _, ok := seen[want]; !ok {
			t.Fatal("missing", want, seen)
		}
	}
	if ev := seen["account.created"]; ev.Actor != "mati" || ev.Target != "bob" || ev.IP == "" {
		t.Fatal(ev)
	}
	if ev := seen["login.failed"]; ev.Actor != "" || ev.Target != "mati" {
		t.Fatal(ev)
	}
	// Paging and filtering.
	list := activity(t, a, admin)
	var older []AuditEvent
	json.Unmarshal(call(a, "GET", fmt.Sprintf("/api/admin/activity?before=%d", list[0].ID), "", admin).Body.Bytes(), &older)
	if len(older) != len(list)-1 {
		t.Fatal("paging", len(list), len(older))
	}
	var logins []AuditEvent
	json.Unmarshal(call(a, "GET", "/api/admin/activity?action=login", "", admin).Body.Bytes(), &logins)
	for _, ev := range logins {
		if ev.Action[:5] != "login" {
			t.Fatal("filter", ev)
		}
	}
	o := decodeObject(t, call(a, "GET", "/api/admin/overview", "", admin))
	if o["users"] != float64(2) || o["active_links"] != float64(1) || o["files"] != float64(1) || o["failed_logins"] != float64(1) || o["email_enabled"] != false {
		t.Fatal(o)
	}
	if free, ok := o["disk_free"].(float64); !ok || free <= 0 {
		t.Fatal("free space missing", o)
	}
	// Old entries are pruned.
	a.db.Exec("UPDATE audit SET at=?", time.Now().Add(-auditRetention-time.Hour).Unix())
	a.Cleanup(time.Now())
	if left := activity(t, a, admin); len(left) != 0 {
		t.Fatal("old activity kept", len(left))
	}
}

func TestActivityComesInPages(t *testing.T) {
	a := testApp(t)
	setupAdmin(t, a)
	admin := login(t, a, "mati")
	for i := 0; i < 20; i++ {
		a.audit(nil, "mati", "settings.policy", "")
	}
	page := func(query string) []AuditEvent {
		t.Helper()
		var list []AuditEvent
		w := call(a, "GET", "/api/admin/activity?"+query, "", admin)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return list
	}
	first := page("limit=15")
	if len(first) != 15 {
		t.Fatal(len(first))
	}
	rest := page("limit=15&before=" + strconv.FormatInt(first[14].ID, 10))
	if len(rest) == 0 || rest[0].ID >= first[14].ID {
		t.Fatal("older page", rest)
	}
	if len(page("limit=1000")) > 50 || len(page("")) > 50 {
		t.Fatal("limit not capped")
	}
}

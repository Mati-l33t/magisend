package server

import (
	"database/sql"
	"fmt"
	"html"
	"time"
)

// A day before a link expires its owner gets an email with a button that
// opens MagiSend at the link, where one click keeps it open longer. The
// button only opens the page, so a mail scanner that follows links cannot
// extend anything. Links that last less than two days get no reminder, and
// each person can turn reminders off.
const reminderAhead = 24 * time.Hour

func migrateReminders(db *sql.DB) error {
	for _, c := range [][3]string{
		{"users", "remind_expiry", "INTEGER NOT NULL DEFAULT 1"},
		{"shares", "reminded", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if e := addColumn(db, c[0], c[1], c[2]); e != nil {
			return e
		}
	}
	return nil
}

func (a *App) sendExpiryReminders(now time.Time) {
	mail := a.emailEnabled()
	rows, e := a.db.Query(`SELECT s.id,s.title,s.expires,s.notify_language,s.notify_origin,s.owner,u.email,u.remind_expiry,
 (SELECT COUNT(*) FROM files f WHERE f.share_id=s.id AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id)),
 (SELECT COALESCE(SUM(f.size),0) FROM files f WHERE f.share_id=s.id AND NOT EXISTS(SELECT 1 FROM file_deleting d WHERE d.file_id=f.id))
 FROM shares s JOIN users u ON u.id=s.owner
 WHERE s.published=1 AND s.reminded=0 AND s.expires>? AND s.expires<=? AND s.expires-s.created>=?
 AND u.disabled=0
 AND NOT EXISTS(SELECT 1 FROM library_containers c WHERE c.share_id=s.id)
 AND NOT EXISTS(SELECT 1 FROM deleting d WHERE d.share_id=s.id)`,
		now.Unix(), now.Add(reminderAhead).Unix(), int64(2*reminderAhead/time.Second))
	if e != nil {
		return
	}
	type due struct {
		id, title, language, origin, owner, email string
		remind                                    bool
		expires, files, size                      int64
	}
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.title, &d.expires, &d.language, &d.origin, &d.owner, &d.email, &d.remind, &d.files, &d.size) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	origin := a.linkOrigin()
	for _, d := range list {
		if origin != "" {
			d.origin = origin
		}
		// Email when it can go out; a full queue tries again next minute.
		if mail && d.remind && d.email != "" && !a.queueMail(reminderMail(d.email, d.title, d.id, d.language, d.origin, d.expires, d.files, d.size)) {
			continue
		}
		when := time.Unix(d.expires, 0).UTC().Format("2006-01-02 15:04") + " UTC"
		a.notify(d.owner, pushExpiring, func(t map[string]string) (string, string) {
			return fmt.Sprintf(t["expiring"], d.title, when), ""
		}, linkTo(d.origin, "/shares?extend="+d.id))
		_, _ = a.db.Exec("UPDATE shares SET reminded=1 WHERE id=?", d.id)
	}
}

func reminderMail(to, title, id, language, origin string, expires, files, size int64) outgoingMail {
	t := emailText[emailLanguage(language)]
	when := time.Unix(expires, 0).UTC().Format("2006-01-02 15:04") + " UTC"
	line := fmt.Sprintf(t["expiry"], title, when)
	summary := fmt.Sprintf(t["files"], files, humanSize(size))
	text := line + "\n" + summary + "\n"
	body := `<p style="font-size:16px;margin:0 0 8px">` + html.EscapeString(line) + `</p>` +
		`<p style="font-family:monospace;font-size:12px;color:#8b867f;margin:0">` + html.EscapeString(summary) + `</p>`
	if origin != "" {
		link := origin + "/shares?extend=" + id
		text += "\n" + t["expiryKeep"] + ": " + link + "\n"
		body += emailButton(link, t["expiryKeep"])
	}
	text += "\n" + t["expiryOff"] + "\n"
	body += `<p style="font-size:12px;color:#8b867f;margin:16px 0 0">` + html.EscapeString(t["expiryOff"]) + `</p>`
	return outgoingMail{to: to, subject: fmt.Sprintf(t["expirySubject"], title), text: text, body: emailFrame(body)}
}

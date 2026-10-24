package server

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxEmailRecipients    = 10
	emailsPerHour         = 30
	notifyInterval        = 15 * time.Minute
	maxEmailMessageRunes  = 1000
	emailQueueDepth       = 200
	emailDeliveryDeadline = 60 * time.Second
)

// EmailSettings are the administrator's SMTP settings. The password is never
// returned to the browser.
type EmailSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`
	FromName string `json:"from_name"`
	// SiteURL is the address used in links of system emails.
	SiteURL string `json:"site_url"`
}

func (s EmailSettings) configured() bool { return s.Host != "" && s.From != "" }

type outgoingMail struct {
	to                  string
	subject, text, body string
}

func migrateEmail(db *sql.DB) error {
	columns := []struct{ table, name, definition string }{
		{"settings", "smtp_host", "TEXT NOT NULL DEFAULT ''"},
		{"settings", "smtp_port", "INTEGER NOT NULL DEFAULT 587"},
		{"settings", "smtp_security", "TEXT NOT NULL DEFAULT 'starttls'"},
		{"settings", "smtp_username", "TEXT NOT NULL DEFAULT ''"},
		{"settings", "smtp_password", "TEXT NOT NULL DEFAULT ''"},
		{"settings", "smtp_from", "TEXT NOT NULL DEFAULT ''"},
		{"settings", "smtp_from_name", "TEXT NOT NULL DEFAULT 'MagiSend'"},
		{"users", "email", "TEXT NOT NULL DEFAULT ''"},
		{"shares", "downloads", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "notify", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "notify_pending", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "notify_last", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "notify_language", "TEXT NOT NULL DEFAULT 'en'"},
		{"shares", "notify_origin", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if e := addColumn(db, c.table, c.name, c.definition); e != nil {
			return e
		}
	}
	return nil
}

// optionalEmail accepts an empty value or one valid address.
func optionalEmail(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", true
	}
	return validEmail(raw)
}

func (a *App) emailSettings() (EmailSettings, error) {
	var s EmailSettings
	e := a.db.QueryRow(`SELECT smtp_host,smtp_port,smtp_security,smtp_username,smtp_password,smtp_from,smtp_from_name,site_url FROM settings WHERE id=1`).
		Scan(&s.Host, &s.Port, &s.Security, &s.Username, &s.Password, &s.From, &s.FromName, &s.SiteURL)
	return s, e
}

func (a *App) emailEnabled() bool {
	s, e := a.emailSettings()
	return e == nil && s.configured()
}

// validEmail accepts one plain address (no display name, no header tricks).
func validEmail(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n<>\",;") {
		return "", false
	}
	address, e := mail.ParseAddress(value)
	if e != nil || address.Name != "" || address.Address != value || !strings.Contains(value[strings.LastIndexByte(value, '@'):], ".") {
		return "", false
	}
	return value, true
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == ':') {
			return false
		}
	}
	return true
}

func headerText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, value)
}

func buildMessage(s EmailSettings, m outgoingMail) []byte {
	var b bytes.Buffer
	boundary := "magisend-" + randomID()[:24]
	domain := s.From[strings.LastIndexByte(s.From, '@')+1:]
	from := (&mail.Address{Name: headerText(s.FromName), Address: s.From}).String()
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\n",
		from, m.to, mime.QEncoding.Encode("utf-8", headerText(m.subject)), time.Now().Format(time.RFC1123Z), randomID(), domain)
	fmt.Fprintf(&b, "Auto-Submitted: auto-generated\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	for _, part := range []struct{ kind, body string }{{"text/plain", m.text}, {"text/html", m.body}} {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.kind)
		q := quotedprintable.NewWriter(&b)
		q.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n")))
		q.Close()
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

// sendSMTP delivers one message. Certificates are always verified, and a
// password is never sent over an unencrypted connection (net/smtp refuses
// PLAIN authentication without TLS except to localhost).
func sendSMTP(s EmailSettings, m outgoingMail) error {
	address := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	config := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var e error
	if s.Security == "tls" {
		conn, e = tls.DialWithDialer(dialer, "tcp", address, config)
	} else {
		conn, e = dialer.Dial("tcp", address)
	}
	if e != nil {
		return e
	}
	_ = conn.SetDeadline(time.Now().Add(emailDeliveryDeadline))
	client, e := smtp.NewClient(conn, s.Host)
	if e != nil {
		conn.Close()
		return e
	}
	defer client.Close()
	if s.Security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS")
		}
		if e = client.StartTLS(config); e != nil {
			return e
		}
	}
	if s.Username != "" {
		if e = client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); e != nil {
			return e
		}
	}
	if e = client.Mail(s.From); e != nil {
		return e
	}
	if e = client.Rcpt(m.to); e != nil {
		return e
	}
	writer, e := client.Data()
	if e != nil {
		return e
	}
	if _, e = writer.Write(buildMessage(s, m)); e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	return client.Quit()
}

// queueMail hands a message to the background sender without blocking the
// request; delivery problems are logged without addresses.
func (a *App) queueMail(m outgoingMail) bool {
	select {
	case a.mail <- m:
		return true
	default:
		return false
	}
}

func (a *App) mailWorker() {
	for m := range a.mail {
		s, e := a.emailSettings()
		if e == nil && s.configured() {
			e = a.sendMail(s, a.branded(m))
		}
		if e != nil {
			log.Print("email delivery failed")
		}
	}
}

// notificationWorker sends bundled download notifications: the first one
// shortly after a download, then at most one per share every 15 minutes.
func (a *App) notificationWorker() {
	for {
		time.Sleep(time.Minute)
		a.flushNotifications(time.Now())
		a.flushRequestNotifications(time.Now())
		a.sendExpiryReminders(time.Now())
		a.flushPush(time.Now())
	}
}

func (a *App) flushNotifications(now time.Time) { a.sendNotifications(now, "") }

// sendNotifications sends the due notifications, and with closed set also
// that share's pending one at once, because its download limit closed it.
func (a *App) sendNotifications(now time.Time, closed string) {
	if !a.emailEnabled() {
		return
	}
	rows, e := a.db.Query(`SELECT s.id,s.title,s.notify_pending,s.notify_last,s.notify_language,s.notify_origin,u.email FROM shares s JOIN users u ON u.id=s.owner
 WHERE s.notify=1 AND s.notify_pending>0 AND (s.notify_last<=? OR s.id=?) AND u.email<>'' AND u.disabled=0`, now.Add(-notifyInterval).Unix(), closed)
	if e != nil {
		return
	}
	type due struct {
		id, title, language, origin, email string
		count, since                       int64
	}
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.title, &d.count, &d.since, &d.language, &d.origin, &d.email) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		if a.queueMail(notificationMail(d.email, d.title, d.count, d.language, d.origin, d.id == closed)) {
			_, _ = a.db.Exec("UPDATE shares SET notify_pending=notify_pending-?, notify_last=? WHERE id=?", d.count, now.Unix(), d.id)
		}
	}
}

// requestOrigin is the address links in emails, notifications and answers
// use: the one the administrator set when there is one, so a request's Host
// header never decides where they point; otherwise the request's own.
func (a *App) requestOrigin(r *http.Request) string {
	if origin := a.linkOrigin(); origin != "" {
		return origin
	}
	return a.ownOrigin(r)
}

// ownOrigin is the address the request itself came to.
func (a *App) ownOrigin(r *http.Request) string {
	scheme := "http"
	if a.secureRequest(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func emailLanguage(value string) string {
	if value == "sv" {
		return "sv"
	}
	return "en"
}

func humanSize(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value, unit := float64(size), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func emailButton(link, label string) string {
	return `<p style="margin:28px 0"><a href="` + html.EscapeString(link) + `" style="background:#ff5f00;color:#0a0a0a;text-decoration:none;font-weight:600;padding:12px 22px;border-radius:3px;display:inline-block">` + html.EscapeString(label) + `</a></p>`
}

func emailFrame(content string) string {
	return `<!doctype html><html><body style="margin:0;background:#f5f4f1;font-family:Helvetica,Arial,sans-serif;color:#141414">` +
		`<div style="max-width:560px;margin:0 auto;padding:32px 20px"><div style="background:#ffffff;border:1px solid #e4e1db;border-radius:4px;padding:32px">` +
		`<div style="font-family:monospace;font-size:11px;letter-spacing:2px;color:#c84a00;margin-bottom:20px">■ {BRAND}</div>` +
		content + `</div><p style="font-size:12px;color:#8b867f;margin:16px 4px">{brand} · private file sharing</p></div></body></html>`
}

var emailText = map[string]map[string]string{
	"en": {
		"subject":               "%s shared “%s” with you",
		"intro":                 "%s shared files with you.",
		"files":                 "%d files · %s",
		"more":                  "… and %d more",
		"open":                  "Open the files",
		"until":                 "Available until %s.",
		"never":                 "This link does not expire.",
		"password":              "The link is protected with a password. Ask %s for it.",
		"notified":              "“%s” was downloaded",
		"downloads":             "“%s” was downloaded %d times since the last notification.",
		"download":              "“%s” was downloaded.",
		"receivedSubject":       "New files for “%s”",
		"received":              "%d files were uploaded to “%s”.",
		"receivedOne":           "A file was uploaded to “%s”.",
		"inviteSubject":         "%s invited you to {brand}",
		"inviteIntro":           "%s invited you to share files with {brand}.",
		"inviteAction":          "Create your account",
		"inviteExpiry":          "The invitation is valid for 7 days.",
		"resetSubject":          "Reset your {brand} password",
		"emailChangedSubject":   "Your {brand} email address was changed",
		"emailChangedIntro":     "The email address of the account %s was changed to %s.",
		"emailChangedWarning":   "If you did not do this, sign in and change your password, or ask your administrator for help.",
		"resetIntro":            "Someone asked to reset the password for the account %s.",
		"resetAction":           "Choose a new password",
		"resetExpiry":           "The link is valid for one hour and works once. If you did not ask for this, ignore this email; your password stays the same.",
		"closed":                "The download limit has been reached, so the link is closed. Files not kept in your library will be deleted.",
		"storageOfflineSubject": "Storage offline: %s",
		"storageOffline":        "{brand} cannot reach the storage folder %s. Downloads and uploads of files stored there are paused; everything else keeps working, and the files come back by themselves when the folder is reachable again.",
		"storageOnlineSubject":  "Storage back online: %s",
		"storageOnline":         "The storage folder %s can be reached again. Downloads and uploads work as usual.",
		"limit":                 "Each file can be downloaded %d times.",
		"limitOne":              "Each file can be downloaded once.",
		"manage":                "Open {brand}",
		"test":                  "{brand} test email",
		"testbody":              "Email from {brand} works.",
		"expirySubject":         "“%s” expires tomorrow",
		"expiry":                "Your link “%s” stops working on %s.",
		"expiryKeep":            "Keep it open longer",
		"expiryOff":             "You can turn these reminders off under your email address in {brand}.",
	},
	"sv": {
		"subject":               "%s har delat ”%s” med dig",
		"intro":                 "%s har delat filer med dig.",
		"files":                 "%d filer · %s",
		"more":                  "… och %d till",
		"open":                  "Öppna filerna",
		"until":                 "Tillgänglig till och med %s.",
		"never":                 "Länken upphör inte.",
		"password":              "Länken är skyddad med ett lösenord. Fråga %s efter det.",
		"notified":              "”%s” har laddats ner",
		"downloads":             "”%s” har laddats ner %d gånger sedan förra aviseringen.",
		"download":              "”%s” har laddats ner.",
		"receivedSubject":       "Nya filer till ”%s”",
		"received":              "%d filer har laddats upp till ”%s”.",
		"receivedOne":           "En fil har laddats upp till ”%s”.",
		"inviteSubject":         "%s har bjudit in dig till {brand}",
		"inviteIntro":           "%s har bjudit in dig att dela filer med {brand}.",
		"inviteAction":          "Skapa ditt konto",
		"inviteExpiry":          "Inbjudan gäller i 7 dagar.",
		"resetSubject":          "Återställ ditt lösenord för {brand}",
		"emailChangedSubject":   "Din e-postadress hos {brand} har ändrats",
		"emailChangedIntro":     "E-postadressen för kontot %s har ändrats till %s.",
		"emailChangedWarning":   "Om det inte var du, logga in och byt lösenord, eller be din administratör om hjälp.",
		"resetIntro":            "Någon har bett om att återställa lösenordet för kontot %s.",
		"resetAction":           "Välj ett nytt lösenord",
		"resetExpiry":           "Länken gäller i en timme och fungerar en gång. Om det inte var du kan du strunta i mejlet; lösenordet är oförändrat.",
		"closed":                "Nedladdningsgränsen är nådd, så länken är stängd. Filer som inte sparas i ditt bibliotek raderas.",
		"storageOfflineSubject": "Lagring offline: %s",
		"storageOffline":        "{brand} når inte lagringsmappen %s. Nedladdningar och uppladdningar av filer som ligger där är pausade; allt annat fungerar, och filerna kommer tillbaka av sig själva när mappen går att nå igen.",
		"storageOnlineSubject":  "Lagringen är tillbaka: %s",
		"storageOnline":         "Lagringsmappen %s går att nå igen. Nedladdningar och uppladdningar fungerar som vanligt.",
		"limit":                 "Varje fil kan laddas ner %d gånger.",
		"limitOne":              "Varje fil kan laddas ner en gång.",
		"manage":                "Öppna {brand}",
		"test":                  "Testmejl från {brand}",
		"testbody":              "E-post från {brand} fungerar.",
		"expirySubject":         "”%s” går ut i morgon",
		"expiry":                "Din länk ”%s” slutar fungera %s.",
		"expiryKeep":            "Håll den öppen längre",
		"expiryOff":             "Du kan stänga av påminnelserna under din e-postadress i {brand}.",
	},
}

func shareMail(to, language, sender, link, message string, s Share) outgoingMail {
	t := emailText[emailLanguage(language)]
	var total int64
	for _, f := range s.Files {
		total += f.Size
	}
	var text, body strings.Builder
	fmt.Fprintf(&text, t["intro"]+"\n\n", sender)
	body.WriteString(`<p style="font-size:15px;margin:0 0 16px">` + html.EscapeString(fmt.Sprintf(t["intro"], sender)) + `</p>`)
	if message != "" {
		fmt.Fprintf(&text, "%s\n\n", message)
		body.WriteString(`<blockquote style="margin:0 0 20px;padding:12px 16px;border-left:3px solid #ff5f00;background:#f5f4f1;white-space:pre-wrap">` + html.EscapeString(message) + `</blockquote>`)
	}
	fmt.Fprintf(&text, "%s\n", s.Title)
	body.WriteString(`<h1 style="font-size:24px;font-weight:600;margin:0 0 6px">` + html.EscapeString(s.Title) + `</h1>`)
	if s.Description != "" {
		fmt.Fprintf(&text, "%s\n", s.Description)
		body.WriteString(`<p style="color:#5d5953;margin:0 0 10px;white-space:pre-wrap">` + html.EscapeString(s.Description) + `</p>`)
	}
	summary := fmt.Sprintf(t["files"], len(s.Files), humanSize(total))
	fmt.Fprintf(&text, "%s\n", summary)
	body.WriteString(`<p style="font-family:monospace;font-size:12px;color:#8b867f;margin:0 0 12px">` + html.EscapeString(summary) + `</p><ul style="padding-left:18px;margin:0;color:#141414">`)
	for i, f := range s.Files {
		if i == 15 {
			more := fmt.Sprintf(t["more"], len(s.Files)-15)
			fmt.Fprintf(&text, "  %s\n", more)
			body.WriteString(`<li style="color:#8b867f">` + html.EscapeString(more) + `</li>`)
			break
		}
		fmt.Fprintf(&text, "  - %s (%s)\n", f.Name, humanSize(f.Size))
		body.WriteString(`<li>` + html.EscapeString(f.Name) + ` <span style="color:#8b867f">(` + humanSize(f.Size) + `)</span></li>`)
	}
	body.WriteString(`</ul>`)
	fmt.Fprintf(&text, "\n%s: %s\n", t["open"], link)
	body.WriteString(emailButton(link, t["open"]))
	notes := []string{}
	if s.Expires == 0 {
		notes = append(notes, t["never"])
	} else {
		notes = append(notes, fmt.Sprintf(t["until"], time.Unix(s.Expires, 0).UTC().Format("2006-01-02")))
	}
	if s.DownloadLimit == 1 {
		notes = append(notes, t["limitOne"])
	} else if s.DownloadLimit > 1 {
		notes = append(notes, fmt.Sprintf(t["limit"], s.DownloadLimit))
	}
	if s.Protected {
		notes = append(notes, fmt.Sprintf(t["password"], sender))
	}
	for _, note := range notes {
		fmt.Fprintf(&text, "%s\n", note)
		body.WriteString(`<p style="font-size:13px;color:#5d5953;margin:0 0 4px">` + html.EscapeString(note) + `</p>`)
	}
	body.WriteString(`<p style="font-size:12px;color:#8b867f;margin:16px 0 0;word-break:break-all">` + html.EscapeString(link) + `</p>`)
	return outgoingMail{to: to, subject: fmt.Sprintf(t["subject"], sender, s.Title), text: text.String(), body: emailFrame(body.String())}
}

func notificationMail(to, title string, count int64, language, origin string, closed bool) outgoingMail {
	t := emailText[emailLanguage(language)]
	line := fmt.Sprintf(t["download"], title)
	if count > 1 {
		line = fmt.Sprintf(t["downloads"], title, count)
	}
	if closed {
		line += " " + t["closed"]
	}
	text := line + "\n"
	body := `<p style="font-size:16px;margin:0">` + html.EscapeString(line) + `</p>`
	if origin != "" {
		text += "\n" + t["manage"] + ": " + origin + "\n"
		body += emailButton(origin, t["manage"])
	}
	return outgoingMail{to: to, subject: fmt.Sprintf(t["notified"], title), text: text, body: emailFrame(body)}
}

func (a *App) emailRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/email", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		s, e := a.emailSettings()
		if internal(w, e) {
			return
		}
		passwordSet := s.Password != ""
		s.Password = ""
		respond(w, 200, map[string]any{"settings": s, "password_set": passwordSet, "configured": s.configured()})
	})
	m.HandleFunc("PUT /api/admin/email", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			EmailSettings
			ClearPassword bool `json:"clear_password"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.require(w, r, true); !ok {
			return
		}
		s := b.EmailSettings
		s.Host, s.Username, s.FromName = strings.TrimSpace(s.Host), strings.TrimSpace(s.Username), strings.TrimSpace(headerText(s.FromName))
		from, fromOK := validEmail(s.From)
		if s.Host == "" && s.From == "" {
			from, fromOK = "", true
		}
		if s.Host != "" && !validHost(s.Host) || !fromOK || s.Port < 1 || s.Port > 65535 ||
			(s.Security != "starttls" && s.Security != "tls" && s.Security != "none") ||
			len(s.Username) > 254 || len(s.Password) > 1024 || utf8.RuneCountInString(s.FromName) > 80 {
			fail(w, 400, "invalid_email_settings")
			return
		}
		s.From = from
		site, siteOK := validSiteURL(s.SiteURL)
		if !siteOK {
			fail(w, 400, "invalid_site_url")
			return
		}
		// The administrator's own address is the natural default; the stored
		// one is what is being cleared, so it is not used.
		if site == "" && s.Host != "" {
			site = a.publicOrigin
			if site == "" {
				site = a.ownOrigin(r)
			}
		}
		s.SiteURL = site
		if s.FromName == "" {
			s.FromName = "MagiSend"
		}
		current, e := a.emailSettings()
		if internal(w, e) {
			return
		}
		if s.Password == "" && !b.ClearPassword {
			s.Password = current.Password
		}
		if internal(w, exec(a, `UPDATE settings SET smtp_host=?,smtp_port=?,smtp_security=?,smtp_username=?,smtp_password=?,smtp_from=?,smtp_from_name=?,site_url=? WHERE id=1`,
			s.Host, s.Port, s.Security, s.Username, s.Password, s.From, s.FromName, s.SiteURL)) {
			return
		}
		a.loadSiteURL()
		passwordSet := s.Password != ""
		s.Password = ""
		if admin, e := a.currentUser(r); e == nil {
			a.audit(r, admin.Username, "settings.email", "")
		}
		respond(w, 200, map[string]any{"settings": s, "password_set": passwordSet, "configured": s.configured()})
	})
	m.HandleFunc("POST /api/admin/email/test", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			To       string `json:"to"`
			Language string `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		admin, ok := a.require(w, r, true)
		if !ok {
			return
		}
		to, valid := validEmail(b.To)
		if !valid {
			fail(w, 400, "invalid_email")
			return
		}
		s, e := a.emailSettings()
		if internal(w, e) {
			return
		}
		if !s.configured() {
			fail(w, 409, "email_not_configured")
			return
		}
		a.mu.Lock()
		limited := a.limited("email-test:" + admin.ID)
		a.mu.Unlock()
		if limited {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		t := emailText[emailLanguage(b.Language)]
		test := outgoingMail{to: to, subject: t["test"], text: t["testbody"] + "\n", body: emailFrame(`<p style="font-size:16px;margin:0">` + t["testbody"] + `</p>`)}
		if e = a.sendMail(s, a.branded(test)); e != nil {
			// The administrator needs the server's answer to fix the settings.
			respond(w, 502, map[string]string{"error": headerText(e.Error()), "error_code": "email_send_failed"})
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("PUT /api/account/email", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Email        string `json:"email"`
			RemindExpiry *bool  `json:"remind_expiry"`
			Password     string `json:"password"`
			Language     string `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		u, ok := a.require(w, r, false)
		if !ok {
			return
		}
		email, valid := optionalEmail(b.Email)
		if !valid {
			fail(w, 400, "invalid_email")
			return
		}
		var previous string
		if internal(w, a.db.QueryRow("SELECT email FROM users WHERE id=?", u.ID).Scan(&previous)) {
			return
		}
		// Password resets go to this address, so changing it takes the password,
		// and the old address hears about it.
		if !strings.EqualFold(email, previous) {
			if !a.confirmPassword(w, u.ID, b.Password) {
				return
			}
			if previous != "" && a.emailEnabled() {
				a.queueMail(emailChangedMail(previous, u.Username, email, b.Language))
			}
		}
		if internal(w, exec(a, "UPDATE users SET email=? WHERE id=?", email, u.ID)) {
			return
		}
		if b.RemindExpiry != nil && internal(w, exec(a, "UPDATE users SET remind_expiry=? WHERE id=?", *b.RemindExpiry, u.ID)) {
			return
		}
		if email == "" {
			_ = exec(a, "UPDATE shares SET notify=0, notify_pending=0 WHERE owner=?", u.ID)
		}
		respond(w, 200, map[string]string{"email": email})
	})
	m.HandleFunc("POST /api/shares/{id}/email", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			To       []string `json:"to"`
			Message  string   `json:"message"`
			Language string   `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.ownShare(w, r)
		if !ok {
			return
		}
		u, _ := a.currentUser(r)
		if !a.allowed(w, u.ID, "share") {
			return
		}
		if !s.Published || a.isLibraryContainer(s.ID) || a.isDeleting(s.ID) || shareExpired(s.Expires, time.Now().Unix()) {
			fail(w, 409, "share_not_available")
			return
		}
		if !a.emailEnabled() {
			fail(w, 409, "email_not_configured")
			return
		}
		if len(b.To) == 0 || len(b.To) > maxEmailRecipients {
			fail(w, 400, "too_many_recipients")
			return
		}
		seen := map[string]bool{}
		recipients := []string{}
		for _, raw := range b.To {
			to, valid := validEmail(raw)
			if !valid {
				respond(w, 400, map[string]string{"error": "Invalid email address", "error_code": "invalid_email", "address": headerText(raw)})
				return
			}
			if key := strings.ToLower(to); !seen[key] {
				seen[key] = true
				recipients = append(recipients, to)
			}
		}
		message := strings.TrimSpace(b.Message)
		if utf8.RuneCountInString(message) > maxEmailMessageRunes {
			fail(w, 400, "invalid_description")
			return
		}
		key := "email:" + u.ID
		if a.blocked(key, emailsPerHour-len(recipients)+1) {
			fail(w, 429, "too_many_attempts_wait")
			return
		}
		// Emails to outside addresses carry a link only to an address the
		// administrator chose.
		origin := a.linkOrigin()
		if origin == "" {
			fail(w, 409, "site_url_missing")
			return
		}
		link := origin + "/s/" + s.ID
		if s.Slug != "" {
			link = origin + "/s/" + s.Slug
		}
		for _, to := range recipients {
			if !a.queueMail(shareMail(to, b.Language, u.Username, link, message, s)) {
				fail(w, 503, "email_queue_full")
				return
			}
			a.recordAttempt(key, time.Hour)
		}
		respond(w, 202, map[string]int{"sent": len(recipients)})
	})
	m.HandleFunc("PUT /api/shares/{id}/notify", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Enabled  bool   `json:"enabled"`
			Language string `json:"language"`
		}
		if !decode(w, r, &b) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		s, ok := a.ownShare(w, r)
		if !ok {
			return
		}
		u, _ := a.currentUser(r)
		if b.Enabled {
			var email string
			if internal(w, a.db.QueryRow("SELECT email FROM users WHERE id=?", u.ID).Scan(&email)) {
				return
			}
			if email == "" {
				fail(w, 409, "account_email_missing")
				return
			}
			if !a.emailEnabled() {
				fail(w, 409, "email_not_configured")
				return
			}
		}
		if internal(w, exec(a, "UPDATE shares SET notify=?, notify_pending=0, notify_language=?, notify_origin=? WHERE id=?",
			b.Enabled, emailLanguage(b.Language), a.requestOrigin(r), s.ID)) {
			return
		}
		respond(w, 200, map[string]bool{"notify": b.Enabled})
	})
}

// recordAttempt counts one use of key in a window of the given length.
// Caller holds mu.
func (a *App) recordAttempt(key string, window time.Duration) {
	now := time.Now()
	v := a.attempts[key]
	if now.After(v.until) {
		v = attempt{until: now.Add(window)}
	}
	v.count++
	a.attempts[key] = v
}

// branded puts the installation's name into a message built with {brand}
// (and {BRAND} for capitals).
func (a *App) branded(m outgoingMail) outgoingMail {
	name := a.brandName()
	plain := strings.NewReplacer("{brand}", name, "{BRAND}", strings.ToUpper(name))
	markup := strings.NewReplacer("{brand}", html.EscapeString(name), "{BRAND}", html.EscapeString(strings.ToUpper(name)))
	m.subject, m.text, m.body = plain.Replace(m.subject), plain.Replace(m.text), markup.Replace(m.body)
	return m
}

func emailChangedMail(to, username, changed, language string) outgoingMail {
	t := emailText[emailLanguage(language)]
	shown := changed
	if shown == "" {
		shown = "—"
	}
	line := fmt.Sprintf(t["emailChangedIntro"], username, shown)
	text := line + "\n\n" + t["emailChangedWarning"] + "\n"
	body := `<p style="font-size:16px;margin:0">` + html.EscapeString(line) + `</p>` +
		`<p style="color:#5d5953;margin:16px 0 0">` + html.EscapeString(t["emailChangedWarning"]) + `</p>`
	return outgoingMail{to: to, subject: t["emailChangedSubject"], text: text, body: emailFrame(body)}
}

package main

import (
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Chat apps (Discord, Slack, Teams, WhatsApp, iMessage) read a link's page
// before anyone opens it and show a card from its meta tags. Share links get
// a card with the share's name, how many files it holds and its first
// picture; a password-protected or expired link gets a card that says only
// that, so the card never shows more than the page would.
type linkCard struct {
	title, description, image string
}

var cardText = map[string]map[string]string{
	"en": {
		"files":   "%d files · %s",
		"file":    "1 file · %s",
		"locked":  "Password-protected files",
		"expired": "This link has expired",
	},
	"sv": {
		"files":   "%d filer · %s",
		"file":    "1 fil · %s",
		"locked":  "Lösenordsskyddade filer",
		"expired": "Länken har gått ut",
	},
}

var sharePage = regexp.MustCompile(`^s/([A-Za-z0-9-]{1,64})$`)

// Caller holds mu.
func (a *App) shareCard(ref, language string) (linkCard, bool) {
	s, _, password, e := a.loadShareRef(ref)
	if e != nil || !s.Published || a.isLibraryContainer(s.ID) {
		return linkCard{}, false
	}
	t := cardText[emailLanguage(language)]
	brand := a.brandName()
	if shareExpired(s.Expires, time.Now().Unix()) {
		return linkCard{title: brand, description: t["expired"]}, true
	}
	if password != "" {
		return linkCard{title: brand, description: t["locked"]}, true
	}
	var total int64
	image := ""
	for _, f := range s.Files {
		total += f.Size
		if image == "" && f.HasThumb {
			image = "/api/public/" + s.ID + "/files/" + f.ID + "/thumbnail"
		}
	}
	summary := fmt.Sprintf(t["files"], len(s.Files), humanSize(total))
	if len(s.Files) == 1 {
		summary = fmt.Sprintf(t["file"], humanSize(total))
	}
	description := summary
	if s.Description != "" {
		description = s.Description + " · " + summary
	}
	return linkCard{title: s.Title, description: description, image: image}, true
}

var (
	titleTag       = regexp.MustCompile(`<title>[^<]*</title>`)
	descriptionTag = regexp.MustCompile(`<meta name="description" content="[^"]*"\s*/?>`)
	ogTitleTag     = regexp.MustCompile(`<meta property="og:title" content="[^"]*"\s*/?>`)
	ogImageTag     = regexp.MustCompile(`<meta property="og:image" content="[^"]*"\s*/?>`)
)

// cardPage returns the app shell with the card's tags, or nil when the path
// is not a share link that has one.
func (a *App) cardPage(r *http.Request, name string) []byte {
	match := sharePage.FindStringSubmatch(name)
	if match == nil {
		return nil
	}
	a.mu.Lock()
	// The first language the reader's app asks for, e.g. "sv" from "sv-SE,en".
	language := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language")))
	card, ok := a.shareCard(match[1], language[:min(2, len(language))])
	origin := a.linkOrigin()
	brand := a.brandName()
	a.mu.Unlock()
	if !ok {
		return nil
	}
	page, e := fs.ReadFile(a.ui, "index.html")
	if e != nil {
		return nil
	}
	image := card.image
	kind := "summary"
	if image == "" {
		image = "/og.png"
		kind = "summary_large_image"
	}
	// Chat apps need a full address for the picture; without a configured
	// site address the path is the best there is.
	image = origin + image
	title := html.EscapeString(card.title)
	description := html.EscapeString(card.description)
	text := string(page)
	text = titleTag.ReplaceAllLiteralString(text, "<title>"+title+"</title>")
	text = descriptionTag.ReplaceAllLiteralString(text, `<meta name="description" content="`+description+`" />`)
	text = ogTitleTag.ReplaceAllLiteralString(text, `<meta property="og:title" content="`+title+`" />`)
	text = ogImageTag.ReplaceAllLiteralString(text, `<meta property="og:image" content="`+html.EscapeString(image)+`" />`)
	extra := `<meta property="og:description" content="` + description + `" />` +
		`<meta property="og:site_name" content="` + html.EscapeString(brand) + `" />` +
		`<meta property="og:type" content="website" />` +
		`<meta name="twitter:card" content="` + kind + `" />`
	if origin != "" {
		extra += `<meta property="og:url" content="` + html.EscapeString(origin+"/"+name) + `" />`
	}
	return []byte(strings.Replace(text, "</head>", extra+"</head>", 1))
}

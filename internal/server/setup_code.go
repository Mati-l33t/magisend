package server

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The first administrator account can only be created with a one-time setup
// code, so whoever reaches a fresh installation first over the network cannot
// claim it. The code is made on the first start, kept in the data folder
// (readable by the app and root only) and written to the log; installers
// print it. MAGISEND_SETUP_CODE sets it instead, for automated setups. The
// file is removed once the account exists.
const (
	setupCodeFile     = "setup-code"
	setupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	setupFailures     = 10
)

// prepareSetupCode makes or loads the code while setup is still open.
func (a *App) prepareSetupCode() error {
	required, e := a.setupRequired()
	if e != nil || !required {
		return e
	}
	if code := normalSetupCode(os.Getenv("MAGISEND_SETUP_CODE")); code != "" {
		a.setupCode = code
		log.Print("setup: the first administrator needs the setup code from MAGISEND_SETUP_CODE")
		return nil
	}
	path := filepath.Join(a.root, setupCodeFile)
	if data, e := readLimited(path, 128); e == nil {
		if code := normalSetupCode(string(data)); len(code) >= 12 {
			a.setupCode = code
			log.Printf("setup: create the first administrator with setup code %s (also in %s)", formatSetupCode(code), path)
			return nil
		}
	}
	code, e := newSetupCode()
	if e != nil {
		return e
	}
	_ = os.Remove(path)
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("the setup code cannot be saved: " + e.Error())
	}
	if _, e = file.WriteString(formatSetupCode(code) + "\n"); e == nil {
		e = file.Sync()
	}
	if closeErr := file.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	a.setupCode = code
	log.Printf("setup: create the first administrator with setup code %s (also in %s)", formatSetupCode(code), path)
	return nil
}

func newSetupCode() (string, error) {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	for i := range b {
		b[i] = setupCodeAlphabet[int(b[i])%len(setupCodeAlphabet)]
	}
	return string(b), nil
}

// normalSetupCode ignores case, spaces and dashes, as people type codes.
func normalSetupCode(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return -1
	}, s)
}

func formatSetupCode(code string) string {
	var parts []string
	for len(code) > 4 {
		parts = append(parts, code[:4])
		code = code[4:]
	}
	return strings.Join(append(parts, code), "-")
}

func (a *App) setupCodeMatches(given string) bool {
	want := a.setupCode
	return want != "" && subtle.ConstantTimeCompare([]byte(normalSetupCode(given)), []byte(want)) == 1
}

// setupDone forgets the code once the first account exists.
func (a *App) setupDone() {
	a.setupCode = ""
	_ = os.Remove(filepath.Join(a.root, setupCodeFile))
}

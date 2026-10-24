package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

// Every release's SHA256SUMS is signed with an Ed25519 key that is kept off
// GitHub. Before installing a new release, the updater asks the installed,
// already trusted program to check that signature, so taking over the GitHub
// account or the release downloads is not enough to push an update.
var releaseKeys = []string{
	"mwQ+AfwwjYrkLcEkTQW5Yw2qRY2gss/dC7ourfTQe7E=",
}

const (
	maxSignedFile    = 1 << 20
	maxSignatureFile = 4 << 10
)

// verifyRelease checks that sig (base64, one line) is a valid signature of
// the file at sumsPath by one of the release keys.
func verifyRelease(sumsPath, sigPath string) error {
	return verifyWith(releaseKeys, sumsPath, sigPath)
}

func verifyWith(keys []string, sumsPath, sigPath string) error {
	data, e := readLimited(sumsPath, maxSignedFile)
	if e != nil {
		return e
	}
	text, e := readLimited(sigPath, maxSignatureFile)
	if e != nil {
		return e
	}
	sig, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
	if e != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("the signature file is not valid")
	}
	for _, k := range keys {
		key, e := base64.StdEncoding.DecodeString(k)
		if e != nil || len(key) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(ed25519.PublicKey(key), data, sig) {
			return nil
		}
	}
	return errors.New("the release signature does not match")
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	data, e := io.ReadAll(io.LimitReader(file, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > limit {
		return nil, errors.New(path + " is too large")
	}
	return data, nil
}

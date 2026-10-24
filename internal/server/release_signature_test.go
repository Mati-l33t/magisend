package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseSignature(t *testing.T) {
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	_, otherPrivate, _ := ed25519.GenerateKey(rand.Reader)
	keys := []string{base64.StdEncoding.EncodeToString(public)}
	dir := t.TempDir()
	sums := filepath.Join(dir, "SHA256SUMS")
	sig := filepath.Join(dir, "SHA256SUMS.sig")
	content := []byte("abc  magisend_1.0.1_linux_amd64.tar.gz\n")
	write := func(path string, data []byte) {
		t.Helper()
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(sums, content)
	write(sig, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, content))+"\n"))
	if e := verifyWith(keys, sums, sig); e != nil {
		t.Fatal("a valid signature was rejected:", e)
	}
	// A changed checksum list, another key, or a broken signature file fail.
	write(sums, []byte("abd  magisend_1.0.1_linux_amd64.tar.gz\n"))
	if verifyWith(keys, sums, sig) == nil {
		t.Fatal("a changed file passed")
	}
	write(sums, content)
	write(sig, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(otherPrivate, content))))
	if verifyWith(keys, sums, sig) == nil {
		t.Fatal("another key's signature passed")
	}
	for _, bad := range []string{"", "not base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		write(sig, []byte(bad))
		if verifyWith(keys, sums, sig) == nil {
			t.Fatalf("signature %q passed", bad)
		}
	}
	if verifyWith(keys, filepath.Join(dir, "missing"), sig) == nil {
		t.Fatal("a missing file passed")
	}
	// The shipped key is well formed.
	for _, k := range releaseKeys {
		if key, e := base64.StdEncoding.DecodeString(k); e != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("malformed release key", k)
		}
	}
}

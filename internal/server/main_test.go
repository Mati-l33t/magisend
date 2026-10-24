package server

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests run in this folder; the error catalog and the built interface live in
// web/ at the repository root, as they do for the command.
func TestMain(m *testing.M) {
	root := filepath.Join("..", "..")
	contract, e := os.ReadFile(filepath.Join(root, "web", "backend-errors-contract.json"))
	if e != nil {
		panic(e)
	}
	if e = SetErrorCatalog(contract); e != nil {
		panic(e)
	}
	os.Setenv("MAGISEND_SETUP_CODE", "TEST-SETUP-CODE")
	if os.Getenv("MAGISEND_WEB") == "" {
		os.Setenv("MAGISEND_WEB", filepath.Join(root, "web", "dist"))
	}
	os.Exit(m.Run())
}

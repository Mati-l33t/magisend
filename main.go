// Command magisend is a private, self-hosted file sharing server.
package main

import (
	_ "embed"

	"magisend/internal/server"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// The API error catalog is shared with the web interface's translation checks.
//
//go:embed web/backend-errors-contract.json
var errorCatalog []byte

func main() { server.Main(version, webInterface(), errorCatalog) }

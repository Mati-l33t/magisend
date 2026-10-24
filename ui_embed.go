//go:build embedui

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var embeddedWeb embed.FS

// Release builds carry the interface inside the binary, so the server and the
// interface it serves always come from the same version.
func webInterface() fs.FS {
	ui, e := fs.Sub(embeddedWeb, "web/dist")
	if e != nil {
		panic(e)
	}
	return ui
}

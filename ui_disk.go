//go:build !embedui

package main

import "io/fs"

// Development builds serve the interface from disk (MAGISEND_WEB, default
// web/dist). Release builds use -tags embedui and carry it inside the binary.
func webInterface() fs.FS { return nil }

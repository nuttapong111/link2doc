// Package web embeds the single-page UI so the binary is self-contained.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

func Static() fs.FS {
	sub, _ := fs.Sub(files, "static")
	return sub
}

// Package ui serves the shared browser and desktop frontend.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

// Build with: just frontend.
//
//go:embed all:dist
var assets embed.FS

func Handler() http.Handler {
	files, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(files))
}

// Package web serves the RouteCat public frontend: landing page, API docs,
// pricing, and a playground for testing models.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an HTTP handler serving the embedded static frontend.
func Handler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	return http.FileServer(http.FS(sub))
}

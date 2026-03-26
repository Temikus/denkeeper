package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded Svelte SPA.
// Requests for files that exist in the embedded filesystem are served directly
// with appropriate cache headers. All other paths fall back to index.html so
// the client-side hash router handles navigation.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: failed to sub dist FS: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		if _, err := fs.Stat(sub, path); err != nil {
			// File not found in embedded FS — serve index.html for SPA routing.
			// Use "/" so http.FileServer serves the directory index directly;
			// setting path to "/index.html" triggers a 301 redirect in Go's file server.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, r2)
			return
		}

		// Hashed assets (e.g. /assets/index-abc123.js) are immutable.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

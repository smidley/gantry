//go:build webdist

package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// webFS embeds the Vite build output. This file (and the embed
// directive below) only compiles under -tags webdist, i.e. `make
// release`, the Dockerfile's go-build stage, and CI's dedicated webdist
// build step -- every one of which runs `make web` first to populate
// internal/server/webdist/ (gitignored, never tracked) before this
// directive is ever evaluated. A plain `go build` / `go test ./...`
// never compiles this file at all, so it never needs that directory to
// exist.
//
//go:embed all:webdist
var webFS embed.FS

// webHandler serves the Vite-built SPA from the embedded webdist
// directory, with an SPA fallback: any request path that doesn't match
// a real embedded file falls back to index.html. The hash router means
// the server almost never actually sees a deep path -- everything after
// "#" never leaves the browser -- so this fallback is mostly moot in
// practice, but it's still the correct behavior for a stray direct
// request (a bookmark, a typo, a health-check probe hitting a
// non-existent asset path).
func webHandler() http.Handler {
	sub, err := fs.Sub(webFS, "webdist")
	if err != nil {
		panic(err) // embedded FS shape is a compile-time property
	}
	fileServer := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			// An /api/* path that didn't match one of server.go's specific
			// route patterns (a typo, a route from a future version, ...)
			// falling through to this catch-all must 404, not silently
			// receive the app shell -- see isAPIPath's own doc.
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		upath := strings.TrimPrefix(r.URL.Path, "/")
		if upath == "" {
			upath = "."
		}
		if _, err := fs.Stat(sub, upath); err != nil {
			// No matching embedded file for this path (or an invalid
			// path, e.g. a ".." traversal attempt -- fs.Stat rejects
			// those via fs.ValidPath the same way) -- serve the app
			// shell instead of a bare 404, on a request cloned with its
			// path reset to "/" so FileServerFS resolves index.html.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

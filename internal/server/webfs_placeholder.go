//go:build !webdist

package server

import "net/http"

// placeholderHTML is served at "/" for every request path when the binary
// is built WITHOUT the webdist tag (plain `go build`, `make build`, and
// every `go test` invocation) -- i.e. no node/vite toolchain required for
// backend development or tests. See webfs_dist.go for the -tags webdist
// counterpart that embeds the real Vite app shell instead. Content is
// carried over byte-for-byte from the tracked placeholder this build tag
// flip retires (internal/server/webdist/index.html).
const placeholderHTML = `<!doctype html>
<meta charset="utf-8">
<title>Gantry</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; display: grid; place-items: center;
         min-height: 100vh; margin: 0; background: #0f172a; color: #e2e8f0; }
  @media (prefers-color-scheme: light) { body { background: #f8fafc; color: #0f172a; } }
</style>
<div>
  <h1>Gantry</h1>
  <p>The UI ships in Phase 3. <a href="/api/healthz">healthz</a></p>
</div>
`

// webHandler serves the inline placeholder page at "/" -- the !webdist
// build has no real SPA to route within, so every path gets the same
// simple 200 rather than any attempt at file-shaped dispatch.
func webHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(placeholderHTML)) // write failure here means the client already disconnected
	})
}

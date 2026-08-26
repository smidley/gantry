package server

import "strings"

// apiPathPrefix is every request path this server treats as part of the
// JSON /api surface rather than the SPA/placeholder shell.
const apiPathPrefix = "/api/"

// isAPIPath reports whether path belongs to the /api surface. server.go
// registers each /api/... route as its own exact pattern, and the SPA/
// placeholder webHandler as the catch-all "GET /" -- so any request path
// that LOOKS like an API call but doesn't match one of those exact
// patterns (a typo, a route from a future version, a stray probe) still
// reaches webHandler rather than a 404 from the mux itself. Without this
// check, webfs_dist.go's SPA fallback would serve that request
// index.html with a 200 -- a misleading response for what's obviously a
// broken API call -- and webfs_placeholder.go's placeholder would do the
// same with its own 200 HTML page. Both call this before falling back so
// an unmatched /api/* path gets a real 404 instead.
//
// Deliberately build-tag-free (unlike webfs_dist.go/webfs_placeholder.go
// themselves) so this pure classification logic is unit-testable via a
// plain `go test ./...`, without needing -tags webdist or a populated
// webdist/ directory.
func isAPIPath(path string) bool {
	return strings.HasPrefix(path, apiPathPrefix)
}

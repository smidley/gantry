package server

import "testing"

// TestIsAPIPath pins the path-classification rule webfs_dist.go's SPA
// fallback and webfs_placeholder.go's placeholder both guard on before
// serving their own catch-all response -- see isAPIPath's doc for why
// this lives in its own build-tag-free file: the dist behavior itself
// (embedded-file lookup, index.html fallback) can only be exercised
// under -tags webdist, but the classification rule it depends on is
// pure and deserves an ordinary `go test ./...` case.
func TestIsAPIPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/live/snapshot", true},
		{"/api/bogus-route-that-does-not-exist", true},
		{"/api/containers/jellyfin/logs", true},
		{"/api/", true},
		{"/api", false},    // no trailing slash -- not a matched-prefix path
		{"/apifoo", false}, // "api" as a literal path-segment prefix, not the /api/ tree
		{"/", false},
		{"/index.html", false},
		{"/assets/main.js", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isAPIPath(c.path); got != c.want {
			t.Errorf("isAPIPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

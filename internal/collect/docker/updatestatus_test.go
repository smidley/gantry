package docker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNormalizeImageRef pins the implicit ":latest" tag docker elides
// from Config.Image when a container was created without an explicit
// one -- the join against unraid-update-status.json's own keys (which
// always carry an explicit tag) needs the same form dockerMan itself
// would have used. The registry-port case is the whole reason this
// isn't a plain "does the string contain a colon" check: "host:port/repo"
// carries a colon that is a port separator, not a tag separator.
func TestNormalizeImageRef(t *testing.T) {
	cases := []struct {
		name  string
		image string
		want  string
	}{
		{name: "already tagged", image: "ghcr.io/advplyr/audiobookshelf:latest", want: "ghcr.io/advplyr/audiobookshelf:latest"},
		{name: "no tag gets :latest appended", image: "ghcr.io/advplyr/audiobookshelf", want: "ghcr.io/advplyr/audiobookshelf:latest"},
		{name: "official image with no registry or tag", image: "redis", want: "redis:latest"},
		{name: "official image already tagged", image: "redis:7", want: "redis:7"},
		{name: "registry host with a port and no tag", image: "localhost:5000/myimage", want: "localhost:5000/myimage:latest"},
		{name: "registry host with a port and an explicit tag", image: "localhost:5000/myimage:1.0", want: "localhost:5000/myimage:1.0"},
		{name: "pinned by digest, left untouched", image: "redis@sha256:deadbeef", want: "redis@sha256:deadbeef"},
		{name: "empty", image: "", want: ":latest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, normalizeImageRef(c.image))
		})
	}
}

// TestJoinUpdateStatus pins the join itself: normalization happens
// before the lookup, a nil statuses map (no reader wired, or the file
// was unreadable this poll) degrades to "" rather than panicking, and a
// normalized image with no entry in statuses is likewise "".
func TestJoinUpdateStatus(t *testing.T) {
	statuses := map[string]string{"ghcr.io/advplyr/audiobookshelf:latest": "available"}

	require.Equal(t, "available", joinUpdateStatus("ghcr.io/advplyr/audiobookshelf", statuses), "no-tag image must normalize to :latest before the lookup")
	require.Equal(t, "available", joinUpdateStatus("ghcr.io/advplyr/audiobookshelf:latest", statuses))
	require.Equal(t, "", joinUpdateStatus("ghcr.io/someone/else:latest", statuses), "no entry for this image")
	require.Equal(t, "", joinUpdateStatus("ghcr.io/advplyr/audiobookshelf", nil), "nil statuses (no reader wired, or unreadable this poll) must not panic")
}

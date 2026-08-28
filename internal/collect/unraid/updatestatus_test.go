package unraid

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseUpdateStatus is a table test over dockerMan's real-box JSON
// shape: {"<image-ref>": {"local": "sha256:...", "remote": "sha256:...",
// "status": "true"|"false"|...}}. status "true" means up to date,
// "false" means an update is available; anything else (an unrecognized
// value, or the key missing entirely) is dropped from the result rather
// than guessed at -- absence from the map already means "unknown" to
// the docker-package join (joinUpdateStatus).
func TestParseUpdateStatus(t *testing.T) {
	cases := []struct {
		name string
		data string
		want map[string]string
	}{
		{
			name: "real shape: mixed current/available/unknown",
			data: `{
				"ghcr.io/advplyr/audiobookshelf:latest": {"local": "sha256:aaa", "remote": "sha256:bbb", "status": "false"},
				"jellyfin/jellyfin:latest": {"local": "sha256:ccc", "remote": "sha256:ccc", "status": "true"},
				"linuxserver/radarr:latest": {"local": "sha256:ddd", "remote": "sha256:eee", "status": "pending"}
			}`,
			want: map[string]string{
				"ghcr.io/advplyr/audiobookshelf:latest": "available",
				"jellyfin/jellyfin:latest":              "current",
			},
		},
		{name: "empty object", data: `{}`, want: map[string]string{}},
		{name: "entry missing the status key entirely", data: `{"foo/bar:latest": {"local": "sha256:aaa"}}`, want: map[string]string{}},
		{name: "malformed json", data: `not json`, want: nil},
		{name: "empty file (the @unlink'd state)", data: ``, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseUpdateStatus([]byte(c.data))
			require.Equal(t, c.want, got)
		})
	}
}

// TestUpdateStatusReaderStatusesMissingFile pins the "absent file =
// feature silently off" contract: no error, just a nil map.
func TestUpdateStatusReaderStatusesMissingFile(t *testing.T) {
	r := NewUpdateStatusReader(filepath.Join(t.TempDir(), "no-such-file.json"))
	require.Nil(t, r.Statuses())
}

// TestUpdateStatusReaderStatusesBailsOnOversizedFile pins the size
// guard: a file above maxUpdateStatusFileSize is never read at all
// (Stat alone decides), degrading to nil the same as a missing file --
// dockerMan's own file is a handful of KB per image, so anything this
// large is corruption or hostile, not a real box's own data.
func TestUpdateStatusReaderStatusesBailsOnOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unraid-update-status.json")
	oversized := make([]byte, maxUpdateStatusFileSize+1)
	require.NoError(t, os.WriteFile(path, oversized, 0o644))

	r := NewUpdateStatusReader(path)
	require.Nil(t, r.Statuses(), "a file above the size cap must never be read, not just fail to parse")
}

// TestUpdateStatusReaderStatusesReadsRealFile pins the ordinary case:
// a real file on disk, parsed the same way ParseUpdateStatus's own
// tests pin.
func TestUpdateStatusReaderStatusesReadsRealFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unraid-update-status.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"jellyfin/jellyfin:latest": {"status": "true"}}`), 0o644))

	r := NewUpdateStatusReader(path)
	require.Equal(t, map[string]string{"jellyfin/jellyfin:latest": "current"}, r.Statuses())
}

// TestUpdateStatusReaderStatusesSelfHealsAcrossUnlink pins the box's
// own documented quirk: dockerMan rewrites this file in place via PHP's
// file_put_contents (same inode, so a single-file bind mount stays
// live), but occasionally @unlink's it on empty output. Statuses must
// degrade to nil the moment the file's gone and recover the moment
// dockerMan writes it again -- no cached/sticky state of its own.
func TestUpdateStatusReaderStatusesSelfHealsAcrossUnlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unraid-update-status.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"jellyfin/jellyfin:latest": {"status": "true"}}`), 0o644))

	r := NewUpdateStatusReader(path)
	require.Equal(t, map[string]string{"jellyfin/jellyfin:latest": "current"}, r.Statuses())

	require.NoError(t, os.Remove(path))
	require.Nil(t, r.Statuses(), "an unlinked file must degrade to nil, not the last-seen map")

	require.NoError(t, os.WriteFile(path, []byte(`{"jellyfin/jellyfin:latest": {"status": "false"}}`), 0o644))
	require.Equal(t, map[string]string{"jellyfin/jellyfin:latest": "available"}, r.Statuses(), "a rewritten file must be picked up on the very next read, self-healing")
}

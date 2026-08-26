package unraid

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestTickSharesExactBytesPerShare(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/shares.ini", filepath.Join(dir, "shares.ini"))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickShares(time.Unix(1000, 0))

	require.InDelta(t, 1073741824,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.appdata.used_bytes"}], 1e-9)
	require.InDelta(t, 2147483648,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.media.used_bytes"}], 1e-9)
}

// TestTickSharesRealCaptureFromLiveUnraidBox replays a trimmed, anonymized
// shares.ini captured from a live Unraid 7.3.2 box (see
// docs/superpowers/fixtures.md). It also locks in a real, easy-to-misread
// shape: a share with no dedicated cache pool reports the whole array's
// used/free space (not its own footprint), and two shares pinned to the
// same cache pool report identical numbers.
//
// The capture also has "share2" and "Share2" -- two real shares whose
// names differ only by the case of one letter. Task 1's SlugSegment
// lowercases every share name for metric-name hygiene, so post-slug they
// now collide onto one series (share.share2.used_bytes) instead of two;
// this is an accepted, intentional trade of that one pre-existing edge
// case for uniform, predictable metric names everywhere else -- and the
// collapsed series still reports the right number, since both shares were
// already pinned to the same cache pool.
func TestTickSharesRealCaptureFromLiveUnraidBox(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/shares_real.ini", filepath.Join(dir, "shares.ini"))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickShares(time.Unix(1000, 0))

	// share1: no dedicated cache pool -- "used" mirrors the whole array's
	// used space (verified against the real disks.ini capture: it's the
	// exact sum of every data disk's fsUsed), not this share's own footprint.
	require.InDelta(t, 155231611535360.0,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.share1.used_bytes"}], 1e-6)

	// share2 and Share2 both slug to "share2"; they were pinned to the
	// same cache pool anyway, so the now-single series still reports the
	// correct (identical either way) number.
	require.InDelta(t, 625070358528.0,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.share2.used_bytes"}], 1e-6)
	_, hasCapitalized := sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.Share2.used_bytes"}]
	require.False(t, hasCapitalized, "share names are slugged (lowercased) before entering the metric name")

	// share3: pinned to a different pool (rocket_pool) -- that pool's used/free.
	require.InDelta(t, 545635610624.0,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.share3.used_bytes"}], 1e-6)
}

// TestTickSharesSlugsNameWithSpacesAndDots pins Task 1's hygiene fix: a
// share name with spaces and dots used to fracture the metric name into
// something SQLite/downstream consumers can't rely on; it must now slug
// to one clean segment.
func TestTickSharesSlugsNameWithSpacesAndDots(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/shares_slug.ini", filepath.Join(dir, "shares.ini"))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickShares(time.Unix(1000, 0))

	require.InDelta(t, 1073741824,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.my_movies_4k.used_bytes"}], 1e-9)
}

func TestTickSharesMissingFileDegradesSilently(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())
	c.tickShares(time.Unix(1000, 0))
	require.Empty(t, sink.records)
}

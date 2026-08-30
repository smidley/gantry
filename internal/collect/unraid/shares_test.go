package unraid

import (
	"os"
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
	require.Empty(t, c.SharePlacement())
}

// TestTickSharesPlacementFromRealCapture pins the real useCache/cachePool
// shape against testdata/shares_real.ini (see
// TestTickSharesRealCaptureFromLiveUnraidBox's own doc for its
// provenance) -- share1 (useCache="no"), share2/Share2 (useCache="only",
// cachePool="cache"), and share3 (useCache="only", cachePool=
// "rocket_pool", a different pool than share2's). Keyed by the RAW
// share name (shares.ini's own section header, unslugged) since that's
// what ResolveStoragePath's own share-name extraction returns from a
// mount path -- unlike the used_bytes metric name above, share2/Share2
// do NOT collide here.
func TestTickSharesPlacementFromRealCapture(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/shares_real.ini", filepath.Join(dir, "shares.ini"))
	c := New(newFakeSink(), &fakeEvents{}, dir, t.TempDir())

	c.tickShares(time.Unix(1000, 0))

	placement := c.SharePlacement()
	require.Equal(t, SharePlacement{Mode: "no", Pool: ""}, placement["share1"])
	require.Equal(t, SharePlacement{Mode: "only", Pool: "cache"}, placement["share2"])
	require.Equal(t, SharePlacement{Mode: "only", Pool: "cache"}, placement["Share2"])
	require.Equal(t, SharePlacement{Mode: "only", Pool: "rocket_pool"}, placement["share3"])
}

// TestTickSharesPlacementCoversYesAndPrefer locks in the two useCache
// values the real capture above doesn't happen to exercise.
func TestTickSharesPlacementCoversYesAndPrefer(t *testing.T) {
	dir := t.TempDir()
	ini := `["backups"]
name="backups"
useCache="yes"
cachePool="cache"
used="1048576"
["hot"]
name="hot"
useCache="prefer"
cachePool="rocket_pool"
used="2097152"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shares.ini"), []byte(ini), 0o644))
	c := New(newFakeSink(), &fakeEvents{}, dir, t.TempDir())

	c.tickShares(time.Unix(1000, 0))

	placement := c.SharePlacement()
	require.Equal(t, SharePlacement{Mode: "yes", Pool: "cache"}, placement["backups"])
	require.Equal(t, SharePlacement{Mode: "prefer", Pool: "rocket_pool"}, placement["hot"])
}

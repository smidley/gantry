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

	// share2 and Share2: two real shares whose names differ only by the
	// case of one letter, pinned to the same cache pool -- both report
	// that pool's used/free, so both come out identical, but as
	// independent series.
	require.InDelta(t, 625070358528.0,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.share2.used_bytes"}], 1e-6)
	require.InDelta(t, 625070358528.0,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.Share2.used_bytes"}], 1e-6,
		"a case-differing share name must be its own series, not merged with share2")

	// share3: pinned to a different pool (rocket_pool) -- that pool's used/free.
	require.InDelta(t, 545635610624.0,
		sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.share3.used_bytes"}], 1e-6)
}

func TestTickSharesMissingFileDegradesSilently(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())
	c.tickShares(time.Unix(1000, 0))
	require.Empty(t, sink.records)
}

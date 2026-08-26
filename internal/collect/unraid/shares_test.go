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

func TestTickSharesMissingFileDegradesSilently(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())
	c.tickShares(time.Unix(1000, 0))
	require.Empty(t, sink.records)
}

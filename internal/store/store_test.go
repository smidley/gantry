package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T, clock func() time.Time) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gantry.db"), clock)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreRecordGoesToLive(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	s.Record(k, 100, 7.5)
	got, ok := s.Live().Latest(k)
	require.True(t, ok)
	require.Equal(t, 7.5, got.Val)
}

func TestSeriesIDStableAndCached(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "cpu"}

	id1, err := s.seriesID(k)
	require.NoError(t, err)
	id2, err := s.seriesID(k)
	require.NoError(t, err)
	require.Equal(t, id1, id2)

	other, err := s.seriesID(SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "mem"})
	require.NoError(t, err)
	require.NotEqual(t, id1, other)

	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM series`).Scan(&n))
	require.Equal(t, 2, n)
}

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T, clock func() time.Time) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gantry.db"), clock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
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

	id1, err := s.seriesID(context.Background(), k)
	require.NoError(t, err)
	id2, err := s.seriesID(context.Background(), k)
	require.NoError(t, err)
	require.Equal(t, id1, id2)

	other, err := s.seriesID(context.Background(), SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "mem"})
	require.NoError(t, err)
	require.NotEqual(t, id1, other)

	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM series`).Scan(&n))
	require.Equal(t, 2, n)
}

// TestReadPoolConcurrentWithWriteHandle is the I3 regression guard: the
// read pool (MaxOpenConns(4)) and the single-writer handle (MaxOpenConns
// (1)) must both be usable at once from many goroutines without racing or
// deadlocking — that's the entire point of WAL mode allowing concurrent
// readers alongside one writer.
func TestReadPoolConcurrentWithWriteHandle(t *testing.T) {
	s := newTestStore(t, nil)
	require.NotNil(t, s.ReadDB())
	require.NotSame(t, s.DB(), s.ReadDB(), "read pool must be a distinct handle from the write handle")

	var wg sync.WaitGroup
	errCh := make(chan error, 40)

	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_, err := s.DB().Exec(`INSERT INTO series (kind, entity, metric, created_at) VALUES (?,?,?,?)`,
				"host", "", fmt.Sprintf("m%d", i), 0)
			errCh <- err
		}(i)
		go func() {
			defer wg.Done()
			var n int
			errCh <- s.ReadDB().QueryRow(`SELECT count(*) FROM series`).Scan(&n)
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	var total int
	require.NoError(t, s.ReadDB().QueryRow(`SELECT count(*) FROM series`).Scan(&total))
	require.Equal(t, 10, total)
}

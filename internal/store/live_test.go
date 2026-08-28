package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveRecordAndRead(t *testing.T) {
	l := NewLive(8)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	l.Record(k, 100, 42.0)
	l.Record(k, 102, 43.0)

	latest, ok := l.Latest(k)
	require.True(t, ok)
	require.Equal(t, Sample{TS: 102, Val: 43.0}, latest)
	require.Len(t, l.Since(k, 0), 2)
	require.Equal(t, []SeriesKey{k}, l.Keys())
}

func TestLiveConcurrentRecord(t *testing.T) {
	l := NewLive(512)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := SeriesKey{Kind: "container", Entity: string(rune('a' + g)), Metric: "cpu"}
			for i := int64(0); i < 100; i++ {
				l.Record(k, i, float64(i))
			}
		}(g)
	}
	wg.Wait()
	require.Len(t, l.Keys(), 8)
	for _, k := range l.Keys() {
		require.Len(t, l.Since(k, 0), 100)
	}
}

// TestLiveSnapshotLatestMatchesPerKeyLatest pins Task 3's single-lock
// snapshot: SnapshotLatest's result must be identical to calling
// Latest(key) for every key returned by Keys() -- just taken under one
// read-lock pass instead of N+1.
func TestLiveSnapshotLatestMatchesPerKeyLatest(t *testing.T) {
	l := NewLive(8)
	hostKey := SeriesKey{Kind: "host", Metric: "cpu.total"}
	diskKey := SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}
	l.Record(hostKey, 100, 1.0)
	l.Record(hostKey, 102, 2.0) // latest for hostKey
	l.Record(diskKey, 100, 30.0)

	snap := l.SnapshotLatest()
	require.Len(t, snap, 2)
	require.Equal(t, Sample{TS: 102, Val: 2.0}, snap[hostKey])
	require.Equal(t, Sample{TS: 100, Val: 30.0}, snap[diskKey])

	for _, k := range l.Keys() {
		want, ok := l.Latest(k)
		require.True(t, ok)
		require.Equal(t, want, snap[k])
	}
}

func TestLiveSnapshotLatestEmptyWhenNoSeries(t *testing.T) {
	l := NewLive(8)
	require.Empty(t, l.SnapshotLatest())
}

// TestLiveLatestByMetricPrefix pins the per-container storage endpoint's
// per-device IO lookup: only rings matching kind+entity AND whose metric
// starts with prefix come back, keyed by their full (unstripped) metric
// name -- stripping the "live:io." prefix and the device name out of
// that key is the handler's job (see api_storage.go), not this
// accessor's.
func TestLiveLatestByMetricPrefix(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 100, 10.0)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 102, 20.0) // latest wins
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.write_bps"}, 100, 5.0)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}, 100, 99.0)             // wrong prefix
	l.Record(SeriesKey{Kind: "container", Entity: "other", Metric: "live:io.sda.read_bps"}, 100, 1) // wrong entity
	l.Record(SeriesKey{Kind: "disk", Entity: "web", Metric: "live:io.sda.read_bps"}, 100, 1)        // wrong kind

	got := l.LatestByMetricPrefix("container", "web", "live:io.")

	require.Equal(t, map[string]Sample{
		"live:io.sda.read_bps":  {TS: 102, Val: 20.0},
		"live:io.sda.write_bps": {TS: 100, Val: 5.0},
	}, got)
}

// TestLiveLatestByMetricPrefixEmptyWhenNoMatch pins the no-match shape:
// an empty-but-non-nil map, matching SnapshotLatest's own convention,
// rather than nil -- a caller that immediately ranges over the result
// (as the storage handler does) needs no nil check either way, but
// staying consistent with the rest of this type avoids a surprise for
// any test that asserts NotNil.
func TestLiveLatestByMetricPrefixEmptyWhenNoMatch(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}, 100, 1.0)

	got := l.LatestByMetricPrefix("container", "web", "live:io.")

	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestLiveEvict(t *testing.T) {
	l := NewLive(8)
	k1 := SeriesKey{Kind: "container", Entity: "app1", Metric: "cpu"}
	k2 := SeriesKey{Kind: "container", Entity: "app1", Metric: "mem"}
	k3 := SeriesKey{Kind: "container", Entity: "app2", Metric: "cpu"}

	// Record two metrics for entity app1
	l.Record(k1, 100, 42.0)
	l.Record(k2, 100, 100.0)
	// Record one metric for entity app2
	l.Record(k3, 100, 50.0)

	require.Len(t, l.Keys(), 3)

	// Evict all rings for container:app1
	l.Evict("container", "app1")

	// k1 and k2 should be gone, k3 should remain
	keys := l.Keys()
	require.Len(t, keys, 1)
	require.Equal(t, k3, keys[0])
}

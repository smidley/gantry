package store

import (
	"sync"
	"testing"
	"time"

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

// TestLiveMatchSinceGroupsByEntityForOneKindMetricPair pins the alert
// engine's data-source shape (Task 4): every entity currently carrying a
// kind+metric series comes back keyed by entity, filtered to samples at
// or after since, in one read-lock pass -- the N+1-avoiding counterpart
// to calling Keys() then Since() per matching key.
func TestLiveMatchSinceGroupsByEntityForOneKindMetricPair(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}, 100, 40.0)
	l.Record(SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}, 200, 42.0)
	l.Record(SeriesKey{Kind: "disk", Entity: "disk2", Metric: "temp.c"}, 150, 55.0)
	l.Record(SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.used_pct"}, 100, 90.0) // wrong metric
	l.Record(SeriesKey{Kind: "host", Entity: "", Metric: "temp.c"}, 100, 30.0)           // wrong kind

	got, oldest := l.MatchSince("disk", "temp.c", 0)

	require.Equal(t, map[string][]Sample{
		"disk1": {{TS: 100, Val: 40.0}, {TS: 200, Val: 42.0}},
		"disk2": {{TS: 150, Val: 55.0}},
	}, got)
	require.Equal(t, map[string]int64{"disk1": 100, "disk2": 150}, oldest)
}

// TestLiveMatchSinceRespectsSince pins the window filter itself, and that
// an entity with no samples in the window is simply absent as a key --
// the alert engine's own contract for "series absent" (see
// internal/alert/engine.go's no-data handling).
func TestLiveMatchSinceRespectsSince(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "host", Metric: "cpu.total"}, 100, 10.0)
	l.Record(SeriesKey{Kind: "host", Metric: "cpu.total"}, 200, 20.0)

	got, _ := l.MatchSince("host", "cpu.total", 150)
	require.Equal(t, map[string][]Sample{"": {{TS: 200, Val: 20.0}}}, got)

	got, _ = l.MatchSince("host", "cpu.total", 500)
	require.Empty(t, got)
}

// TestLiveMatchSinceEmptyForUnknownPair pins the always-non-nil, empty-
// map convention every other Live accessor already follows -- for both
// return values.
func TestLiveMatchSinceEmptyForUnknownPair(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "host", Metric: "cpu.total"}, 100, 10.0)

	got, oldest := l.MatchSince("gpu", "busy_pct", 0)
	require.NotNil(t, got)
	require.Empty(t, got)
	require.NotNil(t, oldest)
	require.Empty(t, oldest)
}

// TestLiveMatchSinceOldestTSIsTheRingsTrueFloorNotClippedToSince pins the
// F1 fix at its source: oldestTS reports the ring's actual retention
// floor for an entity even when since clips the samples slice itself to
// a narrower window. The alert engine needs the TRUE floor to tell "the
// ring provably covers my whole window" from "this series just hasn't
// been running that long" -- the samples slice alone can't say that once
// it's already been filtered down to since..now.
func TestLiveMatchSinceOldestTSIsTheRingsTrueFloorNotClippedToSince(t *testing.T) {
	l := NewLive(8)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	l.Record(k, 100, 1.0) // the ring's true oldest retained sample
	l.Record(k, 200, 2.0)
	l.Record(k, 300, 3.0)

	samples, oldest := l.MatchSince("host", "cpu.total", 250) // clips samples to TS>=250

	require.Equal(t, map[string][]Sample{"": {{TS: 300, Val: 3.0}}}, samples, "samples themselves stay clipped to since")
	require.Equal(t, map[string]int64{"": 100}, oldest, "but oldestTS reports the ring's real floor, not the clipped fetch")
}

// TestLiveMatchSinceOldestTSPresentEvenWhenSamplesEmptyForSince pins that
// oldestTS is reported off the ring's own existence for this kind+metric,
// independent of whatever the samples map excludes for having nothing in
// [since, now] -- the two return values answer different questions.
func TestLiveMatchSinceOldestTSPresentEvenWhenSamplesEmptyForSince(t *testing.T) {
	l := NewLive(8)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	l.Record(k, 100, 1.0) // only sample; stale relative to the since below

	samples, oldest := l.MatchSince("host", "cpu.total", 500)
	require.Empty(t, samples)
	require.Equal(t, map[string]int64{"": 100}, oldest)
}

// TestLiveMatchPrefixSinceGroupsByEntityThenMetric pins the insight
// engine's per-device attribution shape (Task 3): every series of this
// kind whose metric starts with prefix comes back keyed first by entity,
// then by its own full (unstripped) metric name -- "every container's IO
// on every device" in one read-lock pass, the same N+1-avoiding reasoning
// MatchSince documents for its own single (kind, metric) pair, one level
// deeper because a prefix can match several metrics per entity (one per
// device) that the caller doesn't know by name in advance.
func TestLiveMatchPrefixSinceGroupsByEntityThenMetric(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "qbittorrent", Metric: "live:io.sda.read_bps"}, 100, 10.0)
	l.Record(SeriesKey{Kind: "container", Entity: "qbittorrent", Metric: "live:io.sda.write_bps"}, 100, 5.0)
	l.Record(SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sda.read_bps"}, 100, 2.0)
	l.Record(SeriesKey{Kind: "container", Entity: "qbittorrent", Metric: "cpu.pct"}, 100, 99.0)        // wrong prefix
	l.Record(SeriesKey{Kind: "disk", Entity: "qbittorrent", Metric: "live:io.sda.read_bps"}, 100, 1.0) // wrong kind

	got, _ := l.MatchPrefixSince("container", "live:io.", 0)

	require.Equal(t, map[string]map[string][]Sample{
		"qbittorrent": {
			"live:io.sda.read_bps":  {{TS: 100, Val: 10.0}},
			"live:io.sda.write_bps": {{TS: 100, Val: 5.0}},
		},
		"jellyfin": {
			"live:io.sda.read_bps": {{TS: 100, Val: 2.0}},
		},
	}, got)
}

// TestLiveMatchPrefixSinceRespectsSince pins the window filter itself,
// mirroring MatchSince's own since-filter test.
func TestLiveMatchPrefixSinceRespectsSince(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 100, 10.0)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 200, 20.0)

	got, _ := l.MatchPrefixSince("container", "live:io.", 150)
	require.Equal(t, map[string]map[string][]Sample{
		"web": {"live:io.sda.read_bps": {{TS: 200, Val: 20.0}}},
	}, got)

	got, _ = l.MatchPrefixSince("container", "live:io.", 500)
	require.Empty(t, got)
}

// TestLiveMatchPrefixSinceEmptyForUnknownPrefix pins the always-non-nil,
// empty-map convention every other Live accessor follows, for both
// return values.
func TestLiveMatchPrefixSinceEmptyForUnknownPrefix(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}, 100, 1.0)

	samples, oldest := l.MatchPrefixSince("container", "live:io.", 0)
	require.NotNil(t, samples)
	require.Empty(t, samples)
	require.NotNil(t, oldest)
	require.Empty(t, oldest)
}

// TestLiveMatchPrefixSinceOldestTSIsPerEntityAndMetric pins the same
// oldestTS lesson MatchSince encodes (F1: the ring's true retention floor,
// independent of since clipping the samples slice), extended one level:
// since MatchPrefixSince can match several distinct metrics per entity
// (one per device), the floor is reported per (entity, metric) pair, not
// collapsed across a container's devices -- a container whose sda ring
// has run for an hour and whose freshly-attached sdb ring has run for ten
// seconds has two different true floors, and collapsing them would let
// the newer ring silently borrow the older one's window-coverage claim.
func TestLiveMatchPrefixSinceOldestTSIsPerEntityAndMetric(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 100, 1.0) // true floor for sda
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 300, 3.0)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sdb.read_bps"}, 250, 9.0) // true floor for sdb

	samples, oldest := l.MatchPrefixSince("container", "live:io.", 200) // clips sda to TS>=200

	require.Equal(t, map[string]map[string][]Sample{
		"web": {
			"live:io.sda.read_bps": {{TS: 300, Val: 3.0}},
			"live:io.sdb.read_bps": {{TS: 250, Val: 9.0}},
		},
	}, samples, "samples themselves stay clipped to since")
	require.Equal(t, map[string]map[string]int64{
		"web": {
			"live:io.sda.read_bps": 100, // the ring's real floor, not the clipped fetch
			"live:io.sdb.read_bps": 250,
		},
	}, oldest)
}

// TestLiveMatchPrefixSinceOldestTSPresentEvenWhenSamplesEmptyForSince pins
// that oldestTS is reported off each matching ring's own existence,
// independent of whatever the samples map excludes for having nothing in
// [since, now] -- the two return values answer different questions, same
// as MatchSince.
func TestLiveMatchPrefixSinceOldestTSPresentEvenWhenSamplesEmptyForSince(t *testing.T) {
	l := NewLive(8)
	l.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}, 100, 1.0)

	samples, oldest := l.MatchPrefixSince("container", "live:io.", 500)
	require.Empty(t, samples)
	require.Equal(t, map[string]map[string]int64{"web": {"live:io.sda.read_bps": 100}}, oldest)
}

// TestLiveMatchPrefixSinceNoDeadlockUnderConcurrentWriters is the
// concurrent-writer probe MatchSince's single-lock-pass design exists to
// survive: sync.RWMutex is not reentrant, so a read path that acquired
// RLock more than once per call (an N+1-style walk instead of one pass)
// could deadlock against a concurrent Lock() writer queued in between the
// two RLock calls. Runs under -race so a torn read would also be caught.
func TestLiveMatchPrefixSinceNoDeadlockUnderConcurrentWriters(t *testing.T) {
	l := NewLive(64)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := SeriesKey{Kind: "container", Entity: string(rune('a' + g)), Metric: "live:io.sda.read_bps"}
			for i := int64(0); ; i++ {
				select {
				case <-stop:
					return
				default:
					l.Record(k, i, float64(i))
				}
			}
		}(g)
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			l.MatchPrefixSince("container", "live:io.", 0)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MatchPrefixSince deadlocked under concurrent writers")
	}
	close(stop)
	wg.Wait()
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

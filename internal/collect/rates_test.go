package collect

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRateTrackerFirstObservationIsFalse(t *testing.T) {
	rt := NewRateTracker()
	rate, ok := rt.Rate("k", time.Now(), 100)
	require.False(t, ok)
	require.Equal(t, 0.0, rate)
}

func TestRateTrackerComputesPerSecondRate(t *testing.T) {
	rt := NewRateTracker()
	t0 := time.Now()
	_, ok := rt.Rate("k", t0, 100)
	require.False(t, ok)

	rate, ok := rt.Rate("k", t0.Add(2*time.Second), 160)
	require.True(t, ok)
	require.InDelta(t, 30.0, rate, 1e-9)
}

func TestRateTrackerCounterResetReturnsFalseThenResumes(t *testing.T) {
	rt := NewRateTracker()
	t0 := time.Now()
	rt.Rate("k", t0, 100)
	rate, ok := rt.Rate("k", t0.Add(2*time.Second), 160)
	require.True(t, ok)
	require.InDelta(t, 30.0, rate, 1e-9)

	// counter reset (process/counter restarted): must report false, not a
	// large negative rate.
	rate, ok = rt.Rate("k", t0.Add(3*time.Second), 10)
	require.False(t, ok)
	require.Equal(t, 0.0, rate)

	// resumes tracking from the new baseline on the next call.
	rate, ok = rt.Rate("k", t0.Add(4*time.Second), 30)
	require.True(t, ok)
	require.InDelta(t, 20.0, rate, 1e-9)
}

func TestRateTrackerKeysAreIndependent(t *testing.T) {
	rt := NewRateTracker()
	t0 := time.Now()
	rt.Rate("a", t0, 0)
	rt.Rate("b", t0, 1000)

	rateA, okA := rt.Rate("a", t0.Add(time.Second), 5)
	rateB, okB := rt.Rate("b", t0.Add(time.Second), 1010)
	require.True(t, okA)
	require.True(t, okB)
	require.InDelta(t, 5.0, rateA, 1e-9)
	require.InDelta(t, 10.0, rateB, 1e-9)
}

func TestRateTrackerZeroElapsedIsFalse(t *testing.T) {
	rt := NewRateTracker()
	now := time.Now()
	rt.Rate("k", now, 10)
	rate, ok := rt.Rate("k", now, 20) // same instant, no time passed
	require.False(t, ok)
	require.Equal(t, 0.0, rate)
}

func TestRateTrackerEvictPrefixRemovesOnlyMatchingKeys(t *testing.T) {
	rt := NewRateTracker()
	t0 := time.Now()
	rt.Rate("client1.render", t0, 100)
	rt.Rate("client1.video", t0, 100)
	rt.Rate("client2.render", t0, 100)
	require.Equal(t, 3, rt.Len())

	rt.EvictPrefix("client1.")
	require.Equal(t, 1, rt.Len())

	// client1's keys must be gone: the tracker has no prior sample for
	// them, so the very next Rate call is a first observation again.
	_, ok := rt.Rate("client1.render", t0.Add(time.Second), 200)
	require.False(t, ok, "evicted key must behave like a brand-new key")

	// client2's key must survive untouched (still has its real baseline).
	rate, ok := rt.Rate("client2.render", t0.Add(time.Second), 105)
	require.True(t, ok)
	require.InDelta(t, 5.0, rate, 1e-9)
}

func TestRateTrackerEvictPrefixNoMatchIsNoop(t *testing.T) {
	rt := NewRateTracker()
	rt.Rate("a.x", time.Now(), 1)
	rt.EvictPrefix("nonexistent.")
	require.Equal(t, 1, rt.Len())
}

// TestRateTrackerChurnReturnsToBaseline simulates N ephemeral keys (e.g.
// GPU client ids, container names) being created and then fully evicted,
// as would happen across repeated client/container churn over the
// process lifetime. Len() must return to the pre-churn baseline every
// time, not grow unbounded.
func TestRateTrackerChurnReturnsToBaseline(t *testing.T) {
	rt := NewRateTracker()
	rt.Rate("steady.cpu", time.Now(), 1) // one key that never gets evicted
	baseline := rt.Len()

	const n = 200
	for i := 0; i < n; i++ {
		prefix := fmt.Sprintf("ephemeral%d.", i)
		rt.Rate(prefix+"engine_a", time.Now(), 1)
		rt.Rate(prefix+"engine_b", time.Now(), 1)
		rt.EvictPrefix(prefix)
	}

	require.Equal(t, baseline, rt.Len(), "churned keys must not accumulate")
}

// TestRateTrackerConcurrentRateAndEvictPrefixIsRace is a regression guard
// for a real production shape: docker's event-stream goroutine calls
// EvictPrefix (container destroy/remove) while the collector's own tick
// goroutine concurrently calls Rate on the same *RateTracker
// (internal/collect/docker/docker.go's evictContainer, called from
// applyEventRecovered, races recordContainerStats's Rate calls). Without
// internal synchronization this is an unsynchronized map read/write —
// under -race it's reported directly; without -race it can crash the
// whole process with a fatal error that bypasses recover(). Meaningful
// only under `go test -race`.
func TestRateTrackerConcurrentRateAndEvictPrefixIsRace(t *testing.T) {
	rt := NewRateTracker()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		now := time.Now()
		for i := 0; i < 1000; i++ {
			rt.Rate("c1.cpu", now.Add(time.Duration(i)*time.Millisecond), float64(i))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			rt.EvictPrefix("c1.")
			rt.EvictPrefix("c2.")
		}
	}()

	wg.Wait()

	require.GreaterOrEqual(t, rt.Len(), 0, "Len() must return a sane count, not corrupted map state")
	require.LessOrEqual(t, rt.Len(), 1, "only c1.cpu can possibly remain, and only if it landed after the last evict")
}

func TestRateUsesFractionalSecondElapsed(t *testing.T) {
	rt := NewRateTracker()
	t0 := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	_, ok := rt.Rate("k", t0, 100)
	require.False(t, ok)

	// 1.5s later, counter +15 → exactly 10.0/s. Integer-second arithmetic
	// would yield 15.0 (truncated elapsed 1s) or 7.5 (rounded 2s) — both wrong.
	rate, ok := rt.Rate("k", t0.Add(1500*time.Millisecond), 115)
	require.True(t, ok)
	require.InDelta(t, 10.0, rate, 0.0001)
}

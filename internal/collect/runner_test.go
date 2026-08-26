package collect

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scripted struct {
	name      string
	avail     atomic.Bool
	ticks     atomic.Int64
	panicOnce atomic.Bool
	failing   atomic.Bool
}

func (s *scripted) Name() string            { return s.name }
func (s *scripted) Interval() time.Duration { return 10 * time.Millisecond }
func (s *scripted) Probe(context.Context) Status {
	if s.avail.Load() {
		return Status{Available: true}
	}
	return Status{Available: false, Detail: "mount missing"}
}
func (s *scripted) Tick(context.Context, time.Time) error {
	s.ticks.Add(1)
	if s.panicOnce.CompareAndSwap(true, false) {
		panic("boom")
	}
	if s.failing.Load() {
		return errors.New("tick failed")
	}
	return nil
}

func TestRunnerTicksAndSurvivesPanic(t *testing.T) {
	c := &scripted{name: "fake"}
	c.avail.Store(true)
	c.panicOnce.Store(true)

	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)

	require.Eventually(t, func() bool { return c.ticks.Load() >= 3 }, 2*time.Second, 5*time.Millisecond,
		"runner must keep ticking after a panic")
	cancel()
	wg.Wait()
	require.Equal(t, "ok", r.Sources()["fake"])
}

func TestRunnerReportsUnavailableWithDetail(t *testing.T) {
	c := &scripted{name: "unraid"} // avail stays false
	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)
	require.Eventually(t, func() bool { return r.Sources()["unraid"] == "mount missing" }, time.Second, 5*time.Millisecond)
	require.Equal(t, int64(0), c.ticks.Load(), "unavailable collectors must not tick")
	cancel()
	wg.Wait()
}

func TestRunnerBacksOffOnConsecutiveErrors(t *testing.T) {
	c := &scripted{name: "flaky"}
	c.avail.Store(true)
	c.failing.Store(true)
	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)

	time.Sleep(300 * time.Millisecond)
	n := c.ticks.Load()
	require.Less(t, n, int64(12), "backoff must slow a persistently failing collector well below the 10ms cadence (30 ticks)")
	require.GreaterOrEqual(t, n, int64(1))
	cancel()
	wg.Wait()
}

// blockingCollector's Tick never returns on its own — it waits for its
// ctx to end — standing in for a wedged docker daemon or any other
// collector whose one dependency call hangs forever.
type blockingCollector struct {
	name     string
	interval time.Duration
}

func (b *blockingCollector) Name() string                 { return b.name }
func (b *blockingCollector) Interval() time.Duration      { return b.interval }
func (b *blockingCollector) Probe(context.Context) Status { return Status{Available: true} }
func (b *blockingCollector) Tick(ctx context.Context, _ time.Time) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestSafeTickTimesOutOnBlockedCollector pins I2: safeTick must cut off a
// Tick call that never returns on its own, deriving the deadline from the
// collector's own (here, tiny) Interval rather than blocking on the
// run-lifetime ctx forever.
func TestSafeTickTimesOutOnBlockedCollector(t *testing.T) {
	c := &blockingCollector{name: "wedged", interval: 20 * time.Millisecond}

	start := time.Now()
	err := safeTick(context.Background(), c)
	elapsed := time.Since(start)

	require.Error(t, err, "a Tick that never returns must be cut off by the per-tick timeout")
	require.GreaterOrEqual(t, elapsed, 80*time.Millisecond, "timeout must be derived from Interval (5x), not fire immediately")
	require.Less(t, elapsed, 2*time.Second, "timeout must not fall back to some large or absent default")
}

// TestRunnerDowngradesAfterFiveConsecutiveErrorsAndRecoversAfterProbe pins
// I2's second half: Sources() must stop reporting "ok" for a collector
// whose Tick keeps failing (a wedged/erroring dependency, indistinguishable
// from the outside from one that's actually gone) once that's happened 5
// times in a row, and must recover to "ok" once a Probe call succeeds and
// ticking resumes cleanly.
func TestRunnerDowngradesAfterFiveConsecutiveErrorsAndRecoversAfterProbe(t *testing.T) {
	origReprobe, origBackoffFloor := reprobeEvery, backoffFloor
	reprobeEvery = 15 * time.Millisecond
	backoffFloor = time.Millisecond // scripted's 10ms Interval dwarfs backoff() until several doublings in
	defer func() { reprobeEvery, backoffFloor = origReprobe, origBackoffFloor }()

	c := &scripted{name: "flaky"}
	c.avail.Store(true)
	c.failing.Store(true)

	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)

	require.Eventually(t, func() bool {
		return r.Sources()["flaky"] == "failing: tick failed"
	}, time.Second, 2*time.Millisecond, "must downgrade to a failing detail after 5 consecutive tick errors")

	c.failing.Store(false) // next Probe (fast reprobe) then Tick will succeed

	require.Eventually(t, func() bool {
		return r.Sources()["flaky"] == "ok"
	}, time.Second, 2*time.Millisecond, "must recover to ok once Probe succeeds and ticks resume")

	cancel()
	wg.Wait()
}

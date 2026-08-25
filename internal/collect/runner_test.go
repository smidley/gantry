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

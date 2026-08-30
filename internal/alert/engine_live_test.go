package alert

import (
	"context"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// F7: every test in engine_test.go drives the engine through matchRouter,
// a stub whose fixtures (flat/series) always land a sample exactly on the
// window's own boundary -- since = now-window, and the oldest sample the
// helper ever emits sits at exactly that timestamp. That is not how a
// real *store.Live ring behaves: a collector records on its own cadence,
// started at whatever moment it happened to boot, and the engine ticks on
// a completely unrelated cadence. The two phases essentially never
// coincide in production. This file reproduces that on purpose -- a real
// *store.Live, fed at a fixed cadence from a deliberately non-zero phase
// offset relative to the engine's own tick clock -- across the three
// shapes the plan names explicitly: sustained fire at the boundary, clear
// hysteresis, and a full flap cycle. This is the family that would have
// caught F1: the old "oldest = samples[0].TS" line read the oldest
// sample of the already-since-clipped fetch, which is only ever equal to
// the window floor by exact coincidence -- guaranteed by every stub
// fixture above, guaranteed by nothing in reality.

// mustDefaultRule looks up one of the twelve real seeded rules by id, so
// these tests exercise the actual production window sizes (host-cpu-high:
// fire >85 for 600s, clear <70 for 300s) rather than a shortened stand-in.
func mustDefaultRule(t *testing.T, id string) store.AlertRule {
	t.Helper()
	for _, r := range store.DefaultAlertRules() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no default rule %q", id)
	return store.AlertRule{}
}

// phaseRecorder appends samples to a real *store.Live on its own fixed
// cadence, independent of whatever cadence the caller's engine ticks on --
// catchUpTo(now) appends every sample due at or before now and advances
// past it, the same shape a real collector's ticker produces relative to
// an unrelated consumer polling it at its own pace.
type phaseRecorder struct {
	next     int64
	interval int64
}

func newPhaseRecorder(start, interval int64) *phaseRecorder {
	return &phaseRecorder{next: start, interval: interval}
}

func (p *phaseRecorder) catchUpTo(live *store.Live, key store.SeriesKey, now int64, val float64) {
	for p.next <= now {
		live.Record(key, p.next, val)
		p.next += p.interval
	}
}

// TestRealLiveSustainedFireAtBoundaryWithUnalignedPhase is the user's
// named repro: real Live, host-cpu-high, sustained 99% recorded every 2s
// on a 3s phase offset against the engine's 10s tick cadence. Under F1
// this stays pending forever (RED); fixed, it fires once ~600s of
// coverage has genuinely accumulated (GREEN).
func TestRealLiveSustainedFireAtBoundaryWithUnalignedPhase(t *testing.T) {
	live := store.NewLive(store.DefaultRingCap)
	rule := mustDefaultRule(t, "host-cpu-high") // fire >85 for 600s
	st := newFakeStore(rule)
	clk := &clockAt{t: 1_700_000_000}
	eng := New(st, live.MatchSince, nil, nil, nil, clk.now)
	key := store.SeriesKey{Kind: "host", Metric: "cpu.total"}

	const tickEvery = int64(10)
	rec := newPhaseRecorder(clk.t+3, 2) // 3s off the tick boundary, 2s cadence
	breachStart := clk.t + 3

	for i := 0; i < 90; i++ { // 90*10s = 900s, comfortably past the 600s window
		clk.t += tickEvery
		rec.catchUpTo(live, key, clk.t, 99)
		require.NoError(t, eng.Tick(context.Background()))

		active, err := st.ActiveAlertInstances(context.Background())
		require.NoError(t, err)

		covered := clk.t-breachStart >= rule.ForSeconds
		if len(active) == 1 && active[0].State == "firing" {
			require.True(t, covered, "fired before the %ds window was actually covered (t-breachStart=%d)", rule.ForSeconds, clk.t-breachStart)
			return
		}
		if len(active) == 1 {
			require.NotEqual(t, "firing", active[0].State)
		}
	}
	t.Fatal("sustained breach for 900s (simulated) never fired: the window-coverage gate never resolved (F1)")
}

// TestRealLiveClearHysteresisWithUnalignedPhase seeds a firing instance
// directly (isolating the clear path from the fire path above) and feeds
// it 650s of real, phase-misaligned history under both thresholds --
// comfortably past the 600s fire window (trivially not breaching there)
// and the 300s clear window. Under F1 the clear-window coverage check
// suffers the exact same clipped-floor bug and this never resolves (RED);
// fixed, one tick is enough (GREEN).
func TestRealLiveClearHysteresisWithUnalignedPhase(t *testing.T) {
	live := store.NewLive(store.DefaultRingCap)
	rule := mustDefaultRule(t, "host-cpu-high") // clear <70 for 300s; fire window 600s
	st := newFakeStore(rule)
	now := int64(1_700_000_000)
	key := store.SeriesKey{Kind: "host", Metric: "cpu.total"}

	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 60, Threshold: rule.Threshold, StartedAt: now - 1000, FiredAt: now - 1000,
	})
	require.NoError(t, err)

	// 650s of history under both thresholds, recorded every 2s starting
	// 3s off the tick boundary -- no sample ever lands exactly on
	// now-600 or now-300 the way every stub-fixture test's flat()/
	// series() helper guarantees.
	for ts := now - 650 + 3; ts <= now; ts += 2 {
		live.Record(key, ts, 60)
	}

	eng := New(st, live.MatchSince, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State, "sustained clear well past the 300s window must resolve (F1)")
	require.Equal(t, "cleared", inst.ResolveReason)
}

// TestRealLiveFlapSequenceWithUnalignedPhase drives a full organic
// fire -> resolve -> fire cycle through real Live, phase-misaligned
// throughout, and pins idx_alert_active's invariant (a refire is always a
// fresh row) end to end -- both transitions share F1's bug, so this only
// completes under the fix.
func TestRealLiveFlapSequenceWithUnalignedPhase(t *testing.T) {
	live := store.NewLive(store.DefaultRingCap)
	rule := mustDefaultRule(t, "host-cpu-high") // fire >85/600s, clear <70/300s
	st := newFakeStore(rule)
	clk := &clockAt{t: 1_700_000_000}
	eng := New(st, live.MatchSince, nil, nil, nil, clk.now)
	key := store.SeriesKey{Kind: "host", Metric: "cpu.total"}
	rec := newPhaseRecorder(clk.t+3, 2) // 3s off the 10s tick boundary throughout

	runUntilActive := func(val float64, maxTicks int) store.AlertInstance {
		t.Helper()
		for i := 0; i < maxTicks; i++ {
			clk.t += 10
			rec.catchUpTo(live, key, clk.t, val)
			require.NoError(t, eng.Tick(context.Background()))
			active, err := st.ActiveAlertInstances(context.Background())
			require.NoError(t, err)
			if len(active) == 1 && active[0].State == "firing" {
				return active[0]
			}
		}
		t.Fatalf("never reached firing within %d ticks", maxTicks)
		return store.AlertInstance{}
	}
	runUntilResolved := func(val float64, maxTicks int) store.AlertInstance {
		t.Helper()
		for i := 0; i < maxTicks; i++ {
			clk.t += 10
			rec.catchUpTo(live, key, clk.t, val)
			require.NoError(t, eng.Tick(context.Background()))
			active, err := st.ActiveAlertInstances(context.Background())
			require.NoError(t, err)
			if len(active) == 0 {
				for _, inst := range st.instances {
					if inst.State == "resolved" {
						return inst
					}
				}
			}
		}
		t.Fatalf("never resolved within %d ticks", maxTicks)
		return store.AlertInstance{}
	}

	first := runUntilActive(99, 80)      // ~600s to fire, well within 800s of budget
	resolved := runUntilResolved(60, 40) // ~300s to clear, well within 400s of budget
	require.Equal(t, first.ID, resolved.ID)

	second := runUntilActive(99, 80)
	require.NotEqual(t, first.ID, second.ID, "a refire must be a fresh row, not the resolved one reopened")
}

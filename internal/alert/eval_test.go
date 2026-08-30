package alert

import (
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// series builds `count` samples, `spacing` seconds apart, ending exactly
// at now (ascending, matching Ring.Since's own convention) -- val applied
// to every sample, so a caller who needs a dip or a tail simply
// overwrites the returned slice's own entries in place.
func series(now, spacing int64, count int, val float64) []store.Sample {
	out := make([]store.Sample, count)
	for i := 0; i < count; i++ {
		out[i] = store.Sample{TS: now - int64(count-1-i)*spacing, Val: val}
	}
	return out
}

const now = int64(1_800_000_000)

func TestEvaluateThresholdAllBreachingFullWindowFires(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 300}
	s := series(now, 60, 11, 90) // now-600 .. now, every 60s
	v, val := EvaluateThreshold(r, s, s[0].TS, now)
	require.Equal(t, VerdictBreaching, v)
	require.Equal(t, 90.0, val)
}

func TestEvaluateThresholdOneDipMidWindowDoesNotFire(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 300}
	s := series(now, 60, 11, 90)
	s[5].Val = 80 // dips below Threshold but still above ClearThreshold
	v, _ := EvaluateThreshold(r, s, s[0].TS, now)
	require.Equal(t, VerdictHolding, v)
}

func TestEvaluateThresholdWindowNotCoveredIsInsufficientRegardlessOfValues(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 300}
	s := series(now, 60, 6, 999) // only 300s of history; every value absurdly breaching
	v, _ := EvaluateThreshold(r, s, s[0].TS, now)
	require.Equal(t, VerdictInsufficient, v)
}

func TestEvaluateThresholdExactlyOnThresholdDoesNotBreach(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 300}
	s := series(now, 60, 11, 85) // sitting exactly on the boundary
	v, _ := EvaluateThreshold(r, s, s[0].TS, now)
	require.NotEqual(t, VerdictBreaching, v)
}

func TestEvaluateThresholdLessThanOpFiresOnZeroAndClearsOnOne(t *testing.T) {
	r := store.AlertRule{Op: "<", Threshold: 1, ClearThreshold: 0, ForSeconds: 300, ClearSeconds: 60}

	stopped := series(now, 60, 6, 0) // array.started == 0 for the whole 300s window
	v, val := EvaluateThreshold(r, stopped, stopped[0].TS, now)
	require.Equal(t, VerdictBreaching, v)
	require.Equal(t, 0.0, val)

	running := series(now, 60, 6, 1) // array.started == 1 throughout, including the 60s clear window
	v, val = EvaluateThreshold(r, running, running[0].TS, now)
	require.Equal(t, VerdictClearing, v)
	require.Equal(t, 1.0, val)
}

func TestEvaluateThresholdClearWindowShorterThanFireWindowBehavesIndependently(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 120}
	s := series(now, 60, 11, 90) // now-600..now, all breaching
	// Only the most recent 120s (the clear window) actually recovered;
	// the rest of the (larger) fire window is still elevated.
	for i := range s {
		if s[i].TS >= now-120 {
			s[i].Val = 60
		}
	}
	v, _ := EvaluateThreshold(r, s, s[0].TS, now)
	require.Equal(t, VerdictClearing, v)
}

func TestEvaluateThresholdEmptyWindowIsInsufficientNeverClearing(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 300}
	// oldest claims coverage (700s ago, older than the 600s window needs)
	// but there is a total gap: not one sample actually falls inside
	// [now-600, now]. Silence must never read as recovery.
	s := []store.Sample{{TS: now - 700, Val: 90}}
	v, _ := EvaluateThreshold(r, s, s[0].TS, now)
	require.Equal(t, VerdictInsufficient, v)
}

func TestEvaluateThresholdUnknownSeriesIsInsufficient(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 600, ClearSeconds: 300}
	v, val := EvaluateThreshold(r, nil, 0, now)
	require.Equal(t, VerdictInsufficient, v)
	require.Equal(t, 0.0, val)
}

// TestEvaluateThresholdSustainedForBoundary pins the exact tick boundary
// at the pure-evaluator level: N-1 ticks of history (90s covered, a 100s
// window) is Insufficient; the Nth tick (100s covered) fires. The
// engine-level lifecycle test (engine_test.go) exercises the same
// boundary through actual Tick() calls and the pending->firing
// transition; this one isolates it to the windowing arithmetic alone.
func TestEvaluateThresholdSustainedForBoundary(t *testing.T) {
	r := store.AlertRule{Op: ">", Threshold: 80, ClearThreshold: 70, ForSeconds: 100, ClearSeconds: 100}

	notYet := series(now, 10, 10, 90) // now-90 .. now: 9 ticks of history, 90s < 100s
	v, _ := EvaluateThreshold(r, notYet, notYet[0].TS, now)
	require.Equal(t, VerdictInsufficient, v, "N-1 ticks must not breach")

	covered := series(now, 10, 11, 90) // now-100 .. now: 10 ticks of history, 100s
	v, _ = EvaluateThreshold(r, covered, covered[0].TS, now)
	require.Equal(t, VerdictBreaching, v, "N ticks must breach")
}

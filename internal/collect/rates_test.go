package collect

import (
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

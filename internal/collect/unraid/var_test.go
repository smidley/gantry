package unraid

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeSink captures every recorded sample, keyed by its full SeriesKey
// (last write wins per key) — shared by every *_test.go file in this
// package, matching the pressure package's own fakeSink convention.
type fakeSink struct {
	records map[store.SeriesKey]float64
}

func newFakeSink() *fakeSink { return &fakeSink{records: make(map[store.SeriesKey]float64)} }

func (f *fakeSink) Record(key store.SeriesKey, ts int64, val float64) {
	f.records[key] = val
}

// fakeEvents captures every appended event, in call order.
type fakeEvents struct {
	events []store.Event
}

func (f *fakeEvents) AppendEvent(e store.Event) (int64, error) {
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}

// copyFixture copies a testdata fixture's content to dst, standing in for
// "the collector observed this ini content on this tick" across a
// sequence of Tick calls against the same dir.
func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o644))
}

func interpretVarFile(t *testing.T, path string) ArrayState {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	kv, err := ParseINI(f)
	require.NoError(t, err)
	return interpretVar(kv)
}

func TestInterpretVarStarted(t *testing.T) {
	state := interpretVarFile(t, "testdata/var_started.ini")
	require.Equal(t, "STARTED", state.State)
	require.False(t, state.ParityRunning)
	require.InDelta(t, 0, state.ParityProgress, 1e-9)
	require.InDelta(t, 0, state.ParitySpeedBps, 1e-9)
	require.Equal(t, "7.3.2", state.Version)
}

func TestInterpretVarStopped(t *testing.T) {
	state := interpretVarFile(t, "testdata/var_stopped.ini")
	require.Equal(t, "STOPPED", state.State)
	require.False(t, state.ParityRunning)
	require.InDelta(t, 0, state.ParityProgress, 1e-9)
	require.InDelta(t, 0, state.ParitySpeedBps, 1e-9)
	require.Equal(t, "7.3.2", state.Version)
}

func TestInterpretVarParityRunning(t *testing.T) {
	state := interpretVarFile(t, "testdata/var_parity_running.ini")
	require.Equal(t, "STARTED", state.State)
	require.True(t, state.ParityRunning)
	require.InDelta(t, 50.0, state.ParityProgress, 1e-9)
	require.InDelta(t, 128000000, state.ParitySpeedBps, 1e-9)
	require.Equal(t, "7.3.2", state.Version)
}

// TestInterpretVarRealCaptureToleratesEveryUnreadKey parses a full,
// anonymized var.ini captured from a live Unraid 7.3.2 box (see
// docs/superpowers/fixtures.md) — roughly 150 keys this package never
// reads alongside the handful it does — and checks the read subset still
// comes out right.
func TestInterpretVarRealCaptureToleratesEveryUnreadKey(t *testing.T) {
	state := interpretVarFile(t, "testdata/var_real.ini")
	require.Equal(t, "STARTED", state.State)
	require.False(t, state.ParityRunning)
	require.InDelta(t, 0, state.ParityProgress, 1e-9)
	require.InDelta(t, 0, state.ParitySpeedBps, 1e-9)
	require.Equal(t, "7.3.2", state.Version)
}

func TestInterpretVarProgressGuardsZeroSize(t *testing.T) {
	state := interpretVar(map[string]map[string]string{"": {
		"mdState":      "STARTED",
		"mdResyncPos":  "500",
		"mdResyncSize": "0",
		"mdResyncDb":   "0",
		"mdResyncDt":   "0",
	}})
	require.True(t, state.ParityRunning, "pos > 0 alone marks parity as running, independent of size")
	require.InDelta(t, 0, state.ParityProgress, 1e-9, "a zero size must guard the division, not produce +Inf/NaN")
}

// TestInterpretVarSpeedDerivedFromResyncDbOverDt pins the real derivation:
// mdResyncSpeed does not exist on a real Unraid box (see
// docs/superpowers/fixtures.md discrepancy 5) — only mdResyncDb (1KB
// blocks transferred) and mdResyncDt (seconds) do, per emhttp's resync-
// rate block convention. 250000 blocks / 2s * 1024 bytes/block =
// 128,000,000 bytes/s.
func TestInterpretVarSpeedDerivedFromResyncDbOverDt(t *testing.T) {
	state := interpretVar(map[string]map[string]string{"": {
		"mdResyncDb": "250000",
		"mdResyncDt": "2",
	}})
	require.InDelta(t, 128000000, state.ParitySpeedBps, 1e-9)
}

func TestInterpretVarSpeedGuardsZeroDt(t *testing.T) {
	state := interpretVar(map[string]map[string]string{"": {
		"mdResyncDb": "500",
		"mdResyncDt": "0",
	}})
	require.InDelta(t, 0, state.ParitySpeedBps, 1e-9, "a zero Dt must guard the division, not produce +Inf")
}

func TestInterpretVarSpeedZeroWhenKeysAbsent(t *testing.T) {
	state := interpretVar(map[string]map[string]string{"": {
		"mdState": "STARTED",
	}})
	require.InDelta(t, 0, state.ParitySpeedBps, 1e-9, "absent mdResyncDb/mdResyncDt must default to 0, not error")
}

// --- transitionEvents: pure edge-detector tests ---

func TestTransitionEventsStateChangeToStartedIsInfo(t *testing.T) {
	prev := ArrayState{State: "STOPPED"}
	next := ArrayState{State: "STARTED"}
	events := transitionEvents(prev, next)
	require.Equal(t, []store.Event{
		{Kind: "array.state", Entity: "array", Severity: "info", Detail: "STARTED"},
	}, events)
}

func TestTransitionEventsStateChangeAwayFromStartedIsWarning(t *testing.T) {
	prev := ArrayState{State: "STARTED"}
	next := ArrayState{State: "STOPPED"}
	events := transitionEvents(prev, next)
	require.Equal(t, []store.Event{
		{Kind: "array.state", Entity: "array", Severity: "warning", Detail: "STOPPED"},
	}, events)
}

func TestTransitionEventsNoStateChangeEmitsNothing(t *testing.T) {
	prev := ArrayState{State: "STARTED"}
	next := ArrayState{State: "STARTED"}
	require.Empty(t, transitionEvents(prev, next))
}

func TestTransitionEventsParityStart(t *testing.T) {
	prev := ArrayState{State: "STARTED", ParityRunning: false}
	next := ArrayState{State: "STARTED", ParityRunning: true, ParityProgress: 0.1}
	events := transitionEvents(prev, next)
	require.Equal(t, []store.Event{
		{Kind: "parity.start", Entity: "array", Severity: "info"},
	}, events)
}

func TestTransitionEventsParityFinishReportsThePreviousTicksProgress(t *testing.T) {
	// next's own ParityProgress is 0 here because emhttp's mdResyncPos has
	// already reset by the time the run shows as stopped — the finish
	// event must report how far the run had gotten (prev), not that 0.
	prev := ArrayState{State: "STARTED", ParityRunning: true, ParityProgress: 99.9}
	next := ArrayState{State: "STARTED", ParityRunning: false, ParityProgress: 0}
	events := transitionEvents(prev, next)
	require.Equal(t, []store.Event{
		{Kind: "parity.finish", Entity: "array", Severity: "info", Detail: "reached 99.9%"},
	}, events)
}

func TestTransitionEventsStateAndParityCanBothFireInOneTick(t *testing.T) {
	prev := ArrayState{State: "STARTED", ParityRunning: true, ParityProgress: 100}
	next := ArrayState{State: "STOPPED", ParityRunning: false}
	events := transitionEvents(prev, next)
	require.Equal(t, []store.Event{
		{Kind: "array.state", Entity: "array", Severity: "warning", Detail: "STOPPED"},
		{Kind: "parity.finish", Entity: "array", Severity: "info", Detail: "reached 100.0%"},
	}, events)
}

// --- Collector-level: Name/Interval/Probe/Version/Tick wiring ---

func TestUnraidCollectorNameAndInterval(t *testing.T) {
	c := New(newFakeSink(), &fakeEvents{}, t.TempDir(), t.TempDir())
	require.Equal(t, "unraid", c.Name())
	require.Equal(t, 15*time.Second, c.Interval())
}

func TestProbeAvailableIffVarIniReadable(t *testing.T) {
	dir := t.TempDir()
	c := New(newFakeSink(), &fakeEvents{}, dir, t.TempDir())

	status := c.Probe(context.Background())
	require.False(t, status.Available)
	require.Contains(t, status.Detail, "/var/local/emhttp")

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.True(t, c.Probe(context.Background()).Available)
}

func TestTickFirstObservationSetsVersionButEmitsNoEvents(t *testing.T) {
	dir := t.TempDir()
	events := &fakeEvents{}
	c := New(newFakeSink(), events, dir, t.TempDir())

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	require.Equal(t, "7.3.2", c.Version())
	require.Empty(t, events.events, "the first tick must only seed prev state, never emit a transition event")
}

func TestTickEmitsParityMetricsOnlyWhileRunning(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	_, hasProgress := sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}]
	_, hasSpeed := sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}]
	require.False(t, hasProgress, "parity.progress_pct must not be emitted while ParityRunning is false")
	require.False(t, hasSpeed, "parity.speed_bps must not be emitted while ParityRunning is false")

	copyFixture(t, "testdata/var_parity_running.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1015, 0)))
	require.InDelta(t, 50.0, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}], 1e-9)
	require.InDelta(t, 128000000, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}], 1e-9)
}

// TestTickAlwaysEmitsArrayStartedMetric pins array.started as an
// unconditional-every-tick metric (unlike parity.progress_pct/speed_bps,
// gated on ParityRunning) -- the UI's Overview array-card state badge
// needs a live-frame value even on a box that has never once transitioned
// state (transitionEvents' array.state event only fires ON A CHANGE, so
// a box that boots already STARTED and stays that way would otherwise
// never surface its state at all).
func TestTickAlwaysEmitsArrayStartedMetric(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	require.Equal(t, 1.0, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "array.started"}])

	copyFixture(t, "testdata/var_stopped.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1010, 0)))
	require.Equal(t, 0.0, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "array.started"}])
}

func TestTickTwiceEmitsArrayStateEventInIsolation(t *testing.T) {
	dir := t.TempDir()
	events := &fakeEvents{}
	c := New(newFakeSink(), events, dir, t.TempDir())

	copyFixture(t, "testdata/var_stopped.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	require.Empty(t, events.events, "first tick establishes prev silently")

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1015, 0)))

	require.Equal(t, []store.Event{
		{Kind: "array.state", Entity: "array", Severity: "info", Detail: "STARTED"},
	}, events.events, "STOPPED->STARTED with parity never running must fire only the state event")
}

func TestTickThriceEmitsParityStartThenFinishWithoutStateNoise(t *testing.T) {
	dir := t.TempDir()
	events := &fakeEvents{}
	sink := newFakeSink()
	c := New(sink, events, dir, t.TempDir())

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	require.Empty(t, events.events, "first tick establishes prev silently")

	copyFixture(t, "testdata/var_parity_running.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1015, 0)))
	require.Equal(t, []store.Event{
		{Kind: "parity.start", Entity: "array", Severity: "info"},
	}, events.events)

	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1030, 0)))
	require.Equal(t, []store.Event{
		{Kind: "parity.start", Entity: "array", Severity: "info"},
		{Kind: "parity.finish", Entity: "array", Severity: "info", Detail: "reached 50.0%"},
	}, events.events, "STARTED throughout means only the parity edge should fire, isolated from any state event")

	// On the same finish tick, the collector must overwrite both parity
	// metrics with an explicit final zero -- otherwise the last real
	// sample recorded above (50%, 128MB/s) is what Ring.Latest keeps
	// forever, since nothing else ever writes those keys again until a
	// NEXT check starts. This is the fix for the live frame reading as
	// "still running" indefinitely after a real finish (Storage/
	// ArrayCard's parityRunning derivation depends on this "zero means
	// not running" wire semantic).
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}], 1e-9,
		"finish must overwrite parity.progress_pct with an explicit 0, not leave the last running sample in place")
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}], 1e-9,
		"finish must overwrite parity.speed_bps with an explicit 0, not leave the last running sample in place")
}

func TestTickMissingVarIniReturnsError(t *testing.T) {
	dir := t.TempDir()
	c := New(newFakeSink(), &fakeEvents{}, dir, t.TempDir())
	require.Error(t, c.Tick(context.Background(), time.Unix(1000, 0)))
}

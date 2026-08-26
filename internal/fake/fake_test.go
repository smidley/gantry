package fake

import (
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

type capture struct {
	recs map[store.SeriesKey][]store.Sample
}

func (c *capture) Record(k store.SeriesKey, ts int64, v float64) {
	if c.recs == nil {
		c.recs = map[store.SeriesKey][]store.Sample{}
	}
	c.recs[k] = append(c.recs[k], store.Sample{TS: ts, Val: v})
}

// eventCapture is a minimal EventSink recording every appended event in
// order, for asserting the fake generator's synthesized lifecycle
// events (parity.start/finish, disk.errors, container.start/oom).
type eventCapture struct {
	events []store.Event
}

func (e *eventCapture) AppendEvent(ev store.Event) (int64, error) {
	e.events = append(e.events, ev)
	return int64(len(e.events)), nil
}

func (e *eventCapture) kinds(kind string) []store.Event {
	var out []store.Event
	for _, ev := range e.events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestTickEmitsHostAndContainerSeries(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	now := time.Unix(1_000_000, 0)
	g.Tick(now)
	g.Tick(now.Add(2 * time.Second))

	require.Len(t, sink.recs[store.SeriesKey{Kind: "host", Metric: "cpu.total"}], 2)

	containers := map[string]bool{}
	for k := range sink.recs {
		if k.Kind == "container" {
			containers[k.Entity] = true
		}
	}
	require.GreaterOrEqual(t, len(containers), 15, "want a fleet worth rendering")

	for k, samples := range sink.recs {
		for _, s := range samples {
			if k.Metric == "cpu.total" || k.Metric == "cpu.pct" || k.Metric == "mem.used_pct" {
				require.GreaterOrEqual(t, s.Val, 0.0, "%v", k)
				require.LessOrEqual(t, s.Val, 100.0, "%v", k)
			}
		}
	}
}

func TestDeterministicWithSameSeed(t *testing.T) {
	a, b := &capture{}, &capture{}
	now := time.Unix(1_000_000, 0)
	New(a, nil, 42).Tick(now)
	New(b, nil, 42).Tick(now)
	require.Equal(t, a.recs, b.recs)
}

// tickEvery simulates n ticks spaced interval apart, starting at start
// (the generator's own boot, since Tick captures whatever `now` it
// first sees) -- used throughout this file to reach an elapsed-time
// threshold (parity start/finish, mover toggles, periodic events)
// without actually sleeping: Tick is a pure function of the `now` it's
// given, so jumping the clock directly is equivalent to (and far
// cheaper than) real time passing.
func tickEvery(g *Generator, start time.Time, interval time.Duration, n int) {
	for i := 0; i < n; i++ {
		g.Tick(start.Add(time.Duration(i) * interval))
	}
}

func TestTickEmitsDiskUnraidAndGPUKinds(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 5)

	disks := map[string]bool{}
	sawArray, sawGPU := false, false
	for k := range sink.recs {
		switch k.Kind {
		case "disk":
			disks[k.Entity] = true
		case "unraid":
			if k.Entity == "array" {
				sawArray = true
			}
		case "gpu":
			if k.Entity == "gpu0" {
				sawGPU = true
			}
		}
	}
	for _, want := range []string{"parity", "disk1", "disk2", "disk3", "disk4", "cache"} {
		require.True(t, disks[want], "missing disk entity %q", want)
	}
	require.True(t, sawArray, "unraid entity \"array\" must be present")
	require.True(t, sawGPU, "gpu entity \"gpu0\" must be present")
}

// TestSpunDownDiskEmitsNoTemp pins disk3's contract: spun_up must read
// 0 and temp.c must never appear for it, across many ticks -- but its
// filesystem usage must still be reported (a real spun-down disk's
// fsSize/fsFree come from a cached mount-time stat, not a live SMART
// query, so they're independent of spin state).
func TestSpunDownDiskEmitsNoTemp(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 50)

	_, hasTemp := sink.recs[store.SeriesKey{Kind: "disk", Entity: "disk3", Metric: "temp.c"}]
	require.False(t, hasTemp, "disk3 is spun down and must never emit temp.c")

	spunUp := sink.recs[store.SeriesKey{Kind: "disk", Entity: "disk3", Metric: "spun_up"}]
	require.NotEmpty(t, spunUp)
	for _, s := range spunUp {
		require.Equal(t, 0.0, s.Val, "disk3's spun_up must always read 0")
	}

	fsUsed := sink.recs[store.SeriesKey{Kind: "disk", Entity: "disk3", Metric: "fs.used_bytes"}]
	require.NotEmpty(t, fsUsed, "a spun-down disk still reports cached filesystem usage")

	for _, other := range []string{"parity", "disk1", "disk2", "disk4", "cache"} {
		temps := sink.recs[store.SeriesKey{Kind: "disk", Entity: other, Metric: "temp.c"}]
		require.NotEmpty(t, temps, "%s is spun up and must report temp.c", other)
		for _, s := range temps {
			require.GreaterOrEqual(t, s.Val, 32.0, "%s temp out of [32,45]", other)
			require.LessOrEqual(t, s.Val, 45.0, "%s temp out of [32,45]", other)
		}
	}
}

// TestParityCheckStartsTwoMinutesAfterBootAndFinishesMonotonically
// simulates the fake array's one-shot parity check end to end: no
// progress before elapsed 2min, a parity.start event on the first tick
// running becomes true, monotonically non-decreasing progress while
// running, and a parity.finish event once it completes -- exactly the
// belt real var.go's transitionEvents fires on ParityRunning's edges.
func TestParityCheckStartsTwoMinutesAfterBootAndFinishesMonotonically(t *testing.T) {
	sink := &capture{}
	events := &eventCapture{}
	g := New(sink, events, 1)
	boot := time.Unix(1_000_000, 0)

	// Every 5 simulated seconds for 8 simulated minutes: fine enough to
	// catch the 2-minute start boundary and the ~6m10s finish (100% /
	// 0.4%/s = 250s after the 2-minute mark) within one step.
	tickEvery(g, boot, 5*time.Second, (8*60)/5)

	key := store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}
	progress := sink.recs[key]
	require.NotEmpty(t, progress, "parity.progress_pct must be recorded once the check starts")
	for _, s := range progress {
		require.GreaterOrEqual(t, s.TS, boot.Add(2*time.Minute).Unix(), "no progress before the 2-minute start")
	}
	for i := 1; i < len(progress); i++ {
		require.GreaterOrEqual(t, progress[i].Val, progress[i-1].Val, "progress must be monotonically non-decreasing while running")
	}

	speed := sink.recs[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}]
	require.NotEmpty(t, speed)
	for _, s := range speed {
		require.InDelta(t, 130_000_000, s.Val, 130_000_000*0.15, "parity speed must be ~130MB/s")
	}

	starts := events.kinds("parity.start")
	finishes := events.kinds("parity.finish")
	require.Len(t, starts, 1, "parity.start must fire exactly once")
	require.Equal(t, "array", starts[0].Entity)
	require.Len(t, finishes, 1, "parity.finish must fire exactly once")
	require.Equal(t, "array", finishes[0].Entity)
	require.Contains(t, finishes[0].Detail, "%", "finish detail should report reached progress, mirroring var.go's transitionEvents")
	require.Greater(t, finishes[0].TS, starts[0].TS)
}

func TestMoverTogglesRoughlyEverySevenMinutes(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	boot := time.Unix(1_000_000, 0)
	tickEvery(g, boot, 30*time.Second, (22*60)/30) // 22 simulated minutes

	key := store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "mover.running"}
	samples := sink.recs[key]
	require.NotEmpty(t, samples)

	var flips int
	for i := 1; i < len(samples); i++ {
		if samples[i].Val != samples[i-1].Val {
			flips++
		}
	}
	// Over 22 minutes with a ~7-minute toggle period, the value must flip
	// at least twice (7min, 14min) without flipping on every tick (which
	// would mean it's just noise, not a slow toggle).
	require.GreaterOrEqual(t, flips, 2, "mover.running must toggle across a 22-minute run")
	require.Less(t, flips, len(samples)/2, "mover.running must not flip every tick")
}

// TestPeriodicRestartAndOOMEvents pins the "periodic container events"
// contract: a restart every ~3min and an OOM every ~10min, on two
// distinct containers, edge-triggered (once per boundary crossed, not
// once per tick).
func TestPeriodicRestartAndOOMEvents(t *testing.T) {
	events := &eventCapture{}
	g := New(&capture{}, events, 1)
	boot := time.Unix(1_000_000, 0)
	tickEvery(g, boot, 10*time.Second, (11*60)/10) // 11 simulated minutes

	restarts := events.kinds("container.start")
	ooms := events.kinds("container.oom")

	require.GreaterOrEqual(t, len(restarts), 3, "want restarts at ~3/6/9 minutes")
	require.Len(t, ooms, 1, "want exactly one OOM by 11 minutes (next at ~20min)")

	restartEntity := restarts[0].Entity
	oomEntity := ooms[0].Entity
	require.NotEqual(t, restartEntity, oomEntity, "restart and OOM must land on distinct containers")
	for _, e := range restarts {
		require.Equal(t, restartEntity, e.Entity, "every restart must land on the same container")
	}
}

// TestRareDiskErrorsEventFiresOnce pins the "one disk with a rare
// disk.errors event" contract: exactly one event, firing in step with
// that disk's errors metric flipping from 0 to a nonzero count.
func TestRareDiskErrorsEventFiresOnce(t *testing.T) {
	sink := &capture{}
	events := &eventCapture{}
	g := New(sink, events, 1)
	boot := time.Unix(1_000_000, 0)
	tickEvery(g, boot, 10*time.Second, (20*60)/10) // 20 simulated minutes: comfortably past any "rare" threshold

	errs := events.kinds("disk.errors")
	require.Len(t, errs, 1, "disk.errors must fire exactly once across the whole run")
	require.Equal(t, "alert", errs[0].Severity)

	errored := errs[0].Entity
	metric := sink.recs[store.SeriesKey{Kind: "disk", Entity: errored, Metric: "errors"}]
	require.NotEmpty(t, metric)
	require.Equal(t, 0.0, metric[0].Val, "errors must start at 0")
	last := metric[len(metric)-1].Val
	require.Greater(t, last, 0.0, "errors must have risen by the end of the run")

	// The event must fire on the same tick the metric first shows the
	// risen count, not a tick later or earlier.
	var firstRiseTS int64
	for _, s := range metric {
		if s.Val > 0 {
			firstRiseTS = s.TS
			break
		}
	}
	require.Equal(t, firstRiseTS, errs[0].TS, "the event must land on the same tick the metric rises")
}

// TestMetasReturnsRunningHealthyDemoImagePerFleetMember pins Metas'
// exact shape (Task 11's ledger-carried fake-mode DTO-v2 filter fix):
// main wires this straight into buildSnapshot/buildContainersList as if
// it were dc.Running()'s real output.
func TestMetasReturnsRunningHealthyDemoImagePerFleetMember(t *testing.T) {
	g := New(&capture{}, nil, 1)
	metas := g.Metas()

	require.Len(t, metas, len(fleet))
	seen := map[string]bool{}
	for _, m := range metas {
		require.Equal(t, "running", m.State)
		require.Equal(t, "healthy", m.Health)
		require.Equal(t, "demo/"+m.Name+":latest", m.Image)
		seen[m.Name] = true
	}
	require.True(t, seen["jellyfin"])
	require.True(t, seen["frigate"])
	require.Len(t, seen, len(fleet), "every fleet member must have a distinct name")
}

// TestMetasIsPureAndStable pins that Metas needs no ticks at all (a
// freshly constructed Generator answers it immediately) and returns
// the same content every call.
func TestMetasIsPureAndStable(t *testing.T) {
	g := New(&capture{}, nil, 1)
	require.Equal(t, g.Metas(), g.Metas())
}

// TestNilEventSinkDoesNotPanic proves every event-emitting path
// tolerates a nil EventSink (main only ever passes a real *store.Store,
// but the type itself must stay nil-safe like every other optional
// dependency in this codebase).
func TestNilEventSinkDoesNotPanic(t *testing.T) {
	g := New(&capture{}, nil, 1)
	require.NotPanics(t, func() {
		tickEvery(g, time.Unix(1_000_000, 0), 10*time.Second, (20*60)/10)
	})
}

// TestGPUBusyPctStaysInContainerAndGPUKinds pins the two named
// containers' attribution plus the gpu0 entity's aggregate, per the
// brief's "jellyfin bursts, frigate steady ~20%".
func TestGPUBusyPctStaysInContainerAndGPUKinds(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 3)
	// 1000 ticks: with a 2%-per-idle-tick burst chance, the odds of
	// jellyfin never once bursting are astronomically small regardless
	// of seed, so this assertion doesn't depend on a lucky draw.
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 1000)

	frigate := sink.recs[store.SeriesKey{Kind: "container", Entity: "frigate", Metric: "gpu.video.busy_pct"}]
	require.NotEmpty(t, frigate)
	var sum float64
	for _, s := range frigate {
		require.GreaterOrEqual(t, s.Val, 0.0)
		require.LessOrEqual(t, s.Val, 100.0)
		sum += s.Val
	}
	avg := sum / float64(len(frigate))
	require.InDelta(t, 20, avg, 5, "frigate should sit steady around ~20%%")

	jellyfin := sink.recs[store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "gpu.video.busy_pct"}]
	require.NotEmpty(t, jellyfin)
	var sawLow, sawHigh bool
	for _, s := range jellyfin {
		require.GreaterOrEqual(t, s.Val, 0.0)
		require.LessOrEqual(t, s.Val, 100.0)
		if s.Val < 10 {
			sawLow = true
		}
		if s.Val > 30 {
			sawHigh = true
		}
	}
	require.True(t, sawLow, "jellyfin must be idle most of the time")
	require.True(t, sawHigh, "jellyfin must burst over 200 ticks")

	gpu0Video := sink.recs[store.SeriesKey{Kind: "gpu", Entity: "gpu0", Metric: "engine.video.busy_pct"}]
	require.NotEmpty(t, gpu0Video)
	for _, s := range gpu0Video {
		require.GreaterOrEqual(t, s.Val, 0.0)
		require.LessOrEqual(t, s.Val, 100.0)
	}
	for _, engine := range []string{"render", "video-enhance", "copy"} {
		samples := sink.recs[store.SeriesKey{Kind: "gpu", Entity: "gpu0", Metric: "engine." + engine + ".busy_pct"}]
		require.NotEmpty(t, samples, "engine %s must be present even if idle", engine)
	}
}

func TestGantrySelfFootprintMetrics(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	g.Tick(time.Unix(1_000_000, 0))

	cpu := sink.recs[store.SeriesKey{Kind: "host", Metric: "gantry.cpu_pct"}]
	rss := sink.recs[store.SeriesKey{Kind: "host", Metric: "gantry.rss_bytes"}]
	require.Len(t, cpu, 1)
	require.Len(t, rss, 1)
	require.GreaterOrEqual(t, cpu[0].Val, 0.0)
	require.Less(t, cpu[0].Val, 5.0, "gantry's own CPU footprint should be small")
	require.Greater(t, rss[0].Val, 0.0)
}

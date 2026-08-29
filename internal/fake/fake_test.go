package fake

import (
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/collect/unraid"
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

// TestContainerCPUIsHostShareWithMatchingCores pins the top-consumers
// host-share fix's fake-mode half: cpu.pct must read as this container's
// share of the WHOLE host, not docker-stats' own per-core percent, so it
// must stay well clear of 100 across many ticks (spikes included) --
// unlike the old per-core-style number, which routinely approached it --
// and cpu.cores/fakeHostCores*100 must always reproduce cpu.pct exactly,
// the same relationship the real collector's cgroupv2.go now guarantees.
func TestContainerCPUIsHostShareWithMatchingCores(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 300) // ~10 simulated minutes: several spike rolls per archetype

	var maxPct float64
	for k, samples := range sink.recs {
		if k.Kind != "container" || k.Metric != "cpu.pct" {
			continue
		}
		cores := sink.recs[store.SeriesKey{Kind: "container", Entity: k.Entity, Metric: "cpu.cores"}]
		require.Len(t, cores, len(samples), "%s: cpu.cores must be emitted alongside every cpu.pct sample", k.Entity)
		for i, s := range samples {
			require.InDelta(t, cores[i].Val/fakeHostCores*100, s.Val, 1e-9, "%s: cpu.pct must equal cpu.cores' own host-share", k.Entity)
			if s.Val > maxPct {
				maxPct = s.Val
			}
		}
	}
	require.Greater(t, maxPct, 0.0, "want at least some CPU activity across the fleet")
	require.Less(t, maxPct, 20.0, "host-share must stay far from 100%% even during a spike -- that was the whole bug")
}

func TestFakeContainerStartedAtIsPastAndVariesByIndex(t *testing.T) {
	boot := time.Unix(5_000_000, 0)
	first := fakeContainerStartedAt(boot, 0)
	second := fakeContainerStartedAt(boot, 1)

	require.True(t, first.Before(boot), "a container's synthetic start must be before boot, not after")
	require.True(t, second.Before(boot))
	require.NotEqual(t, first, second, "different fleet indices must get different synthetic uptimes")
	require.Equal(t, first, fakeContainerStartedAt(boot, 0), "must be a pure function of (boot, i) -- no hidden randomness")
}

// TestTickEmitsContainerMetaMetricsStableAcrossTicks pins meta.started_at/
// meta.restart_count as the fake-mode counterpart of the real docker
// collector's Meta.StartedAt/RestartCount: present per running fake
// container, and -- unlike cpu.pct etc., which vary every tick --
// started_at must read IDENTICAL across ticks (a real container's start
// instant doesn't move once it's running) and restart_count must stay
// the fixed 0 Metas()'s own "identity never changes" convention already
// promises.
func TestTickEmitsContainerMetaMetricsStableAcrossTicks(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 7)
	boot := time.Unix(2_000_000, 0)
	g.Tick(boot)
	g.Tick(boot.Add(2 * time.Second))

	startedAtKey := store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "meta.started_at"}
	restartKey := store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "meta.restart_count"}
	require.Len(t, sink.recs[startedAtKey], 2)
	require.Less(t, sink.recs[startedAtKey][0].Val, float64(boot.Unix()), "a container's synthetic start must be in the past relative to boot")
	require.Equal(t, sink.recs[startedAtKey][0].Val, sink.recs[startedAtKey][1].Val, "started_at must stay stable across ticks, not rejitter")
	require.Equal(t, 0.0, sink.recs[restartKey][0].Val)
	require.Equal(t, 0.0, sink.recs[restartKey][1].Val)
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
	for _, want := range []string{"parity", "disk1", "disk2", "disk3", "disk4", "cache", "rocket_pool", "flash"} {
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

// TestDiskRotationalDistinguishesCacheAsSolidState pins the fake array's
// rotational contract: every spinning array/parity disk reads 1, while
// cache (an NVMe/SSD pool in a realistic Unraid layout) reads 0 -- so
// dev/Playwright exercise the same HDD-vs-SSD distinction a real box's
// disks.ini rotational key drives (see disks.go's tickOneDisk).
// rotational is a static hardware property, recorded every tick
// regardless of spin state -- unlike temp.c, it must be present even for
// the permanently-spun-down disk3.
func TestDiskRotationalDistinguishesCacheAsSolidState(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	g.Tick(time.Unix(1_000_000, 0))

	for _, spinning := range []string{"parity", "disk1", "disk2", "disk3", "disk4"} {
		samples := sink.recs[store.SeriesKey{Kind: "disk", Entity: spinning, Metric: "rotational"}]
		require.NotEmpty(t, samples, "%s must report rotational", spinning)
		require.Equal(t, 1.0, samples[0].Val, "%s should read as spinning", spinning)
	}

	cacheSamples := sink.recs[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "rotational"}]
	require.NotEmpty(t, cacheSamples, "cache must report rotational")
	require.Equal(t, 0.0, cacheSamples[0].Val, "cache should read as solid-state")
}

// TestFlashDiskHasNoTempSensorRegardlessOfSpinState pins flash's noSensor
// contract: temp.c must never appear for it (a USB stick has no SMART
// temperature sensor at all -- a distinct reason from disk3's spunDown,
// which omits temp.c for a different one), while spun_up still always
// reads 1 -- flash is never actually "spun down", there's nothing to
// spin. Regression coverage for Scott's own report: rotational=1 (real
// hardware behavior, asserted below via DiskMetas' own test) must not
// be confused with an ordinary spinning disk's temp behavior either.
func TestFlashDiskHasNoTempSensorRegardlessOfSpinState(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 50)

	_, hasTemp := sink.recs[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "temp.c"}]
	require.False(t, hasTemp, "flash has no temp sensor and must never emit temp.c")

	spunUp := sink.recs[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "spun_up"}]
	require.NotEmpty(t, spunUp)
	for _, s := range spunUp {
		require.Equal(t, 1.0, s.Val, "flash is never spun down -- spun_up must always read 1")
	}
}

// TestDiskMetasCoversAllFourKinds pins DiskMetas' own classification
// output -- the whole point of growing the fake fleet past its original
// 6 disks (Scott's own report: a live box misread its boot flash device
// as HDD and its NVMe pools as generic SSD) is that dev/Playwright now
// exercise every one of Storage's four type badges, not just HDD-vs-SSD.
func TestDiskMetasCoversAllFourKinds(t *testing.T) {
	g := New(&capture{}, nil, 1)
	meta := g.DiskMetas()

	require.Equal(t, unraid.DiskMeta{Device: "sdi", Kind: "usb"}, meta["flash"], "the boot device must classify usb despite rotational=1")
	require.Equal(t, unraid.DiskMeta{Device: "nvme0n1", Kind: "nvme"}, meta["rocket_pool"])
	require.Equal(t, unraid.DiskMeta{Device: "sdh", Kind: "ssd"}, meta["cache"], "a non-nvme solid-state pool must classify plain ssd, not nvme")
	require.Equal(t, unraid.DiskMeta{Device: "sdc", Kind: "hdd"}, meta["disk1"])
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
	// The very last sample is the one-time finish-zero (asserted below),
	// deliberately NOT part of the "running" climb -- excluded here so
	// the monotonic check still covers only the climb itself.
	runningProgress := progress[:len(progress)-1]
	for i := 1; i < len(runningProgress); i++ {
		require.GreaterOrEqual(t, runningProgress[i].Val, runningProgress[i-1].Val, "progress must be monotonically non-decreasing while running")
	}

	speed := sink.recs[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}]
	require.NotEmpty(t, speed)
	runningSpeed := speed[:len(speed)-1]
	require.NotEmpty(t, runningSpeed, "the run must have produced at least one real speed sample before the finish-zero")
	for _, s := range runningSpeed {
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

	// Mirrors real var.go's identical fix: the finish tick must also
	// overwrite both parity metrics with an explicit terminal zero, on
	// the SAME tick as parity.finish -- otherwise the live frame keeps
	// reporting the last real sample (~100%, ~130MB/s) forever, since
	// nothing else ever writes those keys again until a next check
	// starts (see var_test.go's TestTickThriceEmitsParityStartThenFinish
	// WithoutStateNoise for the real-collector half of this same fix).
	lastProgress := progress[len(progress)-1]
	lastSpeed := speed[len(speed)-1]
	require.Equal(t, 0.0, lastProgress.Val, "finish must append an explicit zero progress sample")
	require.Equal(t, 0.0, lastSpeed.Val, "finish must append an explicit zero speed sample")
	require.Equal(t, finishes[0].TS, lastProgress.TS, "the zero progress sample must land on the same tick as parity.finish")
	require.Equal(t, finishes[0].TS, lastSpeed.TS, "the zero speed sample must land on the same tick as parity.finish")
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
// it were a real registry's own output. Every member gets Image; State/
// Health follow its own archetype.stopped/archetype.created flag (the
// stopped/created-containers demo coverage) rather than a blanket
// "running"/"healthy".
func TestMetasReturnsRunningHealthyDemoImagePerFleetMember(t *testing.T) {
	g := New(&capture{}, nil, 1)
	metas := g.Metas()

	require.Len(t, metas, len(fleet))
	byName := map[string]int{}
	for i, m := range metas {
		byName[m.Name] = i
	}
	require.Len(t, byName, len(fleet), "every fleet member must have a distinct name")

	for _, a := range fleet {
		m := metas[byName[a.name]]
		require.Equal(t, "demo/"+a.name+":latest", m.Image)
		switch {
		case a.stopped:
			require.Equal(t, "exited", m.State, "%s is modeled stopped", a.name)
			require.Equal(t, "", m.Health, "%s: a stopped container has no health status", a.name)
		case a.created:
			require.Equal(t, "created", m.State, "%s is modeled created (never started)", a.name)
			require.Equal(t, "", m.Health, "%s: a created container has no health status", a.name)
		default:
			require.Equal(t, "running", m.State, "%s is modeled running", a.name)
			require.Equal(t, "healthy", m.Health, "%s is modeled running", a.name)
		}
	}
	require.Equal(t, "running", metas[byName["jellyfin"]].State)
	require.Equal(t, "running", metas[byName["frigate"]].State)
}

// TestMetasIsPureAndStable pins that Metas needs no ticks at all (a
// freshly constructed Generator answers it immediately) and returns
// the same content every call.
func TestMetasIsPureAndStable(t *testing.T) {
	g := New(&capture{}, nil, 1)
	require.Equal(t, g.Metas(), g.Metas())
}

// TestMetasIncludesPlausibleMounts pins that fake containers carry plausible Mounts.
func TestMetasIncludesPlausibleMounts(t *testing.T) {
	g := New(&capture{}, nil, 1)
	metas := g.Metas()

	for _, m := range metas {
		require.NotEmpty(t, m.Mounts, "%s must have at least one plausible mount", m.Name)
		for _, mount := range m.Mounts {
			isUnraidPath := strings.HasPrefix(mount.Source, "/mnt/") || strings.HasPrefix(mount.Source, "/boot/")
			require.True(t, isUnraidPath, "%s mount source %q must look like a real Unraid path", m.Name, mount.Source)
		}
	}
}

// TestMetasGridmindFamilySharesComposeProject pins the compare view's own
// fake-mode fixture: the four gridmind-* archetypes must all report the
// SAME ComposeProject ("gridmind-cloud"), and an unrelated standalone
// archetype must report none at all -- the Containers view's Groups chip
// row (>=2 containers sharing a project) needs a real multi-member group
// to render against in fake mode, and must not see one where there isn't.
func TestMetasGridmindFamilySharesComposeProject(t *testing.T) {
	g := New(&capture{}, nil, 1)
	metas := g.Metas()

	byName := map[string]docker.Meta{}
	for _, m := range metas {
		byName[m.Name] = m
	}

	gridmindNames := []string{"gridmind-api", "gridmind-worker", "gridmind-scheduler", "gridmind-db"}
	for _, name := range gridmindNames {
		m, ok := byName[name]
		require.True(t, ok, "%s must be part of the fake fleet", name)
		require.Equal(t, "gridmind-cloud", m.ComposeProject, "%s must share the gridmind-cloud compose project", name)
	}

	require.Equal(t, "", byName["jellyfin"].ComposeProject, "a standalone archetype must report no compose project")
}

// TestTickEmitsContainerDeviceIOSeries pins that fake containers also emit live:io.<dev>.* samples per tick.
func TestTickEmitsContainerDeviceIOSeries(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	g.Tick(time.Unix(1_000_000, 0))

	devices := map[string]bool{}
	for k := range sink.recs {
		if k.Kind != "container" || k.Entity != "jellyfin" || !strings.HasPrefix(k.Metric, "live:io.") {
			continue
		}
		dev, _, ok := strings.Cut(strings.TrimPrefix(k.Metric, "live:io."), ".")
		require.True(t, ok)
		devices[dev] = true
	}
	require.GreaterOrEqual(t, len(devices), 2, "want a couple of fake device rows per container")
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

// TestGantrySelfFootprintMetrics pins emitSelf's host-share conversion:
// the pre-conversion figure is clamp(0.6±0.15, 0, 100) (never clamped in
// practice), so ÷fakeHostCores must land in exactly [0.45/8, 0.75/8] --
// centered on 0.075, not the old per-core-style ~0.6.
func TestGantrySelfFootprintMetrics(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 1)
	g.Tick(time.Unix(1_000_000, 0))

	cpu := sink.recs[store.SeriesKey{Kind: "host", Metric: "gantry.cpu_pct"}]
	rss := sink.recs[store.SeriesKey{Kind: "host", Metric: "gantry.rss_bytes"}]
	require.Len(t, cpu, 1)
	require.Len(t, rss, 1)
	require.InDelta(t, 0.6/fakeHostCores, cpu[0].Val, 0.15/fakeHostCores,
		"gantry's own CPU footprint must read as this generator's host-share, not the old per-core-style ~0.6%%")
	require.Greater(t, rss[0].Val, 0.0)
}

// TestFakeMemoryLimitedArchetypeExactPct pins postgres as the fleet's
// memory-limited demo container: mem.limit_bytes is a fixed ceiling
// (unlike mem.bytes, which jitters every tick), and mem.limit_pct must
// equal 100*mem.bytes/mem.limit_bytes exactly on every tick -- the same
// "derived from the very same usage number" contract the real collector's
// recordContainerStats guarantees.
func TestFakeMemoryLimitedArchetypeExactPct(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 3)
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 20)

	memBytes := sink.recs[store.SeriesKey{Kind: "container", Entity: "postgres", Metric: "mem.bytes"}]
	limitBytes := sink.recs[store.SeriesKey{Kind: "container", Entity: "postgres", Metric: "mem.limit_bytes"}]
	limitPct := sink.recs[store.SeriesKey{Kind: "container", Entity: "postgres", Metric: "mem.limit_pct"}]
	require.Len(t, limitBytes, 20, "postgres must get mem.limit_bytes every tick")
	require.Len(t, limitPct, 20)

	for i := range limitPct {
		require.Equal(t, limitBytes[0].Val, limitBytes[i].Val, "the ceiling itself must not jitter tick to tick")
		require.Equal(t, 100*memBytes[i].Val/limitBytes[i].Val, limitPct[i].Val)
		require.GreaterOrEqual(t, limitPct[i].Val, 60.0, "postgres is modeled at roughly 60-80%% of its limit")
		require.LessOrEqual(t, limitPct[i].Val, 85.0)
	}
}

// TestFakeCPUSetPinnedArchetypeExactPct pins minecraft as the fleet's
// cpuset-pinned demo container: cpu.alloc_cores is a fixed ceiling, and
// cpu.alloc_pct must equal 100*cpu.cores/cpu.alloc_cores exactly.
func TestFakeCPUSetPinnedArchetypeExactPct(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 3)
	tickEvery(g, time.Unix(1_000_000, 0), 2*time.Second, 20)

	cpuCores := sink.recs[store.SeriesKey{Kind: "container", Entity: "minecraft", Metric: "cpu.cores"}]
	allocCores := sink.recs[store.SeriesKey{Kind: "container", Entity: "minecraft", Metric: "cpu.alloc_cores"}]
	allocPct := sink.recs[store.SeriesKey{Kind: "container", Entity: "minecraft", Metric: "cpu.alloc_pct"}]
	require.Len(t, allocCores, 20, "minecraft must get cpu.alloc_cores every tick")
	require.Len(t, allocPct, 20)

	for i := range allocPct {
		require.Equal(t, allocCores[0].Val, allocCores[i].Val, "the ceiling itself must not jitter tick to tick")
		require.Equal(t, 100*cpuCores[i].Val/allocCores[i].Val, allocPct[i].Val)
	}
}

// TestFakePidsLimitOnEveryContainerWithLowUsage pins the real-box
// default (pids.max=2048 on every container, docker default) onto every
// fake fleet member, at a low percentage -- unlike the memory/cpu pairs,
// this one is universal, not archetype-specific. The real collector
// always emits a bare `pids` usage metric alongside pids.limit/pids.pct
// (cgroupv2.go's recordContainerStats) -- demo mode must match, so a
// UI's "142 / 2048" treatment has real numerator data in fake mode too.
func TestFakePidsLimitOnEveryContainerWithLowUsage(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 3)
	g.Tick(time.Unix(1_000_000, 0))

	for _, name := range []string{"jellyfin", "postgres", "minecraft", "redis"} {
		pids := sink.recs[store.SeriesKey{Kind: "container", Entity: name, Metric: "pids"}]
		limit := sink.recs[store.SeriesKey{Kind: "container", Entity: name, Metric: "pids.limit"}]
		pct := sink.recs[store.SeriesKey{Kind: "container", Entity: name, Metric: "pids.pct"}]
		require.Len(t, pids, 1, "%s must get a bare pids sample too, matching the real collector's contract", name)
		require.Len(t, limit, 1, "%s must get pids.limit", name)
		require.Equal(t, 2048.0, limit[0].Val)
		require.Len(t, pct, 1)
		require.Equal(t, 100*pids[0].Val/limit[0].Val, pct[0].Val, "pids.pct must be derived from the same value pids reports")
		require.Greater(t, pct[0].Val, 0.0)
		require.Less(t, pct[0].Val, 5.0, "%s: pids.pct must read as a low percentage, not near capacity", name)
	}
}

// TestFakeUnlimitedArchetypesEmitNoMemOrCPUAllocMetrics pins "most
// containers unlimited": jellyfin is neither the memory-limited nor the
// cpuset-pinned demo archetype, so it must get neither pair, even though
// it still gets pids.limit/pids.pct like everything else.
func TestFakeUnlimitedArchetypesEmitNoMemOrCPUAllocMetrics(t *testing.T) {
	sink := &capture{}
	g := New(sink, nil, 3)
	g.Tick(time.Unix(1_000_000, 0))

	for _, metric := range []string{"mem.limit_bytes", "mem.limit_pct", "cpu.alloc_cores", "cpu.alloc_pct"} {
		_, ok := sink.recs[store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: metric}]
		require.False(t, ok, "jellyfin is an unlimited archetype; must not emit %s", metric)
	}
	_, ok := sink.recs[store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "pids.limit"}]
	require.True(t, ok, "pids.limit is universal, unrelated to the mem/cpu archetype choice")
}

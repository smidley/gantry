// Package fake synthesizes a plausible Unraid box (host + container fleet,
// disks, array/parity, GPU, and Gantry's own footprint) through the
// production MetricSink/EventSink paths, for UI development and demos.
// Enabled by GANTRY_FAKE_DATA=1. Never active by default.
package fake

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/collect/unraid"
	"github.com/smidley/gantry/internal/store"
)

// EventSink is the narrow slice of store.Store the fake generator needs
// to append its synthesized lifecycle/health events (parity.start/
// finish, disk.errors, container.start/oom) -- the same narrow, package-
// local interface convention internal/collect/docker and internal/
// collect/unraid each already use for the same dependency.
type EventSink interface {
	AppendEvent(store.Event) (int64, error)
}

type archetype struct {
	name     string
	cpuBase  float64 // steady core-fraction baseline, on the old docker-stats 0-100 scale (see fakeHostCores)
	cpuAmp   float64 // sinusoidal swing, same scale
	cpuSpike float64 // probability per tick of a hard spike
	memBytes float64
	netScale float64 // bytes/s magnitude

	// memLimitBytes/cpuAllocCores are FEATURE's allocation-side demo
	// variety: 0 means unlimited (the real-box default, and most of this
	// fleet) -- only postgres (a memory limit) and minecraft (a cpuset
	// pin) get one, matching the "at least one of each, most containers
	// unlimited" brief.
	memLimitBytes float64
	cpuAllocCores float64
}

var fleet = []archetype{
	{name: "jellyfin", cpuBase: 4, cpuAmp: 3, cpuSpike: 0.02, memBytes: 900e6, netScale: 4e6},
	{name: "plex", cpuBase: 3, cpuAmp: 2, cpuSpike: 0.02, memBytes: 800e6, netScale: 3e6},
	{name: "radarr", cpuBase: 1, cpuAmp: 1, cpuSpike: 0.005, memBytes: 300e6, netScale: 2e5},
	{name: "sonarr", cpuBase: 1, cpuAmp: 1, cpuSpike: 0.005, memBytes: 320e6, netScale: 2e5},
	{name: "prowlarr", cpuBase: 0.5, cpuAmp: 0.5, cpuSpike: 0.002, memBytes: 150e6, netScale: 5e4},
	{name: "qbittorrent", cpuBase: 6, cpuAmp: 4, cpuSpike: 0.01, memBytes: 500e6, netScale: 8e6},
	{name: "sabnzbd", cpuBase: 2, cpuAmp: 6, cpuSpike: 0.01, memBytes: 400e6, netScale: 9e6},
	// postgres: FEATURE's memory-limited demo container, ~60-80% of its limit.
	{name: "postgres", cpuBase: 2, cpuAmp: 0.5, cpuSpike: 0.001, memBytes: 1.2e9, netScale: 1e5, memLimitBytes: 1.7e9},
	{name: "redis", cpuBase: 0.5, cpuAmp: 0.2, cpuSpike: 0.001, memBytes: 200e6, netScale: 8e4},
	{name: "homeassistant", cpuBase: 3, cpuAmp: 1, cpuSpike: 0.005, memBytes: 700e6, netScale: 1e5},
	{name: "grafana", cpuBase: 1, cpuAmp: 0.5, cpuSpike: 0.002, memBytes: 250e6, netScale: 6e4},
	{name: "pihole", cpuBase: 0.5, cpuAmp: 0.3, cpuSpike: 0.001, memBytes: 120e6, netScale: 4e4},
	{name: "nginx", cpuBase: 0.3, cpuAmp: 0.2, cpuSpike: 0.001, memBytes: 80e6, netScale: 5e5},
	{name: "vaultwarden", cpuBase: 0.2, cpuAmp: 0.1, cpuSpike: 0.001, memBytes: 90e6, netScale: 1e4},
	{name: "immich", cpuBase: 5, cpuAmp: 4, cpuSpike: 0.02, memBytes: 1.5e9, netScale: 1e6},
	{name: "paperless", cpuBase: 1, cpuAmp: 2, cpuSpike: 0.01, memBytes: 400e6, netScale: 8e4},
	{name: "gitea", cpuBase: 0.5, cpuAmp: 0.5, cpuSpike: 0.002, memBytes: 300e6, netScale: 6e4},
	// minecraft: FEATURE's cpuset-pinned demo container (pinned to 2 of the fake host's 8 cores).
	{name: "minecraft", cpuBase: 8, cpuAmp: 5, cpuSpike: 0.01, memBytes: 2.5e9, netScale: 3e5, cpuAllocCores: 2.0},
	{name: "frigate", cpuBase: 12, cpuAmp: 4, cpuSpike: 0.02, memBytes: 1.1e9, netScale: 5e6},
	{name: "unifi-controller", cpuBase: 2, cpuAmp: 1, cpuSpike: 0.005, memBytes: 900e6, netScale: 2e5},
}

// diskSpec describes one of the fake array's fixed 8 disks: parity
// (false hasFS, matching real disks.ini's fsSize=0 for a parity slot --
// it has no filesystem view), 4 data disks, a plain SATA/SAS SSD pool,
// an NVMe pool, and the boot flash device.
type diskSpec struct {
	name       string
	device     string // disks.ini's own device key, e.g. "sdg", "nvme0n1" -- unraid.DiskKind's classification signal
	hasFS      bool
	sizeBytes  float64
	baseUsed   float64 // fraction 0..1, before its slow drift
	tempBase   float64 // °C baseline; meaningless when spunDown or noSensor
	spunDown   bool
	noSensor   bool    // true for a device with no temp sensor at all (e.g. USB flash) -- distinct from spunDown; never emits temp.c regardless of spun state
	rotational float64 // disks.ini's own rotational value: 1 spinning, 0 solid-state
}

// disks is the fake array's fixed 8-disk fleet, covering all four of
// Storage's own type badges (Scott's own report: a live box misread its
// boot flash device as HDD and its NVMe cache pools as generic SSD, so
// dev/Playwright need every kind on screen, not just HDD-vs-SSD).
// disk3 is permanently spun down: real spun-down disks never report a
// temp (disks.ini's temp key reads the literal "*"), but DO still report
// cached filesystem usage -- fsSize/fsFree come from a mount-time stat,
// not a live SMART query, so they're independent of spin state. cache is
// a plain solid-state pool (rotational=0, a non-nvme device name) and
// rocket_pool is an NVMe one (device "nvme0n1", matching disks_real.ini's
// own fixture naming) -- both rotational=0, only the device name tells
// them apart, same as a real box. flash is the boot device: like the
// real fixture it reports rotational=1 (a USB stick isn't magically
// exempt from that field) and a plain SCSI-style device name ("sdi") --
// classifying it correctly depends entirely on unraid.DiskKind's
// slot-name override, never on either of those two signals.
var disks = []diskSpec{
	{"parity", "sdb", false, 12e12, 0, 38, false, false, 1},
	{"disk1", "sdc", true, 8e12, 0.62, 34, false, false, 1},
	{"disk2", "sdd", true, 8e12, 0.71, 35, false, false, 1},
	{"disk3", "sde", true, 8e12, 0.55, 36, true, false, 1},
	{"disk4", "sdf", true, 4e12, 0.40, 37, false, false, 1},
	{"cache", "sdh", true, 1e12, 0.35, 41, false, false, 0},
	{"rocket_pool", "nvme0n1", true, 2e12, 0.28, 44, false, false, 0},
	{"flash", "sdi", true, 32e9, 0.05, 0, false, true, 1},
}

const (
	// fakeHostCores is the demo box's assumed logical core count: each
	// archetype's cpuBase/cpuAmp/cpuSpike are authored on the old
	// docker-stats 0-100 per-core scale (100 = one full core), and Tick
	// divides by this to turn that into cpu.cores (÷100) and cpu.pct, a
	// host-share percentage (÷fakeHostCores again) -- see the real
	// collector's cgroupv2.go doc for the same math against a real
	// runtime.NumCPU()/proc-stat count.
	fakeHostCores = 8

	// fakePidsLimit is pids.max on every fake container, matching the
	// real-box default (docker's own pids.max, seen on every container
	// regardless of any other limit) -- unlike memLimitBytes/
	// cpuAllocCores, this one is universal, not archetype-specific.
	fakePidsLimit = 2048

	// errorDiskEntity is the one disk (of the six above) that gets the
	// brief's "one disk with a rare disk.errors event" -- an arbitrary
	// but fixed choice, named once here rather than scattered as a
	// literal.
	errorDiskEntity = "disk2"
	// diskErrorsAt is when errorDiskEntity's error count rises from 0 --
	// "rare" meaning once, well into a session, not at boot.
	diskErrorsAt = 5 * time.Minute

	// parityStartAt/parityRatePctPS/paritySpeedBps model the fake
	// array's one-shot parity check (see parityState): idle for the
	// first parityStartAt, then progress climbs at parityRatePctPS per
	// second until it reaches 100%, at ~paritySpeedBps (decimal MB/s,
	// matching the plan's docker-convention byte-rate formatting).
	parityStartAt   = 2 * time.Minute
	parityRatePctPS = 0.4
	paritySpeedBps  = 130_000_000.0

	// moverTogglePeriod is how often unraid entity "array"'s
	// mover.running flips -- a pure function of elapsed time, no stored
	// state needed.
	moverTogglePeriod = 7 * time.Minute

	// restartEvery/oomEvery/restartContainer/oomContainer drive the
	// "periodic container events" contract: a restart on one container
	// every ~3min, an OOM on a DIFFERENT container every ~10min --
	// sonarr (auto-update restarts are common for the *arr stack) and
	// minecraft (a classic Java-heap OOM candidate) are arbitrary but
	// plausible, fixed choices.
	restartEvery     = 3 * time.Minute
	oomEvery         = 10 * time.Minute
	restartContainer = "sonarr"
	oomContainer     = "minecraft"

	// gpuEntity is the fake GPU device's kind="gpu" entity name.
	gpuEntity = "gpu0"
	// jellyfinBurstChance/MinTicks/MaxTicks give jellyfin's GPU usage a
	// multi-tick burst shape (mirroring Tick's existing per-tick-
	// probability CPU spike idiom) rather than isolated single-tick
	// spikes, so a chart actually shows a visible burst.
	jellyfinBurstChance   = 0.02
	jellyfinBurstMinTicks = 10
	jellyfinBurstMaxTicks = 40
)

type Generator struct {
	sink   store.MetricSink
	events EventSink
	rng    *rand.Rand

	// boot is the `now` of this Generator's first Tick call -- every
	// "N minutes after boot" schedule below (parity check, periodic
	// container events, the rare disk error) is relative to it, not
	// wall-clock time, so a test driving Tick with arbitrary injected
	// `now` values sees the same schedule a real 2s-interval Run would.
	haveBoot bool
	boot     time.Time

	// Edge-trigger state for events that must fire exactly once per
	// threshold crossing, not once per tick while a condition holds.
	lastRestartBoundary int64
	lastOOMBoundary     int64
	parityStarted       bool
	parityFinished      bool
	diskErrorsFired     bool

	// jellyfinBurstTicks counts down a GPU-usage burst in progress; 0
	// means idle and eligible to roll a new burst.
	jellyfinBurstTicks int
}

func New(sink store.MetricSink, events EventSink, seed int64) *Generator {
	return &Generator{sink: sink, events: events, rng: rand.New(rand.NewSource(seed))}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// fakeContainerStartedAt gives fleet member i a plausible, fixed synthetic
// "container started" instant: boot (this generator's own first-Tick
// instant) minus an index-derived offset that grows with i^2 -- an
// arbitrary but deterministic spread from tens of minutes to several days
// across the 20-member fleet, so the UI's uptime column/header shows
// varied, plausible-looking figures instead of an identical one for every
// container. Pure and boot-relative (not wall-clock `time.Now()`) so it
// stays in step with every other "N after boot" schedule in this file and
// is exercised the same deterministic way by a test driving Tick with an
// injected clock.
func fakeContainerStartedAt(boot time.Time, i int) time.Time {
	return boot.Add(-time.Duration(37*(i+1)*(i+1)) * time.Minute)
}

// appendEvent stamps ts explicitly rather than leaving Event.TS zero
// for AppendEvent's own clock to fill in, so a synthesized event's
// timestamp always matches the simulated `now` that triggered it, not
// wall-clock time -- essential for the deterministic, clock-injected
// tests in fake_test.go. A nil events sink (defensive; main always
// wires the real *store.Store) is a silent no-op, the same nil-
// tolerance every other optional dependency in this codebase has.
func (g *Generator) appendEvent(ts int64, e store.Event) {
	if g.events == nil {
		return
	}
	e.TS = ts
	if _, err := g.events.AppendEvent(e); err != nil {
		log.Printf("fake: events: %v", err)
	}
}

// Tick emits one sample per series for the instant `now`.
func (g *Generator) Tick(now time.Time) {
	if !g.haveBoot {
		g.haveBoot = true
		g.boot = now
	}
	elapsed := now.Sub(g.boot)

	ts := now.Unix()
	phase := float64(ts) / 300.0 // slow 5-minute swells

	hostCPUPct := 0.0
	for i, a := range fleet {
		// raw is on the old docker-stats 0-100 per-core scale; /100 turns
		// it into cpu.cores (1.00 = one full core), and dividing THAT by
		// fakeHostCores turns it into cpu.pct, a host-share percentage --
		// see fakeHostCores' own doc. Nothing here ever exceeds one full
		// core, so host-share never approaches 100%, the confusing bug
		// this whole metric redefinition fixes.
		raw := a.cpuBase + a.cpuAmp*math.Sin(phase+float64(i)) + g.rng.Float64()
		if g.rng.Float64() < a.cpuSpike {
			raw += 30 + g.rng.Float64()*40
		}
		cpuCores := clamp(raw/100, 0, 1)
		cpuPct := cpuCores / fakeHostCores * 100
		hostCPUPct += cpuPct

		mem := a.memBytes * (0.9 + 0.2*g.rng.Float64())
		rx := a.netScale * (0.5 + g.rng.Float64())
		tx := a.netScale * 0.2 * (0.5 + g.rng.Float64())

		e := a.name
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "cpu.pct"}, ts, cpuPct)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "cpu.cores"}, ts, cpuCores)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "mem.bytes"}, ts, mem)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "net.rx_bps"}, ts, rx)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "net.tx_bps"}, ts, tx)

		// Allocation-side pair: absence means unlimited, matching the real
		// collector's own contract (cgroupv2.go) -- only postgres/minecraft
		// (see the fleet var) have a non-zero ceiling here.
		if a.memLimitBytes > 0 {
			g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "mem.limit_bytes"}, ts, a.memLimitBytes)
			g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "mem.limit_pct"}, ts, 100*mem/a.memLimitBytes)
		}
		if a.cpuAllocCores > 0 {
			g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "cpu.alloc_cores"}, ts, a.cpuAllocCores)
			g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "cpu.alloc_pct"}, ts, 100*cpuCores/a.cpuAllocCores)
		}
		pidsUsed := 6.0 + g.rng.Float64()*14 // low, ~0.3-1.0% of fakePidsLimit
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "pids.limit"}, ts, fakePidsLimit)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "pids.pct"}, ts, 100*pidsUsed/fakePidsLimit)

		// meta.started_at/meta.restart_count are the fake-mode counterpart
		// of the real docker collector's own same-named metrics (see
		// docker.go's refreshInventory) -- ContainerDTO carries no
		// StartedAt/RestartCount fields of its own (Sample.Val is
		// float64-only), so both flow through as ordinary per-container
		// metrics that land in ContainerDTO.Metrics via buildSnapshot's
		// existing generic per-sample grouping, no DTO/main.go change
		// needed. started_at uses g.boot (safe: only Tick's own
		// single goroutine ever reads or writes it) minus a fixed,
		// index-derived offset -- deterministic and STABLE across ticks
		// (never rejittered), so uptime reads as a plausible, varied,
		// steadily-climbing figure per container rather than either an
		// identical one for all of them or a value that jumps around.
		// restart_count stays a constant 0 for every fake container,
		// mirroring Metas()' own "identity never stops/restarts"
		// convention -- it is deliberately NOT tied to
		// emitContainerEvents' lastRestartBoundary (that field belongs to
		// the periodic-event schedule, not container identity, and Metas
		// is called from other goroutines, so reading it here would race).
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "meta.started_at"}, ts, float64(fakeContainerStartedAt(g.boot, i).Unix()))
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "meta.restart_count"}, ts, 0)
	}

	// hostCPUPct is already a sum of host-share percentages (see the loop
	// above), so it's a direct proxy for host total -- +5 is the same
	// baseline overhead (other host/OS processes) the old per-core-style
	// sum used, just no longer needing that formula's /3 fudge to land in
	// a plausible range now that the terms it's summing are host-share,
	// not inflated per-core, percentages.
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "cpu.total"}, ts, clamp(hostCPUPct+5, 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.used_pct"}, ts, clamp(55+10*math.Sin(phase/3)+2*g.rng.Float64(), 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "net.rx_bps"}, ts, 20e6*(0.5+g.rng.Float64()))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "net.tx_bps"}, ts, 5e6*(0.5+g.rng.Float64()))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio.read_bps"}, ts, 30e6*g.rng.Float64())
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio.write_bps"}, ts, 15e6*g.rng.Float64())

	g.emitDisks(ts, elapsed)
	g.emitArray(ts, elapsed)
	g.emitGPU(ts)
	g.emitSelf(ts)
	g.emitContainerEvents(ts, elapsed)
}

// emitDisks records every fake disk's spun_up/temp.c/fs.*/errors series
// for one tick -- see the disks var and errorDiskEntity/diskErrorsAt
// consts for the per-disk shape and the one rare error's schedule.
func (g *Generator) emitDisks(ts int64, elapsed time.Duration) {
	phase := elapsed.Seconds() / 300.0
	for i, d := range disks {
		spunUp := 1.0
		if d.spunDown {
			spunUp = 0
		}
		g.sink.Record(store.SeriesKey{Kind: "disk", Entity: d.name, Metric: "spun_up"}, ts, spunUp)
		g.sink.Record(store.SeriesKey{Kind: "disk", Entity: d.name, Metric: "rotational"}, ts, d.rotational)

		if !d.spunDown && !d.noSensor {
			temp := clamp(d.tempBase+2.5*math.Sin(phase+float64(i))+(g.rng.Float64()-0.5)*1.5, 32, 45)
			g.sink.Record(store.SeriesKey{Kind: "disk", Entity: d.name, Metric: "temp.c"}, ts, temp)
		}

		if d.hasFS {
			// A slow (~0.05%/hour) upward creep plus small tick-to-tick
			// noise -- "slowly-drifting fs usage", not a random walk.
			usedFrac := clamp(d.baseUsed+elapsed.Hours()*0.0005+g.rng.Float64()*0.002, 0, 0.98)
			used := d.sizeBytes * usedFrac
			g.sink.Record(store.SeriesKey{Kind: "disk", Entity: d.name, Metric: "fs.used_bytes"}, ts, used)
			g.sink.Record(store.SeriesKey{Kind: "disk", Entity: d.name, Metric: "fs.free_bytes"}, ts, d.sizeBytes-used)
		}

		errCount := 0.0
		if d.name == errorDiskEntity && elapsed >= diskErrorsAt {
			errCount = 1
		}
		g.sink.Record(store.SeriesKey{Kind: "disk", Entity: d.name, Metric: "errors"}, ts, errCount)
	}

	if !g.diskErrorsFired && elapsed >= diskErrorsAt {
		g.diskErrorsFired = true
		g.appendEvent(ts, store.Event{Kind: "disk.errors", Entity: errorDiskEntity, Severity: "alert", Detail: "errors 0 → 1"})
	}
}

// parityState derives the fake array's one-shot parity check purely
// from elapsed time since boot: idle for the first parityStartAt, then
// running with progress climbing at parityRatePctPS per second until it
// reaches 100%, then done (not running) forever after -- mirroring real
// var.go's ParityRunning/ParityProgress shape (mdResyncPos>0,
// pos/size*100) without needing an actual mdResync file to read.
func parityState(elapsed time.Duration) (running bool, progressPct float64) {
	if elapsed < parityStartAt {
		return false, 0
	}
	progressPct = (elapsed - parityStartAt).Seconds() * parityRatePctPS
	if progressPct >= 100 {
		return false, 100
	}
	return true, progressPct
}

// emitArray records unraid entity "array"'s mover + parity series and
// fires parity.start/finish on parityState's running-edge transitions
// -- the same edge-triggered shape real var.go's transitionEvents uses,
// just driven by a synthetic schedule instead of var.ini.
func (g *Generator) emitArray(ts int64, elapsed time.Duration) {
	// array.started mirrors real var.go's own unconditional-every-tick
	// metric of the same name (see its doc): the fake array is always
	// modeled as started, matching Metas()' own "identity never
	// stops/restarts" convention -- there's no fake "array stopped"
	// scenario anywhere in this generator.
	g.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "array.started"}, ts, 1.0)

	moverOn := int64(elapsed/moverTogglePeriod)%2 == 1
	moverVal := 0.0
	if moverOn {
		moverVal = 1
	}
	g.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "mover.running"}, ts, moverVal)

	running, progress := parityState(elapsed)
	if running {
		speed := paritySpeedBps * (0.95 + 0.1*g.rng.Float64())
		g.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}, ts, progress)
		g.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}, ts, speed)
	} else if g.parityStarted && !g.parityFinished {
		// Mirrors real var.go's identical fix (see its tickArray doc): on
		// the one tick where the check flips from running to not, record
		// an explicit zero for both metrics so the live frame doesn't
		// keep reporting the last real sample (e.g. 98%, ~130MB/s)
		// forever -- the store's live ring has no sample-expiry. Guarded
		// on parityStarted && !parityFinished (both still reflecting the
		// PRIOR tick's state -- the switch below is what sets
		// parityFinished, after this) so it fires exactly once, on the
		// same tick as the parity.finish event, not on every idle tick
		// before the check even started.
		g.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}, ts, 0)
		g.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}, ts, 0)
	}

	switch {
	case running && !g.parityStarted:
		g.parityStarted = true
		g.appendEvent(ts, store.Event{Kind: "parity.start", Entity: "array", Severity: "info"})
	case !running && g.parityStarted && !g.parityFinished:
		g.parityFinished = true
		g.appendEvent(ts, store.Event{
			Kind: "parity.finish", Entity: "array", Severity: "info",
			Detail: fmt.Sprintf("reached %.1f%%", 100.0),
		})
	}
}

// emitGPU records gpu entity "gpu0"'s per-engine busy_pct plus the two
// GPU-attributed containers' own gpu.video.busy_pct: frigate steady
// around 20% (continuous NVR object detection), jellyfin bursty
// (occasional hardware-transcode spikes) via a multi-tick burst state
// machine. gpu0's own engine.video.busy_pct is modeled as roughly the
// sum of its two attributed consumers -- the device-level number a real
// collector would report is the aggregate of every client's share on
// that engine, clamped the same way the real collector clamps at 100.
func (g *Generator) emitGPU(ts int64) {
	frigateBusy := clamp(20+(g.rng.Float64()-0.5)*4, 0, 100)
	g.sink.Record(store.SeriesKey{Kind: "container", Entity: "frigate", Metric: "gpu.video.busy_pct"}, ts, frigateBusy)

	if g.jellyfinBurstTicks > 0 {
		g.jellyfinBurstTicks--
	} else if g.rng.Float64() < jellyfinBurstChance {
		g.jellyfinBurstTicks = jellyfinBurstMinTicks + g.rng.Intn(jellyfinBurstMaxTicks-jellyfinBurstMinTicks)
	}
	jellyfinBusy := clamp(g.rng.Float64()*4, 0, 100)
	if g.jellyfinBurstTicks > 0 {
		jellyfinBusy = clamp(45+g.rng.Float64()*45, 0, 100)
	}
	g.sink.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "gpu.video.busy_pct"}, ts, jellyfinBusy)

	g.sink.Record(store.SeriesKey{Kind: "gpu", Entity: gpuEntity, Metric: "engine.video.busy_pct"}, ts, clamp(frigateBusy+jellyfinBusy, 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "gpu", Entity: gpuEntity, Metric: "engine.render.busy_pct"}, ts, clamp(g.rng.Float64()*3, 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "gpu", Entity: gpuEntity, Metric: "engine.video-enhance.busy_pct"}, ts, clamp(g.rng.Float64()*3, 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "gpu", Entity: gpuEntity, Metric: "engine.copy.busy_pct"}, ts, clamp(g.rng.Float64()*2, 0, 100))
}

// emitSelf records Gantry's own footprint (kind "host", no entity,
// mirroring internal/collect/selfstat's exact metric names) around the
// real, observed-in-production figures (~0.6% CPU on the old per-core
// scale, ~30MB RSS) -- ÷fakeHostCores turns that into this generator's
// own host-share number, the same conversion the container loop above
// applies, so the Settings page's footprint receipt looks like the real
// thing even in fake mode.
func (g *Generator) emitSelf(ts int64) {
	cpu := clamp(0.6+(g.rng.Float64()-0.5)*0.3, 0, 100) / fakeHostCores
	rss := 30e6 + g.rng.Float64()*4e6
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "gantry.cpu_pct"}, ts, cpu)
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "gantry.rss_bytes"}, ts, rss)
}

// emitContainerEvents fires the "periodic container events" contract:
// a restart on restartContainer every ~restartEvery, an OOM on the
// DIFFERENT oomContainer every ~oomEvery -- edge-triggered on each
// elapsed-time boundary crossed (once per boundary, however many ticks
// land inside it), not once per tick. A single Tick call that somehow
// leaps across more than one boundary at once (never happens at Run's
// real 2s cadence, or in this file's own tests) fires only the latest
// one, not a backfill of every skipped boundary.
func (g *Generator) emitContainerEvents(ts int64, elapsed time.Duration) {
	if b := int64(elapsed / restartEvery); b > g.lastRestartBoundary {
		g.lastRestartBoundary = b
		g.appendEvent(ts, store.Event{
			Kind: "container.start", Entity: restartContainer, Severity: "info",
			Detail: fmt.Sprintf("restart count %d", b),
		})
	}
	if b := int64(elapsed / oomEvery); b > g.lastOOMBoundary {
		g.lastOOMBoundary = b
		g.appendEvent(ts, store.Event{Kind: "container.oom", Entity: oomContainer, Severity: "alert"})
	}
}

// Metas returns one synthetic docker.Meta per fleet archetype, always
// reporting state "running"/health "healthy" (the fake fleet's own
// identity never stops or restarts -- emitContainerEvents' periodic
// events simulate that instead, without actually changing state here).
// main wiring passes this to buildSnapshot/buildContainersList
// (GANTRY_FAKE_DATA=1 only) so the fake fleet is treated exactly like
// dc.Running()'s real entries: without it, Task 4's DTO-v2 container
// filter (only dc.Running() OR a name with both a fresh live sample AND
// a known Meta) would empty every fake-mode frame, since this
// generator writes samples straight to the store, bypassing docker's
// registry entirely.
func (g *Generator) Metas() []docker.Meta {
	out := make([]docker.Meta, len(fleet))
	for i, a := range fleet {
		out[i] = docker.Meta{Name: a.name, State: "running", Health: "healthy", Image: "demo/" + a.name + ":latest"}
	}
	return out
}

// DiskMetas is Metas' disk-metadata analogue: one unraid.DiskMeta per
// disks var member, classified through unraid.DiskKind -- the same pure
// rule a real box's own unraid collector applies in tickOneDisk, so fake
// mode can never drift from the real classification. main wiring passes
// this to buildSnapshot (GANTRY_FAKE_DATA=1 only) so the fake fleet's
// disk types populate the snapshot's disk_meta map exactly like a real
// box's unraid.Collector.DiskMeta() would, since this generator writes
// disk metrics straight to the store, bypassing that collector entirely.
func (g *Generator) DiskMetas() map[string]unraid.DiskMeta {
	out := make(map[string]unraid.DiskMeta, len(disks))
	for _, d := range disks {
		out[d.name] = unraid.DiskMeta{Device: d.device, Kind: unraid.DiskKind(d.name, d.device, d.rotational, true)}
	}
	return out
}

// Run ticks until ctx is done. clock defaults to time.Now when nil.
func (g *Generator) Run(ctx context.Context, interval time.Duration, clock func() time.Time) {
	if clock == nil {
		clock = time.Now
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Tick(clock())
		}
	}
}

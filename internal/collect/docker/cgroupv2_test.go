package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeSink mirrors the host package's fakeSink convention (last-write-
// wins per key), parameterized by entity since every metric here is
// SeriesKey{Kind: "container"}.
type fakeSink struct {
	records map[store.SeriesKey]float64
}

func newFakeSink() *fakeSink { return &fakeSink{records: make(map[store.SeriesKey]float64)} }

func (f *fakeSink) Record(key store.SeriesKey, ts int64, val float64) {
	f.records[key] = val
}

func (f *fakeSink) value(entity, metric string) (float64, bool) {
	v, ok := f.records[store.SeriesKey{Kind: "container", Entity: entity, Metric: metric}]
	return v, ok
}

// copyFixtures links the five cgroup v2 fixture files from testdata/ into
// dir, as if dir were one container's real cgroup directory.
func copyFixtures(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o644))
	}
}

var allCgroupFixtures = []string{
	"cpu.stat", "memory.current", "memory.stat", "pids.current", "io.stat",
	"memory.max", "cpu.max", "pids.max", "cpuset.cpus.effective",
}

func TestReadCgroupStatsParsesRealShapeFixtures(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir, allCgroupFixtures...)

	cg, err := readCgroupStats(dir)
	require.NoError(t, err)

	require.Equal(t, uint64(46000000), cg.CPUUsageUsec)
	require.Equal(t, uint64(750000), cg.ThrottledUsec)
	require.Equal(t, uint64(25), cg.NrThrottled)
	require.Equal(t, uint64(104857600), cg.MemCurrent)
	require.Equal(t, uint64(20971520), cg.MemInactiveFile)
	require.Equal(t, uint64(12), cg.Pids)
	require.Equal(t, map[string]ioCounters{
		"8:0":  {RBytes: 1024000, WBytes: 2048000},
		"8:16": {RBytes: 512000, WBytes: 256000},
	}, cg.IO)

	// memory.max=1073741824 (1GiB), cpu.max="400000 100000" (4.0 cores),
	// pids.max=2048, cpuset.cpus.effective="0-15" (16 of 16 -- unpinned).
	require.Equal(t, alloc{
		MemLimitBytes: 1073741824, HasMemLimit: true,
		CPUQuotaCores: 4.0, HasCPUQuota: true,
		CPUSetCores: 16, HasCPUSet: true,
		PidsLimit: 2048, HasPidsLimit: true,
	}, cg.Alloc)
}

// TestReadCgroupStatsUnlimitedAllocFilesReadAsNoLimit pins the real-box
// default shape (most containers): memory.max/pids.max hold the literal
// "max", cpu.max is "max 100000", and cpuset.cpus.effective lists every
// host core -- all four must come back Has*=false, not a zero-value
// limit that would read as "capped at 0 bytes/cores/pids".
func TestReadCgroupStatsUnlimitedAllocFilesReadAsNoLimit(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir, "cpu.stat", "memory.current", "memory.stat", "pids.current", "io.stat")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.max"), []byte("max\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cpu.max"), []byte("max 100000\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pids.max"), []byte("max\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cpuset.cpus.effective"), []byte("0-15\n"), 0o644))

	cg, err := readCgroupStats(dir)
	require.NoError(t, err)
	require.Equal(t, alloc{CPUSetCores: 16, HasCPUSet: true}, cg.Alloc,
		"cpuset still parses (it lists every core, not \"max\"), but every Has* limit flag must be false")
}

// TestReadCgroupStatsMissingMemoryMaxErrors pins the new allocation files
// into the same "any missing required file fails the whole read"
// contract memory.current/cpu.stat/etc. already have (see readCgroupStats'
// own doc) -- rather than mixing partial cgroup data with an API fallback.
func TestReadCgroupStatsMissingMemoryMaxErrors(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir, "cpu.stat", "memory.current", "memory.stat", "pids.current", "io.stat", "cpu.max", "pids.max", "cpuset.cpus.effective")
	_, err := readCgroupStats(dir)
	require.Error(t, err)
}

func TestReadCgroupStatsMissingDirErrors(t *testing.T) {
	_, err := readCgroupStats(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}

func TestReadCgroupStatsMissingCPUStatErrors(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir, "memory.current", "memory.stat", "pids.current", "io.stat")
	_, err := readCgroupStats(dir)
	require.Error(t, err, "cpu.stat missing must fail the whole read (triggers the API fallback, Task 8)")
}

func TestReadCgroupStatsMissingMemoryStatInactiveFileErrors(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir, "cpu.stat", "memory.current", "pids.current", "io.stat")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.stat"), []byte("anon 123\n"), 0o644))
	_, err := readCgroupStats(dir)
	require.Error(t, err)
}

func TestReadCgroupStatsEmptyIOStatYieldsEmptyMap(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir, "cpu.stat", "memory.current", "memory.stat", "pids.current", "memory.max", "cpu.max", "pids.max", "cpuset.cpus.effective")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "io.stat"), []byte(""), 0o644))

	cg, err := readCgroupStats(dir)
	require.NoError(t, err)
	require.Empty(t, cg.IO)
}

// TestParseCPUSetCount pins cpuset.cpus.effective's list-format parse
// exactly (real-box fixture: "0,1,2,3,4,5,13,14,15" pinned to 9 of 16
// threads) -- mixed ranges and singles, and the malformed/empty cases
// FEATURE requires to read as "unrestricted" (ok=false) rather than a
// hard error, since a garbled cpuset file must not block the rest of a
// tick's allocation reporting.
func TestParseCPUSetCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{name: "mixed ranges and singles", in: "0-5,13-15", want: 9, ok: true},
		{name: "single full-width range", in: "0-15", want: 16, ok: true},
		{name: "one core, no range", in: "3", want: 1, ok: true},
		{name: "all singles, no ranges", in: "0,1,2,3,4,5,13,14,15", want: 9, ok: true},
		{name: "trailing newline from a raw file read", in: "0-5,13-15\n", want: 9, ok: true},
		{name: "empty", in: "", want: 0, ok: false},
		{name: "whitespace only", in: "   ", want: 0, ok: false},
		{name: "malformed non-numeric", in: "abc", want: 0, ok: false},
		{name: "malformed trailing comma", in: "0-5,", want: 0, ok: false},
		{name: "malformed inverted range", in: "5-2", want: 0, ok: false},
		{name: "malformed dangling dash", in: "0-5,-", want: 0, ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseCPUSetCount(c.in)
			require.Equal(t, c.ok, ok)
			require.Equal(t, c.want, got)
		})
	}
}

// recordContainerStats: the tick math, driven by two synthetic cgStats
// readings 2 seconds apart through a real RateTracker so the exact rate
// formulas are pinned, not just "greater than 0".
func newStatsCollector(sink store.MetricSink) *Collector {
	return &Collector{
		sink:      sink,
		rates:     collect.NewRateTracker(),
		MemTotal:  func() uint64 { return 1_000_000_000 },
		HostCores: func() int { return 8 },
		DeviceName: func(majMin string) (string, bool) {
			if majMin == "8:0" {
				return "sda", true
			}
			return "", false // "9:0" deliberately unresolved
		},
	}
}

func TestRecordContainerStatsFirstTickEmitsOnlyGauges(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)

	c.recordContainerStats("web", cgStats{
		CPUUsageUsec: 1_000_000, ThrottledUsec: 100_000,
		MemCurrent: 500_000_000, MemInactiveFile: 50_000_000,
		Pids: 10,
		IO: map[string]ioCounters{
			"8:0": {RBytes: 1_000_000, WBytes: 2_000_000},
			"9:0": {RBytes: 500_000, WBytes: 100_000},
		},
	}, time.Unix(1000, 0))

	_, ok := sink.value("web", "cpu.pct")
	require.False(t, ok, "first tick must not emit a CPU rate")
	_, ok = sink.value("web", "cpu.cores")
	require.False(t, ok, "first tick must not emit a CPU rate")
	_, ok = sink.value("web", "io.read_bps")
	require.False(t, ok, "first tick must not emit an IO rate")

	memBytes, ok := sink.value("web", "mem.bytes")
	require.True(t, ok, "gauges are emitted from the first tick")
	require.Equal(t, 450_000_000.0, memBytes)

	pids, ok := sink.value("web", "pids")
	require.True(t, ok)
	require.Equal(t, 10.0, pids)
}

func TestRecordContainerStatsComputesRatesAcrossTwoTicks(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)

	c.recordContainerStats("web", cgStats{
		CPUUsageUsec: 1_000_000, ThrottledUsec: 100_000,
		MemCurrent: 500_000_000, MemInactiveFile: 50_000_000,
		Pids: 10,
		IO: map[string]ioCounters{
			"8:0": {RBytes: 1_000_000, WBytes: 2_000_000},
			"9:0": {RBytes: 500_000, WBytes: 100_000},
		},
	}, time.Unix(1000, 0))

	c.recordContainerStats("web", cgStats{
		CPUUsageUsec: 3_000_000, ThrottledUsec: 300_000, // +2,000,000 / +200,000 usec over 2s
		MemCurrent: 600_000_000, MemInactiveFile: 60_000_000,
		Pids: 15,
		IO: map[string]ioCounters{
			"8:0": {RBytes: 1_200_000, WBytes: 2_400_000}, // +200,000 / +400,000 over 2s
			"9:0": {RBytes: 600_000, WBytes: 150_000},     // +100,000 / +50,000 over 2s, unresolved device
		},
	}, time.Unix(1002, 0))

	cpuCores, ok := sink.value("web", "cpu.cores")
	require.True(t, ok)
	require.InDelta(t, 1.0, cpuCores, 1e-9, "2,000,000 usec CPU time consumed over 2,000,000 usec wall = one full core")

	cpuPct, ok := sink.value("web", "cpu.pct")
	require.True(t, ok)
	require.InDelta(t, 12.5, cpuPct, 1e-9, "one core's worth of usage is 12.5%% of an 8-core host, not 100%%")

	throttledPct, ok := sink.value("web", "cpu.throttled_pct")
	require.True(t, ok)
	require.InDelta(t, 10.0, throttledPct, 1e-9)

	memBytes, ok := sink.value("web", "mem.bytes")
	require.True(t, ok)
	require.Equal(t, 540_000_000.0, memBytes)

	memPct, ok := sink.value("web", "mem.pct")
	require.True(t, ok)
	require.InDelta(t, 54.0, memPct, 1e-9)

	pids, ok := sink.value("web", "pids")
	require.True(t, ok)
	require.Equal(t, 15.0, pids)

	// total read: (200,000 + 100,000)/2s = 150,000 bps; total write: (400,000 + 50,000)/2s = 225,000 bps
	readBps, ok := sink.value("web", "io.read_bps")
	require.True(t, ok)
	require.InDelta(t, 150_000.0, readBps, 1e-9)
	writeBps, ok := sink.value("web", "io.write_bps")
	require.True(t, ok)
	require.InDelta(t, 225_000.0, writeBps, 1e-9)

	// per-device: only "8:0" resolves to a name ("sda"); "9:0" contributes
	// to the totals above but gets no live: series of its own.
	devRead, ok := sink.value("web", "live:io.sda.read_bps")
	require.True(t, ok)
	require.InDelta(t, 100_000.0, devRead, 1e-9)
	devWrite, ok := sink.value("web", "live:io.sda.write_bps")
	require.True(t, ok)
	require.InDelta(t, 200_000.0, devWrite, 1e-9)

	_, ok = sink.value("web", "live:io.9:0.read_bps")
	require.False(t, ok, "unresolved major:minor must not get a per-device series")
}

// Regression: io totals must be sum-of-per-device-rates, not a rate
// computed on the sum of raw counters. The latter lets a device that
// first appears mid-run (its whole cumulative counter, never seen
// before) fold straight into one tick's aggregate delta, producing a
// spurious massive spike. Four ticks: A only (baseline), A advances
// (totals = A), B appears with a huge cumulative counter while A
// advances normally (totals must reflect only A — B's first sight is
// suppressed, same as any other counter's first observation), then B
// advances from its own new baseline (totals now include B's real rate).
func TestRecordContainerStatsIOTotalsSuppressFirstSightOfNewDevice(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)
	c.DeviceName = func(majMin string) (string, bool) {
		switch majMin {
		case "8:0":
			return "sda", true
		case "8:16":
			return "sdb", true
		default:
			return "", false
		}
	}

	c.recordContainerStats("web", cgStats{
		IO: map[string]ioCounters{"8:0": {RBytes: 1_000_000, WBytes: 500_000}},
	}, time.Unix(1000, 0))
	_, ok := sink.value("web", "io.read_bps")
	require.False(t, ok, "tick 1: baseline only, nothing has a prior sample yet")

	c.recordContainerStats("web", cgStats{
		IO: map[string]ioCounters{"8:0": {RBytes: 1_200_000, WBytes: 600_000}}, // +200,000 / +100,000 over 2s
	}, time.Unix(1002, 0))
	readBps, ok := sink.value("web", "io.read_bps")
	require.True(t, ok)
	require.InDelta(t, 100_000.0, readBps, 1e-9, "tick 2: A's rate only")
	writeBps, ok := sink.value("web", "io.write_bps")
	require.True(t, ok)
	require.InDelta(t, 50_000.0, writeBps, 1e-9)
	devReadA, ok := sink.value("web", "live:io.sda.read_bps")
	require.True(t, ok)
	require.InDelta(t, 100_000.0, devReadA, 1e-9)

	c.recordContainerStats("web", cgStats{
		IO: map[string]ioCounters{
			"8:0":  {RBytes: 1_400_000, WBytes: 700_000},     // +200,000 / +100,000 over 2s: normal
			"8:16": {RBytes: 50_000_000, WBytes: 20_000_000}, // brand new device, huge cumulative counter
		},
	}, time.Unix(1004, 0))
	readBps, ok = sink.value("web", "io.read_bps")
	require.True(t, ok)
	require.InDelta(t, 100_000.0, readBps, 1e-9, "tick 3: B's huge first-sight counter must be suppressed, not folded into the total")
	writeBps, ok = sink.value("web", "io.write_bps")
	require.True(t, ok)
	require.InDelta(t, 50_000.0, writeBps, 1e-9)
	_, ok = sink.value("web", "live:io.sdb.read_bps")
	require.False(t, ok, "tick 3: B's own first observation must not get a live: series either")

	c.recordContainerStats("web", cgStats{
		IO: map[string]ioCounters{
			"8:0":  {RBytes: 1_600_000, WBytes: 800_000},     // +200,000 / +100,000 over 2s
			"8:16": {RBytes: 50_500_000, WBytes: 20_200_000}, // +500,000 / +200,000 over 2s: B's real rate now
		},
	}, time.Unix(1006, 0))
	readBps, ok = sink.value("web", "io.read_bps")
	require.True(t, ok)
	require.InDelta(t, 350_000.0, readBps, 1e-9, "tick 4: A(100,000) + B(250,000)")
	writeBps, ok = sink.value("web", "io.write_bps")
	require.True(t, ok)
	require.InDelta(t, 150_000.0, writeBps, 1e-9, "tick 4: A(50,000) + B(100,000)")
	devReadB, ok := sink.value("web", "live:io.sdb.read_bps")
	require.True(t, ok)
	require.InDelta(t, 250_000.0, devReadB, 1e-9)
}

// TestRecordContainerStatsSlugsDeviceNameForLiveSeries pins Task 1's
// hygiene fix for the live:io.<device>.* series: a resolved device name
// with characters outside [a-z0-9_-] must be slugged before it becomes
// part of the metric name, the same as hwmon labels and share names.
func TestRecordContainerStatsSlugsDeviceNameForLiveSeries(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)
	c.DeviceName = func(majMin string) (string, bool) {
		if majMin == "8:0" {
			return "My Disk!", true
		}
		return "", false
	}

	c.recordContainerStats("web", cgStats{
		IO: map[string]ioCounters{"8:0": {RBytes: 1_000_000, WBytes: 500_000}},
	}, time.Unix(1000, 0))
	c.recordContainerStats("web", cgStats{
		IO: map[string]ioCounters{"8:0": {RBytes: 1_200_000, WBytes: 600_000}},
	}, time.Unix(1002, 0))

	devRead, ok := sink.value("web", "live:io.my_disk.read_bps")
	require.True(t, ok, "device name must be slugged before entering the metric name")
	require.InDelta(t, 100_000.0, devRead, 1e-9)

	_, ok = sink.value("web", "live:io.My Disk!.read_bps")
	require.False(t, ok, "the unslugged device name must never appear in a metric name")
}

// TestRecordContainerStatsCPUHostShareTwoCoresOnEightCoreHost pins the
// host-share fix's own worked example: two full cores' worth of usage on
// an 8-core host is 25% of the HOST, not 200% of one core.
func TestRecordContainerStatsCPUHostShareTwoCoresOnEightCoreHost(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)

	c.recordContainerStats("web", cgStats{CPUUsageUsec: 0}, time.Unix(1000, 0))
	c.recordContainerStats("web", cgStats{CPUUsageUsec: 4_000_000}, time.Unix(1002, 0)) // 4,000,000 usec / 2s = 2 cores

	cores, ok := sink.value("web", "cpu.cores")
	require.True(t, ok)
	require.InDelta(t, 2.0, cores, 1e-9)

	pct, ok := sink.value("web", "cpu.pct")
	require.True(t, ok)
	require.InDelta(t, 25.0, pct, 1e-9)
}

// TestRecordContainerStatsFallsBackToNumCPUWhenHostCoresZero pins the
// last-resort fallback: a zero HostCores() (host collector hasn't ticked
// yet, or was never wired) must not leave cpu.pct blank fleet-wide --
// runtime.NumCPU() stands in as the denominator instead.
func TestRecordContainerStatsFallsBackToNumCPUWhenHostCoresZero(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)
	c.HostCores = func() int { return 0 }

	c.recordContainerStats("web", cgStats{CPUUsageUsec: 1_000_000}, time.Unix(1000, 0))
	c.recordContainerStats("web", cgStats{CPUUsageUsec: 3_000_000}, time.Unix(1002, 0))

	cores, ok := sink.value("web", "cpu.cores")
	require.True(t, ok, "cpu.cores needs no host core count, so it must still be emitted")
	require.InDelta(t, 1.0, cores, 1e-9)

	pct, ok := sink.value("web", "cpu.pct")
	require.True(t, ok, "cpu.pct must fall back to runtime.NumCPU() rather than go blank")
	require.InDelta(t, cores/float64(runtime.NumCPU())*100, pct, 1e-9)
}

func TestRecordContainerStatsSkipsMemPctWhenMemTotalZero(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)
	c.MemTotal = func() uint64 { return 0 }

	c.recordContainerStats("web", cgStats{MemCurrent: 100, MemInactiveFile: 10}, time.Unix(1000, 0))

	_, ok := sink.value("web", "mem.pct")
	require.False(t, ok)
	memBytes, ok := sink.value("web", "mem.bytes")
	require.True(t, ok)
	require.Equal(t, 90.0, memBytes)
}

func TestRecordContainerStatsMemBytesFloorsAtZero(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink)

	// Pathological: inactive_file reported larger than current. Must not
	// underflow (these are uint64 counters).
	c.recordContainerStats("web", cgStats{MemCurrent: 10, MemInactiveFile: 100}, time.Unix(1000, 0))

	memBytes, ok := sink.value("web", "mem.bytes")
	require.True(t, ok)
	require.Equal(t, 0.0, memBytes)
}

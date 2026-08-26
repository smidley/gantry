package docker

import (
	"os"
	"path/filepath"
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

var allCgroupFixtures = []string{"cpu.stat", "memory.current", "memory.stat", "pids.current", "io.stat"}

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
	copyFixtures(t, dir, "cpu.stat", "memory.current", "memory.stat", "pids.current")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "io.stat"), []byte(""), 0o644))

	cg, err := readCgroupStats(dir)
	require.NoError(t, err)
	require.Empty(t, cg.IO)
}

// recordContainerStats: the tick math, driven by two synthetic cgStats
// readings 2 seconds apart through a real RateTracker so the exact rate
// formulas are pinned, not just "greater than 0".
func newStatsCollector(sink store.MetricSink) *Collector {
	return &Collector{
		sink:     sink,
		rates:    collect.NewRateTracker(),
		MemTotal: func() uint64 { return 1_000_000_000 },
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

	cpuPct, ok := sink.value("web", "cpu.pct")
	require.True(t, ok)
	require.InDelta(t, 100.0, cpuPct, 1e-9, "2,000,000 usec CPU time consumed over 2,000,000 usec wall = 100%%")

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

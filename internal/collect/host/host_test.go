package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeSink struct {
	records map[store.SeriesKey]float64
}

func newFakeSink() *fakeSink { return &fakeSink{records: make(map[store.SeriesKey]float64)} }

func (f *fakeSink) Record(key store.SeriesKey, ts int64, val float64) {
	f.records[key] = val
}

func (f *fakeSink) value(metric string) (float64, bool) {
	v, ok := f.records[store.SeriesKey{Kind: "host", Metric: metric}]
	return v, ok
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

const statA = `cpu  1000 0 500 8000 500 0 0 0
cpu0 500 0 250 4000 250 0 0 0
cpu1 500 0 250 4000 250 0 0 0
intr 12345
ctxt 6789
btime 1690000000
`

const statB = `cpu  1100 0 550 8200 550 0 0 0
cpu0 550 0 275 4100 275 0 0 0
cpu1 550 0 275 4100 275 0 0 0
intr 12345
ctxt 6789
btime 1690000000
`

const meminfoA = `MemTotal:       1000000 kB
MemAvailable:    400000 kB
SwapTotal:       200000 kB
SwapFree:        200000 kB
`

const meminfoB = `MemTotal:       1000000 kB
MemAvailable:    250000 kB
SwapTotal:       200000 kB
SwapFree:        150000 kB
`

func TestHostNameAndInterval(t *testing.T) {
	c := New(newFakeSink(), "/proc", "/host/sys")
	require.Equal(t, "host", c.Name())
	require.Equal(t, 2*time.Second, c.Interval())
}

func TestHostProbeAvailableWhenStatReadable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	c := New(newFakeSink(), dir, t.TempDir())
	require.True(t, c.Probe(context.Background()).Available)
}

func TestHostProbeUnavailableWhenStatMissing(t *testing.T) {
	dir := t.TempDir()
	c := New(newFakeSink(), dir, t.TempDir())
	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.NotEmpty(t, st.Detail)
}

func TestHostTickFirstTickEmitsOnlyGauges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "loadavg", "1.25 1.10 0.95 3/512 6789\n")
	writeFile(t, dir, "uptime", "12345.67 9999.99\n")

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	_, ok := sink.value("cpu.total")
	require.False(t, ok, "first tick must not emit a CPU rate")
	_, ok = sink.value("cpu.core.0")
	require.False(t, ok, "first tick must not emit a per-core CPU rate")

	pct, ok := sink.value("mem.used_pct")
	require.True(t, ok, "first tick must still emit gauges")
	require.InDelta(t, 60.0, pct, 1e-9)
}

func TestHostTickComputesCPUAndMemDeltas(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "loadavg", "1.25 1.10 0.95 3/512 6789\n")
	writeFile(t, dir, "uptime", "12345.67 9999.99\n")

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "stat", statB)
	writeFile(t, dir, "meminfo", meminfoB)
	writeFile(t, dir, "loadavg", "2.50 1.80 1.20 4/512 6790\n")
	writeFile(t, dir, "uptime", "12347.67 9999.99\n")
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	// total: delta-idle=200 delta-iowait=50 delta-total=400 -> 100*(1-250/400)=37.5
	cpuTotal, ok := sink.value("cpu.total")
	require.True(t, ok)
	require.InDelta(t, 37.5, cpuTotal, 1e-9)

	// core0/core1: delta-idle=100 delta-iowait=25 delta-total=200 -> 100*(1-125/200)=37.5
	core0, ok := sink.value("cpu.core.0")
	require.True(t, ok)
	require.InDelta(t, 37.5, core0, 1e-9)
	core1, ok := sink.value("cpu.core.1")
	require.True(t, ok)
	require.InDelta(t, 37.5, core1, 1e-9)

	memPct, ok := sink.value("mem.used_pct")
	require.True(t, ok)
	require.InDelta(t, 75.0, memPct, 1e-9)
	memBytes, ok := sink.value("mem.used_bytes")
	require.True(t, ok)
	require.Equal(t, 750000.0*1024, memBytes)

	swapPct, ok := sink.value("swap.used_pct")
	require.True(t, ok)
	require.InDelta(t, 25.0, swapPct, 1e-9)

	load1, ok := sink.value("load.1m")
	require.True(t, ok)
	require.InDelta(t, 2.50, load1, 1e-9)

	uptime, ok := sink.value("uptime_s")
	require.True(t, ok)
	require.InDelta(t, 12347.67, uptime, 1e-6)
}

func TestHostTickEmitsArcBytesWhenPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "spl/kstat/zfs/arcstats", "size 4 8589934592\n")

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	v, ok := sink.value("mem.arc_bytes")
	require.True(t, ok)
	require.Equal(t, float64(8589934592), v)
}

func TestHostTickOmitsArcBytesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	_, ok := sink.value("mem.arc_bytes")
	require.False(t, ok)
}

func TestHostMemTotalUpdatesEachTick(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.Equal(t, uint64(0), c.MemTotal(), "MemTotal must be 0 before the first tick")

	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	require.Equal(t, uint64(1000000*1024), c.MemTotal())
}

package host

import (
	"context"
	"os"
	"path/filepath"
	"sync"
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

const netDevA = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: 1000000     900    0    0    0     0          0         5   200000      300    0    0    0     0       0          0
docker0:     500       5    0    0    0     0          0         0      500        5    0    0    0     0       0          0
`

const netDevB = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: 1004000     920    0    0    0     0          0         6   200800      310    0    0    0     0       0          0
docker0:     600       6    0    0    0     0          0         0      600        6    0    0    0     0       0          0
`

const diskstatsA = `   8       0 sda 1000 5 1000 100 2000 10 2000 200 0 300 300
   8       1 sda1 900 4 900 90 1800 9 1800 180 0 270 270
`

const diskstatsB = `   8       0 sda 1010 5 1100 110 2010 10 2200 220 0 330 330
   8       1 sda1 905 4 950 95 1810 9 1900 190 0 280 280
`

func TestHostTickEmitsNetDiskIOAndHwmon(t *testing.T) {
	dir := t.TempDir()
	sysRoot := buildHwmonTree(t)
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "1/net/dev", netDevA)
	writeFile(t, dir, "diskstats", diskstatsA)

	sink := newFakeSink()
	c := New(sink, dir, sysRoot)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	// rate metrics need a second sample; gauges (hwmon) show up immediately.
	_, ok := sink.value("net.eth0.rx_bps")
	require.False(t, ok, "first tick has no baseline yet for rate metrics")
	_, ok = sink.value("diskio.sda.read_bps")
	require.False(t, ok, "first tick has no baseline yet for rate metrics")

	tempVal, ok := sink.value("temp.coretemp_package_id_0.c")
	require.True(t, ok)
	require.InDelta(t, 45.0, tempVal, 1e-9)
	fanVal, ok := sink.value("fan.nct6779_fan1.rpm")
	require.True(t, ok)
	require.Equal(t, 1200.0, fanVal)

	name, ok := c.DeviceName("8:0")
	require.True(t, ok)
	require.Equal(t, "sda", name)
	name, ok = c.DeviceName("8:1")
	require.True(t, ok)
	require.Equal(t, "sda1", name, "DeviceMap includes partitions even though diskio.* metrics don't")

	writeFile(t, dir, "1/net/dev", netDevB)
	writeFile(t, dir, "diskstats", diskstatsB)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	rxBps, ok := sink.value("net.eth0.rx_bps")
	require.True(t, ok)
	require.InDelta(t, 2000.0, rxBps, 1e-6) // (1004000-1000000)/2s
	txBps, ok := sink.value("net.eth0.tx_bps")
	require.True(t, ok)
	require.InDelta(t, 400.0, txBps, 1e-6) // (200800-200000)/2s

	readBps, ok := sink.value("diskio.sda.read_bps")
	require.True(t, ok)
	require.InDelta(t, 25600.0, readBps, 1e-6) // (1100-1000)*512/2s
	writeBps, ok := sink.value("diskio.sda.write_bps")
	require.True(t, ok)
	require.InDelta(t, 51200.0, writeBps, 1e-6) // (2200-2000)*512/2s

	_, ok = sink.value("net.docker0.rx_bps")
	require.False(t, ok, "docker0 must be filtered")
	_, ok = sink.value("diskio.sda1.read_bps")
	require.False(t, ok, "partitions must be filtered from emitted diskio metrics")
}

func TestHostDeviceNameBeforeFirstTick(t *testing.T) {
	c := New(newFakeSink(), t.TempDir(), t.TempDir())
	_, ok := c.DeviceName("8:0")
	require.False(t, ok)
}

func TestHostDeviceNameSafeDuringConcurrentTicks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", diskstatsA)

	c := New(newFakeSink(), dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c.DeviceName("8:0")
			}
		}
	}()

	for i := 0; i < 50; i++ {
		require.NoError(t, c.Tick(context.Background(), time.Unix(int64(1001+i), 0)))
	}
	close(stop)
	wg.Wait()
}

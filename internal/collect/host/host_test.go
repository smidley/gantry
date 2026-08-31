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
	_, ok = sink.value("cpu.iowait_pct")
	require.False(t, ok, "first tick must not emit an iowait rate")

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

	// same two snapshots as cpu.total above: delta-iowait=50 delta-total=400 -> 100*50/400=12.5
	iowait, ok := sink.value("cpu.iowait_pct")
	require.True(t, ok)
	require.InDelta(t, 12.5, iowait, 1e-9)

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

func TestHostNumCPUCountsPerCoreLinesFromFirstTick(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.Equal(t, 0, c.NumCPU(), "NumCPU must be 0 before the first tick")

	// Unlike cpu.total (which needs a second sample for a rate), NumCPU
	// only needs a COUNT of one /proc/stat read's own cpuN lines -- so it
	// must already be right after the very first tick, not the second.
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	require.Equal(t, 2, c.NumCPU(), "statA has two cpuN lines")
}

// TestHostTickEmitsCPUCountFromFirstTick pins cpu.count's own metric
// analogue of NumCPU() above: the core-budget ribbon (Top Consumers' CPU
// breakdown) reads it off the live frame, not the Go method, so it needs
// the same "already right on tick 1" contract as a plain sample.
func TestHostTickEmitsCPUCountFromFirstTick(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	count, ok := sink.value("cpu.count")
	require.True(t, ok, "cpu.count must be recorded from the first tick, unlike cpu.total")
	require.Equal(t, 2.0, count, "statA has two cpuN lines")
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

func loadDiskstatsFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(data)
}

// TestHostTickComputesDiskSaturationLatencyAndQueue pins a hand-computed
// case against testdata/diskstats_saturation_{1,2}.txt's sda row (a 2s
// tick): io_ticks +1600ms -> util_pct 80 (1600/2000ms); time_in_queue
// +2400ms -> queue_avg 1.2 (2400/2000ms); (msReading+msWriting)=(300+500)
// over (reads+writes)=(50+50) -> await_ms 8.0; inflight is field 12's raw
// value, a gauge rather than a delta.
func TestHostTickComputesDiskSaturationLatencyAndQueue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", loadDiskstatsFixture(t, "diskstats_saturation_1.txt"))

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	inflight, ok := sink.value("diskio.sda.inflight")
	require.True(t, ok, "inflight is a gauge and must be live from the first tick")
	require.Equal(t, 3.0, inflight)
	_, ok = sink.value("diskio.sda.util_pct")
	require.False(t, ok, "first tick has no baseline yet for the delta-derived series")
	_, ok = sink.value("diskio.sda.await_ms")
	require.False(t, ok)
	_, ok = sink.value("diskio.sda.queue_avg")
	require.False(t, ok)

	writeFile(t, dir, "diskstats", loadDiskstatsFixture(t, "diskstats_saturation_2.txt"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	util, ok := sink.value("diskio.sda.util_pct")
	require.True(t, ok)
	require.InDelta(t, 80.0, util, 1e-9)

	await, ok := sink.value("diskio.sda.await_ms")
	require.True(t, ok)
	require.InDelta(t, 8.0, await, 1e-9)

	queue, ok := sink.value("diskio.sda.queue_avg")
	require.True(t, ok)
	require.InDelta(t, 1.2, queue, 1e-9)

	inflight, ok = sink.value("diskio.sda.inflight")
	require.True(t, ok)
	require.Equal(t, 4.0, inflight, "inflight must reflect the live gauge, not tick 1's value")
}

// TestHostTickDiskSaturationCoversMdDevices proves md* devices go through
// the identical saturation/latency path as sd*/nvme* -- the array's own
// parity rule needs util/await on md1, not just its members.
func TestHostTickDiskSaturationCoversMdDevices(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", loadDiskstatsFixture(t, "diskstats_saturation_1.txt"))

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "diskstats", loadDiskstatsFixture(t, "diskstats_saturation_2.txt"))
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	util, ok := sink.value("diskio.md1.util_pct")
	require.True(t, ok, "md* devices must get the full saturation set, not just sd*/nvme*")
	require.InDelta(t, 80.0, util, 1e-9)
	await, ok := sink.value("diskio.md1.await_ms")
	require.True(t, ok)
	require.InDelta(t, 8.0, await, 1e-9)
	queue, ok := sink.value("diskio.md1.queue_avg")
	require.True(t, ok)
	require.InDelta(t, 1.2, queue, 1e-9)
}

const diskstatsUtilClampA = "   8       0 sda 1000 0 1000 100 2000 0 2000 200 2 1000 1000\n"
const diskstatsUtilClampB = "   8       0 sda 1000 0 1000 100 2000 0 2000 200 5 4000 1000\n"

// TestHostTickUtilPctClampsAt100 covers io_ticks incrementing by more
// milliseconds than the tick interval contains -- possible because the
// field is millisecond-granular and a short tick can round above it.
// 3000ms of io_ticks over a 2000ms interval must read 100, not 150.
func TestHostTickUtilPctClampsAt100(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", diskstatsUtilClampA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "diskstats", diskstatsUtilClampB)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	util, ok := sink.value("diskio.sda.util_pct")
	require.True(t, ok)
	require.Equal(t, 100.0, util)
}

const diskstatsNoCompletedIOA = "   8       0 sda 500 0 5000 200 800 0 8000 300 1 2000 2000\n"
const diskstatsNoCompletedIOB = "   8       0 sda 500 0 5200 200 800 0 8200 300 1 2400 2300\n"

// TestHostTickAwaitMsOmittedWhenNoCompletedIO covers a device that had
// activity (io_ticks and sector counts moved) but zero completed reads or
// writes across the window: await_ms must be absent, not zero, because a
// zero would read as "instant" rather than "unmeasurable this tick".
func TestHostTickAwaitMsOmittedWhenNoCompletedIO(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", diskstatsNoCompletedIOA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "diskstats", diskstatsNoCompletedIOB)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	_, ok := sink.value("diskio.sda.await_ms")
	require.False(t, ok, "no reads or writes completed in the window: await_ms must be absent")

	util, ok := sink.value("diskio.sda.util_pct")
	require.True(t, ok, "util_pct is independent of completed IO -- io_ticks moved regardless")
	require.InDelta(t, 20.0, util, 1e-9)
}

const diskstatsBackwardsA = "   8       0 sda 100 0 1000 50 200 0 2000 80 1 5000 5000\n"
const diskstatsBackwardsB = "   8       0 sda 150 0 1100 150 250 0 2200 180 2 4000 6000\n"

// TestHostTickCounterGoingBackwardsSkipsThatSampleOnly covers io_ticks
// decreasing tick-over-tick (a counter reset) while reads/writes/ms
// counters keep advancing normally: util_pct must be skipped for that
// tick, never reported as a negative rate, and the sibling series that
// don't depend on io_ticks must be unaffected -- degradation is
// per-counter, not per-device.
func TestHostTickCounterGoingBackwardsSkipsThatSampleOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", diskstatsBackwardsA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "diskstats", diskstatsBackwardsB)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	_, ok := sink.value("diskio.sda.util_pct")
	require.False(t, ok, "io_ticks went backwards (5000->4000): must skip, never report a negative rate")

	await, ok := sink.value("diskio.sda.await_ms")
	require.True(t, ok, "await_ms doesn't depend on io_ticks and must still be recorded")
	require.InDelta(t, 2.0, await, 1e-9)
}

const diskstatsShortRowA = "   8       0 sdb 1000 5 1000 100 2000 10 2000 200 0\n"
const diskstatsShortRowB = "   8       0 sdb 1010 5 1100 110 2010 10 2200 220 0\n"

// TestHostTickShortRowYieldsThroughputOnlyNoLatencySeries covers a
// pre-4.18 12-field row end to end through Tick: read_bps/write_bps must
// still be emitted, and none of util_pct/await_ms/queue_avg/inflight may
// appear, because the kernel never reported the fields they need.
func TestHostTickShortRowYieldsThroughputOnlyNoLatencySeries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statA)
	writeFile(t, dir, "meminfo", meminfoA)
	writeFile(t, dir, "diskstats", diskstatsShortRowA)

	sink := newFakeSink()
	c := New(sink, dir, t.TempDir())
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "diskstats", diskstatsShortRowB)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	readBps, ok := sink.value("diskio.sdb.read_bps")
	require.True(t, ok, "a pre-4.18 12-field row must still yield throughput")
	require.InDelta(t, 25600.0, readBps, 1e-6)
	writeBps, ok := sink.value("diskio.sdb.write_bps")
	require.True(t, ok)
	require.InDelta(t, 51200.0, writeBps, 1e-6)

	for _, metric := range []string{"util_pct", "await_ms", "queue_avg", "inflight"} {
		_, ok := sink.value("diskio.sdb." + metric)
		require.False(t, ok, "a short row must not report "+metric)
	}
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

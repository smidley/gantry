package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const tickInterval = 2 * time.Second

// Collector reads host-wide metrics from procRoot and sysRoot (normally
// "/proc" and "/host/sys" in production; temp dirs in tests).
type Collector struct {
	sink     store.MetricSink
	procRoot string
	sysRoot  string
	rates    *collect.RateTracker

	memTotalBytes atomic.Uint64
	deviceMap     atomic.Pointer[map[string]string]

	havePrevCPU bool
	prevTotal   cpuTimes
	prevPerCore []cpuTimes
}

var _ collect.Collector = (*Collector)(nil)

func New(sink store.MetricSink, procRoot, sysRoot string) *Collector {
	return &Collector{sink: sink, procRoot: procRoot, sysRoot: sysRoot, rates: collect.NewRateTracker()}
}

func (c *Collector) Name() string            { return "host" }
func (c *Collector) Interval() time.Duration { return tickInterval }

func (c *Collector) Probe(context.Context) collect.Status {
	if _, err := os.Stat(filepath.Join(c.procRoot, "stat")); err != nil {
		return collect.Status{Available: false, Detail: "mount /proc read-only at " + c.procRoot}
	}
	return collect.Status{Available: true}
}

// MemTotal returns the most recently observed total system memory in
// bytes, or 0 before the first tick. Used by the docker collector as the
// mem.pct denominator.
func (c *Collector) MemTotal() uint64 { return c.memTotalBytes.Load() }

// DeviceName resolves a "major:minor" block device identifier (as reported
// by a container's cgroup io.stat) to its device name, e.g. "sda". Safe
// for concurrent callers while a tick rebuilds the map: the map is
// replaced wholesale, never mutated in place.
func (c *Collector) DeviceName(majMin string) (string, bool) {
	p := c.deviceMap.Load()
	if p == nil {
		return "", false
	}
	name, ok := (*p)[majMin]
	return name, ok
}

// Tick's only hard requirement is /proc/stat, matching Probe's contract;
// every other metric source degrades independently and silently when its
// file is missing or unreadable.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	if err := c.tickCPU(now); err != nil {
		return err
	}
	c.tickMem(now)
	c.tickLoadUptime(now)
	c.tickArc(now)
	c.tickNet(now)
	c.tickDiskIO(now)
	c.tickHwmon(now)
	return nil
}

func (c *Collector) tickCPU(now time.Time) error {
	f, err := os.Open(filepath.Join(c.procRoot, "stat"))
	if err != nil {
		return fmt.Errorf("host: open stat: %w", err)
	}
	defer func() { _ = f.Close() }()
	total, perCore, err := parseProcStat(f)
	if err != nil {
		return fmt.Errorf("host: parse stat: %w", err)
	}

	if c.havePrevCPU {
		ts := now.Unix()
		if pct, ok := cpuBusyPct(c.prevTotal, total); ok {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "cpu.total"}, ts, pct)
		}
		n := len(perCore)
		if len(c.prevPerCore) < n {
			n = len(c.prevPerCore)
		}
		for i := 0; i < n; i++ {
			if pct, ok := cpuBusyPct(c.prevPerCore[i], perCore[i]); ok {
				c.sink.Record(store.SeriesKey{Kind: "host", Metric: fmt.Sprintf("cpu.core.%d", i)}, ts, pct)
			}
		}
	}
	c.prevTotal, c.prevPerCore, c.havePrevCPU = total, perCore, true
	return nil
}

func (c *Collector) tickMem(now time.Time) {
	f, err := os.Open(filepath.Join(c.procRoot, "meminfo"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	memTotal, memAvailable, swapTotal, swapFree, err := parseMeminfo(f)
	if err != nil {
		return
	}
	c.memTotalBytes.Store(memTotal * 1024)

	usedKB := 0.0
	if memTotal > memAvailable {
		usedKB = float64(memTotal - memAvailable)
	}
	usedPct := 0.0
	if memTotal > 0 {
		usedPct = 100 * usedKB / float64(memTotal)
	}
	ts := now.Unix()
	c.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.used_pct"}, ts, usedPct)
	c.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.used_bytes"}, ts, usedKB*1024)

	swapUsedPct := 0.0
	if swapTotal > 0 {
		swapUsedPct = 100 * float64(swapTotal-swapFree) / float64(swapTotal)
	}
	c.sink.Record(store.SeriesKey{Kind: "host", Metric: "swap.used_pct"}, ts, swapUsedPct)
}

func (c *Collector) tickLoadUptime(now time.Time) {
	ts := now.Unix()
	if f, err := os.Open(filepath.Join(c.procRoot, "loadavg")); err == nil {
		load1, perr := parseLoadavg(f)
		_ = f.Close()
		if perr == nil {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "load.1m"}, ts, load1)
		}
	}
	if f, err := os.Open(filepath.Join(c.procRoot, "uptime")); err == nil {
		uptime, perr := parseUptime(f)
		_ = f.Close()
		if perr == nil {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "uptime_s"}, ts, uptime)
		}
	}
}

func (c *Collector) tickArc(now time.Time) {
	f, err := os.Open(filepath.Join(c.procRoot, "spl", "kstat", "zfs", "arcstats"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if arcBytes, ok := parseArcstats(f); ok {
		c.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.arc_bytes"}, now.Unix(), float64(arcBytes))
	}
}

func (c *Collector) tickNet(now time.Time) {
	// procRoot+"/1/net/dev": with --pid=host, PID 1 is the host's init, so
	// its netns is the host netns (our own netns would show container-only
	// interfaces).
	f, err := os.Open(filepath.Join(c.procRoot, "1", "net", "dev"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	all, err := ParseNetDev(f)
	if err != nil {
		return
	}

	ts := now.Unix()
	for iface, cnt := range filteredIfaces(all) {
		if bps, ok := c.rates.Rate("net."+iface+".rx", now, float64(cnt.RxBytes)); ok {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "net." + iface + ".rx_bps"}, ts, bps)
		}
		if bps, ok := c.rates.Rate("net."+iface+".tx", now, float64(cnt.TxBytes)); ok {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "net." + iface + ".tx_bps"}, ts, bps)
		}
	}
}

func (c *Collector) tickDiskIO(now time.Time) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "diskstats"))
	if err != nil {
		return
	}
	counters, err := parseDiskstats(bytes.NewReader(data))
	if err != nil {
		return
	}
	if devMap, derr := buildDeviceMap(bytes.NewReader(data)); derr == nil {
		c.deviceMap.Store(&devMap)
	}

	ts := now.Unix()
	for dev, cnt := range counters {
		if bps, ok := c.rates.Rate("diskio."+dev+".read", now, float64(cnt.readSectors)*512); ok {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio." + dev + ".read_bps"}, ts, bps)
		}
		if bps, ok := c.rates.Rate("diskio."+dev+".write", now, float64(cnt.writeSectors)*512); ok {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio." + dev + ".write_bps"}, ts, bps)
		}
	}
}

func (c *Collector) tickHwmon(now time.Time) {
	ts := now.Unix()
	for _, r := range scanHwmon(c.sysRoot) {
		switch r.kind {
		case hwmonTemp:
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "temp." + r.label + ".c"}, ts, r.value)
		case hwmonFan:
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "fan." + r.label + ".rpm"}, ts, r.value)
		}
	}
}

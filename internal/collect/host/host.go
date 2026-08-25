package host

import (
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

	memTotalBytes atomic.Uint64

	havePrevCPU bool
	prevTotal   cpuTimes
	prevPerCore []cpuTimes
}

var _ collect.Collector = (*Collector)(nil)

func New(sink store.MetricSink, procRoot, sysRoot string) *Collector {
	return &Collector{sink: sink, procRoot: procRoot, sysRoot: sysRoot}
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

func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	if err := c.tickCPU(now); err != nil {
		return err
	}
	if err := c.tickMem(now); err != nil {
		return err
	}
	c.tickLoadUptime(now)
	c.tickArc(now)
	return nil
}

func (c *Collector) tickCPU(now time.Time) error {
	f, err := os.Open(filepath.Join(c.procRoot, "stat"))
	if err != nil {
		return fmt.Errorf("host: open stat: %w", err)
	}
	defer f.Close()
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

func (c *Collector) tickMem(now time.Time) error {
	f, err := os.Open(filepath.Join(c.procRoot, "meminfo"))
	if err != nil {
		return fmt.Errorf("host: open meminfo: %w", err)
	}
	defer f.Close()
	memTotal, memAvailable, swapTotal, swapFree, err := parseMeminfo(f)
	if err != nil {
		return fmt.Errorf("host: parse meminfo: %w", err)
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
	return nil
}

func (c *Collector) tickLoadUptime(now time.Time) {
	ts := now.Unix()
	if f, err := os.Open(filepath.Join(c.procRoot, "loadavg")); err == nil {
		load1, perr := parseLoadavg(f)
		f.Close()
		if perr == nil {
			c.sink.Record(store.SeriesKey{Kind: "host", Metric: "load.1m"}, ts, load1)
		}
	}
	if f, err := os.Open(filepath.Join(c.procRoot, "uptime")); err == nil {
		uptime, perr := parseUptime(f)
		f.Close()
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
	defer f.Close()
	if arcBytes, ok := parseArcstats(f); ok {
		c.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.arc_bytes"}, now.Unix(), float64(arcBytes))
	}
}

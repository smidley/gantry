package docker

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

// ioCounters is one block device's cumulative read/write byte counters
// from a cgroup's io.stat (or the docker stats API's blkio_stats, in the
// Task 8 fallback), keyed by "major:minor" in the containing map.
type ioCounters struct {
	RBytes, WBytes uint64
}

// cgStats is one point-in-time reading of a container's cgroup v2
// counters — from the fast path (readCgroupStats, this file) or the API
// fallback (apistats.go). recordContainerStats turns either into the
// same recorded metrics.
type cgStats struct {
	CPUUsageUsec, ThrottledUsec, NrThrottled uint64
	MemCurrent, MemInactiveFile              uint64
	Pids                                     uint64
	IO                                       map[string]ioCounters // maj:min -> rbytes/wbytes
}

// readCgroupStats reads one container's cgroup v2 directory (e.g.
// /host/sys/fs/cgroup/docker/<id>): cpu.stat, memory.current,
// memory.stat, pids.current, io.stat. Any missing or malformed required
// file fails the whole read — the caller (tickStats) treats that as "no
// cgroup v2 here" and falls back to the docker stats API (apistats.go)
// rather than mixing partial cgroup data with API data.
func readCgroupStats(dir string) (cgStats, error) {
	usageUsec, throttledUsec, nrThrottled, err := readCPUStat(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	memCurrent, err := readSingleUint(filepath.Join(dir, "memory.current"))
	if err != nil {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	inactiveFile, err := readMemoryStatInactiveFile(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	pids, err := readSingleUint(filepath.Join(dir, "pids.current"))
	if err != nil {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	io, err := readIOStat(filepath.Join(dir, "io.stat"))
	if err != nil {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	return cgStats{
		CPUUsageUsec:    usageUsec,
		ThrottledUsec:   throttledUsec,
		NrThrottled:     nrThrottled,
		MemCurrent:      memCurrent,
		MemInactiveFile: inactiveFile,
		Pids:            pids,
		IO:              io,
	}, nil
}

// readCPUStat reads cpu.stat's usage_usec/throttled_usec/nr_throttled.
// Keys not present are left at 0 (matches host/proc.go's parseMeminfo
// tolerance for unknown/missing fields).
func readCPUStat(path string) (usageUsec, throttledUsec, nrThrottled uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = f.Close() }()

	dst := map[string]*uint64{
		"usage_usec":     &usageUsec,
		"throttled_usec": &throttledUsec,
		"nr_throttled":   &nrThrottled,
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		p, ok := dst[fields[0]]
		if !ok {
			continue
		}
		n, perr := strconv.ParseUint(fields[1], 10, 64)
		if perr != nil {
			return 0, 0, 0, fmt.Errorf("cpu.stat: parse %q: %w", fields[0], perr)
		}
		*p = n
	}
	if err := sc.Err(); err != nil {
		return 0, 0, 0, err
	}
	return usageUsec, throttledUsec, nrThrottled, nil
}

// readMemoryStatInactiveFile reads only the inactive_file line of
// memory.stat — the other ~40 fields aren't needed by this phase.
func readMemoryStatInactiveFile(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "inactive_file" {
			continue
		}
		return strconv.ParseUint(fields[1], 10, 64)
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("memory.stat: missing inactive_file")
}

// readSingleUint reads a file holding exactly one integer (memory.current,
// pids.current).
func readSingleUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse: %w", filepath.Base(path), err)
	}
	return v, nil
}

// readIOStat reads io.stat: one line per block device, "maj:min
// key=value ...". Only rbytes/wbytes are kept. A missing key on a device
// line just leaves that counter at 0; an empty file yields an empty (not
// nil) map — a container that hasn't touched a device yet is not an
// error.
func readIOStat(path string) (map[string]ioCounters, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := make(map[string]ioCounters)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		var c ioCounters
		for _, kv := range fields[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			n, perr := strconv.ParseUint(v, 10, 64)
			if perr != nil {
				continue // tolerate one malformed counter rather than failing the whole device line
			}
			switch k {
			case "rbytes":
				c.RBytes = n
			case "wbytes":
				c.WBytes = n
			}
		}
		out[fields[0]] = c
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// tickStats records per-container stats every tick: the cgroup v2 fast
// path for each container the registry reports as running, falling back
// to a one-shot docker stats API call (apistats.go) when the cgroup dir
// can't be read (v1 host, masked path). Selection is automatic and
// per-container; the fallback is logged once per container (keyed by
// name — the stable identity across recreations, spec §5, and what
// evictContainer prunes on removal) so a whole-fleet v1 box doesn't spam
// the log every 2s.
func (c *Collector) tickStats(ctx context.Context, now time.Time) {
	for _, m := range c.reg.running() {
		dir := filepath.Join(c.CgroupRoot, "docker", m.ID)
		cg, err := readCgroupStats(dir)
		if err != nil {
			cg, err = c.statsViaAPI(ctx, m.ID)
			if err != nil {
				continue
			}
			if _, alreadyLogged := c.loggedFallback.LoadOrStore(m.Name, struct{}{}); !alreadyLogged {
				log.Printf("docker: %s: cgroup v2 stats unavailable, using stats API fallback", m.Name)
			}
		}
		c.recordContainerStats(m.Name, cg, now)
	}
}

// recordContainerStats turns one point-in-time cgStats reading into
// recorded metrics, keyed by container name (the stable identity across
// recreations — spec §5). Shared by the cgroup v2 fast path (tickStats,
// above) and the docker stats API fallback (apistats.go) — same shape
// in, same math out, so which source fed a given tick is invisible
// downstream.
//
// cpu.cores/cpu.pct/cpu.throttled_pct reuse RateTracker.Rate, which
// already computes delta/elapsed-seconds: for a microsecond counter
// that's "usec consumed per second of wall time". /1,000,000 turns that
// into cpu.cores (1.00 = one full core busy, docker-stats' own per-core
// convention -- may exceed 1 for a multi-threaded container). cpu.pct
// then divides cores by HostCores() for a HOST-share percentage instead
// -- the two numbers read on the same "% of this machine" scale as
// cpu.total, rather than docker stats' inflated per-core one that reads
// as if it exceeded the whole host. Skipped (like mem.pct below) when
// HostCores() isn't known yet. cpu.throttled_pct keeps the old /10,000
// (Δusec/Δwall_usec×100): throttling has no host-wide analog to
// normalize against.
func (c *Collector) recordContainerStats(name string, cg cgStats, now time.Time) {
	ts := now.Unix()
	key := func(metric string) store.SeriesKey {
		return store.SeriesKey{Kind: "container", Entity: name, Metric: metric}
	}

	if usecPerSec, ok := c.rates.Rate(name+".cpu.usage", now, float64(cg.CPUUsageUsec)); ok {
		cores := usecPerSec / 1_000_000
		c.sink.Record(key("cpu.cores"), ts, cores)
		if hostCores := c.HostCores(); hostCores > 0 {
			c.sink.Record(key("cpu.pct"), ts, cores/float64(hostCores)*100)
		}
	}
	if usecPerSec, ok := c.rates.Rate(name+".cpu.throttled", now, float64(cg.ThrottledUsec)); ok {
		c.sink.Record(key("cpu.throttled_pct"), ts, usecPerSec/10000)
	}

	// docker-stats definition of "used" memory (spec §4.1): current minus
	// reclaimable page cache. uint64 counters, so guard the subtraction
	// against an (unexpected) inactive_file > current reading instead of
	// wrapping around to a huge number.
	memBytes := 0.0
	if cg.MemCurrent > cg.MemInactiveFile {
		memBytes = float64(cg.MemCurrent - cg.MemInactiveFile)
	}
	c.sink.Record(key("mem.bytes"), ts, memBytes)
	if total := c.MemTotal(); total > 0 {
		c.sink.Record(key("mem.pct"), ts, 100*memBytes/float64(total))
	}

	c.sink.Record(key("pids"), ts, float64(cg.Pids))

	// io.read_bps/write_bps are the SUM of each device's own rate, not a
	// rate computed on the summed counters: a device's cumulative counter
	// can be large the first time it's ever seen (e.g. it existed before
	// this container was tracked, or just appeared in io.stat), and an
	// aggregate-counter rate would fold that whole history into one
	// tick's delta as soon as it joins an already-warm sum. Keying each
	// device's Rate call by its own major:minor makes a new device's
	// first observation return ok=false — same "first sample is silent"
	// rule as everywhere else — so it's correctly excluded from the total
	// until its own second reading, one tick later, gives a real delta.
	// Unresolved (unnamed) devices still get their own rate key and still
	// count toward the total; they only miss out on the live: per-device
	// series, which needs a name.
	var totalReadBps, totalWriteBps float64
	var haveRead, haveWrite bool
	for majMin, dev := range cg.IO {
		devName, named := c.DeviceName(majMin)
		slugName := collect.SlugSegment(devName)

		if bps, ok := c.rates.Rate(name+".io."+majMin+".read", now, float64(dev.RBytes)); ok {
			totalReadBps += bps
			haveRead = true
			if named {
				c.sink.Record(key("live:io."+slugName+".read_bps"), ts, bps)
			}
		}
		if bps, ok := c.rates.Rate(name+".io."+majMin+".write", now, float64(dev.WBytes)); ok {
			totalWriteBps += bps
			haveWrite = true
			if named {
				c.sink.Record(key("live:io."+slugName+".write_bps"), ts, bps)
			}
		}
	}
	if haveRead {
		c.sink.Record(key("io.read_bps"), ts, totalReadBps)
	}
	if haveWrite {
		c.sink.Record(key("io.write_bps"), ts, totalWriteBps)
	}
}

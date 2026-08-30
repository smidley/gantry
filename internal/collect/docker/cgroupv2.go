package docker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	Alloc                                    alloc
}

// alloc is one container's configured resource allocation ceiling: the
// cgroup v2 fast path's memory.max/cpu.max/cpuset.cpus.effective/
// pids.max, or the API fallback's HostConfig equivalent (Meta.Alloc,
// docker.go's allocFromHostConfig) — same shape either way, so
// recordContainerStats' allocation math never forks by source. Each
// resource's Has* flag is the only thing callers may trust: the paired
// value is meaningless (not necessarily zero) when Has* is false, and a
// false Has* is how "unlimited" propagates through to "emit nothing for
// this pair" in recordContainerStats.
type alloc struct {
	MemLimitBytes uint64
	HasMemLimit   bool

	CPUQuotaCores float64 // quota/period; meaningful only when HasCPUQuota
	HasCPUQuota   bool

	CPUSetCores int // cpuset.cpus.effective's core count; meaningful only when HasCPUSet
	// CPUSetRaw is the cpuset core-list string CPUSetCores was counted
	// from (cpuset.cpus.effective, or the API fallback's HostConfig.
	// CpusetCpus) -- kept alongside the count so a caller that wants the
	// actual pinned core ids for display (CPUSetPin) doesn't need its own
	// second read of the same file/field. Meaningless when HasCPUSet is
	// false, same as CPUSetCores.
	CPUSetRaw string
	HasCPUSet bool

	PidsLimit    uint64
	HasPidsLimit bool
}

// readCgroupStats reads one container's cgroup v2 directory (e.g.
// /host/sys/fs/cgroup/docker/<id>): cpu.stat, memory.current,
// memory.stat, pids.current, io.stat, plus the allocation-ceiling files
// memory.max, cpu.max, pids.max, and cpuset.cpus.effective. Any missing
// or malformed usage file fails the whole read — the caller (tickStats)
// treats that as "no cgroup v2 here" and falls back to the docker stats
// API (apistats.go) rather than mixing partial cgroup data with API
// data. The four allocation files are different: a restricted-delegation
// environment (rootless docker, LXC) can legitimately lack any one of
// them, so a missing (not merely malformed) allocation file only demotes
// that one ceiling to unlimited (Has*=false) rather than the whole
// container to the API fallback, which would throw away every usage
// counter this read already has in hand over one absent limit. The
// allocation files are cheap reads in the same directory as the usage
// counters, so a live `docker update` to a container's limits shows up
// on this fast path's very next tick.
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

	memLimit, hasMemLimit, err := readMaxOrLimit(filepath.Join(dir, "memory.max"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	quotaUsec, periodUsec, hasQuota, err := readCPUMax(filepath.Join(dir, "cpu.max"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}
	var quotaCores float64
	if hasQuota && periodUsec > 0 {
		quotaCores = float64(quotaUsec) / float64(periodUsec)
	} else {
		hasQuota = false
	}

	pidsLimit, hasPidsLimit, err := readMaxOrLimit(filepath.Join(dir, "pids.max"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}

	cpusetRaw, err := os.ReadFile(filepath.Join(dir, "cpuset.cpus.effective"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cgStats{}, fmt.Errorf("cgroup: %w", err)
	}
	cpusetCores, hasCPUSet := parseCPUSetCount(string(cpusetRaw))

	return cgStats{
		CPUUsageUsec:    usageUsec,
		ThrottledUsec:   throttledUsec,
		NrThrottled:     nrThrottled,
		MemCurrent:      memCurrent,
		MemInactiveFile: inactiveFile,
		Pids:            pids,
		IO:              io,
		Alloc: alloc{
			MemLimitBytes: memLimit, HasMemLimit: hasMemLimit,
			CPUQuotaCores: quotaCores, HasCPUQuota: hasQuota,
			CPUSetCores: cpusetCores, CPUSetRaw: strings.TrimSpace(string(cpusetRaw)), HasCPUSet: hasCPUSet,
			PidsLimit: pidsLimit, HasPidsLimit: hasPidsLimit,
		},
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

// readMaxOrLimit reads a cgroup v2 control file holding either the
// literal "max" (unlimited — ok=false) or a single non-negative integer
// (memory.max, pids.max).
func readMaxOrLimit(path string) (limit uint64, ok bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	s := strings.TrimSpace(string(data))
	if s == "max" {
		return 0, false, nil
	}
	v, perr := strconv.ParseUint(s, 10, 64)
	if perr != nil {
		return 0, false, fmt.Errorf("%s: parse: %w", filepath.Base(path), perr)
	}
	return v, true, nil
}

// readCPUMax reads cpu.max's "<quota> <period>" pair; quota may be the
// literal "max" (unlimited — hasQuota=false), in which case periodUsec
// is still parsed but meaningless to the caller.
func readCPUMax(path string) (quotaUsec, periodUsec uint64, hasQuota bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false, err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 0, 0, false, fmt.Errorf("cpu.max: want 2 fields, got %d", len(fields))
	}
	periodUsec, err = strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, 0, false, fmt.Errorf("cpu.max: parse period: %w", err)
	}
	if fields[0] == "max" {
		return 0, periodUsec, false, nil
	}
	quotaUsec, err = strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, 0, false, fmt.Errorf("cpu.max: parse quota: %w", err)
	}
	return quotaUsec, periodUsec, true, nil
}

// maxCPUSetRangeSpan caps both how wide a single "lo-hi" range in a
// cpuset list may be AND how many distinct ids the list may name in
// total, before parseCPUSetCount rejects it outright: no real host has
// anywhere near this many cores, so either kind of excess is either a
// corrupt read or an adversarial value, and materializing it into the
// id set below (one map entry per core) would otherwise size that
// allocation off an attacker- or corruption-controlled number. The
// per-range check alone isn't enough: many disjoint ranges, each
// individually under it, can still sum to an arbitrarily large total.
const maxCPUSetRangeSpan = 1 << 16

// parseCPUSetCount parses a cgroup cpuset core list ("0-5,13-15",
// "0-15", "3") into the number of DISTINCT cores it names. Empty or
// malformed input reports ok=false, which callers treat as "no pinning
// info" -- i.e. unrestricted -- rather than failing the whole stats read
// over a garbled cpuset file. Ids are counted through a set rather than
// summed range-by-range, since HostConfig.CpusetCpus stores whatever raw
// string a caller passed to --cpuset-cpus verbatim, and docker doesn't
// reject overlapping ranges ("0-3,2-5") the way cpuset.cpus.effective's
// own kernel-normalized form never would. A single range spanning
// maxCPUSetRangeSpan or more ids is rejected outright (see its own
// doc), and so is the list as a whole once its running total of
// distinct ids crosses that same cap -- checked after every
// comma-separated part, so many disjoint ranges that each individually
// pass the per-range check can't still add up to an unbounded total.
// Shared verbatim by the cgroup v2 fast path (cpuset.cpus.effective)
// and the API fallback (HostConfig.CpusetCpus, docker.go's
// allocFromHostConfig).
func parseCPUSetCount(s string) (count int, ok bool) {
	ids, ok := parseCPUSetIDList(s)
	return len(ids), ok
}

// parseCPUSetIDList is parseCPUSetCount's own shared body, returning the
// distinct core ids themselves (unsorted -- map iteration order) rather
// than just their count -- CPUSetPin, below, needs the actual ids to
// render a display string; parseCPUSetCount only ever needed how many.
func parseCPUSetIDList(s string) (ids []int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	idSet := map[int]struct{}{}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		loN, err := strconv.Atoi(lo)
		if err != nil || loN < 0 {
			return nil, false
		}
		if !isRange {
			idSet[loN] = struct{}{}
		} else {
			hiN, err := strconv.Atoi(hi)
			if err != nil || hiN < loN {
				return nil, false
			}
			if hiN-loN >= maxCPUSetRangeSpan {
				return nil, false
			}
			for i := loN; i <= hiN; i++ {
				idSet[i] = struct{}{}
			}
		}
		if len(idSet) > maxCPUSetRangeSpan {
			return nil, false // per-part total cap: catches many disjoint under-cap ranges summing past it
		}
	}
	out := make([]int, 0, len(idSet))
	for id := range idSet {
		out = append(out, id)
	}
	return out, true
}

// CPUSetPin renders a raw cgroup cpuset core list (parseCPUSetIDList's
// own syntax -- a user-typed --cpuset-cpus value or the kernel-normalized
// cpuset.cpus.effective, either one) as a canonical, sorted, deduped
// display string ("0-5, 13-15"), but only when it actually narrows the
// container below hostCores -- "" (ok=false) for an unrestricted cpuset
// (cpuset.cpus.effective defaults to every host core when nothing is
// pinned, which must read as unlimited, not "pinned to every core" --
// same rule effectiveCPUAllocCores applies to the alloc-cores number),
// a malformed/empty raw string, or an unknown host core count
// (hostCores<=0). Exported (unlike this file's other alloc helpers) so
// fake.go -- which can't construct this package's own unexported `alloc`
// type -- can still compute the identical display string for its own
// synthetic pinned demo container.
func CPUSetPin(raw string, hostCores int) (pin string, ok bool) {
	ids, valid := parseCPUSetIDList(raw)
	if !valid || hostCores <= 0 || len(ids) >= hostCores {
		return "", false
	}
	sort.Ints(ids)
	var parts []string
	for i := 0; i < len(ids); {
		j := i
		for j+1 < len(ids) && ids[j+1] == ids[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, strconv.Itoa(ids[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ids[i], ids[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", "), true
}

// fallbackAlloc merges the stats-API path's own allocation reading
// (apiAlloc, today only ever carrying Alloc.Pids* -- see statsFromAPI)
// with m.Alloc's HostConfig-derived one (hostConfigAlloc) for a fallback
// tick: HostConfig supplies mem/cpu/cpuset unconditionally, since the
// stats API has no room for those at all, while pids prefers apiAlloc's
// own reading -- PidsStats.Limit catches a daemon-level
// --default-pids-limit that never reaches HostConfig -- falling back to
// hostConfigAlloc's PidsLimit only when the API reported none. Extracted
// as a pure function so this priority is testable without a live docker
// daemon.
func fallbackAlloc(apiAlloc, hostConfigAlloc alloc) alloc {
	merged := hostConfigAlloc
	if apiAlloc.HasPidsLimit {
		merged.PidsLimit, merged.HasPidsLimit = apiAlloc.PidsLimit, apiAlloc.HasPidsLimit
	}
	return merged
}

// tickStats records per-container stats every tick: the cgroup v2 fast
// path for each container the registry reports as running, falling back
// to a one-shot docker stats API call (apistats.go) when the cgroup dir
// can't be read (v1 host, masked path). Selection is automatic and
// per-container; the fallback is logged once per container (keyed by
// name — the stable identity across recreations, spec §5, and what
// evictContainer prunes on removal) so a whole-fleet v1 box doesn't spam
// the log every 2s.
//
// statsViaAPI's response has room for only the pids ceiling (PidsStats.
// Limit, mapped into cg.Alloc by statsFromAPI); mem/cpu/cpuset have no
// stats-API equivalent at all. fallbackAlloc merges the two sources with
// pids preferring the API's own reading -- see its own doc. m.Alloc is
// captured at inspect time, refreshed on the 10s inventory poll rather
// than fresh every 2s like the fast path's own read. recordContainerStats
// runs the exact same allocation math either way; only how current the
// ceiling is (and, for pids, which of two sources fed it) can differ by
// source.
func (c *Collector) tickStats(ctx context.Context, now time.Time) {
	for _, m := range c.reg.running() {
		dir := filepath.Join(c.CgroupRoot, "docker", m.ID)
		cg, err := readCgroupStats(dir)
		if err != nil {
			cg, err = c.statsViaAPI(ctx, m.ID)
			if err != nil {
				continue
			}
			cg.Alloc = fallbackAlloc(cg.Alloc, m.Alloc)
			if _, alreadyLogged := c.loggedFallback.LoadOrStore(m.Name, struct{}{}); !alreadyLogged {
				log.Printf("docker: %s: cgroup v2 stats unavailable, using stats API fallback", m.Name)
			}
		}
		c.recordContainerStats(m.Name, cg, now)
	}
}

// hostCoresOrNumCPU returns HostCores(), falling back to runtime.NumCPU()
// when it's zero/unset (host collector hasn't ticked yet, or was never
// wired) -- the same last-resort every host-share metric below already
// relies on.
func (c *Collector) hostCoresOrNumCPU() int {
	if hc := c.HostCores(); hc > 0 {
		return hc
	}
	return runtime.NumCPU()
}

// effectiveCPUAllocCores resolves one alloc's CPU ceiling into a single
// core count: a quota always wins outright when set; a cpuset pin only
// counts when it actually narrows the container below hostCores
// (cpuset.cpus.effective defaults to every host core when nothing is
// pinned, which must read as unlimited rather than "restricted to N
// cores" -- and hostCores itself unknown, <= 0, must not be treated as a
// restriction either); when both are set, the tighter of the two wins.
//
// hostCores and rawHostCores are deliberately different params. hostCores
// (the caller's hostCoresOrNumCPU(), runtime.NumCPU()-backed when the
// host collector hasn't ticked yet) only ever gates the cpuset-narrowing
// decision above -- a rough guess is fine there, it's just a yes/no. The
// final clamp below is different: it can silently overwrite
// cpu.alloc_cores, a precise config readout, so it may only fire against
// rawHostCores (the caller's own c.HostCores(), no NumCPU() fallback) --
// dockerd doesn't validate --cpu-quota against the host's own core
// count, and a ceiling above the machine is unusable headroom, but a
// guessed "host size" is the wrong tool to fix that with. rawHostCores
// <= 0 (host collector hasn't ticked) skips the clamp entirely rather
// than guess.
func effectiveCPUAllocCores(a alloc, hostCores, rawHostCores int) (cores float64, ok bool) {
	cpusetRestricts := a.HasCPUSet && hostCores > 0 && a.CPUSetCores < hostCores
	switch {
	case a.HasCPUQuota && cpusetRestricts:
		cores, ok = math.Min(a.CPUQuotaCores, float64(a.CPUSetCores)), true
	case a.HasCPUQuota:
		cores, ok = a.CPUQuotaCores, true
	case cpusetRestricts:
		cores, ok = float64(a.CPUSetCores), true
	default:
		return 0, false
	}
	if rawHostCores > 0 && cores > float64(rawHostCores) {
		cores = float64(rawHostCores)
	}
	return cores, ok
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
// as if it exceeded the whole host. HostCores() (the /proc/stat-derived,
// cpuset-immune count) stays the primary source; a zero/unset reading
// (not yet ticked, or never wired) falls back to runtime.NumCPU() as a
// last resort so cpu.pct doesn't go blank fleet-wide until the host
// collector's first tick. cpu.throttled_pct keeps the old /10,000
// (Δusec/Δwall_usec×100): throttling has no host-wide analog to
// normalize against.
//
// mem.limit_bytes/cpu.alloc_cores/pids.limit and their *_pct/*_alloc_pct
// partners are the allocation-side half of the same idea: how much of
// the container's OWN ceiling (not the host's) it's using. Each pair is
// gated on its own alloc Has* flag -- unlimited (the real-box default
// for most containers) emits neither metric of the pair, rather than a
// misleading 0-byte/0-core/0-pid ceiling. cpu.alloc_cores is available
// from the first tick (it describes the ceiling, not usage); cpu.alloc_pct
// needs cpu.cores' own rate, so it only appears once that does, one tick
// later.
func (c *Collector) recordContainerStats(name string, cg cgStats, now time.Time) {
	ts := now.Unix()
	key := func(metric string) store.SeriesKey {
		return store.SeriesKey{Kind: "container", Entity: name, Metric: metric}
	}

	hostCores := c.hostCoresOrNumCPU()
	allocCores, hasAllocCores := effectiveCPUAllocCores(cg.Alloc, hostCores, c.HostCores())
	if hasAllocCores {
		c.sink.Record(key("cpu.alloc_cores"), ts, allocCores)
	}

	if usecPerSec, ok := c.rates.Rate(name+".cpu.usage", now, float64(cg.CPUUsageUsec)); ok {
		cores := usecPerSec / 1_000_000
		c.sink.Record(key("cpu.cores"), ts, cores)
		if hostCores > 0 {
			c.sink.Record(key("cpu.pct"), ts, cores/float64(hostCores)*100)
		}
		if hasAllocCores && allocCores > 0 {
			c.sink.Record(key("cpu.alloc_pct"), ts, cores/allocCores*100)
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
	if cg.Alloc.HasMemLimit {
		c.sink.Record(key("mem.limit_bytes"), ts, float64(cg.Alloc.MemLimitBytes))
		if cg.Alloc.MemLimitBytes > 0 {
			c.sink.Record(key("mem.limit_pct"), ts, 100*memBytes/float64(cg.Alloc.MemLimitBytes))
		}
	}

	c.sink.Record(key("pids"), ts, float64(cg.Pids))
	if cg.Alloc.HasPidsLimit {
		c.sink.Record(key("pids.limit"), ts, float64(cg.Alloc.PidsLimit))
		if cg.Alloc.PidsLimit > 0 {
			c.sink.Record(key("pids.pct"), ts, 100*float64(cg.Pids)/float64(cg.Alloc.PidsLimit))
		}
	}

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

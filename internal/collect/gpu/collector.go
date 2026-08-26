package gpu

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const (
	tickInterval     = 2 * time.Second
	fullScanInterval = 30 * time.Second
	engineKeyPrefix  = "drm-engine-"
)

// Collector discovers DRM (GPU) clients via /proc/<pid>/fdinfo and turns
// their cumulative per-engine busy-time counters into per-container and
// per-GPU busy_pct series (spec §4.4, spike S1). Name "gpu", Interval 2s.
type Collector struct {
	sink     store.MetricSink
	procRoot string
	lookup   func(id string) (name string, ok bool)
	rates    *collect.RateTracker

	clients  map[string]client // client-id -> known client; refreshed every 30s
	lastScan time.Time

	warnNonNS sync.Once // one log line total for unsupported (non-ns) engine units
}

var _ collect.Collector = (*Collector)(nil)

// New constructs the GPU collector. lookup resolves a docker container id
// to its current name (Task 6's Collector.Lookup); a miss buckets the
// client as host.
func New(sink store.MetricSink, procRoot string, lookup func(string) (string, bool)) *Collector {
	return &Collector{
		sink:     sink,
		procRoot: procRoot,
		lookup:   lookup,
		rates:    collect.NewRateTracker(),
		clients:  make(map[string]client),
	}
}

func (c *Collector) Name() string            { return "gpu" }
func (c *Collector) Interval() time.Duration { return tickInterval }

// Probe is a cheap walkability check, not a full scan: a box with zero
// current DRM clients is still "available" — clients come and go — so
// the only failure worth reporting is not being able to list procRoot at
// all, which means the container is missing the fdinfo access model
// (--pid=host + --cap-add=SYS_PTRACE) proven by spike S1.
func (c *Collector) Probe(context.Context) collect.Status {
	if _, err := os.ReadDir(c.procRoot); err != nil {
		return collect.Status{Available: false, Detail: "mount /proc with --pid=host and --cap-add=SYS_PTRACE at " + c.procRoot}
	}
	return collect.Status{Available: true}
}

// Tick rescans for DRM clients every 30s (lastScan, tracked here) and
// re-reads every known client's fdinfo every 2s.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	if c.lastScan.IsZero() || now.Sub(c.lastScan) >= fullScanInterval {
		next := c.fullScan()
		c.evictGoneClients(next)
		c.clients = next
		c.lastScan = now
	}
	c.tickClients(now)
	return nil
}

// evictGoneClients diffs the outgoing client set (c.clients, about to be
// replaced) against the incoming full-scan result and evicts every gone
// client's RateTracker keys (clientID+"."-prefixed — see engineBusyPct).
// A client can also vanish via the 2s dead-read path in tickClients,
// which evicts the same way at the point it drops the client from the
// cache; this covers the other case, wholesale replacement at a 30s full
// scan, so RateTracker.prev doesn't grow by one entry per engine per
// client for the life of the process as DRM clients churn.
func (c *Collector) evictGoneClients(next map[string]client) {
	for id := range c.clients {
		if _, still := next[id]; !still {
			c.rates.EvictPrefix(id + ".")
		}
	}
}

// fullScan rediscovers every live DRM client and resolves its container
// attribution once, right here — the 2s tickClients re-reads never touch
// /proc/<pid>/cgroup again for a client already in the cache.
func (c *Collector) fullScan() map[string]client {
	found := scanClients(c.procRoot)
	for id, cl := range found {
		cl.Owner = resolveOwner(c.procRoot, cl.PID, c.lookup)
		found[id] = cl
	}
	return found
}

// tickClients re-reads every known client's current fdinfo, converts each
// drm-engine-* counter into a busy_pct via the shared RateTracker, and
// emits the per-container and per-GPU sums. A client whose fdinfo can no
// longer be read (process or fd gone) is dropped from the cache
// immediately; it returns, if still alive under a new fd, at the next
// full scan.
func (c *Collector) tickClients(now time.Time) {
	containerTotals := make(map[string]map[string]float64) // owner name -> engine -> summed busy_pct
	gpuTotals := make(map[string]map[string]float64)       // pdev (or "gpu0") -> engine -> summed busy_pct

	for id, cl := range c.clients {
		info, ok := readFDInfo(cl.FDPath)
		if !ok {
			delete(c.clients, id)
			c.rates.EvictPrefix(id + ".")
			continue
		}

		pdev := cl.Pdev
		if pdev == "" {
			pdev = "gpu0"
		}

		for key, val := range info.Fields {
			engine, isEngine := strings.CutPrefix(key, engineKeyPrefix)
			if !isEngine {
				continue
			}
			engine = collect.SlugSegment(engine)
			busyPct, ok := c.engineBusyPct(id, engine, val, now)
			if !ok {
				continue
			}
			if cl.Owner != "" {
				addTotal(containerTotals, cl.Owner, engine, busyPct)
			}
			addTotal(gpuTotals, pdev, engine, busyPct)
		}
	}

	ts := now.Unix()
	for owner, engines := range containerTotals {
		for engine, pct := range engines {
			c.sink.Record(store.SeriesKey{Kind: "container", Entity: owner, Metric: "gpu." + engine + ".busy_pct"}, ts, pct)
		}
	}
	for pdev, engines := range gpuTotals {
		for engine, pct := range engines {
			c.sink.Record(store.SeriesKey{Kind: "gpu", Entity: pdev, Metric: "engine." + engine + ".busy_pct"}, ts, pct)
		}
	}
}

// engineBusyPct converts one drm-engine-<name> field's raw value
// ("<n> ns") into a busy_pct via the RateTracker, keyed by clientID+engine
// — a fresh tracking series per client per engine, so a client just added
// to the cache correctly returns ok=false on its first reading rather
// than an inflated one-shot rate. Non-nanosecond units (the xe driver's
// cycle counters, per spikes.md deferred until seen in the wild) are not
// yet supported: skipped, with a single once-only log line rather than
// one per client per tick.
func (c *Collector) engineBusyPct(clientID, engine, raw string, now time.Time) (float64, bool) {
	numStr, unit, found := strings.Cut(raw, " ")
	if !found || unit != "ns" {
		c.warnNonNS.Do(func() {
			log.Printf("gpu: engine %q reports non-nanosecond units (%q), unsupported — skipping", engine, raw)
		})
		return 0, false
	}
	ns, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, false
	}
	nsPerSec, ok := c.rates.Rate(clientID+"."+engine, now, ns)
	if !ok {
		return 0, false
	}
	return nsPerSec / 1e7, true // (nsPerSec / 1e9 ns-per-second) * 100
}

// addTotal accumulates one busy_pct contribution into a nested
// key->engine->sum map, creating the inner map on first use.
func addTotal(m map[string]map[string]float64, key, engine string, val float64) {
	engines, ok := m[key]
	if !ok {
		engines = make(map[string]float64)
		m[key] = engines
	}
	engines[engine] += val
}

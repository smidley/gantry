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

// EntityMeta is one GPU entity's (pdev, or the "gpu0" fallback -- see
// tickClients' own doc) vendor + driver, for the frontend's card title
// (GPUStrip/GPUEntityCard, previously just the raw pdev address, e.g.
// "0000:00:02.0" -- Scott's own question: "what does this mean?").
// Vendor comes from sysRoot's own PCI vendor file (vendorNameForPdev);
// Driver is already known per DRM client (client.Driver, from fdinfo's
// own drm-driver line) -- this just carries it one level up, from
// per-client to per-entity.
type EntityMeta struct {
	Vendor string
	Driver string
}

// Collector discovers DRM (GPU) clients via /proc/<pid>/fdinfo and turns
// their cumulative per-engine busy-time counters into per-container and
// per-GPU busy_pct series (spec §4.4, spike S1). Name "gpu", Interval 2s.
type Collector struct {
	sink     store.MetricSink
	procRoot string
	lookup   func(id string) (name string, ok bool)
	rates    *collect.RateTracker

	// SysRoot is where the host's /sys is mounted (default "/host/sys",
	// matching every other collector's own convention -- see main.go's
	// wiring) -- read for each newly-discovered entity's own PCI vendor
	// file (see fullScan's own doc). Same "public field, overridden by
	// main wiring after New" pattern as docker.Collector's CgroupRoot.
	SysRoot string

	clients  map[string]client // client-id -> known client; refreshed every 30s
	lastScan time.Time

	mu         sync.Mutex
	entityMeta map[string]EntityMeta // guarded by mu; set on fullScan, read via GPUMeta()

	warnNonNS sync.Once // one log line total for unsupported (non-ns) engine units
}

var _ collect.Collector = (*Collector)(nil)

// New constructs the GPU collector. lookup resolves a docker container id
// to its current name (Task 6's Collector.Lookup); a miss buckets the
// client as host.
func New(sink store.MetricSink, procRoot string, lookup func(string) (string, bool)) *Collector {
	return &Collector{
		sink:       sink,
		procRoot:   procRoot,
		lookup:     lookup,
		rates:      collect.NewRateTracker(),
		clients:    make(map[string]client),
		SysRoot:    "/host/sys",
		entityMeta: make(map[string]EntityMeta),
	}
}

// GPUMeta returns a snapshot copy of every currently-known GPU entity's
// vendor+driver, keyed the same way GPU-kind series entities are (pdev,
// or "gpu0") -- a copy, not the live map, so a concurrent snapshot-
// building caller can range over the result without contending with (or
// racing) the next tick's own writes, same convention as unraid.
// Collector.DiskMeta(). Once seen, an entity's meta is remembered for
// the collector's whole lifetime (hardware identity doesn't change),
// even through a window where every one of its clients has since gone
// idle/exited.
func (c *Collector) GPUMeta() map[string]EntityMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]EntityMeta, len(c.entityMeta))
	for pdev, m := range c.entityMeta {
		out[pdev] = m
	}
	return out
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
// /proc/<pid>/cgroup again for a client already in the cache. Also
// resolves each newly-seen ENTITY's own vendor+driver into c.entityMeta
// (GPUMeta's own doc) -- a one-time sysfs read per pdev (vendorNameForPdev
// itself is cheap, but there's no reason to re-read a device's own
// hardware identity every 30s once it's known), guarded by mu since
// GPUMeta() can be called concurrently from the snapshot-building
// goroutine.
func (c *Collector) fullScan() map[string]client {
	found := scanClients(c.procRoot)
	for id, cl := range found {
		cl.Owner = resolveOwner(c.procRoot, cl.PID, c.lookup)
		found[id] = cl
		c.noteEntityMeta(cl)
	}
	return found
}

// noteEntityMeta records cl's own entity (pdev, or the "gpu0" fallback --
// see tickClients' own doc for why that fallback exists) into
// c.entityMeta the first time it's seen; a pdev already known is left
// alone rather than re-resolved, per fullScan's own doc.
func (c *Collector) noteEntityMeta(cl client) {
	pdev := cl.Pdev
	if pdev == "" {
		pdev = "gpu0"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, known := c.entityMeta[pdev]; known {
		return
	}
	c.entityMeta[pdev] = EntityMeta{Vendor: vendorNameForPdev(c.SysRoot, pdev), Driver: cl.Driver}
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
			if strings.HasPrefix(engine, "capacity-") {
				// xe reports drm-engine-capacity-<name> (engine instance
				// counts) alongside the real drm-engine-<name> busy-time
				// counter for the same engine. It's never in nanoseconds,
				// so without this it would reach engineBusyPct and burn
				// the one-shot non-ns warning on this frequent,
				// uninteresting shape instead of a genuinely novel one.
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
			c.sink.Record(store.SeriesKey{Kind: "container", Entity: owner, Metric: "gpu." + engine + ".busy_pct"}, ts, clampPct(pct))
		}
	}
	for pdev, engines := range gpuTotals {
		for engine, pct := range engines {
			c.sink.Record(store.SeriesKey{Kind: "gpu", Entity: pdev, Metric: "engine." + engine + ".busy_pct"}, ts, clampPct(pct))
		}
	}
}

// clampPct bounds a busy_pct value to [0,100] at emission: engineBusyPct's
// rate-derived value (and container/GPU sums of it across multiple
// engines or clients) can overshoot 100 on real hardware -- a ~100.001%
// float overshoot has been observed live, and the summed-across-clients
// GPU total can exceed 100 legitimately in raw form when several
// containers share one engine -- so both callers clamp at the point of
// emission rather than trusting the raw computation.
func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
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

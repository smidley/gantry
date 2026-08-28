package unraid

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const tickInterval = 15 * time.Second

// EventSink is the narrow slice of store.Store the unraid collector needs
// to append array/disk events — defined locally the same way the docker
// package defines its own EventSink.
type EventSink interface {
	AppendEvent(store.Event) (int64, error)
}

// Collector reads Unraid's emhttp ini files from dir (normally
// "/var/local/emhttp", mounted read-only) and the host's /proc (procRoot)
// for the mover's PID. Name "unraid", Interval 15s.
type Collector struct {
	sink     store.MetricSink
	events   EventSink
	dir      string
	procRoot string

	mu        sync.Mutex
	version   string              // guarded by mu; set on tickArray, read via Version()
	poolSlots []string            // guarded by mu; set on tickDisks, read via Slots()
	diskMeta  map[string]DiskMeta // guarded by mu; set on tickDisks, read via DiskMeta()

	havePrevArray bool
	prevArray     ArrayState

	prevDiskErrors map[string]float64 // slot -> last-seen numErrors, for increment detection
}

var _ collect.Collector = (*Collector)(nil)

// New constructs the unraid collector.
func New(sink store.MetricSink, events EventSink, dir, procRoot string) *Collector {
	return &Collector{
		sink: sink, events: events, dir: dir, procRoot: procRoot,
		prevDiskErrors: make(map[string]float64),
		diskMeta:       make(map[string]DiskMeta),
	}
}

func (c *Collector) Name() string            { return "unraid" }
func (c *Collector) Interval() time.Duration { return tickInterval }

func (c *Collector) Probe(context.Context) collect.Status {
	if _, err := os.Stat(filepath.Join(c.dir, "var.ini")); err != nil {
		return collect.Status{Available: false, Detail: "mount /var/local/emhttp read-only at " + c.dir}
	}
	return collect.Status{Available: true}
}

// Version returns the most recently observed Unraid version string, or
// "" before the first tick. Safe for concurrent callers (the snapshot
// API reads this from a different goroutine than the one ticking).
func (c *Collector) Version() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// Slots returns the most recently observed pool slot names -- disks.
// ini's per-slot "type" field is the source of truth ("Cache" -> pool;
// every other type, "Data" included, is not) -- or nil before disks.ini
// has ever been read successfully. Safe for concurrent callers, same
// convention as Version(); the storage-panel path resolver
// (storagepath.go) uses this to recognize a mount source under a
// custom-named cache pool.
func (c *Collector) Slots() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.poolSlots
}

// DiskMeta returns a snapshot copy of every present disk's device name
// and classified type, keyed by slot — a copy (not the live map) so a
// concurrent snapshot-building caller can range over the result without
// contending with (or racing) the next tick's own writes.
func (c *Collector) DiskMeta() map[string]DiskMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]DiskMeta, len(c.diskMeta))
	for slot, m := range c.diskMeta {
		out[slot] = m
	}
	return out
}

// Tick's only hard requirement is var.ini, matching Probe's contract
// (the same convention host.go uses for /proc/stat): every other file
// this collector reads — disks.ini, shares.ini, the mover's /proc entry,
// ups.ini — degrades independently and silently when missing.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	if err := c.tickArray(now); err != nil {
		return err
	}
	c.tickDisks(now)
	c.tickShares(now)
	c.tickMover(now)
	c.tickUPS(now)
	return nil
}

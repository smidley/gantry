// Package docker collects container inventory, lifecycle events, and
// per-container stats from the local docker daemon: client + registry +
// event stream here, cgroup v2 fast-path stats in cgroupv2.go, the docker
// stats API fallback in apistats.go, and per-container network in net.go.
package docker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const (
	tickInterval      = 2 * time.Second
	inventoryInterval = 10 * time.Second
	eventBackoffBase  = time.Second
	eventBackoffCap   = 60 * time.Second
)

// Collector polls the docker daemon for container inventory, translates
// lifecycle events, and (via cgroupv2.go/apistats.go/net.go) records
// per-container stats. Name "docker", Interval 2s.
//
// CgroupRoot, ProcRoot, MemTotal, and DeviceName are injected dependencies
// with safe production defaults (set here in New); wiring code overrides
// MemTotal/DeviceName with the host collector's own methods so mem.pct and
// per-device io attribution work. Until overridden they degrade silently
// (mem.pct skipped, no device named) rather than error.
type Collector struct {
	cli      *client.Client
	sink     store.MetricSink
	events   EventSink
	evict    func(kind, entity string)
	sockPath string

	reg   *registry
	rates *collect.RateTracker

	CgroupRoot string
	ProcRoot   string
	MemTotal   func() uint64
	DeviceName func(majMin string) (string, bool)

	lastInventory time.Time
	eventsOnce    sync.Once

	loggedFallback sync.Map // container id -> struct{}: log the API-stats fallback once per container
}

var _ collect.Collector = (*Collector)(nil)

// New constructs the docker collector. sockPath is the docker socket path
// (e.g. "/var/run/docker.sock"); the client is API-version-negotiated
// against whatever daemon is listening there.
func New(sink store.MetricSink, events EventSink, evict func(kind, entity string), sockPath string) *Collector {
	cli, err := client.NewClientWithOpts(client.WithHost("unix://"+sockPath), client.WithAPIVersionNegotiation())
	if err != nil {
		log.Printf("docker: client init: %v", err)
	}
	return &Collector{
		cli: cli, sink: sink, events: events, evict: evict, sockPath: sockPath,
		reg:   newRegistry(),
		rates: collect.NewRateTracker(),

		CgroupRoot: "/host/sys/fs/cgroup",
		ProcRoot:   "/proc",
		MemTotal:   func() uint64 { return 0 },
		DeviceName: func(string) (string, bool) { return "", false },
	}
}

func (c *Collector) Name() string            { return "docker" }
func (c *Collector) Interval() time.Duration { return tickInterval }

func (c *Collector) Probe(ctx context.Context) collect.Status {
	if c.cli == nil {
		return collect.Status{Available: false, Detail: "docker client: invalid socket path " + c.sockPath}
	}
	if _, err := c.cli.Ping(ctx); err != nil {
		return collect.Status{Available: false, Detail: "mount the docker socket read-only at " + c.sockPath}
	}
	return collect.Status{Available: true}
}

// Lookup returns the current Meta for a container id (for GPU attribution
// in Task 9).
func (c *Collector) Lookup(containerID string) (Meta, bool) { return c.reg.lookup(containerID) }

// Running returns a name-sorted snapshot of every currently-running
// container's Meta.
func (c *Collector) Running() []Meta { return c.reg.running() }

// Tick starts the event stream on its first call (lazily, so it runs for
// the lifetime of ctx — the collector runner's ctx, not a per-Tick one),
// refreshes inventory every 10s, then records per-container stats
// (cgroupv2.go/apistats.go). Network (net.go) hooks in during Task 8.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	c.eventsOnce.Do(func() { go c.runEvents(ctx) })

	if c.lastInventory.IsZero() || now.Sub(c.lastInventory) >= inventoryInterval {
		if err := c.refreshInventory(ctx); err != nil {
			return err
		}
		c.lastInventory = now
	}

	c.tickStats(ctx, now)
	return nil
}

// refreshInventory polls ContainerList(All) + ContainerInspect per
// container and replaces the registry's contents. A container that fails
// to inspect (removed mid-poll) is simply dropped from this snapshot;
// the next refresh (or the destroy event) settles it.
func (c *Collector) refreshInventory(ctx context.Context) error {
	summaries, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	metas := make([]Meta, 0, len(summaries))
	for _, s := range summaries {
		resp, err := c.cli.ContainerInspect(ctx, s.ID)
		if err != nil {
			continue
		}
		metas = append(metas, metaFromInspect(resp))
	}
	c.reg.applyInventory(metas, c.events, c.evict)
	return nil
}

func metaFromInspect(resp container.InspectResponse) Meta {
	m := Meta{
		ID:           resp.ID,
		Name:         normalizeName(resp.Name),
		RestartCount: resp.RestartCount,
	}
	if resp.Config != nil {
		m.Image = resp.Config.Image
	}
	if resp.HostConfig != nil {
		m.HostNet = resp.HostConfig.NetworkMode.IsHost()
	}
	if resp.State != nil {
		m.State = resp.State.Status
		m.Pid = resp.State.Pid
		if resp.State.Health != nil {
			m.Health = resp.State.Health.Status
		}
		if t, err := time.Parse(time.RFC3339Nano, resp.State.StartedAt); err == nil {
			m.StartedAt = t
		}
	}
	return m
}

// runEvents streams docker events for the collector's run lifetime,
// restarting the stream with exponential backoff (1s doubling, cap 60s)
// on any stream error (including the SDK's own EOF-on-clean-close, which
// arrives on the same channel) and exiting when ctx is done. Backoff
// resets to its base once a stream delivers at least one event, so a
// daemon that's been stable doesn't inherit a stale, longer wait from an
// earlier flaky period.
func (c *Collector) runEvents(ctx context.Context) {
	backoff := eventBackoffBase
	for ctx.Err() == nil {
		msgs, errs := c.cli.Events(ctx, events.ListOptions{})
		if c.consumeEvents(ctx, msgs, errs) {
			backoff = eventBackoffBase
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > eventBackoffCap {
			backoff = eventBackoffCap
		}
	}
}

// consumeEvents reads one stream to exhaustion (ctx done, channel closed,
// or an error/EOF on errs) and reports whether it translated at least one
// event before ending.
func (c *Collector) consumeEvents(ctx context.Context, msgs <-chan events.Message, errs <-chan error) (progressed bool) {
	for {
		select {
		case <-ctx.Done():
			return progressed
		case msg, ok := <-msgs:
			if !ok {
				return progressed
			}
			c.reg.applyEvent(msg, c.events, c.evict)
			progressed = true
		case err, ok := <-errs:
			if ok && err != nil {
				log.Printf("docker: event stream: %v", err)
			}
			return progressed
		}
	}
}

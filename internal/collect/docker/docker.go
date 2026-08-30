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
	"github.com/docker/docker/api/types/mount"
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
// CgroupRoot, ProcRoot, MemTotal, HostCores, DeviceName, and
// UpdateStatuses are injected dependencies with safe production defaults
// (set here in New); wiring code overrides MemTotal/HostCores/DeviceName
// with the host collector's own methods so mem.pct, cpu.pct's host-share
// conversion, and per-device io attribution all work, and UpdateStatuses
// with an unraid.UpdateStatusReader's Statuses method so Meta.
// UpdateStatus can join against it. Until overridden they degrade
// silently (mem.pct skipped, no device named, every UpdateStatus "")
// rather than error; cpu.pct is the exception -- it falls back to
// runtime.NumCPU() rather than go blank (see recordContainerStats' own
// doc in cgroupv2.go).
type Collector struct {
	cli      *client.Client
	imgCli   imagesClient     // same value as cli, narrowed -- see imagesClient's own doc
	ctrCli   containersClient // same value as cli, narrowed -- see containersClient's own doc
	sink     store.MetricSink
	events   EventSink
	evict    func(kind, entity string)
	sockPath string

	reg   *registry
	rates *collect.RateTracker

	CgroupRoot     string
	ProcRoot       string
	MemTotal       func() uint64
	HostCores      func() int
	DeviceName     func(majMin string) (string, bool)
	UpdateStatuses func() map[string]string

	lastInventory time.Time
	eventsOnce    sync.Once
	eventsWG      sync.WaitGroup // tracks the runEvents goroutine so Drain can join it at shutdown

	loggedFallback sync.Map // container name -> struct{}: log the API-stats fallback once per container
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
	c := &Collector{
		cli: cli, sink: sink, events: events, evict: evict, sockPath: sockPath,
		reg:   newRegistry(),
		rates: collect.NewRateTracker(),

		CgroupRoot:     "/host/sys/fs/cgroup",
		ProcRoot:       "/proc",
		MemTotal:       func() uint64 { return 0 },
		HostCores:      func() int { return 0 },
		DeviceName:     func(string) (string, bool) { return "", false },
		UpdateStatuses: func() map[string]string { return nil },
	}
	if cli != nil {
		// Not just "imgCli: cli"/"ctrCli: cli" above: NewClientWithOpts can
		// return a nil *client.Client alongside a non-nil err, and
		// assigning a nil concrete pointer into an interface field
		// produces a non-nil interface (it still carries the pointer's
		// type) -- imgCli/ctrCli's own nil checks in Images/RemoveImages/
		// ContainersMaintenance would never trip.
		c.imgCli = cli
		c.ctrCli = cli
	}
	return c
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
	// The SDK's lazy version negotiation is not goroutine-safe; settle it here
	// before the stream goroutine exists (startEvents, below), rather than
	// leaving it to race the first concurrent API calls that need it.
	c.cli.NegotiateAPIVersion(ctx)
	c.startEvents(ctx)
	return collect.Status{Available: true}
}

// startEvents launches the event-stream goroutine at most once (guarded
// by eventsOnce), anchoring it to ctx for its whole lifetime. It's called
// from Probe rather than Tick, and only once Ping has confirmed the
// daemon is actually reachable: the runner's safeTick bounds every Tick
// call to a short per-call deadline (a few multiples of Interval), which
// would be wrong to also use as this goroutine's lifetime — it would get
// cancelled moments after the first Tick returns. Probe's ctx is the
// collector runner's real run-lifetime one (safeTick only wraps the Tick
// call, not Probe), so this is the correct anchor.
func (c *Collector) startEvents(ctx context.Context) {
	c.eventsOnce.Do(func() {
		c.eventsWG.Add(1)
		go func() {
			defer c.eventsWG.Done()
			c.runEvents(ctx)
		}()
	})
}

// Drain blocks until the event-stream goroutine (started by the first
// successful Probe, if any) has exited. Callers should invoke it after
// cancelling the collector's run ctx and joining the registry's own
// WaitGroup, so the docker event stream is guaranteed to have stopped
// touching the registry/sink before shutdown finishes. If the event
// stream never started (docker was never reachable), eventsWG's counter
// is still 0 and this returns immediately.
func (c *Collector) Drain() {
	c.eventsWG.Wait()
}

// Lookup returns the current Meta for a container id (for GPU attribution
// in Task 9).
func (c *Collector) Lookup(containerID string) (Meta, bool) { return c.reg.lookup(containerID) }

// LookupByName returns the current Meta for a container name, regardless
// of state. Used by main's snapshot filter (Task 4) to tell a briefly-
// stale-but-real container's lingering live sample apart from one whose
// container has been fully removed.
func (c *Collector) LookupByName(name string) (Meta, bool) { return c.reg.lookupByName(name) }

// evictContainer is the registry's removal callback (wired in place of a
// bare evict at both call sites below): it clears every trace of a
// removed container, not just its store rings. name+"." is the shared
// rate-key prefix convention cgroupv2.go/net.go already use for every
// per-container counter, so RateTracker.EvictPrefix cleans those up the
// same way Live.Evict cleans up the rings; loggedFallback (also keyed by
// name) gets pruned too. Without this, both maps would otherwise grow by
// one entry per container for the life of the process as containers
// recreate and get removed.
func (c *Collector) evictContainer(kind, name string) {
	c.evict(kind, name)
	c.rates.EvictPrefix(name + ".")
	c.loggedFallback.Delete(name)
}

// Running returns a name-sorted snapshot of every currently-running
// container's Meta.
func (c *Collector) Running() []Meta { return c.reg.running() }

// All returns a name-sorted snapshot of every known container's Meta,
// running or not -- the alert engine's Fleet source (Task 4's boot
// seeding needs to see a stopped container carrying a stale Health, not
// just the running set Running() already serves everything else).
func (c *Collector) All() []Meta { return c.reg.all() }

// Tick refreshes inventory every 10s, then records per-container stats
// (cgroupv2.go, falling back to apistats.go) and network (net.go). The
// event stream itself is started lazily from Probe, not here — see
// startEvents — since Tick's own ctx parameter is a short-lived, per-call
// one (safeTick) unsuitable for anchoring a goroutine meant to outlive
// any single call.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	if c.lastInventory.IsZero() || now.Sub(c.lastInventory) >= inventoryInterval {
		if err := c.refreshInventory(ctx, now); err != nil {
			return err
		}
		c.lastInventory = now
	}

	c.tickStats(ctx, now)
	c.tickNet(now)
	return nil
}

// refreshInventory polls ContainerList(All) + ContainerInspect per
// container and replaces the registry's contents. A container that fails
// to inspect (removed mid-poll) is simply dropped from this snapshot;
// the next refresh (or the destroy event) settles it.
func (c *Collector) refreshInventory(ctx context.Context, now time.Time) error {
	summaries, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	// Read once per inventory poll, not once per container: the update-
	// status reader's own file is small and cheap, but Meta itself (and
	// hence UpdateStatus, which travels with it) is only ever this fresh
	// anyway, so re-reading it per container would buy no fresher data.
	statuses := c.UpdateStatuses()

	metas := make([]Meta, 0, len(summaries))
	for _, s := range summaries {
		resp, err := c.cli.ContainerInspect(ctx, s.ID)
		if err != nil {
			continue
		}
		metas = append(metas, metaFromInspect(resp, statuses))
	}
	c.reg.applyInventory(metas, c.events, c.evictContainer)
	c.recordMeta(metas, now)
	return nil
}

// recordMeta emits meta.started_at/meta.restart_count for every currently
// running container -- ContainerDTO carries no StartedAt/RestartCount
// fields of its own (Sample.Val is float64-only; see store.MetricSink),
// so both flow through as ordinary per-container metrics that land in
// ContainerDTO.Metrics via buildSnapshot's existing generic per-sample
// grouping, the same path cgroupv2.go's cpu.pct/mem.bytes already use --
// no DTO or main.go change needed for the UI's uptime/restart-count
// display to have real data.
//
// Deliberately restricted to State=="running": metas here includes every
// container ContainerList(All) returns, including long-exited ones the
// registry still remembers (see lookupByName's doc) -- recording a fresh
// sample for one of those every 10s inventory poll would keep resetting
// its sampleAge in buildSnapshot's stopped-container filter, which would
// never let it age out of the live frame the way an exited container's
// naturally-aging cgroup stats already do today.
func (c *Collector) recordMeta(metas []Meta, now time.Time) {
	ts := now.Unix()
	for _, m := range metas {
		if m.State != "running" {
			continue
		}
		if !m.StartedAt.IsZero() {
			c.sink.Record(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "meta.started_at"}, ts, float64(m.StartedAt.Unix()))
		}
		c.sink.Record(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "meta.restart_count"}, ts, float64(m.RestartCount))
	}
}

// unraidIconLabel is the docker label Unraid's Community Applications
// sets on every template it installs, naming that container's icon URL
// (a LAN/remote image, not something CA ships as a docker layer). A
// container created some other way carries no such label, so a nil-map
// read of it (Labels is nil, not just missing the key) already yields
// the correct "" via Go's zero-value map-read rule -- Meta.Icon needs no
// separate absence check.
const unraidIconLabel = "net.unraid.docker.icon"

// unraidWebUILabel is Community Applications' WebUI-launch-button label.
// Its value is a template placeholder, not a real URL -- e.g.
// "http://[IP]:[PORT:8096]/" -- left completely unresolved here; only
// the browser making the request knows the host to substitute for
// [IP], so that substitution happens client-side (see ContainerDTO.
// WebUIURL's own doc for the exact contract the frontend implements).
const unraidWebUILabel = "net.unraid.docker.webui"

// metaFromInspect builds one container's Meta from its inspect
// response plus updateStatuses, the unraid-update-status.json reader's
// current image->status snapshot (nil when no reader is wired, or the
// file was unreadable this poll -- see joinUpdateStatus). Everything
// else is derived purely from resp itself: labels (Icon, WebUIURL,
// ChangelogURL/ProjectURL via changelog.go), Mounts (mountsFromInspect),
// and NetworkSettings (Networks/Ports via netinfo.go).
func metaFromInspect(resp container.InspectResponse, updateStatuses map[string]string) Meta {
	m := Meta{
		ID:           resp.ID,
		Name:         normalizeName(resp.Name),
		RestartCount: resp.RestartCount,
	}
	if t, err := time.Parse(time.RFC3339Nano, resp.Created); err == nil {
		m.Created = t
	}
	if resp.Config != nil {
		m.Image = resp.Config.Image
		m.Icon = resp.Config.Labels[unraidIconLabel]
		m.WebUIURL = resp.Config.Labels[unraidWebUILabel]
		m.ChangelogURL, m.ProjectURL = changelogAndProjectURLs(resp.Config.Labels, m.Image)
		m.UpdateStatus = joinUpdateStatus(m.Image, updateStatuses)
	}
	if resp.HostConfig != nil {
		m.HostNet = resp.HostConfig.NetworkMode.IsHost()
		m.Alloc = allocFromHostConfig(resp.HostConfig.Resources)
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
	m.Mounts = mountsFromInspect(resp.Mounts)
	m.Networks = extractNetworks(m.HostNet, resp.NetworkSettings)
	m.Ports = extractPorts(resp.NetworkSettings)
	return m
}

// defaultCPUPeriodUsec is runc's own default cpu.max period (100ms),
// used below when a caller sets --cpu-quota without --cpu-period:
// dockerd leaves HostConfig.CPUPeriod at 0 in that case rather than
// filling in the default itself, so allocFromHostConfig has to.
const defaultCPUPeriodUsec = 100_000

// allocFromHostConfig maps one docker inspect response's HostConfig
// resource fields into the same alloc shape the cgroup v2 fast path
// produces from memory.max/cpu.max/cpuset.cpus.effective/pids.max
// (cgroupv2.go) -- the API fallback's HostConfig-side source of
// allocation data (of the ceilings, only PidsStats.Limit rides along in
// the stats response; see fallbackAlloc). NanoCPUs takes priority over CPUQuota/CPUPeriod when a
// caller (unusually) sets both, matching the docker CLI's own --cpus
// flag, which compiles down to NanoCPUs in the first place. PidsLimit
// follows docker's own convention for that field (HostConfig's doc
// comment): 0 or -1 both mean unlimited, not "a limit of that value".
func allocFromHostConfig(r container.Resources) alloc {
	var a alloc
	if r.Memory > 0 {
		a.MemLimitBytes, a.HasMemLimit = uint64(r.Memory), true
	}
	switch {
	case r.NanoCPUs > 0:
		a.CPUQuotaCores, a.HasCPUQuota = float64(r.NanoCPUs)/1e9, true
	case r.CPUQuota > 0:
		period := r.CPUPeriod
		if period <= 0 {
			period = defaultCPUPeriodUsec
		}
		a.CPUQuotaCores, a.HasCPUQuota = float64(r.CPUQuota)/float64(period), true
	}
	if n, ok := parseCPUSetCount(r.CpusetCpus); ok {
		a.CPUSetCores, a.HasCPUSet = n, true
	}
	if r.PidsLimit != nil && *r.PidsLimit > 0 {
		a.PidsLimit, a.HasPidsLimit = uint64(*r.PidsLimit), true
	}
	return a
}

// mountsFromInspect filters an inspect response's Mounts down to bind and
// volume types -- the only two whose Source is a real host-side path
// (tmpfs's is contractually empty; npipe/cluster/image aren't meaningful
// on an Unraid/Linux host) -- and copies each into a MountInfo, in the
// same order docker reported them. Returns nil, not an empty slice, when
// nothing qualifies (including the empty/nil-Mounts case), matching
// every other zero-value Meta field's convention.
func mountsFromInspect(mps []container.MountPoint) []MountInfo {
	var out []MountInfo
	for _, mp := range mps {
		if mp.Type != mount.TypeBind && mp.Type != mount.TypeVolume {
			continue
		}
		out = append(out, MountInfo{Source: mp.Source, Destination: mp.Destination, RW: mp.RW})
	}
	return out
}

// runEvents streams docker events for the collector's run lifetime (see
// startEvents for how ctx is anchored), restarting the stream with
// exponential backoff (1s doubling, cap 60s) on any stream error
// (including the SDK's own EOF-on-clean-close, which arrives on the same
// channel) and exiting when ctx is done. Backoff resets to its base once
// a stream delivers at least one event, so a daemon that's been stable
// doesn't inherit a stale, longer wait from an earlier flaky period.
// Each pass through this loop is recovered (streamOnce) so a panic
// anywhere in the stream/translate path can't kill this long-lived
// background goroutine and take the whole process down with it (spec
// §9) — it's counted the same as a stream that errored before
// delivering anything, and retried on the same backoff schedule.
func (c *Collector) runEvents(ctx context.Context) {
	backoff := eventBackoffBase
	for ctx.Err() == nil {
		if c.streamOnce(ctx) {
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

// streamOnce opens one docker event stream and consumes it to exhaustion
// (consumeEvents), recovering any panic anywhere in that path — the SDK
// call itself, or a bug in consumeEvents' own loop. consumeEvents already
// recovers a panic in any one event's handling (applyEventRecovered), so
// this is the last-resort net, not the primary one. A recovered panic
// reports no progress, same as a stream that errored before delivering
// anything.
func (c *Collector) streamOnce(ctx context.Context) (progressed bool) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("docker: event stream: recovered from panic: %v", p)
			progressed = false
		}
	}()
	msgs, errs := c.cli.Events(ctx, events.ListOptions{})
	return c.consumeEvents(ctx, msgs, errs)
}

// consumeEvents reads one stream to exhaustion (ctx done, channel closed,
// or an error/EOF on errs) and reports whether it translated at least one
// event before ending. Each event's handling is individually recovered
// (applyEventRecovered) so one malformed event or downstream sink bug
// can't tear down the whole connection — the stream keeps consuming
// whatever arrives after it, rather than forcing a full reconnect.
func (c *Collector) consumeEvents(ctx context.Context, msgs <-chan events.Message, errs <-chan error) (progressed bool) {
	for {
		select {
		case <-ctx.Done():
			return progressed
		case msg, ok := <-msgs:
			if !ok {
				return progressed
			}
			c.applyEventRecovered(msg)
			progressed = true
		case err, ok := <-errs:
			if ok && err != nil {
				log.Printf("docker: event stream: %v", err)
			}
			return progressed
		}
	}
}

// applyEventRecovered applies one docker event to the registry,
// recovering any panic from that path (a malformed event, or a
// downstream sink bug) so it can't kill the event-stream goroutine — an
// unrecovered panic there would crash the whole process (spec §9).
func (c *Collector) applyEventRecovered(msg events.Message) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("docker: event stream: recovered from panic handling event: %v", p)
		}
	}()
	c.reg.applyEvent(msg, c.events, c.evictContainer)
}

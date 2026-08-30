// Command gantry is the Gantry monitor: collectors, storage, and web UI
// in one binary. See docs/superpowers/specs/ for the design.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/collect/gpu"
	"github.com/smidley/gantry/internal/collect/host"
	"github.com/smidley/gantry/internal/collect/pressure"
	"github.com/smidley/gantry/internal/collect/selfstat"
	"github.com/smidley/gantry/internal/collect/unraid"
	"github.com/smidley/gantry/internal/config"
	"github.com/smidley/gantry/internal/fake"
	"github.com/smidley/gantry/internal/server"
	"github.com/smidley/gantry/internal/store"
)

var version = "dev" // set via -ldflags at build

func main() {
	hc := flag.Bool("healthcheck", false, "probe the local healthz endpoint and exit")
	flag.Parse()

	if *hc {
		if err := healthcheck(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, version); err != nil {
		log.Fatal(err)
	}
}

// envOnly resolves keys that must work before the store exists.
func envOnly(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, getenv func(string) string, ver string) error {
	dbPath := envOnly(getenv, "GANTRY_DB_PATH", "/config/gantry.db")

	st, err := store.Open(dbPath, nil)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Println("store close:", cerr)
		}
	}()

	// Seeded before anything else touches alert_rules: an id already
	// present (a prior boot's seed, possibly since edited or disabled)
	// is left untouched; only an id genuinely absent -- first boot, or a
	// default introduced by a later upgrade -- gets inserted. There is
	// no alert engine yet to gate this on (Task 4); "before the engine's
	// first tick" is trivially satisfied by seeding at boot.
	if err := st.SeedAlertRules(store.DefaultAlertRules()); err != nil {
		return fmt.Errorf("seed alert rules: %w", err)
	}
	// GANTRY_WEBHOOK_URL (spec Sec5's documented single-webhook path) is
	// re-synced into the "env" target on every boot, same "before
	// anything else touches it" posture as the rule seed just above.
	// webhookURLEnv is captured once here and reused below for
	// webhooksAdapter's envLocked flag (Task 8): whether the var was set
	// AT BOOT is what actually governs the "env" target's current
	// stored values, since seedWebhookTargetFromEnv only resyncs on
	// boot, not on every request.
	webhookURLEnv := getenv("GANTRY_WEBHOOK_URL")
	if err := seedWebhookTargetFromEnv(st, webhookURLEnv); err != nil {
		return fmt.Errorf("seed webhook target: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg := config.New(st, getenv)
	port := cfg.Int("port", 8380)
	// readOnly is Gantry's write-path kill switch (GANTRY_READ_ONLY=1,
	// resolved through the same env>settings>default precedence every
	// other cfg.Bool call uses): every /api/images mutating route 403s
	// while it's set, GET unaffected -- see server.Options.ReadOnly.
	readOnly := cfg.Bool("read_only", false)

	var wg sync.WaitGroup

	// fakeMetas/fakeDiskMeta, when fake-data mode is on, are threaded into
	// buildSnapshot/buildContainersList below so the fake fleet is
	// treated exactly like dc.Running()'s real entries (Task 11's
	// ledger-carried fix -- see fake.Generator.Metas' own doc for why
	// that's required at all: this generator writes samples straight to
	// the store, never touching dc's registry, and disks.go's own
	// unraid.Collector similarly never sees a real disks.ini in fake
	// mode). fakeDiskMeta mirrors fakeMetas' shape for disk device/type
	// metadata (see fake.Generator.DiskMetas' own doc). fk itself (nil
	// outside fake-data mode) is kept for the image-inventory wiring
	// below, once dc exists to default against.
	var fakeMetas func() []docker.Meta
	var fakeDiskMeta func() map[string]unraid.DiskMeta
	var fk *fake.Generator
	if cfg.Bool("fake_data", false) {
		log.Println("fake data mode: synthesizing a demo fleet")
		fk = fake.New(st, st, time.Now().UnixNano())
		fakeMetas = fk.Metas
		fakeDiskMeta = fk.DiskMetas
		wg.Add(1)
		go func() {
			defer wg.Done()
			fk.Run(runCtx, 2*time.Second, nil)
		}()
	}

	// Collectors are always registered, fake-data mode or not — each one's
	// own Probe decides availability, so a dev box with no docker socket,
	// no /proc, or no Nvidia GPU just reports "unavailable" with a hint
	// (surfaced via healthz sources) rather than erroring. Wiring adapters
	// (Lookup->name, MemTotal, HostCores, DeviceName, Running) live here rather than
	// in any collector package, keeping the collectors mutually decoupled.
	sysRoot := envOnly(getenv, "GANTRY_HOST_SYS", "/host/sys")
	dockerSock := envOnly(getenv, "GANTRY_DOCKER_SOCK", "/var/run/docker.sock")
	cgroupRoot := sysRoot + "/fs/cgroup"

	host := host.New(st, "/proc", sysRoot)

	dc := docker.New(st, st, st.Live().Evict, dockerSock)
	wireDockerCollector(dc, host, cgroupRoot)
	usr := unraid.NewUpdateStatusReader(envOnly(getenv, "GANTRY_UPDATE_STATUS_PATH", "/updates/unraid-update-status.json"))
	dc.UpdateStatuses = usr.Statuses

	// imagesSrc/removeImagesSrc/pruneImagesSrc default to the real
	// docker collector and switch entirely to fk's synthetic inventory
	// in fake-data mode (unlike fakeMetas/fakeDiskMeta above, this is an
	// exclusive swap, not a merge -- fake-data mode's dev box has no
	// real docker daemon for dc's own methods to mean anything against).
	imagesSrc := dc.Images
	removeImagesSrc := dc.RemoveImages
	pruneImagesSrc := dc.PruneImages
	if fk != nil {
		imagesSrc = fk.Images
		removeImagesSrc = fk.RemoveImages
		pruneImagesSrc = fk.PruneImages
	}

	// containersMaintenanceSrc/removeContainersSrc/pruneContainersSrc are
	// the container-maintenance analogue of imagesSrc/removeImagesSrc/
	// pruneImagesSrc immediately above -- same exclusive real/fake swap,
	// same reasoning.
	containersMaintenanceSrc := dc.ContainersMaintenance
	removeContainersSrc := dc.RemoveContainers
	pruneContainersSrc := dc.PruneContainers
	if fk != nil {
		containersMaintenanceSrc = fk.ContainersMaintenance
		removeContainersSrc = fk.RemoveContainers
		pruneContainersSrc = fk.PruneContainers
	}

	// gpuLookup adapts docker's Meta-returning Lookup to the name-only
	// signature both GPU collectors (DRM fdinfo and nvidia-smi) need for
	// pid->container attribution.
	gpuLookup := func(id string) (string, bool) {
		m, ok := dc.Lookup(id)
		return m.Name, ok
	}

	gp := gpu.New(st, "/proc", gpuLookup)
	nv := gpu.NewNvidia(st, "/proc", gpuLookup)
	pr := pressure.New(st, "/proc", cgroupRoot, dc.Running)
	ur := unraid.New(st, st, envOnly(getenv, "GANTRY_UNRAID_DIR", "/unraid"), "/proc")
	du := docker.NewDiskUsage(st, dockerSock)
	ss := selfstat.New(st, "/proc")
	ss.HostCores = host.NumCPU

	registry := collect.NewRegistry()
	registry.Add(host)
	registry.Add(dc)
	registry.Add(du)
	registry.Add(gp)
	registry.Add(nv)
	registry.Add(pr)
	registry.Add(ur)
	registry.Add(ss)
	registry.Run(runCtx, &wg)

	// Maintenance: flush every minute; downsample + prune every 10 minutes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		flush := time.NewTicker(60 * time.Second)
		deep := time.NewTicker(10 * time.Minute)
		defer flush.Stop()
		defer deep.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-flush.C:
				if _, err := st.FlushMinutes(runCtx, time.Now()); err != nil {
					log.Println("flush:", err)
				}
			case <-deep.C:
				// Resolved fresh on every tick -- NOT once before the loop
				// (Phase 2's original shape) -- so a PUT /api/settings
				// retention change (Task 10) takes effect on the very next
				// tick, without a restart.
				ret := store.RetentionFromConfig(cfg.Int)
				if err := st.Maintain(runCtx, time.Now(), ret); err != nil {
					log.Println("maintain:", err)
				}
			}
		}
	}()

	// dispatcher owns delivery: the notify-spool channel (always present;
	// its own Health() degrades to the mount hint rather than an error
	// when /notify isn't there, real mode and fake mode alike -- fake
	// mode's own temp-dir default for GANTRY_NOTIFY_DIR is a later
	// phase's job, not this wiring's) plus one webhook channel per
	// enabled, valid configured target. Its workers start lazily on the
	// first Dispatch call; Run here only wires their shutdown to runCtx.
	dispatcher, err := buildDispatcher(st, cfg, getenv, ver)
	if err != nil {
		return fmt.Errorf("build alert dispatcher: %w", err)
	}
	dispatcher.Run(runCtx, &wg)

	// Alert engine: one 10s ticker beside the collector registry and the
	// maintenance loop above, reading the live ring through Match/ClassOf/
	// Fleet exactly the way server.Options already takes Query/Top/Events
	// -- store/config-shape-agnostic, wired here. Dispatch is now the
	// real dispatcher above: every fired/resolved/renotify transition
	// reaches whatever channels are configured.
	alertEngine := alert.New(st, st.Live().MatchSince, buildClassOf(ur), buildFleet(dc, fakeMetas), dispatcher.Dispatch, time.Now)
	wg.Add(1)
	go func() {
		defer wg.Done()
		alertEngine.Run(runCtx, 10*time.Second)
	}()

	// snapshotFn is the one buildSnapshot instance shared by /api/live/
	// snapshot (Options.Snapshot), /api/live's connect frame (Options.
	// Current), and the publish loop below -- all three read the exact
	// same assembly, just on different triggers (poll, connect, tick).
	snapshotFn := buildSnapshot(st, dc, ur, registry.Sources, fakeMetas, fakeDiskMeta, dispatcher)
	live := server.NewBroadcaster()

	// SSE publish loop: every 2s, marshal the current snapshot and fan it
	// out to every connected /api/live client. A marshal error is logged
	// and skipped rather than fatal -- SnapshotDTO is all plain JSON-safe
	// types, so this is defensive, not expected to ever fire.
	wg.Add(1)
	go func() {
		defer wg.Done()
		publish := time.NewTicker(2 * time.Second)
		defer publish.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-publish.C:
				b, err := json.Marshal(snapshotFn())
				if err != nil {
					log.Println("live publish marshal:", err)
					continue
				}
				live.Publish(b)
			}
		}
	}()

	log.Printf("gantry %s listening on :%d", ver, port)
	err = server.New(server.Options{
		Port:       port,
		Version:    ver,
		Store:      st,
		Started:    time.Now(),
		Sources:    registry.Sources,
		Snapshot:   snapshotFn,
		Containers: buildContainersList(dc, fakeMetas),
		Query:      st.QuerySeries,
		Top:        buildTop(st),
		Events:     st.QueryEvents,
		Live:       live,
		Current:    func() []byte { b, _ := json.Marshal(snapshotFn()); return b },
		Logs:       dc.StreamLogs,
		Storage:    buildContainerStorage(dc, ur, st, fakeMetas),
		Settings:   settingsAdapter{st: st, cfg: cfg},

		Images:       buildImages(imagesSrc),
		RemoveImages: buildRemoveImages(removeImagesSrc),
		PruneImages:  buildPruneImages(pruneImagesSrc),

		ContainersMaintenance: buildContainersMaintenance(containersMaintenanceSrc),
		RemoveContainers:      buildRemoveContainers(removeContainersSrc),
		PruneContainers:       buildPruneContainers(pruneContainersSrc),

		Alerts:   alertsAdapter{st: st, dispatcher: dispatcher},
		Webhooks: webhooksAdapter{st: st, envLocked: webhookURLEnv != ""},

		ReadOnly:    readOnly,
		AppendEvent: st.AppendEvent,
	}).ListenAndServe(runCtx)
	cancel()
	wg.Wait()
	dc.Drain() // join the docker event-stream goroutine before the final flush
	// Shutdown flush must complete even though runCtx is already cancelled.
	if _, ferr := st.FlushMinutes(context.Background(), time.Now()); ferr != nil {
		log.Println("final flush:", ferr)
	}
	return err
}

// wireDockerCollector points the docker collector's injected dependencies
// at the host collector's own methods (see docker.Collector's own doc for
// why: mem.pct, cpu.pct's host-share conversion, and per-device io
// attribution all need them). Extracted out of run() so this wiring --
// HostCores in particular, easy to accidentally swap for a plain
// runtime.NumCPU() -- is unit-testable without a live docker daemon or
// /proc.
func wireDockerCollector(dc *docker.Collector, h *host.Collector, cgroupRoot string) {
	dc.CgroupRoot = cgroupRoot
	dc.MemTotal = h.MemTotal
	dc.HostCores = h.NumCPU
	dc.DeviceName = h.DeviceName
}

// containerFrameMaxAge is how long a non-running container's live sample
// keeps it in the snapshot frame after its last write: long enough that
// the 10s inventory poll and the event stream both have a chance to
// catch up, short enough that a stopped-and-gone container doesn't linger
// for the ~15 minutes its ring would otherwise still hold data.
const containerFrameMaxAge = 60

// buildSnapshot returns the closure wired to server.Options.Snapshot: it
// assembles the current SnapshotDTO from st.Live()'s latest sample per
// series (grouped by SeriesKey Kind, then Entity where the DTO has an
// entity dimension; live:-prefixed metrics are ring-only per flush.go and
// never surfaced here), seeded with every currently-running container's
// inventory metadata from dc.Running() (plus fakeMetas' synthetic fleet,
// when GANTRY_FAKE_DATA=1 -- see fake.Generator.Metas' doc for why that's
// needed at all) so a container with no metrics yet still appears, plus
// ur.Version() and sources() (moved into the frame in v2 so an SSE client
// sees a collector degrade live, not just on its next healthz poll).
//
// fakeMetas is nil outside fake-data mode; when non-nil its entries are
// treated exactly like dc.Running()'s -- unconditionally seeded, not a
// mere lookup fallback -- so the fake fleet survives the same filter a
// real one does. fakeDiskMeta is its disk-metadata analogue: ur.DiskMeta()
// (a real box's own unraid collector) is merged into dto.DiskMeta first,
// then fakeDiskMeta's entries on top when wired -- see server.DiskMetaDTO's
// own doc for why disk type/device strings ride their own map rather than
// Disks' numeric one.
//
// Containers is filtered, not just seeded: an entity not in the merged
// running set only stays in the frame while containerFrameEntities says
// so (a live sample younger than containerFrameMaxAge AND a name
// lookupByName still recognizes) — see containerFrameEntities' own doc
// for why that's two different conditions, not one.
func buildSnapshot(st *store.Store, dc *docker.Collector, ur *unraid.Collector, sources func() map[string]string, fakeMetas func() []docker.Meta, fakeDiskMeta func() map[string]unraid.DiskMeta, dispatcher *alert.Dispatcher) func() server.SnapshotDTO {
	return func() server.SnapshotDTO {
		dto := server.SnapshotDTO{
			TS:            time.Now().Unix(),
			UnraidVersion: ur.Version(),
			Host:          map[string]float64{},
			Containers:    map[string]server.ContainerDTO{},
			Disks:         map[string]map[string]float64{},
			DiskMeta:      map[string]server.DiskMetaDTO{},
			Unraid:        map[string]map[string]float64{},
			GPU:           map[string]map[string]float64{},
			Sources:       map[string]string{},
		}
		if sources != nil {
			dto.Sources = sources()
		}

		// DiskMeta: real first, then fake-mode's own overlay when wired --
		// mirrors fakeMetas' "treated exactly like the real source, not a
		// mere fallback" contract just below, though in practice the two
		// never actually collide (fake mode has no real disks.ini to read).
		for slot, m := range ur.DiskMeta() {
			dto.DiskMeta[slot] = server.DiskMetaDTO{Device: m.Device, Kind: m.Kind}
		}
		if fakeDiskMeta != nil {
			for slot, m := range fakeDiskMeta() {
				dto.DiskMeta[slot] = server.DiskMetaDTO{Device: m.Device, Kind: m.Kind}
			}
		}

		metas := dc.Running()
		if fakeMetas != nil {
			metas = append(metas, fakeMetas()...)
		}
		running := map[string]struct{}{}
		for _, m := range metas {
			running[m.Name] = struct{}{}
			dto.Containers[m.Name] = server.ContainerDTO{
				State:        m.State,
				Health:       m.Health,
				Image:        m.Image,
				Icon:         m.Icon,
				Created:      metaCreatedUnix(m.Created),
				UpdateStatus: m.UpdateStatus,
				ChangelogURL: m.ChangelogURL,
				ProjectURL:   m.ProjectURL,
				WebUIURL:     m.WebUIURL,
				Networks:     convertNetworks(m.Networks),
				Ports:        convertPorts(m.Ports),
				Metrics:      map[string]float64{},
			}
		}

		live := st.Live()
		snap := live.SnapshotLatest()
		nowUnix := dto.TS // same instant as the frame's own timestamp, one call

		// Freshest observed age per non-running container entity: the
		// entity qualifies for inclusion if ANY of its "container"-kind
		// samples is young enough, so the minimum (freshest) age is what
		// containerFrameEntities needs, not every individual sample's age.
		sampleAge := map[string]int64{}
		for key, sample := range snap {
			if key.Kind != "container" {
				continue
			}
			age := nowUnix - sample.TS
			if cur, ok := sampleAge[key.Entity]; !ok || age < cur {
				sampleAge[key.Entity] = age
			}
		}
		include := containerFrameEntities(running, sampleAge, containerFrameMaxAge, dc.LookupByName)
		for name := range include {
			if _, ok := dto.Containers[name]; !ok {
				dto.Containers[name] = server.ContainerDTO{Metrics: map[string]float64{}}
			}
		}

		for key, sample := range snap {
			if strings.HasPrefix(key.Metric, "live:") {
				continue
			}
			switch key.Kind {
			case "host":
				dto.Host[key.Metric] = sample.Val
			case "container":
				if _, ok := include[key.Entity]; !ok {
					continue // filtered out: stopped, stale, or no longer known
				}
				if nowUnix-sample.TS >= containerFrameMaxAge {
					continue // this ONE sample is stale, even though its entity is still in the frame -- e.g. `docker update --memory 0` stops mem.limit_bytes without stopping the container, and the same gate covers container-attributed gpu.*.busy_pct going quiet
				}
				c := dto.Containers[key.Entity]
				c.Metrics[key.Metric] = sample.Val
				dto.Containers[key.Entity] = c
			case "disk":
				d, ok := dto.Disks[key.Entity]
				if !ok {
					d = map[string]float64{}
					dto.Disks[key.Entity] = d
				}
				d[key.Metric] = sample.Val
			case "unraid":
				u, ok := dto.Unraid[key.Entity]
				if !ok {
					u = map[string]float64{}
					dto.Unraid[key.Entity] = u
				}
				u[key.Metric] = sample.Val
			case "gpu":
				g, ok := dto.GPU[key.Entity]
				if !ok {
					g = map[string]float64{}
					dto.GPU[key.Entity] = g
				}
				g[key.Metric] = sample.Val
			}
		}

		dto.Alerts = buildAlertsBlock(st, dispatcher)
		return dto
	}
}

// alertsFrameContext is the fixed background context every buildSnapshot
// tick's alert reads run under: the closure this feeds (server.Options.
// Snapshot/Current, and the 2s publish loop) has no per-call caller
// context of its own to thread through, the same reasoning the shutdown
// flush at the bottom of run() already documents for its own
// context.Background() use.
var alertsFrameContext = context.Background()

// buildAlertsBlock assembles SnapshotDTO.Alerts (Task 8): every FIRING
// instance (pending excluded -- engine bookkeeping, not user-facing, the
// same rule GET /api/alerts' own handler applies), capped at
// server.AlertsFrameCap with a truncated count, plus every channel's
// current health. A read error is logged and treated as empty for this
// one tick, never fatal to the frame -- the same "degrade, don't error"
// posture Sources already models, and the next 2s tick tries again.
func buildAlertsBlock(st *store.Store, dispatcher *alert.Dispatcher) server.AlertsBlockDTO {
	rules, err := st.AlertRules(alertsFrameContext)
	if err != nil {
		log.Println("alerts frame: rules:", err)
	}
	ruleByID := make(map[string]store.AlertRule, len(rules))
	for _, r := range rules {
		ruleByID[r.ID] = r
	}

	active, err := st.ActiveAlertInstances(alertsFrameContext)
	if err != nil {
		log.Println("alerts frame: active instances:", err)
	}
	silences, err := st.Silences(alertsFrameContext, time.Now().Unix())
	if err != nil {
		log.Println("alerts frame: silences:", err)
	}

	var firing []server.FiringAlertDTO
	for _, inst := range active {
		if inst.State != "firing" {
			continue
		}
		firing = append(firing, server.FiringAlertDTO{
			RuleID: inst.RuleID, RuleName: ruleByID[inst.RuleID].Name, Severity: inst.Severity,
			Kind: inst.Kind, Entity: inst.Entity, Metric: inst.Metric,
			Value: inst.Value, Threshold: inst.Threshold, FiredAt: inst.FiredAt,
			Silenced: server.SilenceCovers(silences, inst.RuleID, inst.Entity),
		})
	}

	total := len(firing)
	var truncated int
	if total > server.AlertsFrameCap {
		truncated = total - server.AlertsFrameCap
		firing = firing[:server.AlertsFrameCap]
	}
	if firing == nil {
		firing = []server.FiringAlertDTO{}
	}

	return server.AlertsBlockDTO{
		Firing: firing, FiringCount: total, Truncated: truncated,
		Channels: channelHealthMap(dispatcher),
	}
}

// channelHealthMap reports every configured delivery channel's current
// Health(), keyed by its own ID() -- shared by alertsAdapter.Channels
// (GET /api/alerts) and buildAlertsBlock just above, the same data both
// surfaces document (plan Task 8).
func channelHealthMap(d *alert.Dispatcher) map[string]string {
	out := map[string]string{}
	if d == nil {
		return out
	}
	for _, ch := range d.Channels {
		out[ch.ID()] = ch.Health()
	}
	return out
}

// metaCreatedUnix converts a Meta.Created into ContainerDTO's wire form.
// Go's zero time.Time.Unix() is a large negative number (year 1), not
// 0 -- guarding IsZero() here is what lets ContainerDTO's own
// `json:"created,omitempty"` correctly omit an unparsed/absent Created
// rather than serialize that garbage value.
func metaCreatedUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// convertNetworks/convertPorts adapt docker.Meta's own Networks/Ports
// (docker.NetworkInfo/docker.PortInfo) to their wire-tagged DTO
// counterparts (server.NetworkInfoDTO/server.PortInfoDTO) -- server
// deliberately doesn't import the docker package (collectors stay
// mutually decoupled from the HTTP layer, the same reason buildTop/
// buildContainersList live here rather than in server), so this
// field-by-field copy is main's job, same as Image/Icon/etc. just above.
func convertNetworks(in []docker.NetworkInfo) []server.NetworkInfoDTO {
	if len(in) == 0 {
		return nil
	}
	out := make([]server.NetworkInfoDTO, len(in))
	for i, n := range in {
		out[i] = server.NetworkInfoDTO{Name: n.Name, IP: n.IP}
	}
	return out
}

func convertPorts(in []docker.PortInfo) []server.PortInfoDTO {
	if len(in) == 0 {
		return nil
	}
	out := make([]server.PortInfoDTO, len(in))
	for i, p := range in {
		out[i] = server.PortInfoDTO{ContainerPort: p.ContainerPort, Proto: p.Proto, HostIP: p.HostIP, HostPort: p.HostPort}
	}
	return out
}

// containerFrameEntities decides which container entities belong in this
// tick's frame: every name in running is always included; a name that's
// merely referenced by a live sample gets one more chance, but only if
// its freshest sample is younger than maxAge seconds AND lookupByName
// still recognizes the name. Both conditions matter independently: age
// alone would let a fully-removed container's still-fresh-but-orphaned
// sample flicker back into view for up to maxAge seconds, and
// lookupByName alone would let a long-stopped-but-not-removed
// container's stale metrics linger for as long as its ring happens to
// still hold data (~15 minutes) instead of the frame's own, much shorter
// cutoff. Extracted as a pure function so it's testable without a real
// docker daemon or *store.Store.
func containerFrameEntities(running map[string]struct{}, sampleAge map[string]int64, maxAge int64, lookupByName func(string) (docker.Meta, bool)) map[string]struct{} {
	out := make(map[string]struct{}, len(running))
	for name := range running {
		out[name] = struct{}{}
	}
	for name, age := range sampleAge {
		if _, already := out[name]; already {
			continue
		}
		if age >= maxAge {
			continue
		}
		if _, known := lookupByName(name); !known {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

// buildContainersList returns the closure wired to server.Options.
// Containers: /api/containers' v2 contract serves dc.Running() directly
// (name/state/health/image only), with no snapshot/DTO detour --
// plus fakeMetas' synthetic fleet, when GANTRY_FAKE_DATA=1, so the demo
// fleet is listed the same way a real running one would be (nil in real
// mode: buildSnapshot's own doc explains why this merge exists at all).
func buildContainersList(dc *docker.Collector, fakeMetas func() []docker.Meta) func() []server.ContainerInfo {
	return func() []server.ContainerInfo {
		running := dc.Running()
		if fakeMetas != nil {
			running = append(running, fakeMetas()...)
		}
		out := make([]server.ContainerInfo, 0, len(running))
		for _, m := range running {
			out = append(out, server.ContainerInfo{Name: m.Name, State: m.State, Health: m.Health, Image: m.Image, Icon: m.Icon})
		}
		return out
	}
}

// buildFleet returns the closure wired to alert.Engine.Fleet: dc.All()
// (running or not, unlike buildContainersList's dc.Running()) so boot
// seeding can see a stopped container's stale Health and correctly
// decline to seed it, plus fakeMetas' synthetic fleet merged in the same
// unconditional way every other fake-mode wiring in this file already
// does (nil outside fake-data mode).
func buildFleet(dc *docker.Collector, fakeMetas func() []docker.Meta) func() []alert.FleetMember {
	return func() []alert.FleetMember {
		metas := dc.All()
		if fakeMetas != nil {
			metas = append(metas, fakeMetas()...)
		}
		out := make([]alert.FleetMember, len(metas))
		for i, m := range metas {
			out[i] = alert.FleetMember{Name: m.Name, State: m.State, Health: m.Health}
		}
		return out
	}
}

// buildClassOf returns the closure wired to alert.Engine.ClassOf: disk-
// class scoping (entity_class "nvme"/"!nvme") resolves through the
// unraid collector's own DiskMeta at evaluation time, so a rule's class
// filter tracks whatever the box's disks actually are on this tick
// rather than a value captured once. Every other kind has no notion of
// class yet, so it reads as an empty string -- MatchClass treats an
// empty spec or an empty class as a match, so an entity_class-scoped
// rule simply never excludes a non-disk entity on class grounds, which
// is exactly right (only disk-temp-high/disk-temp-nvme-high use
// entity_class at all).
func buildClassOf(ur *unraid.Collector) func(kind, entity string) string {
	return func(kind, entity string) string {
		if kind != "disk" {
			return ""
		}
		return ur.DiskMeta()[entity].Kind
	}
}

// webhookTargetsSettingsKey is where the whole []alert.WebhookTarget
// list lives as one JSON blob in the settings table -- delivery config,
// not alerting data the engine itself reads, so it rides the same
// settings-table-as-JSON-blob convention every other domain-specific
// config shape in this codebase uses rather than getting its own SQL
// table.
const webhookTargetsSettingsKey = "alert.webhook_targets"

// loadWebhookTargets returns the configured targets, or nil (not an
// error) when the setting has never been written -- the same "meaningful
// empty" convention the rest of this codebase's optional config reads
// use.
func loadWebhookTargets(st *store.Store) ([]alert.WebhookTarget, error) {
	raw, ok, err := st.SettingGet(webhookTargetsSettingsKey)
	if err != nil {
		return nil, err
	}
	if !ok || raw == "" {
		return nil, nil
	}
	var targets []alert.WebhookTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return nil, fmt.Errorf("parse %s: %w", webhookTargetsSettingsKey, err)
	}
	return targets, nil
}

func saveWebhookTargets(st *store.Store, targets []alert.WebhookTarget) error {
	b, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	return st.SettingSet(webhookTargetsSettingsKey, string(b))
}

// seedWebhookTargetFromEnv keeps a target named "env" in sync with
// GANTRY_WEBHOOK_URL (spec Sec5's documented single-webhook path) on
// every boot, in BOTH directions: set, it's added or its URL/Enabled/
// TimeoutS pinned to whatever the env var currently says; blank, the
// "env" target a previous boot created is removed -- an operator who
// clears the variable means "stop delivering there", and a target that
// quietly outlived its env var would keep posting alerts to a URL the
// operator believes is gone. Every other target in the list is left
// untouched either way. The env var is the source of truth for this
// one target -- Task 8's API will enforce the same rule against a
// conflicting PUT (409, per the plan), but there is no API on this
// branch yet, so boot time is the only place that rule is enforced
// today.
func seedWebhookTargetFromEnv(st *store.Store, url string) error {
	targets, err := loadWebhookTargets(st)
	if err != nil {
		return err
	}
	if url == "" {
		for i, t := range targets {
			if t.ID == "env" {
				return saveWebhookTargets(st, append(targets[:i], targets[i+1:]...))
			}
		}
		return nil // no env target to remove; leave the setting untouched (possibly never written at all)
	}
	found := false
	for i, t := range targets {
		if t.ID == "env" {
			targets[i].URL, targets[i].Enabled, targets[i].TimeoutS = url, true, 10
			found = true
			break
		}
	}
	if !found {
		targets = append(targets, alert.WebhookTarget{ID: "env", Name: "Environment", URL: url, Enabled: true, TimeoutS: 10})
	}
	return saveWebhookTargets(st, targets)
}

// buildWebhookChannels turns every enabled, valid target into a Channel.
// A target that fails validation is skipped with a log line rather than
// aborting boot -- the same "one bad rule can't take the rest down"
// posture engine.go's own per-rule recover already applies, here guarding
// against a hand-edited settings blob (Task 8's API will run this same
// validation at the door once it exists; nothing does yet).
func buildWebhookChannels(targets []alert.WebhookTarget, version string, clock func() time.Time) []alert.Channel {
	var out []alert.Channel
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		if err := alert.ValidateWebhookTarget(t); err != nil {
			log.Printf("alert: webhook target %q: %v", t.ID, err)
			continue
		}
		out = append(out, alert.NewWebhookChannel(t, version, clock))
	}
	return out
}

// buildDispatcher assembles the alert.Dispatcher wired to
// alert.Engine.Dispatch: the notify-spool channel is always present
// (GANTRY_NOTIFY_DIR, default /notify -- the CA template's own mount
// point, real mode and fake mode alike; fake mode defaulting it to a
// temp dir so the demo has somewhere to write is a later phase's job,
// not this wiring's), plus one Channel per enabled, valid configured
// webhook target. alert.link_base and alert.notify_resolved are read
// fresh on every call through their own closures -- a settings change
// takes effect on the very next dispatch, no restart, the same
// resolved-fresh-every-tick posture the retention settings above use.
func buildDispatcher(st *store.Store, cfg *config.Config, getenv func(string) string, version string) (*alert.Dispatcher, error) {
	notifyDir := envOnly(getenv, "GANTRY_NOTIFY_DIR", "/notify")
	linkBase := func() string {
		v, _, _ := st.SettingGet("alert.link_base")
		return v
	}
	channels := []alert.Channel{alert.NewNotifyChannel(notifyDir, linkBase, time.Now)}

	targets, err := loadWebhookTargets(st)
	if err != nil {
		return nil, fmt.Errorf("load webhook targets: %w", err)
	}
	channels = append(channels, buildWebhookChannels(targets, version, time.Now)...)

	resolvedNotices := func() bool { return cfg.Bool("alert.notify_resolved", true) }
	return alert.NewDispatcher(st, channels, time.Now, resolvedNotices), nil
}

// buildContainerStorage returns the closure wired to server.Options.
// Storage -- like buildContainersList/buildSnapshot, dc.LookupByName
// falls back to fakeMetas' synthetic fleet when GANTRY_FAKE_DATA=1, so a
// fake container's storage panel resolves instead of 404ing (nil in
// real mode).
func buildContainerStorage(dc *docker.Collector, ur *unraid.Collector, st *store.Store, fakeMetas func() []docker.Meta) func(name string) (server.StorageDTO, bool) {
	lookup := dc.LookupByName
	if fakeMetas != nil {
		lookup = func(name string) (docker.Meta, bool) {
			if m, ok := dc.LookupByName(name); ok {
				return m, true
			}
			for _, m := range fakeMetas() {
				if m.Name == name {
					return m, true
				}
			}
			return docker.Meta{}, false
		}
	}
	return func(name string) (server.StorageDTO, bool) {
		return containerStorage(lookup, ur.Slots, st.Live(), name, time.Now().Unix())
	}
}

func containerStorage(lookupMeta func(string) (docker.Meta, bool), poolSlots func() []string, live *store.Live, name string, now int64) (server.StorageDTO, bool) {
	meta, ok := lookupMeta(name)
	if !ok {
		return server.StorageDTO{}, false
	}

	pools := poolSlots()
	mounts := make([]server.MountDTO, 0, len(meta.Mounts))
	for _, m := range meta.Mounts {
		ref := unraid.ResolveStoragePath(m.Source, pools)
		mounts = append(mounts, server.MountDTO{
			Source:      m.Source,
			Destination: m.Destination,
			RW:          m.RW,
			Storage:     server.StorageRefDTO{Kind: ref.Kind, Name: ref.Name},
		})
	}

	devices := deviceIOFromSamples(live.LatestByMetricPrefix("container", name, "live:io."), now)
	return server.StorageDTO{Mounts: mounts, Devices: devices}, true
}

// deviceIOFromSamples turns store.Live's live:io.<dev>.read_bps/
// write_bps samples (recorded by cgroupv2.go's recordContainerStats,
// live-ring-only by design) into one DeviceIODTO per device, sorted by
// device name for a stable response. A sample containerFrameMaxAge
// seconds old or older is dropped entirely rather than surfaced as a
// device row: its ring is evicted on container REMOVAL, not on stop
// (registry.go's applyInventory/applyEvent), so without this gate a
// long-stopped container's last-ever rates would keep reading as
// current -- the same cutoff and boundary buildSnapshot's own stopped-
// container filter already applies, just against one entity's samples
// instead of every "container"-kind key. A device with only one of the
// two rates yet (its RateTracker key for the other direction hasn't
// produced a second reading) still appears, with the missing half left
// at its float64 zero value rather than being dropped.
func deviceIOFromSamples(samples map[string]store.Sample, now int64) []server.DeviceIODTO {
	byDevice := make(map[string]*server.DeviceIODTO, len(samples))
	for metric, sample := range samples {
		if now-sample.TS >= containerFrameMaxAge {
			continue
		}
		dev, suffix, ok := strings.Cut(strings.TrimPrefix(metric, "live:io."), ".")
		if !ok {
			continue
		}
		switch suffix {
		case "read_bps":
			d, exists := byDevice[dev]
			if !exists {
				d = &server.DeviceIODTO{Device: dev}
				byDevice[dev] = d
			}
			d.ReadBps = sample.Val
		case "write_bps":
			d, exists := byDevice[dev]
			if !exists {
				d = &server.DeviceIODTO{Device: dev}
				byDevice[dev] = d
			}
			d.WriteBps = sample.Val
		}
	}

	names := make([]string, 0, len(byDevice))
	for dev := range byDevice {
		names = append(names, dev)
	}
	sort.Strings(names)

	out := make([]server.DeviceIODTO, 0, len(names))
	for _, dev := range names {
		out = append(out, *byDevice[dev])
	}
	return out
}

// buildTop adapts store.TopEntities' anonymous-struct return type to
// server.TopRow for server.Options.Top: the two are structurally identical
// (same field names/types/order) but Go doesn't let a []struct{...} stand
// in for a []server.TopRow without converting element by element.
func buildTop(st *store.Store) func(ctx context.Context, kind, metric string, from, to int64, agg string, limit int) ([]server.TopRow, error) {
	return func(ctx context.Context, kind, metric string, from, to int64, agg string, limit int) ([]server.TopRow, error) {
		rows, err := st.TopEntities(ctx, kind, metric, from, to, agg, limit)
		if err != nil {
			return nil, err
		}
		out := make([]server.TopRow, len(rows))
		for i, row := range rows {
			out[i] = server.TopRow{Entity: row.Entity, Value: row.Value}
		}
		return out, nil
	}
}

// buildImages adapts docker.ImagesReport (src is dc.Images in real mode,
// fk.Images in fake mode -- see run()'s imagesSrc wiring) to
// server.ImagesDTO for server.Options.Images: a field-by-field copy, the
// same shape as buildTop above. ImagesDTO.Summary.Note is left zero here
// -- it's a fixed caveat about the reclaimable_bytes field itself, not
// data from either source, so the server package's own handler fills it
// in unconditionally.
func buildImages(src func(ctx context.Context) (docker.ImagesReport, error)) func(ctx context.Context) (server.ImagesDTO, error) {
	return func(ctx context.Context) (server.ImagesDTO, error) {
		report, err := src(ctx)
		if err != nil {
			return server.ImagesDTO{}, err
		}
		out := server.ImagesDTO{
			Images: make([]server.ImageInfo, len(report.Images)),
			Summary: server.ImagesSummary{
				InUse:            report.Summary.InUse,
				Unused:           report.Summary.Unused,
				Dangling:         report.Summary.Dangling,
				ReclaimableBytes: report.Summary.ReclaimableBytes,
			},
		}
		for i, im := range report.Images {
			out.Images[i] = server.ImageInfo{
				ID: im.ID, RepoTags: im.RepoTags, RepoDigests: im.RepoDigests, SizeBytes: im.SizeBytes,
				Created: im.Created, State: im.State, Containers: im.Containers,
			}
		}
		return out, nil
	}
}

// buildRemoveImages adapts []docker.ImageRemoveResult to
// []server.ImageRemoveResult for server.Options.RemoveImages -- see
// buildImages.
func buildRemoveImages(src func(ctx context.Context, ids []string) ([]docker.ImageRemoveResult, error)) func(ctx context.Context, ids []string) ([]server.ImageRemoveResult, error) {
	return func(ctx context.Context, ids []string) ([]server.ImageRemoveResult, error) {
		results, err := src(ctx, ids)
		if err != nil {
			return nil, err
		}
		out := make([]server.ImageRemoveResult, len(results))
		for i, r := range results {
			out[i] = server.ImageRemoveResult{ID: r.ID, OK: r.OK, Error: r.Error, RepoTags: r.RepoTags, SizeBytes: r.SizeBytes}
		}
		return out, nil
	}
}

// buildPruneImages adapts docker.ImagePruneResult to
// server.ImagePruneResult for server.Options.PruneImages -- see
// buildImages.
func buildPruneImages(src func(ctx context.Context, mode string) (docker.ImagePruneResult, error)) func(ctx context.Context, mode string) (server.ImagePruneResult, error) {
	return func(ctx context.Context, mode string) (server.ImagePruneResult, error) {
		r, err := src(ctx, mode)
		if err != nil {
			return server.ImagePruneResult{}, err
		}
		deleted := make([]server.DeletedImage, len(r.Deleted))
		for i, d := range r.Deleted {
			deleted[i] = server.DeletedImage{ID: d.ID, RepoTags: d.RepoTags, SizeBytes: d.SizeBytes}
		}
		return server.ImagePruneResult{Deleted: deleted, ReclaimedBytes: r.ReclaimedBytes, Errors: r.Errors}, nil
	}
}

// buildContainersMaintenance adapts docker.ContainerMaintenanceReport
// (src is dc.ContainersMaintenance in real mode, fk.
// ContainersMaintenance in fake mode -- see run()'s
// containersMaintenanceSrc wiring) to server.ContainerMaintenanceDTO for
// server.Options.ContainersMaintenance -- see buildImages.
func buildContainersMaintenance(src func(ctx context.Context) (docker.ContainerMaintenanceReport, error)) func(ctx context.Context) (server.ContainerMaintenanceDTO, error) {
	return func(ctx context.Context) (server.ContainerMaintenanceDTO, error) {
		report, err := src(ctx)
		if err != nil {
			return server.ContainerMaintenanceDTO{}, err
		}
		out := server.ContainerMaintenanceDTO{
			Containers: make([]server.ContainerMaintenanceInfo, len(report.Containers)),
			Summary: server.ContainerMaintenanceSummary{
				Exited:  report.Summary.Exited,
				Created: report.Summary.Created,
				Dead:    report.Summary.Dead,
			},
		}
		for i, ct := range report.Containers {
			out.Containers[i] = server.ContainerMaintenanceInfo{
				ID: ct.ID, Name: ct.Name, Image: ct.Image, State: ct.State,
				ExitCode: ct.ExitCode, Created: ct.Created, FinishedAt: ct.FinishedAt, Managed: ct.Managed,
				RestartPolicy: ct.RestartPolicy,
			}
		}
		return out, nil
	}
}

// buildRemoveContainers adapts []docker.ContainerRemoveResult to
// []server.ContainerRemoveResult for server.Options.RemoveContainers --
// see buildImages/buildRemoveImages.
func buildRemoveContainers(src func(ctx context.Context, ids []string) ([]docker.ContainerRemoveResult, error)) func(ctx context.Context, ids []string) ([]server.ContainerRemoveResult, error) {
	return func(ctx context.Context, ids []string) ([]server.ContainerRemoveResult, error) {
		results, err := src(ctx, ids)
		if err != nil {
			return nil, err
		}
		out := make([]server.ContainerRemoveResult, len(results))
		for i, r := range results {
			out[i] = server.ContainerRemoveResult{ID: r.ID, OK: r.OK, Error: r.Error, Name: r.Name, Image: r.Image}
		}
		return out, nil
	}
}

// buildPruneContainers adapts docker.ContainerPruneResult to
// server.ContainerPruneResult for server.Options.PruneContainers -- see
// buildImages/buildPruneImages.
func buildPruneContainers(src func(ctx context.Context, mode string, olderThanHours int) (docker.ContainerPruneResult, error)) func(ctx context.Context, mode string, olderThanHours int) (server.ContainerPruneResult, error) {
	return func(ctx context.Context, mode string, olderThanHours int) (server.ContainerPruneResult, error) {
		r, err := src(ctx, mode, olderThanHours)
		if err != nil {
			return server.ContainerPruneResult{}, err
		}
		deleted := make([]server.DeletedContainer, len(r.Deleted))
		for i, d := range r.Deleted {
			deleted[i] = server.DeletedContainer{ID: d.ID, Name: d.Name, Image: d.Image}
		}
		return server.ContainerPruneResult{Deleted: deleted, Errors: r.Errors}, nil
	}
}

// retentionConfigKeys maps each /api/settings wire field name
// (server.RetentionSettings' json tags) to its store/config dotted key
// -- the one place that mapping is spelled out, shared by both
// settingsAdapter methods below.
var retentionConfigKeys = map[string]string{
	"r1_hours":    "retention.r1_hours",
	"r2_days":     "retention.r2_days",
	"r3_days":     "retention.r3_days",
	"size_cap_mb": "retention.size_cap_mb",
}

// settingsAdapter implements server.SettingsIface (Task 10): Get reuses
// store.RetentionFromConfig -- the same resolution the maintenance loop
// itself now calls every tick (see the per-tick comment above) -- so
// this can never drift from what's actually in effect, plus cfg's own
// env-override check per key; Set writes through to the settings table
// via st.SettingSet. Keeping this in main, not the server package,
// keeps server store/config-shape-agnostic the same way buildTop/
// buildContainersList already do for Query/Top/Containers.
type settingsAdapter struct {
	st  *store.Store
	cfg *config.Config
}

func (a settingsAdapter) Get() (server.RetentionSettings, map[string]bool) {
	ret := store.RetentionFromConfig(a.cfg.Int)
	out := server.RetentionSettings{
		R1Hours:   int(ret.R1 / time.Hour),
		R2Days:    int(ret.R2 / (24 * time.Hour)),
		R3Days:    int(ret.R3 / (24 * time.Hour)),
		SizeCapMB: int(ret.SizeCapBytes >> 20),
	}
	overridden := make(map[string]bool, len(retentionConfigKeys))
	for wire, key := range retentionConfigKeys {
		overridden[wire] = a.cfg.EnvOverridden(key)
	}
	return out, overridden
}

func (a settingsAdapter) Set(field string, value int) error {
	key, ok := retentionConfigKeys[field]
	if !ok {
		return fmt.Errorf("settings: unknown field %q", field) // unreached: handler only calls Set with its own whitelisted names
	}
	return a.st.SettingSet(key, strconv.Itoa(value))
}

// alertsAdapter implements server.AlertsIface (Task 8) over *store.Store
// plus the running *alert.Dispatcher's own Channels field for health --
// kept in main, not the server package, the same reason settingsAdapter
// is: server stays store/alert-shape-agnostic.
type alertsAdapter struct {
	st         *store.Store
	dispatcher *alert.Dispatcher
}

func (a alertsAdapter) Active(ctx context.Context) ([]store.AlertInstance, error) {
	return a.st.ActiveAlertInstances(ctx)
}

func (a alertsAdapter) History(ctx context.Context, from, to int64, limit int) ([]store.AlertInstance, error) {
	return a.st.AlertHistory(ctx, from, to, limit)
}

func (a alertsAdapter) Rules(ctx context.Context) ([]store.AlertRule, error) {
	return a.st.AlertRules(ctx)
}

func (a alertsAdapter) UpsertRule(r store.AlertRule) error { return a.st.UpsertAlertRule(r) }

func (a alertsAdapter) ReplaceRules(rules []store.AlertRule) error {
	return a.st.ReplaceAlertRules(rules)
}

func (a alertsAdapter) Silences(ctx context.Context) ([]store.Silence, error) {
	return a.st.Silences(ctx, time.Now().Unix())
}

func (a alertsAdapter) AddSilence(sil store.Silence) (store.Silence, error) {
	id, err := a.st.AddSilence(sil)
	if err != nil {
		return store.Silence{}, err
	}
	sil.ID = id
	return sil, nil
}

func (a alertsAdapter) DeleteSilence(id int64) error { return a.st.DeleteSilence(id) }

func (a alertsAdapter) Channels() map[string]string { return channelHealthMap(a.dispatcher) }

// webhooksAdapter implements server.WebhooksIface (Task 8) over the same
// settings-blob-backed target list Task 7 built (loadWebhookTargets/
// saveWebhookTargets), plus whether GANTRY_WEBHOOK_URL was set at boot
// (envLocked, resolved once in run() alongside readOnly).
type webhooksAdapter struct {
	st        *store.Store
	envLocked bool
}

func (a webhooksAdapter) Targets() ([]alert.WebhookTarget, bool, error) {
	targets, err := loadWebhookTargets(a.st)
	return targets, a.envLocked, err
}

func (a webhooksAdapter) Replace(targets []alert.WebhookTarget) error {
	return saveWebhookTargets(a.st, targets)
}

func healthcheck(getenv func(string) string) error {
	port := envOnly(getenv, "GANTRY_PORT", "8380")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/healthz")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

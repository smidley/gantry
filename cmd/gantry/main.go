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
	"github.com/smidley/gantry/internal/insight"
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

	// cfg is constructed here, ahead of the alert-rule seed just below,
	// so fakeMode (env>settings>default, the same precedence every other
	// cfg.Bool call in this function uses) is known in time to pick the
	// right seed table -- config.New itself is a pure wrap of st+getenv
	// with no side effects, so resolving it this early changes nothing
	// else about boot order.
	cfg := config.New(st, getenv)
	fakeMode := cfg.Bool("fake_data", false)

	// Seeded before anything else touches alert_rules: an id already
	// present (a prior boot's seed, possibly since edited or disabled)
	// is left untouched; only an id genuinely absent -- first boot, or a
	// default introduced by a later upgrade -- gets inserted. There is
	// no alert engine yet to gate this on (Task 4); "before the engine's
	// first tick" is trivially satisfied by seeding at boot. fast=
	// fakeMode compresses every threshold rule's sustained-for window to
	// 60s (Task 9's fake-mode alert demo) so it can go pending -> firing
	// -> resolved inside a short interactive session; a real box always
	// seeds the true, uncompressed numbers.
	if err := st.SeedAlertRules(store.DefaultAlertRules(fakeMode)); err != nil {
		return fmt.Errorf("seed alert rules: %w", err)
	}
	// resyncFastModeAlertRules runs right after the seed above for the
	// exact reason its own doc names (I5, review): SeedAlertRules'
	// INSERT OR IGNORE only ever writes fakeMode's compressed 60s/60s
	// windows on a row's FIRST boot, so a database first seeded under
	// one mode stays pinned to that mode's numbers on every later boot,
	// fake or real, forever.
	if err := resyncFastModeAlertRules(st, fakeMode); err != nil {
		return fmt.Errorf("resync fast-mode alert rules: %w", err)
	}
	// Insight rule configs (Task 4's own INSERT-OR-IGNORE seed, same
	// idempotent-on-every-boot posture as SeedAlertRules just above) --
	// there is no "fast" variant here at all: unlike alert_rules'
	// for_seconds/clear_seconds, the insight engine's sustain/clear-for/
	// cooldown are all Engine struct fields, not seeded rows (see
	// insight.Engine's own doc), so fake mode compresses them below at
	// construction time instead -- I5 (review): sustain_secs used to be
	// the one exception, compressed via a seeded override that outlived
	// the fake-data session that needed it, so DefaultRuleConfigs no
	// longer takes a fake/fast parameter at all. StaleActiveInsights
	// runs right after: Open question 5's own recommendation -- the
	// live ring is empty at boot, so a carried-over "active" row would
	// assert something this process cannot currently see; if the
	// contention is still real, the engine re-fires within two ticks
	// anyway.
	if err := st.SeedInsightRuleConfigs(insight.DefaultRuleConfigs()); err != nil {
		return fmt.Errorf("seed insight rule configs: %w", err)
	}
	if err := st.StaleActiveInsights(time.Now().Unix()); err != nil {
		return fmt.Errorf("stale active insights: %w", err)
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
	// metadata (see fake.Generator.DiskMetas' own doc); fakeDeviceLabels
	// is buildContainerStorage's own analogue, for the one device kind
	// fakeDiskMeta's join can't cover (see fake.Generator.DeviceLabels'
	// own doc); fakeSharePlacement is the same overlay again, for the
	// share->cache-pool join (see fake.Generator.SharePlacements' own
	// doc) -- real mode never sees a shares.ini in this closure at all,
	// so there's nothing to overlay onto there either. fk itself (nil
	// outside fake-data mode) is kept for the image-inventory and
	// container-maintenance wiring below, once dc exists to default
	// against.
	var fakeMetas func() []docker.Meta
	var fakeDiskMeta func() map[string]unraid.DiskMeta
	var fakeDeviceLabels func() map[string]unraid.DeviceLabel
	var fakeSharePlacement func() map[string]unraid.SharePlacement
	var fk *fake.Generator
	if fakeMode {
		log.Println("fake data mode: synthesizing a demo fleet")
		fk = fake.New(st, st, time.Now().UnixNano())
		fakeMetas = fk.Metas
		fakeDiskMeta = fk.DiskMetas
		fakeDeviceLabels = fk.DeviceLabels
		fakeSharePlacement = fk.SharePlacements
		wg.Add(1)
		go func() {
			defer wg.Done()
			fk.Run(runCtx, 2*time.Second, nil)
		}()

		// Task 9: two webhook targets seeded (never overwritten once
		// present, same idempotent-insert idiom as SeedAlertRules/
		// seedWebhookTargetFromEnv) so the delivery ledger's SUCCESS and
		// FAILURE paths both render in the Settings channels card with no
		// external service required -- see seedFakeWebhookTargets' own doc.
		if err := seedFakeWebhookTargets(st, port); err != nil {
			return fmt.Errorf("seed fake webhook targets: %w", err)
		}
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
	gp.SysRoot = sysRoot
	nv := gpu.NewNvidia(st, "/proc", gpuLookup)
	nv.SysRoot = sysRoot
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
	dispatcher, err := buildDispatcher(st, cfg, getenv, ver, fakeMode)
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

	// Insight engine: cross-container impact correlation (spec §16), one
	// 60s ticker beside the alert engine above, reading the exact same
	// live ring (Live.MatchSince/MatchPrefixSince) and rebuilding its own
	// Topology snapshot each tick from the host collector's DeviceName
	// plus the unraid collector's disk slots (buildInsightSlots). Dispatch
	// is left nil: Notifiable's own three gates already keep this inert
	// with every seeded rule's notify off, and there is no
	// store.AlertRule/AlertInstance to hand the shared alert Dispatcher's
	// AlertNotification shape without fabricating fields it would then
	// read for its own flap/silence bookkeeping -- a real delivery bridge
	// is later work, not this phase's engine task. In fake-data mode,
	// ClearForSecs/CooldownSecs/FakeSustainSecs are all compressed here
	// (FakeSustainSecs is I5's own read-time replacement for what used
	// to be a DefaultRuleConfigs(fakeMode) seeded override -- see its
	// own doc), so a scripted insight can fire, upgrade, and resolve
	// inside one short demo session.
	insightEngine := insight.New(st)
	insightEngine.MatchSince = st.Live().MatchSince
	insightEngine.MatchPrefixSince = st.Live().MatchPrefixSince
	insightEngine.DeviceName = host.DeviceName
	insightEngine.Slots = buildInsightSlots(ur.DiskMeta, fakeDiskMeta, st.Live())
	insightEngine.PressureTier = pr.Tier
	if fakeMode {
		insightEngine.FakeSustainSecs = 10
		insightEngine.ClearForSecs = 20
		insightEngine.CooldownSecs = 60
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		insightEngine.Run(runCtx, 60*time.Second)
	}()

	// snapshotFn is the one buildSnapshot instance shared by /api/live/
	// snapshot (Options.Snapshot), /api/live's connect frame (Options.
	// Current), and the publish loop below -- all three read the exact
	// same assembly, just on different triggers (poll, connect, tick).
	snapshotFn := buildSnapshot(st, dc, ur, gp, nv, registry.Sources, fakeMetas, fakeDiskMeta, dispatcher, insightEngine, pr.Tier)
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
		Storage:    buildContainerStorage(dc, ur, st, fakeMetas, fakeDiskMeta, fakeDeviceLabels, fakeSharePlacement, sysRoot),
		Settings:   settingsAdapter{st: st, cfg: cfg},
		Groups:     groupsAdapter{st: st},

		Images:       buildImages(imagesSrc),
		RemoveImages: buildRemoveImages(removeImagesSrc),
		PruneImages:  buildPruneImages(pruneImagesSrc),

		ContainersMaintenance: buildContainersMaintenance(containersMaintenanceSrc),
		RemoveContainers:      buildRemoveContainers(removeContainersSrc),
		PruneContainers:       buildPruneContainers(pruneContainersSrc),

		Alerts:   alertsAdapter{st: st, dispatcher: dispatcher},
		Webhooks: webhooksAdapter{st: st, envLocked: webhookURLEnv != ""},
		Insights: insightsAdapter{st: st, engine: insightEngine, pressureTier: pr.Tier},

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
// never surfaced here), seeded with every container the registry
// currently knows about, running or stopped, from dc.All() (plus
// fakeMetas' synthetic fleet, when GANTRY_FAKE_DATA=1 -- see
// fake.Generator.Metas' doc for why that's needed at all) so a container
// with no metrics yet still appears, plus ur.Version() and sources()
// (moved into the frame in v2 so an SSE client sees a collector degrade
// live, not just on its next healthz poll).
//
// fakeMetas is nil outside fake-data mode; when non-nil its entries are
// treated exactly like dc.All()'s -- unconditionally seeded, not a mere
// lookup fallback -- so the fake fleet survives the same filter a real
// one does. fakeDiskMeta is its disk-metadata analogue: ur.DiskMeta()
// (a real box's own unraid collector) is merged into dto.DiskMeta first,
// then fakeDiskMeta's entries on top when wired -- see server.DiskMetaDTO's
// own doc for why disk type/device strings ride their own map rather than
// Disks' numeric one.
//
// A container's METRICS are still filtered even though its identity/meta
// isn't: a "container"-kind sample only lands in dto.Containers when its
// entity is one buildSnapshot just seeded (dc.All() no longer knows the
// name at all once it's actually removed, not merely stopped) AND the
// sample itself is younger than containerFrameMaxAge -- a stopped
// container's last-recorded cpu/mem/etc. reading must not go on reading
// as "current" forever just because the container entry itself sticks
// around.
//
// gp/nv's own GPUMeta() calls merge into dto.GPUMeta the same "each
// source populates its own entities" way as DiskMeta above -- the DRM
// path (gp, pdev-keyed) and the nvidia-smi path (nv, fixed "nvidia0")
// never share an entity id, so there's no real first/overlay ordering
// concern the way fakeDiskMeta has, just two independent merges.
//
// dispatcher feeds dto.Alerts (buildAlertsBlock): the same firing-instance
// and channel-health data GET /api/alerts serves on demand, assembled
// fresh every tick so an SSE client sees an alert fire/resolve/channel
// degrade live rather than on its next poll.
func buildSnapshot(st *store.Store, dc *docker.Collector, ur *unraid.Collector, gp *gpu.Collector, nv *gpu.NvidiaCollector, sources func() map[string]string, fakeMetas func() []docker.Meta, fakeDiskMeta func() map[string]unraid.DiskMeta, dispatcher *alert.Dispatcher, insightEngine *insight.Engine, pressureTier func() string) func() server.SnapshotDTO {
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
			GPUMeta:       map[string]server.GPUMetaDTO{},
			Sources:       map[string]string{},
		}
		for pdev, m := range gp.GPUMeta() {
			dto.GPUMeta[pdev] = server.GPUMetaDTO{Vendor: m.Vendor, Driver: m.Driver}
		}
		for entity, m := range nv.GPUMeta() {
			dto.GPUMeta[entity] = server.GPUMetaDTO{Vendor: m.Vendor, Driver: m.Driver}
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

		metas := dc.All()
		if fakeMetas != nil {
			metas = append(metas, fakeMetas()...)
		}
		for _, m := range metas {
			dto.Containers[m.Name] = server.ContainerDTO{
				State:          m.State,
				Health:         m.Health,
				Image:          m.Image,
				Icon:           m.Icon,
				Created:        metaCreatedUnix(m.Created),
				ComposeProject: m.ComposeProject,
				Cpuset:         m.Cpuset,
				ExitCode:       m.ExitCode,
				UpdateStatus:   m.UpdateStatus,
				ChangelogURL:   m.ChangelogURL,
				ProjectURL:     m.ProjectURL,
				WebUIURL:       m.WebUIURL,
				Networks:       convertNetworks(m.Networks),
				Ports:          convertPorts(m.Ports),
				Metrics:        map[string]float64{},
			}
		}

		live := st.Live()
		snap := live.SnapshotLatest()
		nowUnix := dto.TS // same instant as the frame's own timestamp, one call

		for key, sample := range snap {
			if strings.HasPrefix(key.Metric, "live:") {
				continue
			}
			switch key.Kind {
			case "host":
				dto.Host[key.Metric] = sample.Val
			case "container":
				if _, ok := dto.Containers[key.Entity]; !ok {
					continue // not a container the registry (or fakeMetas) knows about at all -- fully removed, or never real
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
		dto.Insights = buildInsightsBlock(st, insightEngine, pressureTier)
		return dto
	}
}

// snapshotFrameContext is the fixed background context every
// buildSnapshot tick's store reads run under -- alerts' own and, since
// Phase 5, insights' own (buildInsightsBlock): the closure this feeds
// (server.Options.Snapshot/Current, and the 2s publish loop) has no
// per-call caller context of its own to thread through, the same
// reasoning the shutdown flush at the bottom of run() already documents
// for its own context.Background() use.
var snapshotFrameContext = context.Background()

// buildAlertsBlock assembles SnapshotDTO.Alerts (Task 8): every FIRING
// instance (pending excluded -- engine bookkeeping, not user-facing, the
// same rule GET /api/alerts' own handler applies), capped at
// server.AlertsFrameCap with a truncated count, plus every channel's
// current health. A read error is logged and treated as empty for this
// one tick, never fatal to the frame -- the same "degrade, don't error"
// posture Sources already models, and the next 2s tick tries again.
func buildAlertsBlock(st *store.Store, dispatcher *alert.Dispatcher) server.AlertsBlockDTO {
	rules, err := st.AlertRules(snapshotFrameContext)
	if err != nil {
		log.Println("alerts frame: rules:", err)
	}
	ruleByID := make(map[string]store.AlertRule, len(rules))
	for _, r := range rules {
		ruleByID[r.ID] = r
	}

	active, err := st.ActiveAlertInstances(snapshotFrameContext)
	if err != nil {
		log.Println("alerts frame: active instances:", err)
	}
	silences, err := st.Silences(snapshotFrameContext, time.Now().Unix())
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
			Value: inst.Value, Threshold: inst.Threshold, Summary: inst.Summary, FiredAt: inst.FiredAt,
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

// buildInsightsBlock assembles SnapshotDTO.Insights (Phase 5 Task 9):
// every active finding via server.ToInsightDTO with includeEvidence
// false (Task 9's own frame contract: "statements included, evidence
// excluded"; the evidence drawer fetches the full bundle on demand
// through GET /api/insights/{id}), plus the live pressure tier and the
// engine's own dropped-by-cap count. A read error is logged and treated
// as empty for this one tick, never fatal to the frame -- the exact
// buildAlertsBlock posture just above.
func buildInsightsBlock(st *store.Store, engine *insight.Engine, pressureTier func() string) server.InsightsBlockDTO {
	active, err := st.ActiveInsights(snapshotFrameContext)
	if err != nil {
		log.Println("insights frame: active instances:", err)
	}
	out := make([]server.InsightDTO, len(active))
	for i, inst := range active {
		out[i] = server.ToInsightDTO(inst, false)
	}
	tier := "proxy"
	if pressureTier != nil {
		tier = pressureTier()
	}
	suppressed := 0
	if engine != nil {
		suppressed = engine.Dropped()
	}
	return server.InsightsBlockDTO{Active: out, Tier: tier, Suppressed: suppressed}
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

// buildInsightSlots returns the closure wired to insight.Engine.Slots:
// joins diskMeta's own Device name with each slot's live
// disk.<slot>.rotational reading, exactly matching the assembly
// insight.SlotMeta's own doc describes the caller as responsible for.
// diskMeta is real first, fakeDiskMeta's overlay layered on top when
// wired -- the same real-then-fake merge convention buildSnapshot's own
// DiskMeta merge and buildContainerStorage's diskMeta parameter already
// use, needed here for the identical reason: fake-data mode's dev box
// never has a real disks.ini for the real unraid.Collector to report
// anything from, so its own DiskMeta() comes back empty and the fake
// generator's synthetic array is the only source with anything to
// report. Rotational, unlike Device/Kind, needs no such overlay: fake
// mode's own disk.<slot>.rotational sample lands in the exact same
// store.Live this reads from, real collector or not. Extracted out of
// run() (the same reason wireDockerCollector/buildClassOf/buildFleet all
// are) so this join -- easy to get backwards, e.g. reading Kind where
// Device belongs -- is unit-testable without a live unraid.Collector.
func buildInsightSlots(diskMeta func() map[string]unraid.DiskMeta, fakeDiskMeta func() map[string]unraid.DiskMeta, live *store.Live) func() map[string]insight.SlotMeta {
	return func() map[string]insight.SlotMeta {
		merged := make(map[string]unraid.DiskMeta)
		for slot, m := range diskMeta() {
			merged[slot] = m
		}
		if fakeDiskMeta != nil {
			for slot, m := range fakeDiskMeta() {
				merged[slot] = m
			}
		}
		out := make(map[string]insight.SlotMeta, len(merged))
		for slot, meta := range merged {
			rotational := false
			if s, ok := live.Latest(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "rotational"}); ok {
				rotational = s.Val != 0
			}
			out[slot] = insight.SlotMeta{Device: meta.Device, Rotational: rotational}
		}
		return out
	}
}

// alertRulesByID indexes rules by ID -- the small lookup
// resyncFastModeAlertRules needs against the current boot's own
// store.DefaultAlertRules(fakeMode).
func alertRulesByID(rules []store.AlertRule) map[string]store.AlertRule {
	out := make(map[string]store.AlertRule, len(rules))
	for _, r := range rules {
		out[r.ID] = r
	}
	return out
}

// alertSeededFastModeSettingsKey stores which mode --
// strconv.FormatBool(fakeMode), "true" or "false" -- resyncFastModeAlertRules
// last actually resynced every builtin threshold rule's for_seconds/
// clear_seconds FOR. A bare bool needs no JSON envelope, so unlike
// webhookTargetsSettingsKey's []alert.WebhookTarget blob below, this
// setting's value is just that string itself.
const alertSeededFastModeSettingsKey = "alert.seeded_fast_mode"

// resyncFastModeAlertRules keeps every BUILTIN THRESHOLD rule's
// for_seconds/clear_seconds in step with the CURRENT boot's mode by
// recording the fact directly instead of inferring it from a rule's own
// numbers (F1, review -- a correction to I5's own fix, in blame history
// just above this doc): I5's version resynced a rule only when its
// current for_seconds/clear_seconds exactly matched what the OTHER
// mode's compiled default would have seeded for that id, reasoning that
// such a match could only be a leftover seed from a boot under the
// other mode. That guess is wrong in both directions: a real-box user
// who deliberately tunes a rule to for_seconds=60 (a perfectly
// reasonable "alert after a minute") collides with fake mode's own
// compressed 60s constant and gets silently reverted on the very next
// real-mode reboot; symmetrically, a fake-mode user who wants one rule
// to run at real timing gets recompressed right back down.
//
// alertSeededFastModeSettingsKey fixes this by recording which mode
// this function last actually synced FOR, so a rule's numbers are only
// ever touched when that stored marker DISAGREES with the current
// boot's mode -- i.e. exactly a real<->fake flip, the one event that
// really is supposed to force every builtin threshold rule to the new
// mode's numbers (that's what flipping demo mode means). A same-mode
// reboot -- the overwhelmingly common case -- reads the marker, finds
// it already matches, and returns immediately having read or written
// not one alert_rules row: provably incapable of reverting a tuned
// value, because that path never looks at a single rule's for_seconds/
// clear_seconds at all.
//
// First boot (no marker stored yet) is deliberately NOT treated as a
// disagreement, even if the existing rows happen to mismatch the
// current mode (e.g. a database from before this marker existed): the
// contract is simply "no marker means stamp one, full stop" --
// SeedAlertRules, called immediately before this on every boot, already
// writes the current mode's own compiled defaults for any rule id that
// doesn't exist yet, which covers the actually-fresh-database case this
// is designed for. The marker is stamped either way, so the NEXT boot
// has something to compare against.
func resyncFastModeAlertRules(st *store.Store, fakeMode bool) error {
	want := strconv.FormatBool(fakeMode)
	prev, ok, err := st.SettingGet(alertSeededFastModeSettingsKey)
	if err != nil {
		return err
	}
	if ok && prev == want {
		return nil // same mode as last sync -- nothing to resync, no rule touched
	}
	if ok {
		// A genuine mode flip: force every builtin threshold rule to the
		// new mode's numbers, tuned or not -- see doc above.
		rules, err := st.AlertRules(context.Background())
		if err != nil {
			return err
		}
		defaults := alertRulesByID(store.DefaultAlertRules(fakeMode))
		for _, r := range rules {
			if !r.Builtin || r.Type != "threshold" {
				continue
			}
			d, dok := defaults[r.ID]
			if !dok {
				continue
			}
			r.ForSeconds, r.ClearSeconds = d.ForSeconds, d.ClearSeconds
			if err := st.UpsertAlertRule(r); err != nil {
				return fmt.Errorf("resync alert rule %q for_seconds/clear_seconds: %w", r.ID, err)
			}
		}
	}
	return st.SettingSet(alertSeededFastModeSettingsKey, want)
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

// fakeWebhookOKTargetID/fakeWebhookFailTargetID name Task 9's two demo
// webhook targets -- fixed ids (not user-editable identity, the same
// "stable slug" convention alert_rules.id uses) so an idempotent re-seed
// on a later boot recognizes them.
const (
	fakeWebhookOKTargetID   = "fake-ok"
	fakeWebhookFailTargetID = "fake-fail"
)

// seedFakeWebhookTargets inserts Task 9's two fake-mode demo webhook
// targets -- one pointing at Gantry's OWN healthz endpoint (loopback,
// always 200, so the SUCCESS path, its delivery-ledger row, and the
// Settings channels card's "ok" reading all render with no external
// service), one at a guaranteed-unreachable loopback port (1 is never a
// listening service) so the FAILURE path, its ledger row, and the
// channel card's failure text render just as reliably. Idempotent and
// insert-only, the same convention SeedAlertRules/
// seedWebhookTargetFromEnv already use: a target already present (this
// boot or an earlier one) is left completely untouched, so a demo
// session's own edits to either one survive a restart.
//
// server.go's healthz route deliberately answers any HTTP method, not
// just GET, specifically so this loopback POST succeeds -- a health
// check is read-only and side-effect-free regardless of verb, and
// restricting it to GET would make this the one demo target that could
// never actually succeed.
func seedFakeWebhookTargets(st *store.Store, port int) error {
	targets, err := loadWebhookTargets(st)
	if err != nil {
		return err
	}
	existing := make(map[string]bool, len(targets))
	for _, t := range targets {
		existing[t.ID] = true
	}

	changed := false
	if !existing[fakeWebhookOKTargetID] {
		targets = append(targets, alert.WebhookTarget{
			ID: fakeWebhookOKTargetID, Name: "Fake mode: always succeeds",
			URL:     fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port),
			Enabled: true, TimeoutS: 5,
		})
		changed = true
	}
	if !existing[fakeWebhookFailTargetID] {
		targets = append(targets, alert.WebhookTarget{
			ID: fakeWebhookFailTargetID, Name: "Fake mode: always fails",
			URL: "http://127.0.0.1:1/dead", Enabled: true, TimeoutS: 2,
		})
		changed = true
	}
	if !changed {
		return nil
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

// resolveNotifyDir picks the notify-spool directory buildDispatcher's
// channel writes to: GANTRY_NOTIFY_DIR when set (real mode and fake mode
// alike -- an operator's explicit choice always wins), else "/notify"
// (the CA template's own mount point) in real mode, else -- fake mode
// only, Task 9's own contract -- a fresh temp directory, so the demo has
// somewhere to write with zero configuration and the Alerts view's
// channel strip reads "ok" rather than the unmounted-spool hint on a
// box with no real /notify mount at all. The directory is never cleaned
// up by this process; it's a demo aid for the life of one run, not a
// durable path, and the OS reclaims temp storage on its own schedule.
func resolveNotifyDir(getenv func(string) string, fakeMode bool) (string, error) {
	if v := getenv("GANTRY_NOTIFY_DIR"); v != "" {
		return v, nil
	}
	if !fakeMode {
		return "/notify", nil
	}
	dir, err := os.MkdirTemp("", "gantry-fake-notify-")
	if err != nil {
		return "", fmt.Errorf("create fake notify dir: %w", err)
	}
	log.Printf("fake data mode: notify spool at %s", dir)
	return dir, nil
}

// buildDispatcher assembles the alert.Dispatcher wired to
// alert.Engine.Dispatch: the notify-spool channel is always present
// (GANTRY_NOTIFY_DIR, default /notify -- the CA template's own mount
// point in real mode; fake mode defaults it to a fresh temp dir instead
// -- see fakeNotifyDir's own doc, Task 9), plus one Channel per enabled,
// valid configured webhook target. alert.link_base and alert.
// notify_resolved are read fresh on every call through their own
// closures -- a settings change takes effect on the very next dispatch,
// no restart, the same resolved-fresh-every-tick posture the retention
// settings above use.
func buildDispatcher(st *store.Store, cfg *config.Config, getenv func(string) string, version string, fakeMode bool) (*alert.Dispatcher, error) {
	notifyDir, err := resolveNotifyDir(getenv, fakeMode)
	if err != nil {
		return nil, err
	}
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
// real mode). fakeDiskMeta/fakeDeviceLabels are the same fake-data
// overlay convention buildSnapshot's own DiskMeta merge uses (real
// first, fake entries layered on top): fakeDiskMeta lets a fake
// container's device rows join the demo fleet's own slot names (e.g.
// nvme0n1 -> rocket_pool) through the exact same unraid.
// ResolveDeviceLabel path a real box uses; fakeDeviceLabels covers the
// one thing that path can't fake at all -- a loop device's backing_file
// lives on a real host filesystem fake-data mode never has -- see fake.
// Generator.DeviceLabels' own doc. sysRoot is threaded straight through
// to ResolveDeviceLabel, unused by every other call in this function.
func buildContainerStorage(
	dc *docker.Collector,
	ur *unraid.Collector,
	st *store.Store,
	fakeMetas func() []docker.Meta,
	fakeDiskMeta func() map[string]unraid.DiskMeta,
	fakeDeviceLabels func() map[string]unraid.DeviceLabel,
	fakeSharePlacement func() map[string]unraid.SharePlacement,
	sysRoot string,
) func(name string) (server.StorageDTO, bool) {
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
		return containerStorage(lookup, ur.Slots, ur.DiskMeta, fakeDiskMeta, ur.SharePlacement, fakeSharePlacement, fakeDeviceLabels, sysRoot, st.Live(), name, time.Now().Unix())
	}
}

func containerStorage(
	lookupMeta func(string) (docker.Meta, bool),
	poolSlots func() []string,
	diskMeta func() map[string]unraid.DiskMeta,
	fakeDiskMeta func() map[string]unraid.DiskMeta,
	sharePlacement func() map[string]unraid.SharePlacement,
	fakeSharePlacement func() map[string]unraid.SharePlacement,
	fakeDeviceLabels func() map[string]unraid.DeviceLabel,
	sysRoot string,
	live *store.Live,
	name string,
	now int64,
) (server.StorageDTO, bool) {
	meta, ok := lookupMeta(name)
	if !ok {
		return server.StorageDTO{}, false
	}

	// placements: real first, fakeSharePlacement's overlay on top -- same
	// merge order/rationale as knownDevices' disk_meta assembly just below
	// (and buildSnapshot's own disk_meta merge).
	placements := sharePlacement()
	if fakeSharePlacement != nil {
		for name, p := range fakeSharePlacement() {
			placements[name] = p
		}
	}

	pools := poolSlots()
	mounts := make([]server.MountDTO, 0, len(meta.Mounts))
	for _, m := range meta.Mounts {
		ref := unraid.ResolveStoragePath(m.Source, pools)
		dto := server.MountDTO{
			Source:      m.Source,
			Destination: m.Destination,
			RW:          m.RW,
			Storage:     server.StorageRefDTO{Kind: ref.Kind, Name: ref.Name},
		}
		if ref.Kind == "share" {
			if p, ok := placements[ref.Name]; ok {
				dto.Storage.Placement = &server.SharePlacementDTO{Mode: p.Mode, Pool: p.Pool}
			}
		}
		mounts = append(mounts, dto)
	}

	// meta (real) first, fakeDiskMeta's overlay on top -- same merge
	// order/rationale as buildSnapshot's own disk_meta assembly.
	knownDevices := diskMeta()
	if fakeDiskMeta != nil {
		for slot, m := range fakeDiskMeta() {
			knownDevices[slot] = m
		}
	}

	devices := deviceIOFromSamples(live.LatestByMetricPrefix("container", name, "live:io."), now)
	var fakeLabels map[string]unraid.DeviceLabel
	if fakeDeviceLabels != nil {
		fakeLabels = fakeDeviceLabels()
	}
	for i := range devices {
		label := unraid.ResolveDeviceLabel(devices[i].Device, sysRoot, knownDevices)
		if override, ok := fakeLabels[devices[i].Device]; ok {
			label = override
		}
		devices[i].Label, devices[i].Kind = label.Label, label.Kind
	}

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

// groupsSettingsKey is the one settings-table row every saved group
// lives under -- a single JSON-encoded blob, not one row per group
// (there's no per-field env-override dance to support the way
// retention's four keys have, so there's no reason to split it up).
const groupsSettingsKey = "groups"

// groupsAdapter implements server.GroupsIface: unlike settingsAdapter,
// this talks straight to st.SettingGet/SettingSet with no *config.Config
// in between -- groups are plain user data, not a tunable with an env
// var equivalent, so there's nothing for config's env>settings>default
// precedence to resolve. JSON marshal/unmarshal happens here, in main,
// the same "server package stays store-shape-agnostic" reasoning
// buildTop/buildContainersList/settingsAdapter itself already follow.
type groupsAdapter struct {
	st *store.Store
}

func (a groupsAdapter) Get() ([]server.Group, error) {
	raw, ok, err := a.st.SettingGet(groupsSettingsKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []server.Group{}, nil
	}
	var groups []server.Group
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (a groupsAdapter) Set(groups []server.Group) error {
	raw, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	return a.st.SettingSet(groupsSettingsKey, string(raw))
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

func (a alertsAdapter) SaveRules(rules []store.AlertRule) error { return a.st.SaveAlertRules(rules) }

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

// insightsAdapter implements server.InsightsIface (Phase 5 Task 9) over
// *store.Store plus the running *insight.Engine's own Dropped() and the
// pressure collector's Tier() -- kept in main, not the server package,
// the same reason alertsAdapter is: server stays store/engine-shape-
// agnostic. engine/pressureTier are both read live on every call rather
// than snapshotted once at construction, since both can change across
// this adapter's lifetime (Dropped() grows every tick; Tier() flips if
// /proc/pressure ever appears or disappears).
type insightsAdapter struct {
	st           *store.Store
	engine       *insight.Engine
	pressureTier func() string
}

func (a insightsAdapter) Active(ctx context.Context) ([]store.InsightInstance, error) {
	return a.st.ActiveInsights(ctx)
}

func (a insightsAdapter) ByID(ctx context.Context, id int64) (store.InsightInstance, bool, error) {
	return a.st.InsightByID(ctx, id)
}

func (a insightsAdapter) History(ctx context.Context, from, to int64, limit int) ([]store.InsightInstance, error) {
	return a.st.InsightHistory(ctx, from, to, limit)
}

func (a insightsAdapter) RuleConfigs(ctx context.Context) ([]store.InsightRuleConfig, error) {
	return a.st.InsightRuleConfigs(ctx)
}

func (a insightsAdapter) SaveRuleConfig(c store.InsightRuleConfig) error {
	return a.st.UpsertInsightRuleConfig(c)
}

func (a insightsAdapter) AddDismissal(d store.InsightDismissal) (int64, error) {
	return a.st.AddInsightDismissal(d)
}

func (a insightsAdapter) Resolve(id, at int64, reason string) error {
	return a.st.ResolveInsight(id, at, reason)
}

func (a insightsAdapter) Tier() string {
	if a.pressureTier == nil {
		return "proxy"
	}
	return a.pressureTier()
}

func (a insightsAdapter) Suppressed() int {
	if a.engine == nil {
		return 0
	}
	return a.engine.Dropped()
}

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

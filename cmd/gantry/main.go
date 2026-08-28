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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg := config.New(st, getenv)
	port := cfg.Int("port", 8380)

	var wg sync.WaitGroup

	// fakeMetas/fakeDiskMeta, when fake-data mode is on, are threaded into
	// buildSnapshot/buildContainersList below so the fake fleet is
	// treated exactly like dc.Running()'s real entries (Task 11's
	// ledger-carried fix -- see fake.Generator.Metas' own doc for why
	// that's required at all: this generator writes samples straight to
	// the store, never touching dc's registry, and disks.go's own
	// unraid.Collector similarly never sees a real disks.ini in fake
	// mode). fakeDiskMeta mirrors fakeMetas' shape for disk device/type
	// metadata (see fake.Generator.DiskMetas' own doc).
	var fakeMetas func() []docker.Meta
	var fakeDiskMeta func() map[string]unraid.DiskMeta
	if cfg.Bool("fake_data", false) {
		log.Println("fake data mode: synthesizing a demo fleet")
		fk := fake.New(st, st, time.Now().UnixNano())
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

	// snapshotFn is the one buildSnapshot instance shared by /api/live/
	// snapshot (Options.Snapshot), /api/live's connect frame (Options.
	// Current), and the publish loop below -- all three read the exact
	// same assembly, just on different triggers (poll, connect, tick).
	snapshotFn := buildSnapshot(st, dc, ur, registry.Sources, fakeMetas, fakeDiskMeta)
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
func buildSnapshot(st *store.Store, dc *docker.Collector, ur *unraid.Collector, sources func() map[string]string, fakeMetas func() []docker.Meta, fakeDiskMeta func() map[string]unraid.DiskMeta) func() server.SnapshotDTO {
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
		return dto
	}
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

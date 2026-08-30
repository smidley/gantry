package gpu

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const (
	// nvidiaTickInterval is 15s, not the 2s most collectors use: each tick
	// execs nvidia-smi up to twice (query-gpu, query-compute-apps), and at
	// 2s that's roughly 10x the intended CPU budget on Nvidia boxes.
	nvidiaTickInterval = 15 * time.Second
	nvidiaEntity       = "nvidia0" // v1: single-GPU assumption, matching spec §4.4's per-container scope
)

// NvidiaCollector polls `nvidia-smi` (present only when the container was
// started with `--runtime=nvidia` — the Nvidia container runtime injects
// the binary and driver libraries at startup) for host GPU
// utilization/memory and per-process VRAM, attributing processes to
// containers via /proc/<pid>/cgroup — the same PID→container mapping the
// DRM fdinfo path (collector.go) uses. Name "nvidia", Interval 15s.
//
// v1 scope (spec §4.4): per-container Nvidia data is VRAM + presence
// only. CSV output has no per-process SM-utilization column, so
// per-container busy_pct isn't available this way; host-level
// utilization.gpu is. A later phase can add NVML for the finer-grained
// figure if demanded — exec keeps today's static, cgo-free build.
//
// Hardware-unvalidated: no Nvidia GPU was available during development.
// The CSV parsers below are unit-tested against documented nvidia-smi
// output shapes; the exec path itself has only run against fixtures, so
// Tick logs a one-line notice the first time it completes successfully.
type NvidiaCollector struct {
	sink     store.MetricSink
	procRoot string
	lookup   func(id string) (name string, ok bool)

	// SysRoot is where the host's /sys is mounted (default "/host/sys",
	// overridden by main wiring after New -- see docker.Collector's
	// CgroupRoot for the same pattern) -- Probe reads it to tell "no
	// Nvidia GPU on this box at all" apart from "GPU present, nvidia-smi
	// isn't" (see Probe's own doc).
	SysRoot string

	loggedHW sync.Once
}

var _ collect.Collector = (*NvidiaCollector)(nil)

// NewNvidia constructs the nvidia-smi collector. lookup resolves a docker
// container id to its current name (docker.Collector.Lookup); a process
// that doesn't resolve to a running container is skipped, not
// host-bucketed — v1 has no meaningful "host GPU process" series for
// compute-apps (contrast the DRM path, which does bucket host clients).
func NewNvidia(sink store.MetricSink, procRoot string, lookup func(string) (string, bool)) *NvidiaCollector {
	return &NvidiaCollector{sink: sink, procRoot: procRoot, lookup: lookup, SysRoot: "/host/sys"}
}

func (c *NvidiaCollector) Name() string            { return "nvidia" }
func (c *NvidiaCollector) Interval() time.Duration { return nvidiaTickInterval }

// Probe looks for nvidia-smi on PATH. Absence splits into two distinct
// cases (Scott's own report: "I don't have an nvidia GPU, so this
// shouldn't be showing up for me" -- the SourcesBanner hint was showing
// regardless): no Nvidia GPU detected on this box at all (hasPCIVendor
// scans sysRoot/bus/pci/devices for vendor 0x10de) is Status.
// NotApplicable — nothing to fix, so the banner should stay silent (see
// its own doc) — while a genuine Nvidia GPU with no working nvidia-smi
// integration keeps today's existing, actionable Detail unchanged.
func (c *NvidiaCollector) Probe(context.Context) collect.Status {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		if !hasPCIVendor(c.SysRoot, nvidiaVendorID) {
			return collect.Status{Available: false, NotApplicable: true, Detail: "no NVIDIA GPU detected"}
		}
		return collect.Status{Available: false, Detail: "no nvidia-smi on PATH — add --runtime=nvidia and NVIDIA_VISIBLE_DEVICES=all to the container to enable"}
	}
	return collect.Status{Available: true}
}

// GPUMeta returns nvidiaEntity's own fixed vendor+driver -- known
// outright (this collector only ever runs against Nvidia hardware, by
// construction) rather than needing the sysfs vendor lookup the DRM
// path's own per-pdev entities do (collector.go's own GPUMeta/EntityMeta).
func (c *NvidiaCollector) GPUMeta() map[string]EntityMeta {
	return map[string]EntityMeta{nvidiaEntity: {Vendor: "NVIDIA", Driver: "nvidia"}}
}

// Tick queries host GPU utilization/memory (hard requirement — a failure
// here fails the tick, same convention host.go/unraid.go use for their one
// required file) and per-process VRAM (best-effort: some driver/runtime
// combinations don't support the compute-apps query at all, and that
// shouldn't take down the host-level gauges).
func (c *NvidiaCollector) Tick(ctx context.Context, now time.Time) error {
	ts := now.Unix()

	gpuOut, err := runNvidiaSMI(ctx, "--query-gpu=utilization.gpu,memory.used", "--format=csv,noheader,nounits")
	if err != nil {
		return fmt.Errorf("nvidia: query-gpu: %w", err)
	}
	utilPct, memMiB, ok := parseGPUUtil(gpuOut)
	if !ok {
		return fmt.Errorf("nvidia: query-gpu: unparsable output %q", gpuOut)
	}
	c.sink.Record(store.SeriesKey{Kind: "gpu", Entity: nvidiaEntity, Metric: "engine.gpu.busy_pct"}, ts, utilPct)
	c.sink.Record(store.SeriesKey{Kind: "gpu", Entity: nvidiaEntity, Metric: "mem.used_mib"}, ts, memMiB)

	if appsOut, err := runNvidiaSMI(ctx, "--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits"); err == nil {
		c.recordComputeApps(parseComputeApps(appsOut), ts)
	}

	c.loggedHW.Do(func() { log.Printf("nvidia collector is hardware-unvalidated") })
	return nil
}

// recordComputeApps attributes each already-parsed compute-app row to its
// owning container (via resolveOwner, shared with the DRM path in
// walker.go) and records its VRAM. Split out from Tick so the
// pid→container→sink pipeline is testable without exec'ing nvidia-smi.
func (c *NvidiaCollector) recordComputeApps(apps []computeApp, ts int64) {
	for _, app := range apps {
		owner := resolveOwner(c.procRoot, app.PID, c.lookup)
		if owner == "" {
			continue
		}
		c.sink.Record(store.SeriesKey{Kind: "container", Entity: owner, Metric: "gpu.nvidia.mem_mib"}, ts, app.MemMiB)
	}
}

// runNvidiaSMI execs nvidia-smi with the given args, bound to ctx, and
// returns its trimmed stdout. Unexercised by tests (no hardware to run
// it against); the CSV it produces is documented and stable across
// driver versions, which is what parseGPUUtil/parseComputeApps are
// fixture-tested against instead.
func runNvidiaSMI(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "nvidia-smi", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseGPUUtil parses `nvidia-smi --query-gpu=utilization.gpu,memory.used
// --format=csv,noheader,nounits` output, e.g. "23, 4096" (percent, MiB).
// Only the first line is considered — v1 assumes a single GPU
// (nvidiaEntity); multi-GPU hosts are a later phase.
func parseGPUUtil(out string) (utilPct, memMiB float64, ok bool) {
	fields := strings.Split(firstLine(out), ",")
	if len(fields) < 2 {
		return 0, 0, false
	}
	u, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return 0, 0, false
	}
	m, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
	if err != nil {
		return 0, 0, false
	}
	return u, m, true
}

// computeApp is one row of `nvidia-smi
// --query-compute-apps=pid,used_memory --format=csv,noheader,nounits`
// output.
type computeApp struct {
	PID    int
	MemMiB float64
}

// parseComputeApps parses every row of --query-compute-apps output, e.g.
// "1234, 2048\n5678, 512". A malformed row is skipped rather than failing
// the whole parse; no rows at all (no GPU processes running, so
// nvidia-smi prints nothing with --format=csv,noheader) yields a nil
// slice, not an error.
func parseComputeApps(out string) []computeApp {
	var apps []computeApp
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			continue
		}
		mem, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			continue
		}
		apps = append(apps, computeApp{PID: pid, MemMiB: mem})
	}
	return apps
}

// firstLine returns s up to (not including) its first newline, trimmed.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

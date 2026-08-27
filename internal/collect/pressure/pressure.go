// Package pressure reads PSI (pressure stall information) — a two-tier,
// feature-detected insights enabler (spec §16, spikes.md): stock Unraid
// ships PSI compiled in but disabled (CONFIG_PSI_DEFAULT_DISABLED), so
// Probe reports how to opt in rather than treating its absence as an
// error.
package pressure

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/store"
)

const tickInterval = 2 * time.Second

// psiDisabledDetail is the exact Probe Detail string the UI hint shows
// (spec §16 two-tier) when /proc/pressure is absent.
const psiDisabledDetail = "PSI disabled — add psi=1 to the syslinux append line to enable (optional; used by insights)"

// resource pairs one PSI resource's filename with the metric-name segment
// it's recorded under. The host file and the cgroup file are both named
// "memory", but every series (host and container alike) uses the
// shorter "mem" segment.
type resource struct {
	hostFile   string // e.g. "cpu", under procRoot/pressure/
	cgroupFile string // e.g. "cpu.pressure", under a container's cgroup dir
	metric     string // e.g. "cpu" -> psi.cpu.some_pct
}

var resources = []resource{
	{hostFile: "cpu", cgroupFile: "cpu.pressure", metric: "cpu"},
	{hostFile: "io", cgroupFile: "io.pressure", metric: "io"},
	{hostFile: "memory", cgroupFile: "memory.pressure", metric: "mem"},
}

// Collector reads PSI "some avg10" from /proc/pressure (host) and each
// running container's cgroup {cpu,io,memory}.pressure files. Name
// "pressure", Interval 2s.
type Collector struct {
	sink       store.MetricSink
	procRoot   string
	cgroupRoot string
	running    func() []docker.Meta
}

var _ collect.Collector = (*Collector)(nil)

// New constructs the pressure collector. running supplies the current
// running-container snapshot (Task 6's Collector.Running).
func New(sink store.MetricSink, procRoot, cgroupRoot string, running func() []docker.Meta) *Collector {
	return &Collector{sink: sink, procRoot: procRoot, cgroupRoot: cgroupRoot, running: running}
}

func (c *Collector) Name() string            { return "pressure" }
func (c *Collector) Interval() time.Duration { return tickInterval }

// Probe checks for /proc/pressure/io specifically as PSI's canary: on a
// kernel that has it enabled at all, Unraid exposes cpu/io/memory
// together.
func (c *Collector) Probe(context.Context) collect.Status {
	if _, err := os.Stat(filepath.Join(c.procRoot, "pressure", "io")); err != nil {
		return collect.Status{Available: false, Detail: psiDisabledDetail}
	}
	return collect.Status{Available: true}
}

// Tick records host PSI unconditionally (each resource independently —
// a single missing file doesn't block the others) and per-container PSI
// for every currently-running container, skipping any resource whose
// cgroup file is absent (partial PSI exists on some kernels).
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	ts := now.Unix()

	for _, r := range resources {
		pct, ok := readSomeAvg10(filepath.Join(c.procRoot, "pressure", r.hostFile))
		if !ok {
			continue
		}
		c.sink.Record(store.SeriesKey{Kind: "host", Metric: "psi." + r.metric + ".some_pct"}, ts, pct)
	}

	for _, m := range c.running() {
		for _, r := range resources {
			pct, ok := readSomeAvg10(filepath.Join(c.cgroupRoot, "docker", m.ID, r.cgroupFile))
			if !ok {
				continue
			}
			c.sink.Record(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "psi." + r.metric + ".some_pct"}, ts, pct)
		}
	}
	return nil
}

// readSomeAvg10 parses one PSI file's first ("some") line and extracts
// avg10. ok=false on any read or parse failure (missing file, unexpected
// format) — every call site treats that as "skip this series", not an
// error.
func readSomeAvg10(path string) (float64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, false
	}
	return parseSomeLine(sc.Text())
}

// parseSomeLine extracts avg10 from one PSI "some ..." line, e.g.
// "some avg10=1.23 avg60=4.56 avg300=7.89 total=12345678". The second
// ("full") line is a different line entirely and is never passed here by
// readSomeAvg10 (which only scans the first line) — but parseSomeLine
// also rejects it directly (via the leading-token check) so it can be
// unit-tested on its own.
func parseSomeLine(line string) (float64, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "some" {
		return 0, false
	}
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k != "avg10" {
			continue
		}
		val, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return val, true
	}
	return 0, false
}

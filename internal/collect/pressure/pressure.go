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

// Collector reads PSI "some"/"full" avg10 from /proc/pressure (host) and
// each running container's cgroup {cpu,io,memory}.pressure files. Name
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

// psiAvailable reports whether the kernel exposes PSI at all, checking
// /proc/pressure/io as PSI's canary: on a kernel that has it enabled at
// all, Unraid exposes cpu/io/memory together. Probe and Tier both defer
// to this one check so the two can never disagree about availability.
func (c *Collector) psiAvailable() bool {
	_, err := os.Stat(filepath.Join(c.procRoot, "pressure", "io"))
	return err == nil
}

// Probe checks for /proc/pressure/io specifically as PSI's canary: on a
// kernel that has it enabled at all, Unraid exposes cpu/io/memory
// together.
func (c *Collector) Probe(context.Context) collect.Status {
	if !c.psiAvailable() {
		return collect.Status{Available: false, Detail: psiDisabledDetail}
	}
	return collect.Status{Available: true}
}

// Tier reports which evidence tier is currently live: "psi" once the
// kernel exposes pressure stall information, "proxy" on stock Unraid's
// default (PSI compiled in but disabled). Callers use this typed value
// to report what enabling psi=1 would add, instead of string-matching
// Probe's hint text.
func (c *Collector) Tier() string {
	if c.psiAvailable() {
		return "psi"
	}
	return "proxy"
}

// Tick records host and container PSI unconditionally (each resource,
// and each of its "some"/"full" lines, independently -- one missing
// file or line never blocks the rest). "full" tracks the share of time
// every non-idle task was stalled simultaneously, which is a strictly
// stronger claim than "some" (at least one task stalled).
//
// /proc/pressure/cpu has no "full" line at the host level: the kernel
// never emits one there, by definition -- the host's own idle task is
// always eligible to run when nothing else can, so the CPU can never be
// *fully* stalled system-wide. That falls out naturally here: the same
// recordLine call that skips a missing file also skips a missing line
// inside a file it opened fine, so there's no special case for cpu vs.
// io/memory, or for host vs. container.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	ts := now.Unix()

	for _, r := range resources {
		path := filepath.Join(c.procRoot, "pressure", r.hostFile)
		c.recordLine(store.SeriesKey{Kind: "host", Metric: "psi." + r.metric + ".some_pct"}, path, "some", ts)
		c.recordLine(store.SeriesKey{Kind: "host", Metric: "psi." + r.metric + ".full_pct"}, path, "full", ts)
	}

	for _, m := range c.running() {
		for _, r := range resources {
			path := filepath.Join(c.cgroupRoot, "docker", m.ID, r.cgroupFile)
			c.recordLine(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "psi." + r.metric + ".some_pct"}, path, "some", ts)
			c.recordLine(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "psi." + r.metric + ".full_pct"}, path, "full", ts)
		}
	}
	return nil
}

// recordLine reads one line kind ("some" or "full") from a PSI file and
// records it under key if present. Absence -- a missing file, or a
// missing line inside a file that opened fine -- means no series, never
// a zero: a real 0%-stalled reading and "we don't have this number" are
// different facts, and the second must never be reported as the first.
func (c *Collector) recordLine(key store.SeriesKey, path, want string, ts int64) {
	pct, ok := readLineAvg10(path, want)
	if !ok {
		return
	}
	c.sink.Record(key, ts, pct)
}

// readLineAvg10 opens a PSI file and extracts avg10 from whichever line
// has want ("some" or "full") as its leading token, scanning every line
// rather than assuming position. ok=false on any read failure, a
// missing line, or an unparsable value.
func readLineAvg10(path, want string) (float64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if val, ok := parseLine(sc.Text(), want); ok {
			return val, true
		}
	}
	return 0, false
}

// parseLine extracts avg10 from one PSI line if its leading token
// matches want ("some" or "full"), e.g. "some avg10=1.23 avg60=4.56
// avg300=7.89 total=12345678". avg60/avg300/total stay unread -- the
// insight engine samples avg10 every tick across its own window, which
// is a better-resolved version of the same information. ok=false when
// the leading token doesn't match want or the avg10 field is
// missing/unparsable.
func parseLine(line, want string) (float64, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != want {
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

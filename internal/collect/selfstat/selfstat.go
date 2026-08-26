// Package selfstat reads Gantry's own /proc/self/{stat,statm} — the
// "receipt": how much CPU and memory Gantry itself spends to watch the
// box. Powers spec §2's budget check and the Settings-page receipt.
package selfstat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const (
	tickInterval = 10 * time.Second
	clkTck       = 100.0 // USER_HZ on Linux: /proc/<pid>/stat's utime/stime are in these jiffies
	pageSize     = 4096  // getconf PAGESIZE on Linux/amd64/arm64: /proc/<pid>/statm's fields are in pages
)

// Collector reads procRoot/self/stat (cpu.pct) and procRoot/self/statm
// (rss_bytes). Name "selfstat", Interval 10s.
type Collector struct {
	sink     store.MetricSink
	procRoot string
	rates    *collect.RateTracker
}

var _ collect.Collector = (*Collector)(nil)

// New constructs the selfstat collector. procRoot is normally "/proc" —
// "self" always resolves to this process regardless of which pid it's
// running as, so no pid needs to be threaded through.
func New(sink store.MetricSink, procRoot string) *Collector {
	return &Collector{sink: sink, procRoot: procRoot, rates: collect.NewRateTracker()}
}

func (c *Collector) Name() string            { return "selfstat" }
func (c *Collector) Interval() time.Duration { return tickInterval }

// Probe is unconditionally available: /proc/self is this process's own
// procfs entry, which exists for as long as the process does — there's no
// "unmounted" or "not running" failure mode to detect, unlike every other
// collector's host-provided data source.
func (c *Collector) Probe(context.Context) collect.Status {
	return collect.Status{Available: true}
}

// Tick's hard requirement is self/stat (matching every other collector's
// one-hard-file convention); self/statm is independent and degrades
// silently if unreadable or unparseable.
func (c *Collector) Tick(ctx context.Context, now time.Time) error {
	if err := c.tickCPU(now); err != nil {
		return err
	}
	c.tickRSS(now)
	return nil
}

func (c *Collector) tickCPU(now time.Time) error {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "self", "stat"))
	if err != nil {
		return fmt.Errorf("selfstat: open stat: %w", err)
	}
	utimeJiffies, stimeJiffies, err := parseSelfStat(string(data))
	if err != nil {
		return fmt.Errorf("selfstat: parse stat: %w", err)
	}

	cpuSeconds := float64(utimeJiffies+stimeJiffies) / clkTck
	if fraction, ok := c.rates.Rate("cpu", now, cpuSeconds); ok {
		c.sink.Record(store.SeriesKey{Kind: "host", Metric: "gantry.cpu_pct"}, now.Unix(), fraction*100)
	}
	return nil
}

func (c *Collector) tickRSS(now time.Time) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "self", "statm"))
	if err != nil {
		return
	}
	rssPages, err := parseSelfStatm(string(data))
	if err != nil {
		return
	}
	c.sink.Record(store.SeriesKey{Kind: "host", Metric: "gantry.rss_bytes"}, now.Unix(), float64(rssPages)*pageSize)
}

// parseSelfStat extracts utime/stime (fields 14 and 15, both in jiffies)
// from one /proc/<pid>/stat line. The comm field (2) is "(...)" and may
// itself contain spaces (or even, in principle, parentheses) — e.g. a
// binary renamed to "gantry helper" — so fields can't be split on
// whitespace from the start of the line; every field from state (3)
// onward is found by splitting only what comes after the LAST ')',
// which is always comm's closing paren (nothing after comm ever contains
// one).
func parseSelfStat(line string) (utimeJiffies, stimeJiffies uint64, err error) {
	line = strings.TrimSpace(line)
	closeParen := strings.LastIndex(line, ")")
	if closeParen < 0 {
		return 0, 0, fmt.Errorf("no closing paren around comm in %q", line)
	}

	rest := strings.Fields(line[closeParen+1:])
	// rest[0] is field 3 (state); utime is field 14, stime field 15.
	const utimeIdx, stimeIdx = 14 - 3, 15 - 3
	if len(rest) <= stimeIdx {
		return 0, 0, fmt.Errorf("only %d fields after comm, need > %d", len(rest), stimeIdx)
	}

	utimeJiffies, err = strconv.ParseUint(rest[utimeIdx], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse utime %q: %w", rest[utimeIdx], err)
	}
	stimeJiffies, err = strconv.ParseUint(rest[stimeIdx], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse stime %q: %w", rest[stimeIdx], err)
	}
	return utimeJiffies, stimeJiffies, nil
}

// parseSelfStatm extracts resident set size in pages (field 2) from one
// /proc/<pid>/statm line: "size resident shared text lib data dt".
func parseSelfStatm(line string) (rssPages uint64, err error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("statm: fewer than 2 fields in %q", line)
	}
	rssPages, err = strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("statm: parse resident %q: %w", fields[1], err)
	}
	return rssPages, nil
}

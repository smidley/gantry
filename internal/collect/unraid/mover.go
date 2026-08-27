package unraid

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// tickMover records whether Unraid's mover process is currently running.
func (c *Collector) tickMover(now time.Time) {
	running := 0.0
	if moverRunning(c.procRoot) {
		running = 1
	}
	c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "mover.running"}, now.Unix(), running)
}

// moverRunning scans procRoot/*/comm for a pid whose comm is exactly
// "mover" (the trailing newline every /proc/<pid>/comm file carries is
// trimmed before comparison, and the match must be exact — not a
// substring — so e.g. a hypothetical "movery" process wouldn't count).
func moverRunning(procRoot string) bool {
	matches, err := filepath.Glob(filepath.Join(procRoot, "*", "comm"))
	if err != nil {
		return false
	}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		if strings.TrimRight(string(data), "\n") == "mover" {
			return true
		}
	}
	return false
}

// tickUPS probes dir/ups.ini and, when present, records charge/load
// percentages from its battery.charge/ups.load keys. UPS support is
// feature-detected and best-effort: an absent file emits nothing and is
// not an error, and either key alone parsing successfully is enough to
// emit its own metric independent of the other.
func (c *Collector) tickUPS(now time.Time) {
	f, err := os.Open(filepath.Join(c.dir, "ups.ini"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	kv, err := ParseINI(f)
	if err != nil {
		return
	}
	v := kv[""]

	ts := now.Unix()
	if charge, ok := parseFloatOK(v["battery.charge"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "ups.charge_pct"}, ts, charge)
	}
	if load, ok := parseFloatOK(v["ups.load"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "ups.load_pct"}, ts, load)
	}
}

package unraid

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

// tickShares reads shares.ini and records each share's used space. There
// is one shares list per array, not a series-space per share, so the
// share name becomes part of the metric name itself
// (share.<name>.used_bytes) rather than a separate Entity — every share
// gauge lives on the same array-wide series (Kind "unraid", Entity
// "array") as the parity and mover metrics. Missing/unreadable
// shares.ini degrades silently.
func (c *Collector) tickShares(now time.Time) {
	f, err := os.Open(filepath.Join(c.dir, "shares.ini"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	kv, err := ParseINI(f)
	if err != nil {
		return
	}

	names := make([]string, 0, len(kv))
	for name := range kv {
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	ts := now.Unix()
	for _, name := range names {
		usedKB, ok := parseFloatOK(kv[name]["used"])
		if !ok {
			continue
		}
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share." + collect.SlugSegment(name) + ".used_bytes"}, ts, usedKB*1024)
	}
}

package unraid

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

// SharePlacement is one share's own cache-pool placement policy, read
// from shares.ini's useCache/cachePool fields (verified against a real
// Unraid 7.3.2 box capture, testdata/shares_real.ini) — Collector.
// SharePlacement() exposes the latest tick's own map for the storage
// endpoint to join onto its kind=share mount entries, mirroring
// DiskMeta's identical "set on tick under mu, read via a copying getter"
// shape (disks.go).
type SharePlacement struct {
	// Mode is useCache's own value verbatim: "yes" (cache, mover moves it
	// to the array), "no" (array only, cache never used), "only" (cache
	// only, no array copy ever), or "prefer" (kept on cache, mover moves
	// stragglers FROM the array back TO cache — the reverse of "yes").
	Mode string
	// Pool is cachePool's own pool name — "" when Mode is "no" (shares.ini
	// still carries a cachePool field even then, but it names whichever
	// pool a PRIOR useCache="yes" left behind, not where this share's
	// data lives now, so it's dropped rather than surfaced as if current).
	Pool string
}

// tickShares reads shares.ini and records each share's used space, plus
// its cache-pool placement. There is one shares list per array, not a
// series-space per share, so the share name becomes part of the metric
// name itself (share.<name>.used_bytes) rather than a separate Entity —
// every share gauge lives on the same array-wide series (Kind "unraid",
// Entity "array") as the parity and mover metrics. Missing/unreadable
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

	placement := make(map[string]SharePlacement, len(names))
	ts := now.Unix()
	for _, name := range names {
		if mode := kv[name]["useCache"]; mode != "" {
			pool := kv[name]["cachePool"]
			if mode == "no" {
				pool = "" // see SharePlacement.Pool's own doc — stale leftover, not current
			}
			placement[name] = SharePlacement{Mode: mode, Pool: pool}
		}

		usedKB, ok := parseFloatOK(kv[name]["used"])
		if !ok {
			continue
		}
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share." + collect.SlugSegment(name) + ".used_bytes"}, ts, usedKB*1024)
	}

	c.mu.Lock()
	c.sharePlacement = placement
	c.mu.Unlock()
}

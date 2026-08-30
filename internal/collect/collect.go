// Package collect defines the collector framework: the Collector contract
// every metric source implements, and the Registry that runs them with
// probe/backoff/panic isolation.
package collect

import (
	"context"
	"time"
)

// Status reports whether a collector's data source is currently reachable.
type Status struct {
	Available bool
	Detail    string // human hint: why unavailable / what to mount

	// NotApplicable marks an unavailable collector as expected-absent
	// hardware/environment (e.g. no Nvidia GPU on this box at all) rather
	// than a fixable misconfiguration (a mount to add, a flag to set) --
	// distinct from a plain Available=false, which the UI still surfaces
	// as an actionable hint (SourcesBanner). Registry.Sources() maps this
	// to a fixed sentinel instead of Detail's free text (see its own
	// doc) so the frontend can tell the two apart without parsing
	// Detail's own wording. Every existing collector's zero-value Status
	// (NotApplicable: false) is unaffected.
	NotApplicable bool
}

// Collector is one metric source (host, docker, unraid, gpu, ...).
type Collector interface {
	Name() string
	Interval() time.Duration
	Probe(ctx context.Context) Status              // cheap; called at start and every 60s while unavailable
	Tick(ctx context.Context, now time.Time) error // one collection pass; only called while available
}

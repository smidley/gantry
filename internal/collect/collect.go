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
}

// Collector is one metric source (host, docker, unraid, gpu, ...).
type Collector interface {
	Name() string
	Interval() time.Duration
	Probe(ctx context.Context) Status              // cheap; called at start and every 60s while unavailable
	Tick(ctx context.Context, now time.Time) error // one collection pass; only called while available
}

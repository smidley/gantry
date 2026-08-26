package collect

import (
	"strings"
	"time"
)

type rateSample struct {
	val float64
	ts  time.Time
}

// RateTracker converts monotonically-increasing counters into per-second
// rates across ticks. Shared by every counter-based collector (host,
// docker, gpu, ...); one tracker per collector instance — collectors tick
// sequentially, so this is not meant for concurrent use.
type RateTracker struct {
	prev map[string]rateSample
}

func NewRateTracker() *RateTracker {
	return &RateTracker{prev: make(map[string]rateSample)}
}

// Rate returns (delta/seconds, true) for a monotonically-increasing counter,
// or (0, false) on first observation or counter reset (delta < 0). The
// window is computed with now.Sub(prev) on time.Time values (monotonic
// when available) rather than Unix-second arithmetic, so rates survive
// NTP steps.
func (r *RateTracker) Rate(key string, now time.Time, counter float64) (float64, bool) {
	prev, ok := r.prev[key]
	r.prev[key] = rateSample{val: counter, ts: now}
	if !ok {
		return 0, false
	}
	delta := counter - prev.val
	if delta < 0 {
		return 0, false
	}
	elapsed := now.Sub(prev.ts).Seconds()
	if elapsed <= 0 {
		return 0, false
	}
	return delta / elapsed, true
}

// EvictPrefix deletes every tracked key with the given prefix. Without
// it, a RateTracker shared by an ephemeral-identity source (a GPU DRM
// client id, a container name) grows by one entry per key for the life
// of the process as those identities churn — this is the counterpart to
// store.Live.Evict for the per-key rate state Live doesn't hold.
func (r *RateTracker) EvictPrefix(prefix string) {
	for k := range r.prev {
		if strings.HasPrefix(k, prefix) {
			delete(r.prev, k)
		}
	}
}

// Len reports how many keys are currently tracked. Test-only
// introspection, for asserting a RateTracker returns to baseline after
// churn.
func (r *RateTracker) Len() int {
	return len(r.prev)
}

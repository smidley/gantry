package collect

import (
	"strings"
	"sync"
	"time"
)

type rateSample struct {
	val float64
	ts  time.Time
}

// RateTracker converts monotonically-increasing counters into per-second
// rates across ticks. Shared by every counter-based collector (host,
// docker, gpu, ...); one tracker per collector instance. Safe for
// concurrent use: docker's event-stream goroutine can call EvictPrefix
// (on a container destroy/remove event) while the collector's own tick
// goroutine concurrently calls Rate, so prev is mutex-guarded rather than
// assuming single-goroutine access. Contention is negligible at collector
// rates (a handful of calls every couple of seconds per collector).
type RateTracker struct {
	mu   sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prev)
}

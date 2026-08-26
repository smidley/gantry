package collect

import "time"

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

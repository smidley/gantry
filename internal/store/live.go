package store

import (
	"sort"
	"strings"
	"sync"
)

// Live holds the in-RAM ring buffers for every known series.
type Live struct {
	mu      sync.RWMutex
	rings   map[SeriesKey]*Ring
	ringCap int
}

func NewLive(ringCap int) *Live {
	return &Live{rings: make(map[SeriesKey]*Ring), ringCap: ringCap}
}

func (l *Live) Record(key SeriesKey, ts int64, val float64) {
	l.mu.Lock()
	r, ok := l.rings[key]
	if !ok {
		r = NewRing(l.ringCap)
		l.rings[key] = r
	}
	r.Append(Sample{TS: ts, Val: val})
	l.mu.Unlock()
}

func (l *Live) Since(key SeriesKey, ts int64) []Sample {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if r, ok := l.rings[key]; ok {
		return r.Since(ts)
	}
	return nil
}

func (l *Live) Latest(key SeriesKey) (Sample, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if r, ok := l.rings[key]; ok {
		return r.Latest()
	}
	return Sample{}, false
}

func (l *Live) Keys() []SeriesKey {
	l.mu.RLock()
	keys := make([]SeriesKey, 0, len(l.rings))
	for k := range l.rings {
		keys = append(keys, k)
	}
	l.mu.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		return a.Metric < b.Metric
	})
	return keys
}

// SnapshotLatest returns the latest sample for every currently-known
// series, taken under a single read-lock pass -- the counterpart to
// calling Keys() and then Latest(key) per key (N+1 locks total), which
// matters once assembling a snapshot happens far more often (SSE
// fan-out, one per connected client per tick).
func (l *Live) SnapshotLatest() map[SeriesKey]Sample {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[SeriesKey]Sample, len(l.rings))
	for k, r := range l.rings {
		if s, ok := r.Latest(); ok {
			out[k] = s
		}
	}
	return out
}

// ForEach runs fn for every series under the read lock.
// fn must not retain the ring or call back into Live.
func (l *Live) ForEach(fn func(key SeriesKey, ring *Ring)) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for k, r := range l.rings {
		fn(k, r)
	}
}

// LatestByMetricPrefix returns the latest sample of every series matching
// kind and entity whose metric starts with prefix, keyed by that series'
// full (unstripped) metric name. Always non-nil, matching SnapshotLatest's
// own convention, even when nothing matches.
func (l *Live) LatestByMetricPrefix(kind, entity, prefix string) map[string]Sample {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]Sample)
	for k, r := range l.rings {
		if k.Kind != kind || k.Entity != entity || !strings.HasPrefix(k.Metric, prefix) {
			continue
		}
		if s, ok := r.Latest(); ok {
			out[k.Metric] = s
		}
	}
	return out
}

// MatchSince returns, for every currently-known series of this kind and
// metric, the samples at or after since, keyed by entity -- one read-lock
// pass regardless of how many entities match. This is the alert engine's
// data source (internal/alert/engine.go): it asks once per (kind, metric)
// pair shared across every enabled rule, not once per entity, so an N+1
// shape (Keys() then Since() per matching key) would multiply by entity
// count every tick. samples is always non-nil; an unknown (kind, metric)
// pair or an entity with zero samples in [since, now] is simply absent as
// a key -- the engine's own no-data handling reads that absence directly,
// so this deliberately does not distinguish "no such series" from
// "series exists but nothing fell in the window."
//
// oldestTS reports, for every entity that has a ring for this (kind,
// metric) at all, that ring's true retention floor -- independent of
// since, and populated even for an entity absent from samples because
// its ring predates since entirely. A caller filtering samples to a
// window can no longer tell "the ring provably covers this whole window"
// from "this series just hasn't been running that long enough to fill
// it"; oldestTS is the second call that answer would otherwise need.
func (l *Live) MatchSince(kind, metric string, since int64) (samples map[string][]Sample, oldestTS map[string]int64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	samples = make(map[string][]Sample)
	oldestTS = make(map[string]int64)
	for k, r := range l.rings {
		if k.Kind != kind || k.Metric != metric {
			continue
		}
		if s := r.Since(since); len(s) > 0 {
			samples[k.Entity] = s
		}
		if o, ok := r.Oldest(); ok {
			oldestTS[k.Entity] = o.TS
		}
	}
	return samples, oldestTS
}

// Evict deletes every ring whose SeriesKey matches kind and entity.
func (l *Live) Evict(kind, entity string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.rings {
		if k.Kind == kind && k.Entity == entity {
			delete(l.rings, k)
		}
	}
}

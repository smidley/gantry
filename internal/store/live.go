package store

import (
	"sort"
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

// ForEach runs fn for every series under the read lock.
// fn must not retain the ring or call back into Live.
func (l *Live) ForEach(fn func(key SeriesKey, ring *Ring)) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for k, r := range l.rings {
		fn(k, r)
	}
}

package collect

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	reprobeEvery = 60 * time.Second
	backoffCap   = 5 * time.Minute
)

type entry struct {
	c      Collector
	mu     sync.Mutex
	status Status
}

// Registry runs a set of Collectors, one goroutine each, and reports
// their availability for healthz.
type Registry struct {
	mu      sync.RWMutex
	entries []*entry
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Add(c Collector) {
	r.mu.Lock()
	r.entries = append(r.entries, &entry{c: c})
	r.mu.Unlock()
}

// Sources reports name -> "ok" | detail for every registered collector.
func (r *Registry) Sources() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.entries))
	for _, e := range r.entries {
		e.mu.Lock()
		if e.status.Available {
			out[e.c.Name()] = "ok"
		} else {
			out[e.c.Name()] = e.status.Detail
		}
		e.mu.Unlock()
	}
	return out
}

// Run starts one goroutine per registered collector, each WaitGroup-tracked
// and exiting on ctx cancel.
func (r *Registry) Run(ctx context.Context, wg *sync.WaitGroup) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		wg.Add(1)
		go func(e *entry) {
			defer wg.Done()
			r.runOne(ctx, e)
		}(e)
	}
}

func (r *Registry) runOne(ctx context.Context, e *entry) {
	setStatus := func(s Status) { e.mu.Lock(); e.status = s; e.mu.Unlock() }
	consecutive := 0

	setStatus(e.c.Probe(ctx))
	for {
		e.mu.Lock()
		available := e.status.Available
		e.mu.Unlock()

		var wait time.Duration
		switch {
		case !available:
			wait = reprobeEvery
		case consecutive > 0:
			wait = backoff(consecutive, e.c.Interval())
		default:
			wait = e.c.Interval()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		if !available {
			setStatus(e.c.Probe(ctx))
			continue
		}
		if err := safeTick(ctx, e.c); err != nil {
			consecutive++
			if consecutive == 1 || consecutive%10 == 0 {
				log.Printf("collector %s: %v (consecutive=%d)", e.c.Name(), err, consecutive)
			}
		} else {
			consecutive = 0
		}
	}
}

func backoff(consecutive int, base time.Duration) time.Duration {
	d := time.Second
	for i := 1; i < consecutive; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	if d < base {
		return base
	}
	return d
}

func safeTick(ctx context.Context, c Collector) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return c.Tick(ctx, time.Now())
}

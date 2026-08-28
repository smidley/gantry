package collect

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// reprobeEvery and backoffFloor are vars, not consts, so tests can shrink
// them to exercise the reprobe/backoff paths without real multi-second
// waits.
var (
	reprobeEvery = 60 * time.Second
	backoffFloor = time.Second
)

const backoffCap = 5 * time.Minute

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

// NotApplicableSentinel is the fixed wire value Sources() reports for a
// collector whose Status carries NotApplicable -- a THIRD state,
// distinct from both "ok" and a plain Detail string, so the frontend can
// render it quietly (SourcesBanner stays silent; Settings' own sources
// list shows an ok-styled row with its own copy for the name) without
// parsing Detail's free text to guess which case it is. Never itself
// used as a Detail value by any collector -- Status.NotApplicable is
// the one place this gets set.
const NotApplicableSentinel = "not-applicable"

// Sources reports name -> "ok" | NotApplicableSentinel | detail for
// every registered collector.
func (r *Registry) Sources() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.entries))
	for _, e := range r.entries {
		e.mu.Lock()
		switch {
		case e.status.NotApplicable:
			out[e.c.Name()] = NotApplicableSentinel
		case e.status.Available:
			out[e.c.Name()] = "ok"
		default:
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

// maxConsecutiveErrorsBeforeDowngrade is how many Tick failures in a row
// (each one already bounded by safeTick's own per-tick timeout, so this
// can't be "5 indefinite hangs") it takes before Sources() stops
// reporting a collector as "ok" — otherwise a wedged dependency that
// keeps erroring on every bounded attempt would report healthy forever.
const maxConsecutiveErrorsBeforeDowngrade = 5

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
			// A Probe succeeding here is the only recovery path once a
			// collector has been downgraded (below): it always starts
			// consecutive fresh, so a stale error streak from before the
			// downgrade can't linger into the recovered run.
			st := e.c.Probe(ctx)
			setStatus(st)
			if st.Available {
				consecutive = 0
			}
			continue
		}
		if err := safeTick(ctx, e.c); err != nil {
			consecutive++
			if consecutive == 1 || consecutive%10 == 0 {
				log.Printf("collector %s: %v (consecutive=%d)", e.c.Name(), err, consecutive)
			}
			if consecutive == maxConsecutiveErrorsBeforeDowngrade {
				setStatus(Status{Available: false, Detail: "failing: " + err.Error()})
			}
		} else {
			consecutive = 0
		}
	}
}

func backoff(consecutive int, base time.Duration) time.Duration {
	d := backoffFloor
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

// safeTick runs one Tick call under a deadline of 5x the collector's own
// Interval, so a wedged dependency (e.g. a docker daemon that stops
// answering) can't block this collector's goroutine forever with
// Sources() still reporting "ok" — it times out, is counted as a tick
// error like any other, and eventually downgrades via runOne's
// consecutive-error counter.
func safeTick(ctx context.Context, c Collector) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	tctx, cancel := context.WithTimeout(ctx, 5*c.Interval())
	defer cancel()
	return c.Tick(tctx, time.Now())
}

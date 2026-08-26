package docker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeEventSink captures every appended event, in order, for assertions.
// (Contrast with host package's fakeSink, which is last-write-wins per
// key — here we need the full sequence since "no event" is itself an
// assertion this package's tests make.)
type fakeEventSink struct {
	mu     sync.Mutex
	events []store.Event
}

func (f *fakeEventSink) AppendEvent(e store.Event) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}

func (f *fakeEventSink) snapshot() []store.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.Event, len(f.events))
	copy(out, f.events)
	return out
}

// fakeEvictor records every evict(kind, entity) call.
type fakeEvictor struct {
	mu    sync.Mutex
	calls []string // "kind/entity"
}

func (f *fakeEvictor) evict(kind, entity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, kind+"/"+entity)
}

func (f *fakeEvictor) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestNormalizeNameStripsLeadingSlash(t *testing.T) {
	require.Equal(t, "foo", normalizeName("/foo"))
	require.Equal(t, "foo", normalizeName("foo"))
	require.Equal(t, "", normalizeName(""))
}

func TestRegistryLookupAndRunningAfterInventory(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{
		{ID: "id-running-b", Name: "bbb", State: "running", Pid: 200},
		{ID: "id-exited", Name: "ccc", State: "exited"},
		{ID: "id-running-a", Name: "aaa", State: "running", Pid: 100},
	}, sink, ev.evict)

	m, ok := r.lookup("id-running-a")
	require.True(t, ok)
	require.Equal(t, "aaa", m.Name)
	require.Equal(t, 100, m.Pid)

	_, ok = r.lookup("no-such-id")
	require.False(t, ok)

	running := r.running()
	require.Len(t, running, 2, "exited container must not appear in Running()")
	require.Equal(t, "aaa", running[0].Name, "Running() must be name-sorted")
	require.Equal(t, "bbb", running[1].Name)

	require.Empty(t, sink.snapshot(), "first-ever inventory snapshot is a baseline, not a diff")
	require.Empty(t, ev.snapshot())
}

func TestApplyInventoryFirstRefreshEmitsNoEvents(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{
		{ID: "abc", Name: "web", State: "running", Health: "healthy"},
	}, sink, ev.evict)

	require.Empty(t, sink.snapshot())
	require.Empty(t, ev.snapshot())
}

func TestApplyInventoryDiffEmitsStartOnStateChange(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "exited"}}, sink, ev.evict)
	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, store.Event{Kind: "container.start", Entity: "web", Severity: "info"}, got[0])
}

func TestApplyInventoryDiffEmitsDieOnStateChange(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "exited"}}, sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, "container.die", got[0].Kind)
	require.Equal(t, "web", got[0].Entity)
	require.Equal(t, "info", got[0].Severity)
	require.Contains(t, got[0].Detail, "exited")
}

func TestApplyInventoryDiffEmitsHealthEventOnFlip(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running", Health: "healthy"}}, sink, ev.evict)
	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running", Health: "unhealthy"}}, sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, store.Event{Kind: "container.health", Entity: "web", Severity: "warning", Detail: "unhealthy"}, got[0])
}

func TestApplyInventoryDiffEmitsRestartCountEvent(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running", RestartCount: 0}}, sink, ev.evict)
	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running", RestartCount: 1}}, sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, "container.start", got[0].Kind)
	require.Equal(t, "web", got[0].Entity)
	require.Contains(t, got[0].Detail, "1")
}

func TestApplyInventoryNoEventsWhenNothingChanged(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	snap := []Meta{{ID: "abc", Name: "web", State: "running", Health: "healthy", RestartCount: 2}}
	r.applyInventory(snap, sink, ev.evict)
	r.applyInventory(snap, sink, ev.evict)

	require.Empty(t, sink.snapshot(), "identical snapshots must not emit belt events")
}

func TestApplyInventoryEvictsVanishedContainer(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyInventory([]Meta{}, sink, ev.evict) // "web" removed entirely

	require.Equal(t, []string{"container/web"}, ev.snapshot())
	_, ok := r.lookup("abc")
	require.False(t, ok)
}

func TestApplyInventoryDoesNotEvictOnFirstRefresh(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{}, sink, ev.evict)
	require.Empty(t, ev.snapshot())
}

// TestApplyInventoryRecreationGuardSkipsEvictWhenNameStillLive pins the
// recreation guard: a container destroyed and replaced (new id, same
// name) in the same inventory diff must not evict that name -- eviction
// is name-keyed (Live.Evict, RateTracker.EvictPrefix), so evicting "web"
// here would wipe the brand-new container's just-started series, not
// the old one's.
func TestApplyInventoryRecreationGuardSkipsEvictWhenNameStillLive(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "old-id", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyInventory([]Meta{{ID: "new-id", Name: "web", State: "running"}}, sink, ev.evict)

	require.Empty(t, ev.snapshot(), "recreating a container under the same name must not evict its own fresh series")
	m, ok := r.lookup("new-id")
	require.True(t, ok)
	require.Equal(t, "web", m.Name)
	_, ok = r.lookup("old-id")
	require.False(t, ok, "the old id itself is still gone from the registry")
}

// TestApplyInventoryStillEvictsWhenReplacementHasDifferentName confirms
// the recreation guard is scoped to "this exact name still lives" and
// doesn't over-suppress eviction: a vanished container whose name nobody
// else holds must still evict, even while an unrelated container starts
// in the same diff.
func TestApplyInventoryStillEvictsWhenReplacementHasDifferentName(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "old-id", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyInventory([]Meta{{ID: "other-id", Name: "other", State: "running"}}, sink, ev.evict)

	require.Equal(t, []string{"container/web"}, ev.snapshot(), "a genuinely vanished name (no replacement) must still evict")
}

// TestRegistryLookupByNameFindsRunningAndNonRunningContainers pins
// lookupByName (Task 4's snapshot-filter dependency): it must find a
// container by name regardless of state -- a merely-exited-but-not-removed
// container is still "known" -- and miss on a name never seen.
func TestRegistryLookupByNameFindsRunningAndNonRunningContainers(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{
		{ID: "id-a", Name: "web", State: "running"},
		{ID: "id-b", Name: "worker", State: "exited"},
	}, sink, ev.evict)

	m, ok := r.lookupByName("web")
	require.True(t, ok)
	require.Equal(t, "id-a", m.ID)

	m, ok = r.lookupByName("worker")
	require.True(t, ok, "a merely-exited (not removed) container is still known")
	require.Equal(t, "id-b", m.ID)

	_, ok = r.lookupByName("ghost")
	require.False(t, ok, "a name never seen is not known")
}

// TestRegistryLookupByNameForgetsRemovedContainer confirms the other
// half of the snapshot filter's contract: once a container is genuinely
// removed (vanished from an inventory diff), lookupByName must forget it
// too -- this is what lets a stopped-and-removed container drop out of
// the live snapshot frame immediately.
func TestRegistryLookupByNameForgetsRemovedContainer(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "id-a", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyInventory([]Meta{}, sink, ev.evict) // removed

	_, ok := r.lookupByName("web")
	require.False(t, ok, "a genuinely removed container must no longer be known")
}

func msg(action events.Action, id string, attrs map[string]string) events.Message {
	return events.Message{Type: events.ContainerEventType, Action: action, Actor: events.Actor{ID: id, Attributes: attrs}}
}

func TestApplyEventTranslatesStart(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyEvent(msg(events.ActionStart, "abc", map[string]string{"name": "/web"}), sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, store.Event{Kind: "container.start", Entity: "web", Severity: "info"}, got[0])
}

func TestApplyEventDieSeverityOnExitCode(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyEvent(msg(events.ActionDie, "abc", map[string]string{"name": "/web", "exitCode": "1"}), sink, ev.evict)
	r.applyEvent(msg(events.ActionDie, "abc", map[string]string{"name": "/web", "exitCode": "0"}), sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 2)
	require.Equal(t, store.Event{Kind: "container.die", Entity: "web", Severity: "warning", Detail: "exit code 1"}, got[0])
	require.Equal(t, store.Event{Kind: "container.die", Entity: "web", Severity: "info", Detail: "exit code 0"}, got[1])
}

func TestApplyEventOOMIsAlertSeverity(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyEvent(msg(events.ActionOOM, "abc", map[string]string{"name": "/web"}), sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, store.Event{Kind: "container.oom", Entity: "web", Severity: "alert"}, got[0])
}

func TestApplyEventHealthStatusTranslation(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyEvent(msg(events.ActionHealthStatusUnhealthy, "abc", map[string]string{"name": "/web"}), sink, ev.evict)
	r.applyEvent(msg(events.ActionHealthStatusHealthy, "abc", map[string]string{"name": "/web"}), sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 2)
	require.Equal(t, store.Event{Kind: "container.health", Entity: "web", Severity: "warning", Detail: "unhealthy"}, got[0])
	require.Equal(t, store.Event{Kind: "container.health", Entity: "web", Severity: "info", Detail: "healthy"}, got[1])
}

func TestApplyEventDestroyEvictsAndForgetsContainer(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyEvent(msg(events.ActionDestroy, "abc", map[string]string{"name": "/web"}), sink, ev.evict)

	require.Equal(t, []string{"container/web"}, ev.snapshot())
	_, ok := r.lookup("abc")
	require.False(t, ok)
	// destroy is a lifecycle signal for eviction, not a translated store event
	require.Empty(t, sink.snapshot())
}

// TestApplyEventRecreationGuardSkipsEvictWhenNameStillLiveAfterPollRace
// covers the same recreation guard as applyInventory's, but on the event
// path: the event-stream goroutine and the poll goroutine are decoupled
// (applyEvent never registers metas -- only applyInventory does), so a
// destroy event for a superseded id can be PROCESSED after a later poll
// has already registered a same-named replacement (compose redeploys,
// watchtower). Without the guard, applyEvent's unconditional
// evict("container", name) would wipe out the replacement's
// already-started series, not the destroyed container's -- eviction is
// name-keyed, and the name now belongs to the new id.
func TestApplyEventRecreationGuardSkipsEvictWhenNameStillLiveAfterPollRace(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	// old-id ("web") starts, then is destroyed at the daemon -- but
	// delivery of that destroy event to applyEvent is about to lag behind
	// the next poll below.
	r.applyInventory([]Meta{{ID: "old-id", Name: "web", State: "running"}}, sink, ev.evict)

	// The poll runs first: old-id is gone (by id), new-id holds the same
	// name -- applyInventory's own recreation guard already keeps this
	// from evicting (pinned separately above); the registry now knows
	// "web" only via new-id.
	r.applyInventory([]Meta{{ID: "new-id", Name: "web", State: "running"}}, sink, ev.evict)
	require.Empty(t, ev.snapshot(), "sanity: the poll itself must not evict yet")

	// The stale destroy(old-id) event finally arrives.
	r.applyEvent(msg(events.ActionDestroy, "old-id", map[string]string{"name": "/web"}), sink, ev.evict)

	require.Empty(t, ev.snapshot(), "a destroy event for a superseded id must not evict the name a newer id still holds")
	m, ok := r.lookup("new-id")
	require.True(t, ok, "the still-live replacement must survive")
	require.Equal(t, "web", m.Name)

	// Destroying the CURRENT holder of the name, with no replacement
	// anywhere in the registry, must still evict -- the guard is scoped
	// to "another id holds this name right now", not a blanket
	// suppression of the event path's eviction.
	r.applyEvent(msg(events.ActionDestroy, "new-id", map[string]string{"name": "/web"}), sink, ev.evict)
	require.Equal(t, []string{"container/web"}, ev.snapshot())
	_, ok = r.lookup("new-id")
	require.False(t, ok)
}

func TestApplyEventNameFallsBackToRegistryWhenAttributeMissing(t *testing.T) {
	r := newRegistry()
	sink := &fakeEventSink{}
	ev := &fakeEvictor{}

	r.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, ev.evict)
	r.applyEvent(msg(events.ActionOOM, "abc", nil), sink, ev.evict)

	got := sink.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, "web", got[0].Entity)
}

// panicOnDetailSink panics on AppendEvent for one specific Detail value,
// simulating a downstream sink bug (or any other panic-prone code a
// malformed event might reach) that the docker event stream must survive
// without taking the whole process down with it.
type panicOnDetailSink struct {
	fakeEventSink
	panicOn string
}

func (p *panicOnDetailSink) AppendEvent(e store.Event) (int64, error) {
	if e.Detail == p.panicOn {
		panic("simulated sink panic on " + p.panicOn)
	}
	return p.fakeEventSink.AppendEvent(e)
}

// TestConsumeEventsRecoversPanicAndKeepsConsumingSubsequentEvents pins I4:
// a panic while handling one event (a malformed event, or a downstream
// sink bug) must not crash the process, and the stream must keep
// consuming whatever arrives after it on the same connection rather than
// the whole connection being torn down and forced through a reconnect.
func TestConsumeEventsRecoversPanicAndKeepsConsumingSubsequentEvents(t *testing.T) {
	sink := &panicOnDetailSink{panicOn: "exit code boom"}
	c := &Collector{reg: newRegistry(), events: sink, evict: func(string, string) {}}

	msgs := make(chan events.Message, 2)
	errs := make(chan error, 1)
	msgs <- msg(events.ActionDie, "abc", map[string]string{"name": "/flaky", "exitCode": "boom"})
	msgs <- msg(events.ActionStart, "def", map[string]string{"name": "/web"})
	close(msgs)

	progressed := c.consumeEvents(context.Background(), msgs, errs)

	require.True(t, progressed)
	got := sink.snapshot()
	require.Len(t, got, 1, "the panicking event's own append must not land")
	require.Equal(t, store.Event{Kind: "container.start", Entity: "web", Severity: "info"}, got[0],
		"the event after the panic must still be consumed and appended")
}

// newEvictingCollector builds a Collector with a real RateTracker and a
// store-evict spy, wired exactly like New() wires evictContainer — for
// tests exercising Task 2's container-removal cleanup without a docker
// daemon.
func newEvictingCollector(sink EventSink) (*Collector, *fakeEvictor) {
	ev := &fakeEvictor{}
	c := &Collector{
		reg:    newRegistry(),
		events: sink,
		evict:  ev.evict,
		rates:  collect.NewRateTracker(),
	}
	return c, ev
}

// TestEvictContainerClearsStoreRatesAndLoggedFallback pins Task 2:
// removing one container must clear all three places its identity can
// linger — the store (evict, wired to Live.Evict), the RateTracker (the
// name+"." prefix convention shared by cgroupv2.go/net.go), and the
// stats-API-fallback dedupe entry (loggedFallback, keyed by name — the
// stable identity across recreations, spec §5).
func TestEvictContainerClearsStoreRatesAndLoggedFallback(t *testing.T) {
	sink := &fakeEventSink{}
	c, ev := newEvictingCollector(sink)

	c.rates.Rate("web.cpu.usage", time.Now(), 100)
	c.rates.Rate("web.io.8:0.read", time.Now(), 100)
	c.loggedFallback.Store("web", struct{}{})
	require.Equal(t, 2, c.rates.Len())

	c.evictContainer("container", "web")

	require.Equal(t, []string{"container/web"}, ev.snapshot())
	require.Equal(t, 0, c.rates.Len())
	_, stillLogged := c.loggedFallback.Load("web")
	require.False(t, stillLogged)
}

// TestApplyInventoryRemovalEvictsRateKeysAndLoggedFallback exercises the
// real wiring: registry.applyInventory calling evictContainer (not a
// bare store-evict stub) when a container drops out of an inventory
// refresh.
func TestApplyInventoryRemovalEvictsRateKeysAndLoggedFallback(t *testing.T) {
	sink := &fakeEventSink{}
	c, ev := newEvictingCollector(sink)

	c.reg.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, c.evictContainer)
	c.rates.Rate("web.cpu.usage", time.Now(), 100)
	c.loggedFallback.Store("web", struct{}{})

	c.reg.applyInventory([]Meta{}, sink, c.evictContainer) // "web" removed

	require.Equal(t, []string{"container/web"}, ev.snapshot())
	require.Equal(t, 0, c.rates.Len())
	_, stillLogged := c.loggedFallback.Load("web")
	require.False(t, stillLogged)
}

// TestApplyEventDestroyEvictsRateKeysAndLoggedFallback is the event-stream
// counterpart: a destroy event must trigger the same full cleanup as an
// inventory-diff removal.
func TestApplyEventDestroyEvictsRateKeysAndLoggedFallback(t *testing.T) {
	sink := &fakeEventSink{}
	c, ev := newEvictingCollector(sink)

	c.reg.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, c.evictContainer)
	c.rates.Rate("web.cpu.usage", time.Now(), 100)
	c.loggedFallback.Store("web", struct{}{})

	c.reg.applyEvent(msg(events.ActionDestroy, "abc", map[string]string{"name": "/web"}), sink, c.evictContainer)

	require.Equal(t, []string{"container/web"}, ev.snapshot())
	require.Equal(t, 0, c.rates.Len())
	_, stillLogged := c.loggedFallback.Load("web")
	require.False(t, stillLogged)
}

// TestChurnManyContainersRateTrackerReturnsToBaseline simulates N
// containers being created (with real per-container rate keys and a
// loggedFallback entry) and removed via inventory-diff, asserting the
// RateTracker's key count and loggedFallback's size both return to
// their pre-churn baseline every time.
func TestChurnManyContainersRateTrackerReturnsToBaseline(t *testing.T) {
	sink := &fakeEventSink{}
	c, _ := newEvictingCollector(sink)

	c.rates.Rate("steady.cpu.usage", time.Now(), 1) // one key that never gets evicted
	baseline := c.rates.Len()

	const n = 100
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("churn%d", i)
		c.reg.applyInventory([]Meta{{ID: name + "-id", Name: name, State: "running"}}, sink, c.evictContainer)
		c.rates.Rate(name+".cpu.usage", time.Now(), 1)
		c.rates.Rate(name+".io.8:0.read", time.Now(), 1)
		c.loggedFallback.Store(name, struct{}{})

		c.reg.applyInventory([]Meta{}, sink, c.evictContainer) // remove it again
	}

	require.Equal(t, baseline, c.rates.Len(), "churned container rate keys must not accumulate")
	loggedCount := 0
	c.loggedFallback.Range(func(_, _ any) bool { loggedCount++; return true })
	require.Equal(t, 0, loggedCount, "churned containers' loggedFallback entries must not accumulate")
}

// Collector-level smoke tests (Name/Interval/Probe) — mirrors the host
// package's TestHostNameAndInterval convention.

func TestDockerCollectorNameAndInterval(t *testing.T) {
	c := New(nil, nil, func(string, string) {}, "/var/run/docker.sock")
	require.Equal(t, "docker", c.Name())
	require.Equal(t, 2*time.Second, c.Interval())
}

func TestDockerCollectorProbeUnavailableWithoutDaemon(t *testing.T) {
	c := New(nil, nil, func(string, string) {}, "/no/such/socket.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st := c.Probe(ctx)
	require.False(t, st.Available)
	require.NotEmpty(t, st.Detail)
}

// TestCollectorLookupByNameDelegatesToRegistry pins Collector.LookupByName
// (Task 4's snapshot-filter dependency) as a thin passthrough to the
// registry, the same relationship Lookup already has by id.
func TestCollectorLookupByNameDelegatesToRegistry(t *testing.T) {
	c := New(nil, nil, func(string, string) {}, "/var/run/docker.sock")
	sink := &fakeEventSink{}
	c.reg.applyInventory([]Meta{{ID: "abc", Name: "web", State: "running"}}, sink, func(string, string) {})

	m, ok := c.LookupByName("web")
	require.True(t, ok)
	require.Equal(t, "abc", m.ID)

	_, ok = c.LookupByName("ghost")
	require.False(t, ok)
}

// TestDrainReturnsImmediatelyWhenEventsNeverStarted pins I4's Drain()
// against the case where the daemon was never reachable: Probe never got
// past Ping, so startEvents never fired and eventsWG's counter is still
// 0 — Drain must not block waiting for a goroutine that was never
// started.
func TestDrainReturnsImmediatelyWhenEventsNeverStarted(t *testing.T) {
	c := New(nil, nil, func(string, string) {}, "/no/such/socket.sock")

	done := make(chan struct{})
	go func() {
		c.Drain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Drain() blocked even though the event stream never started")
	}
}

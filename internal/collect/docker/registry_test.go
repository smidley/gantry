package docker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
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

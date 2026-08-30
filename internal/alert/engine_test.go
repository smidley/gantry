package alert

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeStore is a minimal, fully in-memory implementation of the Store
// interface, standing in for a real *store.Store the same way the plan's
// own test list asks for ("an injected clock and a stub Match (no store,
// no docker)"). It reproduces the one invariant the engine actually
// depends on the DB for -- idx_alert_active, the partial unique index
// rejecting a second active row for the same (rule_id, entity) -- so a
// bug that would violate it surfaces as an error here exactly like it
// would against SQLite.
type fakeStore struct {
	rules      []store.AlertRule
	instances  map[int64]store.AlertInstance
	nextID     int64
	events     []store.Event
	silences   []store.Silence
	resolveErr map[int64]error // one-shot: consumed by the first ResolveAlertInstance(id, ...) call
}

func newFakeStore(rules ...store.AlertRule) *fakeStore {
	return &fakeStore{rules: rules, instances: map[int64]store.AlertInstance{}, resolveErr: map[int64]error{}}
}

func (f *fakeStore) AlertRules(context.Context) ([]store.AlertRule, error) { return f.rules, nil }

func (f *fakeStore) ActiveAlertInstances(context.Context) ([]store.AlertInstance, error) {
	ids := make([]int64, 0, len(f.instances))
	for id := range f.instances {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []store.AlertInstance
	for _, id := range ids {
		if i := f.instances[id]; i.ResolvedAt == 0 {
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *fakeStore) UpsertAlertInstance(i store.AlertInstance) (int64, error) {
	if i.ID == 0 {
		for _, existing := range f.instances {
			if existing.ResolvedAt == 0 && existing.RuleID == i.RuleID && existing.Entity == i.Entity {
				return 0, fmt.Errorf("UNIQUE constraint failed: idx_alert_active")
			}
		}
		f.nextID++
		i.ID = f.nextID
	}
	f.instances[i.ID] = i
	return i.ID, nil
}

func (f *fakeStore) ResolveAlertInstance(id, at int64, reason string) error {
	if err, ok := f.resolveErr[id]; ok {
		delete(f.resolveErr, id)
		delete(f.instances, id) // simulate a concurrent Maintain prune: the row is really gone, not just unwritable
		return err
	}
	inst, ok := f.instances[id]
	if !ok {
		return fmt.Errorf("alert instance %d: not found", id)
	}
	inst.State, inst.ResolvedAt, inst.ResolveReason = "resolved", at, reason
	f.instances[id] = inst
	return nil
}

func (f *fakeStore) Silences(context.Context, int64) ([]store.Silence, error) { return f.silences, nil }

func (f *fakeStore) QueryEventsSince(_ context.Context, after int64, _ int) ([]store.Event, error) {
	var out []store.Event
	for _, e := range f.events {
		if e.ID > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) MaxEventID(context.Context) (int64, error) {
	var max int64
	for _, e := range f.events {
		if e.ID > max {
			max = e.ID
		}
	}
	return max, nil
}

func (f *fakeStore) AppendEvent(e store.Event) (int64, error) {
	e.ID = int64(len(f.events) + 1)
	f.events = append(f.events, e)
	return e.ID, nil
}

func (f *fakeStore) activeCount() int {
	n := 0
	for _, i := range f.instances {
		if i.ResolvedAt == 0 {
			n++
		}
	}
	return n
}

func (f *fakeStore) soleActive(t *testing.T) store.AlertInstance {
	t.Helper()
	active, err := f.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, active, 1)
	return active[0]
}

func (f *fakeStore) eventKinds() []string {
	out := make([]string, len(f.events))
	for i, e := range f.events {
		out[i] = e.Kind
	}
	return out
}

// matchRouter is the Match stub: data[{kind,metric}][entity] -> samples,
// filtered to TS >= since on every call, the same contract
// Live.MatchSince documents (an entity with nothing in-window is simply
// absent as a key). Every fixture built through flat()/series() happens
// to store exactly one window's worth of history, so its oldest sample
// always sits precisely on since -- this stub's oldestTS return reflects
// that faithfully, which is exactly why it could never have caught F1 on
// its own; see engine_live_test.go for the family that does.
type matchRouter struct {
	data map[[2]string]map[string][]store.Sample
}

func newMatchRouter() *matchRouter {
	return &matchRouter{data: map[[2]string]map[string][]store.Sample{}}
}

func (m *matchRouter) set(kind, metric, entity string, samples []store.Sample) {
	key := [2]string{kind, metric}
	if m.data[key] == nil {
		m.data[key] = map[string][]store.Sample{}
	}
	m.data[key][entity] = samples
}

func (m *matchRouter) fn(kind, metric string, since int64) (map[string][]store.Sample, map[string]int64) {
	out := map[string][]store.Sample{}
	oldest := map[string]int64{}
	for entity, samples := range m.data[[2]string{kind, metric}] {
		if len(samples) > 0 {
			oldest[entity] = samples[0].TS // ascending, matching Ring.Since/Oldest's convention; independent of since
		}
		var win []store.Sample
		for _, s := range samples {
			if s.TS >= since {
				win = append(win, s)
			}
		}
		if len(win) > 0 {
			out[entity] = win
		}
	}
	return out, oldest
}

// flat builds `count` samples, `spacing` seconds apart, ending at end
// (inclusive), every one holding val -- ascending, matching Ring.Since.
func flat(end, spacing int64, count int, val float64) []store.Sample {
	out := make([]store.Sample, count)
	for i := 0; i < count; i++ {
		out[i] = store.Sample{TS: end - int64(count-1-i)*spacing, Val: val}
	}
	return out
}

// clockAt is a settable injected clock: tests advance .t directly between
// Tick() calls to simulate consecutive engine ticks without a real sleep.
type clockAt struct{ t int64 }

func (c *clockAt) now() time.Time { return time.Unix(c.t, 0) }

func newEngine(st Store, match func(kind, metric string, since int64) (map[string][]store.Sample, map[string]int64), classOf func(kind, entity string) string, fleet func() []FleetMember, dispatch func(AlertNotification), clock func() time.Time) *Engine {
	return New(st, match, classOf, fleet, dispatch, clock)
}

const hostCPUHigh = "host-cpu-high"

func cpuRule() store.AlertRule {
	return store.AlertRule{
		ID: hostCPUHigh, Name: "Host CPU high", Enabled: true, Type: "threshold", Kind: "host", EntityGlob: "*",
		Metric: "cpu.total", Op: ">", Threshold: 85, ClearThreshold: 70, ForSeconds: 100, ClearSeconds: 100,
		Severity: "warning",
	}
}

// --- (none) -> firing / pending ---------------------------------------

func TestTickFiresImmediatelyWhenRingAlreadyCoversTheWindow(t *testing.T) {
	now := int64(2_000_000_000)
	st := newFakeStore(cpuRule())
	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 90)) // now-100..now, all breaching
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State)
	require.Equal(t, hostCPUHigh, inst.RuleID)
	require.Equal(t, 90.0, inst.Value)
	require.Equal(t, 85.0, inst.Threshold, "threshold snapshotted onto the instance")
	require.Equal(t, []string{"alert.fired"}, st.eventKinds())
}

func TestTickCreatesPendingWithNoEventNoDispatchWhenWindowNotYetCovered(t *testing.T) {
	now := int64(2_000_000_000)
	st := newFakeStore(cpuRule())
	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 3, 90)) // only 20s of history; ForSeconds=100
	dispatched := 0
	eng := newEngine(st, mr.fn, nil, nil, func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "pending", inst.State)
	require.Empty(t, st.events, "pending never writes an event")
	require.Equal(t, 0, dispatched, "pending never dispatches")
}

func TestTickCreatesNoInstanceWhenNotCurrentlyCrossingAndWindowNotCovered(t *testing.T) {
	now := int64(2_000_000_000)
	st := newFakeStore(cpuRule())
	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 3, 10)) // low value, short window: nothing to track
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 0, st.activeCount())
}

// TestThresholdSustainedForBoundary is the user's named exact-value test:
// breach for N-1 ticks never fires (stays pending); the Nth tick fires.
// Modeled as two consecutive Tick() calls 10s apart with an injected
// clock, the ring accumulating one more sample each time -- exactly how
// the real engine sees a metric that just started breaching.
func TestThresholdSustainedForBoundary(t *testing.T) {
	st := newFakeStore(cpuRule()) // ForSeconds=100
	mr := newMatchRouter()
	clk := &clockAt{t: 2_000_000_000}
	var fired int
	eng := newEngine(st, mr.fn, nil, nil, func(n AlertNotification) {
		if n.Phase == "fired" {
			fired++
		}
	}, clk.now)

	// Ticks 1..10 (10s apart): the ring accumulates one more sample each
	// time, but N samples spaced 10s apart span only (N-1)*10 seconds --
	// the 10th tick reaches 90s, still short of the 100s window, and
	// must stay pending throughout.
	for i := 0; i < 10; i++ {
		mr.set("host", "cpu.total", "", flat(clk.t, 10, i+1, 90))
		require.NoError(t, eng.Tick(context.Background()))
		clk.t += 10
	}
	inst := st.soleActive(t)
	require.Equal(t, "pending", inst.State, "10 ticks span 90s, short of the 100s window")
	require.Equal(t, 0, fired)

	// Tick 11: 11 samples span exactly 100s -- the window is now covered.
	mr.set("host", "cpu.total", "", flat(clk.t, 10, 11, 90))
	require.NoError(t, eng.Tick(context.Background()))

	inst = st.soleActive(t)
	require.Equal(t, "firing", inst.State, "the 11th tick covers the full 100s window and must fire")
	require.Equal(t, 1, fired)
}

// --- pending -> resolved (silent) --------------------------------------

func TestPendingResolvesSilentlyWhenValueDropsBelowThresholdBeforeFiring(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	st := newFakeStore(rule)
	// Seed a pending instance directly (as if a prior tick started it).
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "pending",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 10,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 3, 50)) // dropped back down, well under threshold
	dispatched := 0
	eng := newEngine(st, mr.fn, nil, nil, func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, 0, st.activeCount())
	resolved := st.instances[id]
	require.Equal(t, "resolved", resolved.State)
	require.Equal(t, "cleared", resolved.ResolveReason)
	require.Empty(t, st.events, "a pending alert that never fired produces no event")
	require.Equal(t, 0, dispatched, "and no dispatch")
}

// --- firing: clear hysteresis -------------------------------------------

// TestFiringStaysFiringOnDipAboveClearThreshold is the user's named clear-
// hysteresis test: a dip below the fire threshold but still above the
// clear threshold must not resolve the alert.
func TestFiringStaysFiringOnDipAboveClearThreshold(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule() // fire >85, clear <70, clear_seconds=100
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 200, FiredAt: now - 100,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 78)) // dipped to 78: below 85, above 70
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "firing", inst.State, "78 is below fire (85) but above clear (70): must hold, not clear")
	require.Equal(t, 78.0, inst.Value)
}

// TestFiringResolvesAfterSustainedClear is the user's named clear-
// hysteresis completion: once the value has been below the clear
// threshold for the full clear window, the alert resolves.
func TestFiringResolvesAfterSustainedClear(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule() // clear <70 for 100s
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 300, FiredAt: now - 300,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 60)) // under 70 for the full 100s window
	var notes []string
	eng := newEngine(st, mr.fn, nil, nil, func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State)
	require.Equal(t, "cleared", inst.ResolveReason)
	require.Equal(t, []string{"alert.resolved"}, st.eventKinds())
	require.Equal(t, []string{"resolved"}, notes)
}

// TestFlapSequenceProducesFreshInstancePerRefire drives fire->resolve->
// fire twice, asserting each firing episode is its own row (a fresh id,
// never a reopened resolved one) and idx_alert_active's invariant --
// never more than one active row for the pair -- holds at every step.
func TestFlapSequenceProducesFreshInstancePerRefire(t *testing.T) {
	rule := cpuRule() // for=100, clear_seconds=100
	st := newFakeStore(rule)
	mr := newMatchRouter()
	clk := &clockAt{t: 2_000_000_000}
	eng := newEngine(st, mr.fn, nil, nil, nil, clk.now)

	var firedIDs []int64
	fire := func() {
		mr.set("host", "cpu.total", "", flat(clk.t, 10, 11, 90))
		require.NoError(t, eng.Tick(context.Background()))
		require.LessOrEqual(t, st.activeCount(), 1, "idx_alert_active: never more than one active row")
		inst := st.soleActive(t)
		require.Equal(t, "firing", inst.State)
		firedIDs = append(firedIDs, inst.ID)
		clk.t += 10
	}
	resolve := func() {
		mr.set("host", "cpu.total", "", flat(clk.t, 10, 11, 60))
		require.NoError(t, eng.Tick(context.Background()))
		require.Equal(t, 0, st.activeCount())
		clk.t += 10
	}

	fire()
	resolve()
	fire()
	resolve()

	require.Len(t, firedIDs, 2)
	require.NotEqual(t, firedIDs[0], firedIDs[1], "a refire must be a fresh row, not the resolved one reopened")
}

// --- silence -------------------------------------------------------------

func TestSilenceSuppressesDispatchButNotTheStateTransition(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	st := newFakeStore(rule)
	st.silences = []store.Silence{{RuleID: rule.ID, Entity: "", Until: now + 3600}}
	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 90))
	dispatched := 0
	eng := newEngine(st, mr.fn, nil, nil, func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State, "silence must not block the state transition itself")
	require.Equal(t, 0, dispatched, "but dispatch is suppressed")
}

// TestSilencedFireDispatchesExactlyOnceOnFirstUnsilencedTick pins F2: a
// rule with renotify_hours<=0 (6 of the 12 builtins, including cpuRule's
// own shape) that starts firing while silenced leaves NotifyCount at 0
// forever under the old code -- maybeRenotify only ever re-notifies an
// instance that already notified once, and it no-ops outright at
// renotify_hours<=0 regardless. The first tick the silence no longer
// covers it must dispatch the "fired" notification that was suppressed,
// exactly once, and never again afterward.
func TestSilencedFireDispatchesExactlyOnceOnFirstUnsilencedTick(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule() // RenotifyHours defaults to 0
	st := newFakeStore(rule)
	st.silences = []store.Silence{{RuleID: rule.ID, Entity: "", Until: now + 3600}}
	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 90)) // window already covered: fires immediately, silenced
	var notes []string
	eng := newEngine(st, mr.fn, nil, nil, func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))
	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State)
	require.Equal(t, int64(0), inst.NotifyCount, "silenced fire must not stamp notify bookkeeping")
	require.Empty(t, notes, "silenced fire must not dispatch")

	// Silence lapses (the real Store's Silences() simply excludes an
	// expired row; the fake mirrors that by the caller clearing it).
	st.silences = nil
	mr.set("host", "cpu.total", "", flat(now+10, 10, 12, 90))
	require.NoError(t, eng.Tick(context.Background()))

	inst = st.instances[inst.ID]
	require.Equal(t, "firing", inst.State)
	require.Equal(t, int64(1), inst.NotifyCount)
	require.Equal(t, []string{"fired"}, notes, "exactly one dispatch, on the first unsilenced tick")

	// Further holding ticks with renotify_hours<=0 must stay silent.
	mr.set("host", "cpu.total", "", flat(now+20, 10, 13, 90))
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, []string{"fired"}, notes, "renotify_hours<=0 must not dispatch again")
}

func TestSilenceEmptyRuleAndEntityMeansAny(t *testing.T) {
	require.True(t, silenced([]store.Silence{{RuleID: "", Entity: ""}}, "any-rule", "any-entity"))
	require.True(t, silenced([]store.Silence{{RuleID: "r1", Entity: ""}}, "r1", "whatever"))
	require.False(t, silenced([]store.Silence{{RuleID: "r1", Entity: ""}}, "r2", "whatever"))
	require.True(t, silenced([]store.Silence{{RuleID: "", Entity: "e1"}}, "any-rule", "e1"))
	require.False(t, silenced([]store.Silence{{RuleID: "", Entity: "e1"}}, "any-rule", "e2"))
}

// --- rule disabled / deleted ----------------------------------------------

func TestDisabledRuleResolvesActiveInstanceWithEventButNoDispatch(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	rule.Enabled = false
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", State: "firing", Severity: "warning", StartedAt: now - 100, FiredAt: now - 100,
	})
	require.NoError(t, err)

	dispatched := 0
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil,
		func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State)
	require.Equal(t, "rule-disabled", inst.ResolveReason)
	require.Equal(t, []string{"alert.resolved"}, st.eventKinds())
	require.Equal(t, 0, dispatched)
}

func TestDeletedRuleResolvesOrphanedInstance(t *testing.T) {
	now := int64(2_000_000_000)
	st := newFakeStore() // no rules at all: inst.RuleID references one that no longer exists
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: "ghost-rule", Kind: "host", Entity: "", State: "firing", Severity: "warning", StartedAt: now - 100, FiredAt: now - 100,
	})
	require.NoError(t, err)

	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, "resolved", st.instances[id].State)
	require.Equal(t, "rule-disabled", st.instances[id].ResolveReason)
}

// TestDisabledRulePendingInstanceResolvesSilently pins F8: a pending
// instance that never fired must leave the same way it does everywhere
// else in this file (resolveSilent's doctrine) -- no event, no dispatch --
// even when the reason it's leaving is the rule being disabled rather
// than clearing or falling out of scope.
func TestDisabledRulePendingInstanceResolvesSilently(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	rule.Enabled = false
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", State: "pending", Severity: "warning", StartedAt: now - 10,
	})
	require.NoError(t, err)

	dispatched := 0
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil,
		func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State)
	require.Equal(t, "rule-disabled", inst.ResolveReason)
	require.Empty(t, st.events, "a pending instance that never fired must not get an event either")
	require.Equal(t, 0, dispatched)
}

// TestMissingSinceClearedWhenRuleDisabledWhileAbsent pins F9: the
// per-instance absence timer (missingSince, keyed by instance id) must
// not survive the instance it was tracking on EVERY resolve path -- not
// just the no-data timeout that originally set it. resolveDisabled
// bypasses that timeout entirely, so without its own cleanup the entry
// leaks for the rest of the engine's lifetime.
func TestMissingSinceClearedWhenRuleDisabledWhileAbsent(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 300, FiredAt: now - 300,
	})
	require.NoError(t, err)

	mr := newMatchRouter() // metric produces nothing: entity absent
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background())) // absent tick 1: starts the no-data timer
	require.Contains(t, eng.missingSince, id, "the absence timer must be tracking this instance")

	rule.Enabled = false
	st.rules = []store.AlertRule{rule}
	require.NoError(t, eng.Tick(context.Background())) // now resolved via resolveDisabled instead

	require.Equal(t, "resolved", st.instances[id].State)
	require.NotContains(t, eng.missingSince, id, "resolveDisabled must clean up the absence timer too")
}

// TestMissingSinceClearedWhenResolvedOutOfScopeWhileAbsent is F9's other
// named gap: resolveOutOfScope (F6) also bypasses the no-data timeout,
// and is a second path that never touched missingSince before this fix.
func TestMissingSinceClearedWhenResolvedOutOfScopeWhileAbsent(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	rule.Kind, rule.Metric, rule.EntityGlob = "container", "cpu.total", "*"
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "container", Entity: "plex", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 300, FiredAt: now - 300,
	})
	require.NoError(t, err)

	mr := newMatchRouter() // plex absent from the very first tick
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background())) // still in scope, absent: starts the timer
	require.Contains(t, eng.missingSince, id)

	rule.EntityGlob = "jelly*" // narrowed: plex falls out of scope
	st.rules = []store.AlertRule{rule}
	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, "resolved", st.instances[id].State)
	require.Equal(t, "out-of-scope", st.instances[id].ResolveReason)
	require.NotContains(t, eng.missingSince, id, "resolveOutOfScope must clean up the absence timer too")
}

// --- no-data ---------------------------------------------------------------

func TestNoDataResolvesOnlyAfterClearSecondsNotOnFirstMissingTick(t *testing.T) {
	rule := cpuRule() // clear_seconds=100
	st := newFakeStore(rule)
	clk := &clockAt{t: 2_000_000_000}
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: clk.t - 200, FiredAt: clk.t - 200,
	})
	require.NoError(t, err)

	mr := newMatchRouter() // metric produces nothing at all: entity fully absent
	eng := newEngine(st, mr.fn, nil, nil, nil, clk.now)

	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.instances[id].State, "must not resolve on the first missing tick")

	clk.t += 50
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.instances[id].State, "50s absent, short of the 100s clear window")

	clk.t += 60 // total 110s absent, past clear_seconds=100
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "resolved", st.instances[id].State)
	require.Equal(t, "no-data", st.instances[id].ResolveReason)
}

func TestNoDataTimerResetsWhenSeriesReappears(t *testing.T) {
	rule := cpuRule()
	st := newFakeStore(rule)
	clk := &clockAt{t: 2_000_000_000}
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: clk.t - 200, FiredAt: clk.t - 200,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	eng := newEngine(st, mr.fn, nil, nil, nil, clk.now)

	require.NoError(t, eng.Tick(context.Background())) // absent tick 1
	clk.t += 60
	mr.set("host", "cpu.total", "", flat(clk.t, 10, 11, 90)) // reappears, still breaching
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.instances[id].State)

	mr.set("host", "cpu.total", "", nil)
	clk.t += 60 // if the absence timer hadn't reset, 60+60=120s > 100s would resolve here
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.instances[id].State, "the absence timer must restart after the series reappeared")
}

// --- cleanly inert over an absent metric -----------------------------------

func TestRuleOverAbsentMetricIsCleanlyInert(t *testing.T) {
	rule := cpuRule()
	rule.Metric = "fs.used_pct" // not collected in this branch's scope
	st := newFakeStore(rule)
	mr := newMatchRouter() // never populated for fs.used_pct: Match returns an empty map
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(2_000_000_000, 0) })

	for i := 0; i < 5; i++ {
		require.NoError(t, eng.Tick(context.Background()))
	}
	require.Equal(t, 0, st.activeCount())
	require.Empty(t, st.events)
}

// --- scoping: glob + class negation ----------------------------------------

func diskTempRule() store.AlertRule {
	return store.AlertRule{
		ID: "disk-temp-high", Enabled: true, Type: "threshold", Kind: "disk", EntityGlob: "*", EntityClass: "!nvme",
		Metric: "temp.c", Op: ">", Threshold: 55, ClearThreshold: 50, ForSeconds: 100, ClearSeconds: 100, Severity: "warning",
	}
}

func TestClassNegationScopingExcludesNvmeIncludesOthers(t *testing.T) {
	now := int64(2_000_000_000)
	rule := diskTempRule()
	st := newFakeStore(rule)
	mr := newMatchRouter()
	mr.set("disk", "temp.c", "disk1", flat(now, 10, 11, 60)) // hdd, breaching
	mr.set("disk", "temp.c", "nvme1", flat(now, 10, 11, 90)) // nvme, breaching -- must be excluded by !nvme

	classOf := func(kind, entity string) string {
		if kind != "disk" {
			return ""
		}
		if entity == "nvme1" {
			return "nvme"
		}
		return "hdd"
	}
	eng := newEngine(st, mr.fn, classOf, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	active, err := st.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, "disk1", active[0].Entity)
}

func TestEntityGlobScopingMatrix(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	rule.Kind, rule.EntityGlob, rule.Metric = "container", "jelly*", "cpu.total"
	st := newFakeStore(rule)
	mr := newMatchRouter()
	mr.set("container", "cpu.total", "jellyfin", flat(now, 10, 11, 90))
	mr.set("container", "cpu.total", "plex", flat(now, 10, 11, 90))
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	active, err := st.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, "jellyfin", active[0].Entity)
}

// --- narrowed scope strands a still-breaching stray (F6) ------------------

// TestNarrowedGlobResolvesStillBreachingFiringStrayOutOfScope pins F6: an
// active FIRING instance whose entity no longer matches the rule's
// CURRENT entity_glob must resolve even though it's still numerically
// breaching. evalThresholdRule's union keeps evaluating a stray (its own
// doc says so, "must still be able to resolve"), but the ordinary
// crossing/no-data checks never trigger for an entity that keeps
// reporting a breaching value on its own terms -- without an explicit
// out-of-scope resolve, that promise is broken and it fires forever.
func TestNarrowedGlobResolvesStillBreachingFiringStrayOutOfScope(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	rule.Kind, rule.Metric, rule.EntityGlob = "container", "cpu.total", "jelly*" // narrowed after plex started firing
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "container", Entity: "plex", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 300, FiredAt: now - 300,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	mr.set("container", "cpu.total", "plex", flat(now, 10, 11, 90)) // still breaching, still reporting
	var notes []string
	eng := newEngine(st, mr.fn, nil, nil, func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State)
	require.Equal(t, "out-of-scope", inst.ResolveReason)
	require.Contains(t, st.eventKinds(), "alert.resolved")
	require.Equal(t, []string{"resolved"}, notes)
}

// TestNarrowedGlobResolvesPendingStrayOutOfScopeSilently pins the pending
// half: a stray that never fired resolves with no event and no dispatch,
// matching resolveSilent's doctrine everywhere else in this file.
func TestNarrowedGlobResolvesPendingStrayOutOfScopeSilently(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	rule.Kind, rule.Metric, rule.EntityGlob = "container", "cpu.total", "jelly*"
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "container", Entity: "plex", Metric: rule.Metric, State: "pending",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 10,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	mr.set("container", "cpu.total", "plex", flat(now, 10, 3, 90)) // still breaching, short window
	dispatched := 0
	eng := newEngine(st, mr.fn, nil, nil, func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State)
	require.Equal(t, "out-of-scope", inst.ResolveReason)
	require.Empty(t, st.events, "a pending stray that never fired produces no event")
	require.Equal(t, 0, dispatched)
}

// TestNarrowedClassResolvesStillBreachingStrayOutOfScope pins the same
// behavior off entity_class rather than entity_glob: a disk that stops
// matching a newly-added "!nvme" restriction resolves out-of-scope too.
func TestNarrowedClassResolvesStillBreachingStrayOutOfScope(t *testing.T) {
	now := int64(2_000_000_000)
	rule := diskTempRule() // EntityClass "!nvme"
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "disk", Entity: "nvme1", Metric: rule.Metric, State: "firing",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 300, FiredAt: now - 300,
	})
	require.NoError(t, err)

	mr := newMatchRouter()
	mr.set("disk", "temp.c", "nvme1", flat(now, 10, 11, 90)) // still hot, still reporting
	classOf := func(kind, entity string) string {
		if kind == "disk" && entity == "nvme1" {
			return "nvme" // now excluded by the rule's "!nvme"
		}
		return "hdd"
	}
	eng := newEngine(st, mr.fn, classOf, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.instances[id]
	require.Equal(t, "resolved", inst.State)
	require.Equal(t, "out-of-scope", inst.ResolveReason)
}

// --- panic isolation ---------------------------------------------------

func TestPanicInOneRuleDoesNotAbortOtherRules(t *testing.T) {
	now := int64(2_000_000_000)
	good := cpuRule()
	bad := cpuRule()
	bad.ID, bad.Kind, bad.Metric = "bad-rule", "container", "cpu.total"
	st := newFakeStore(good, bad)

	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 90))
	mr.set("container", "cpu.total", "boom", flat(now, 10, 11, 90))

	classOf := func(kind, entity string) string {
		if kind == "container" {
			panic("simulated malformed-rule panic")
		}
		return ""
	}
	eng := newEngine(st, mr.fn, classOf, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()), "a panicking rule must not surface as a Tick error")

	active, err := st.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, active, 1, "the other rule must still have evaluated")
	require.Equal(t, hostCPUHigh, active[0].RuleID)
}

// --- ResolveAlertInstance error: log-and-continue, never abort the tick ---

// TestResolveErrorOnUnknownIDLogsAndContinuesTheTick pins the carry-
// forward fix from the alert-store review: ResolveAlertInstance now
// errors on an unknown id (store.go), and the engine must swallow that
// per-instance rather than letting one stale handle abort every other
// rule's evaluation this tick.
func TestResolveErrorOnUnknownIDLogsAndContinuesTheTick(t *testing.T) {
	now := int64(2_000_000_000)
	clearing := cpuRule() // will resolve via clearing this tick
	other := cpuRule()
	other.ID, other.Kind, other.Metric = "other-rule", "container", "cpu.total"
	st := newFakeStore(clearing, other)

	staleID, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: clearing.ID, Kind: "host", Entity: "", Metric: clearing.Metric, State: "firing",
		Severity: "warning", Value: 90, Threshold: 85, StartedAt: now - 300, FiredAt: now - 300,
	})
	require.NoError(t, err)
	st.resolveErr[staleID] = fmt.Errorf("alert instance %d: not found", staleID) // simulate a row pruned out from under the engine

	mr := newMatchRouter()
	mr.set("host", "cpu.total", "", flat(now, 10, 11, 60))            // clears
	mr.set("container", "cpu.total", "sonarr", flat(now, 10, 11, 90)) // fresh breach on the other rule
	eng := newEngine(st, mr.fn, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()), "a resolve error on one instance must not fail the tick")

	active, err := st.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, active, 1, "the other rule must still have fired despite the first rule's resolve error")
	require.Equal(t, "other-rule", active[0].RuleID)
}

// --- event rules -------------------------------------------------------

func unhealthyRule() store.AlertRule {
	return store.AlertRule{
		ID: "container-unhealthy", Enabled: true, Type: "event", Kind: "container", EntityGlob: "*",
		EventKinds: "container.health", MinSeverity: "warning",
		ClearEventKinds: "container.health", ClearMaxSeverity: "info",
		ClearSeconds: 21600, Severity: "alert",
	}
}

func TestEventRuleFiresImmediatelyOnMatchingEvent(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick: no events exist yet, cursor clamps to 0

	// A real event arrives after the engine has already booted.
	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning", Detail: "unhealthy"}}
	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State)
	require.Equal(t, "sonarr", inst.Entity)
	require.Equal(t, []string{"alert.fired"}, st.eventKinds()[1:])
	require.Equal(t, []string{"fired"}, notes)
}

func TestEventRuleIgnoresEventBelowMinSeverityFloor(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule() // MinSeverity: warning
	st := newFakeStore(rule)
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "info", Detail: "healthy"}}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 0, st.activeCount())
}

func TestEventRuleClearsOnMatchingClearEvent(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	clk := &clockAt{t: now - 10}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: clk.t, Kind: "container.health", Entity: "sonarr", Severity: "warning", Detail: "unhealthy"}}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State)

	st.events = append(st.events, store.Event{ID: 2, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "info", Detail: "healthy"})
	clk.t = now
	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, 0, st.activeCount())
	require.Contains(t, st.eventKinds(), "alert.resolved")
}

func TestEventRuleClearsOnTimeoutWhenNoClearEventArrives(t *testing.T) {
	rule := unhealthyRule() // ClearSeconds: 21600
	st := newFakeStore(rule)
	clk := &clockAt{t: 2_000_000_000}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: clk.t, Kind: "container.health", Entity: "sonarr", Severity: "warning"}}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State)

	clk.t += 21600 - 10
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State, "not yet timed out")

	clk.t += 20
	require.NoError(t, eng.Tick(context.Background()))
	active, _ := st.ActiveAlertInstances(context.Background())
	require.Empty(t, active)
	for _, i := range st.instances {
		require.Equal(t, "timeout", i.ResolveReason)
	}
}

func TestEventRuleDuplicateMatchDoesNotCreateSecondActiveInstance(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{
		{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning"},
		{ID: 2, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning"},
	}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 1, st.activeCount())
}

// --- event cursor: boot clamp + no replay ------------------------------

// TestEventCursorStartsAtMaxEventIDNoReplayOfPreexistingEvents pins the
// carry-forward defense-in-depth requirement: even though events.id is
// AUTOINCREMENT and never reused, the engine clamps its cursor to
// MaxEventID at boot so a restart can never replay the whole table as
// fresh alerts.
func TestEventCursorStartsAtMaxEventIDNoReplayOfPreexistingEvents(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	for i := 1; i <= 50; i++ {
		st.events = append(st.events, store.Event{ID: int64(i), TS: now - int64(50-i), Kind: "container.health", Entity: "sonarr", Severity: "warning"})
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 0, st.activeCount(), "50 pre-existing events must not replay as fresh alerts")
}

// TestEventCursorPersistsAcrossTicksSameEngineNeverReplays models "across
// restart" at the unit level: eng1 boots with no events yet (cursor 0),
// then a real event arrives after its boot and legitimately fires. A
// second Engine constructed later against the SAME store state (as a
// fresh process would find after a restart) clamps ITS OWN cursor to
// MaxEventID at ITS boot -- so, unlike eng1, it must never re-fire on an
// event that already existed by the time it started.
func TestEventCursorPersistsAcrossTicksSameEngineNeverReplays(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	eng1 := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng1.Tick(context.Background())) // eng1 boots: cursor clamps to 0, nothing to seed

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning"}}
	require.NoError(t, eng1.Tick(context.Background()))
	require.Equal(t, 1, st.activeCount(), "eng1 must fire on an event that arrived after its own boot")

	// "Restart": a brand-new Engine over the same store. Its own boot
	// clamps its cursor to MaxEventID (1, since event 1 already exists
	// by now) -- it must not create a second instance for the same
	// event, the exact replay a bare persisted cursor (or none at all)
	// would risk.
	eng2 := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now+10, 0) })
	require.NoError(t, eng2.Tick(context.Background()))
	require.Equal(t, 1, st.activeCount(), "the restarted engine must not re-fire event id 1 again")
}

// --- boot seeding --------------------------------------------------------

func TestBootSeedingFiresForPreexistingUnhealthyRunningContainer(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	fleet := func() []FleetMember {
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "sonarr", inst.Entity)
	require.Equal(t, "firing", inst.State)
}

func TestBootSeedingDoesNotFireForStoppedContainerWithStaleUnhealthy(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	fleet := func() []FleetMember {
		return []FleetMember{{Name: "sonarr", State: "exited", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, 0, st.activeCount(), "a stopped container's stale unhealthy status must not seed an alert")
}

func TestBootSeedingRunsOnlyOnce(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	calls := 0
	fleet := func() []FleetMember {
		calls++
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 1, calls, "boot seeding must run exactly once per engine lifetime")
	require.Equal(t, 1, st.activeCount())
}

// TestBootSeedingRetriesUntilFleetIsNonEmpty pins F3: a slow docker probe
// can leave Fleet() returning nothing on the engine's very first tick
// (t+10s can easily beat the first inventory poll). Latching "seeded"
// on that empty read would permanently skip the one thing boot seeding
// exists for -- a container already unhealthy before Gantry started,
// which no new event will ever arrive to report. Seeding must keep
// retrying every tick until Fleet() actually reports something.
func TestBootSeedingRetriesUntilFleetIsNonEmpty(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	calls := 0
	fleet := func() []FleetMember {
		calls++
		if calls == 1 {
			return nil // slow probe: nothing yet on the first tick
		}
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background())) // empty fleet: must not latch "seeded"
	require.Equal(t, 0, st.activeCount(), "nothing to seed from an empty fleet")

	require.NoError(t, eng.Tick(context.Background())) // fleet now populated: seeding must retry and fire
	require.Equal(t, 2, calls)
	inst := st.soleActive(t)
	require.Equal(t, "sonarr", inst.Entity)
	require.Equal(t, "firing", inst.State)

	// And now that a real seed happened, it must still run only once.
	calls = 0
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 0, calls, "seeding must not retry once it has actually run")
}

// TestBootSeedingEmptyFleetDoesNotDelayEventCursorClamp guards the F3
// refactor itself: decoupling the seed-retry latch from the one-time
// event-cursor clamp must not delay the cursor -- it still has to clamp
// on the very first tick regardless of the fleet, or a still-empty fleet
// tick would replay the entire events table as fresh alerts (see
// TestEventCursorStartsAtMaxEventIDNoReplayOfPreexistingEvents).
func TestBootSeedingEmptyFleetDoesNotDelayEventCursorClamp(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	st.events = []store.Event{{ID: 1, TS: now - 100, Kind: "container.health", Entity: "sonarr", Severity: "warning"}}
	fleet := func() []FleetMember { return nil } // stays empty for this whole test
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, 0, st.activeCount(), "the pre-existing event must not replay as a fresh alert")
}

// --- Run loop --------------------------------------------------------------

func TestRunStopsOnContextCancelWithoutBlocking(t *testing.T) {
	st := newFakeStore()
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, time.Now)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		eng.Run(ctx, time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

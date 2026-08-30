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

// TestPendingResolvesNoDataNotClearedWhenSeriesVanishes pins F10: a
// pending instance whose series disappears entirely (samples == nil, not
// just a value that dropped below threshold) must resolve with reason
// "no-data", not "cleared" -- currentlyCrossing is false either way
// (len(samples) > 0 is false for both an empty and a nil samples slice),
// so the absent case has to be checked separately and first, or the two
// causes are indistinguishable in the API/UI.
func TestPendingResolvesNoDataNotClearedWhenSeriesVanishes(t *testing.T) {
	now := int64(2_000_000_000)
	rule := cpuRule()
	st := newFakeStore(rule)
	id, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "host", Entity: "", Metric: rule.Metric, State: "pending",
		Severity: rule.Severity, Value: 90, Threshold: rule.Threshold, StartedAt: now - 10,
	})
	require.NoError(t, err)

	mr := newMatchRouter() // never populated: the series vanished entirely, not merely dropped
	dispatched := 0
	eng := newEngine(st, mr.fn, nil, nil, func(AlertNotification) { dispatched++ }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, 0, st.activeCount())
	resolved := st.instances[id]
	require.Equal(t, "resolved", resolved.State)
	require.Equal(t, "no-data", resolved.ResolveReason, "the series vanished -- not a real recovery")
	require.Empty(t, st.events, "a pending alert that never fired produces no event either way")
	require.Equal(t, 0, dispatched)
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
	require.True(t, Silenced([]store.Silence{{RuleID: "", Entity: ""}}, "any-rule", "any-entity"))
	require.True(t, Silenced([]store.Silence{{RuleID: "r1", Entity: ""}}, "r1", "whatever"))
	require.False(t, Silenced([]store.Silence{{RuleID: "r1", Entity: ""}}, "r2", "whatever"))
	require.True(t, Silenced([]store.Silence{{RuleID: "", Entity: "e1"}}, "any-rule", "e1"))
	require.False(t, Silenced([]store.Silence{{RuleID: "", Entity: "e1"}}, "any-rule", "e2"))
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

// TestEventRuleMatchingIgnoresKindByDesign pins F11's resolution: an
// event rule's Kind rides onto the created instance and feeds
// ClassOf-based class matching, but it does NOT gate which events reach
// the rule at all -- EventKinds already does that job precisely. A rule
// whose Kind has nothing to do with its EventKinds' real domain still
// matches normally.
func TestEventRuleMatchingIgnoresKindByDesign(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule() // EventKinds: "container.health"
	rule.Kind = "totally-unrelated-kind"
	st := newFakeStore(rule)
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil, nil, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning", Detail: "unhealthy"}}
	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State, "EventKinds alone gates matching; r.Kind does not, by design")
	require.Equal(t, "sonarr", inst.Entity)
	require.Equal(t, "totally-unrelated-kind", inst.Kind, "Kind still rides onto the created instance as metadata")
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

// --- churn probation (container-exit-nonzero) --------------------------

// exitNonzeroRule mirrors the real container-exit-nonzero default
// exactly (see store.DefaultAlertRules): for_seconds=120 is this rule's
// OWN churn-probation window (engine.go's fireEvent/tickEvents), a
// second, unrelated meaning of that column from a threshold rule's
// sustained-for.
func exitNonzeroRule() store.AlertRule {
	return store.AlertRule{
		ID: "container-exit-nonzero", Enabled: true, Type: "event", Kind: "container", EntityGlob: "*",
		EventKinds: "container.die", MinSeverity: "warning",
		ForSeconds: 120, ClearSeconds: 3600, Severity: "warning",
	}
}

// TestChurnProbationEntersPendingNotFiringOnFreshMatch pins the core
// behavior change: a probation-enabled rule's fresh match never fires
// immediately the way every other event rule does -- it starts pending,
// exactly like startPending's threshold-side "no event, no dispatch"
// contract, giving a routine restart a chance to prove itself before
// anyone is bothered.
func TestChurnProbationEntersPendingNotFiringOnFreshMatch(t *testing.T) {
	now := int64(2_000_000_000)
	rule := exitNonzeroRule()
	st := newFakeStore(rule)
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.die", Entity: "sonarr", Severity: "warning", Detail: "exit code 137"}}
	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "pending", inst.State, "a probation-enabled rule's fresh match must not fire immediately")
	require.Equal(t, "sonarr", inst.Entity)
	require.Empty(t, notes, "pending never dispatches")
	require.NotContains(t, st.eventKinds(), "alert.fired", "pending never appends alert.fired either")
}

// TestChurnProbationResolvesSilentlyWhenFleetShowsRestartedWithinWindow
// is the fix's whole point: Unraid's Appdata Backup and CA auto-update
// plugins both stop-then-restart every container on their own overnight
// schedule. Once Fleet() shows the entity running again, the pending
// instance resolves as "restarted" -- silently, since it never fired in
// the first place (resolveSilent's own doctrine: nothing to announce
// recovery from).
func TestChurnProbationResolvesSilentlyWhenFleetShowsRestartedWithinWindow(t *testing.T) {
	now := int64(2_000_000_000)
	rule := exitNonzeroRule()
	st := newFakeStore(rule)
	running := false
	fleet := func() []FleetMember {
		state := "exited"
		if running {
			state = "running"
		}
		return []FleetMember{{Name: "sonarr", State: state}}
	}
	var notes []string
	clk := &clockAt{t: now}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.die", Entity: "sonarr", Severity: "warning", Detail: "exit code 137"}}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "pending", st.soleActive(t).State)

	// The routine restart, well inside the 120s probation window.
	running = true
	clk.t = now + 20
	require.NoError(t, eng.Tick(context.Background()))

	active, _ := st.ActiveAlertInstances(context.Background())
	require.Empty(t, active, "must resolve, not stay pending")
	require.Empty(t, notes, "zero dispatches -- this never fired")
	for _, i := range st.instances {
		require.Equal(t, "restarted", i.ResolveReason)
	}
	require.NotContains(t, st.eventKinds(), "alert.resolved", "a pending instance never fired, so it gets no resolved event either")
}

// TestChurnProbationPromotesToFiringAfterWindowWithNoRestart is the
// other half: no start, ever, within the window -- a real problem, not
// routine churn -- promotes to firing (and delivers) exactly once the
// probation window elapses, same bookkeeping/dispatch shape as any other
// fresh fire.
func TestChurnProbationPromotesToFiringAfterWindowWithNoRestart(t *testing.T) {
	now := int64(2_000_000_000)
	rule := exitNonzeroRule()
	st := newFakeStore(rule)
	fleet := func() []FleetMember { return []FleetMember{{Name: "sonarr", State: "exited"}} }
	var notes []string
	clk := &clockAt{t: now}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.die", Entity: "sonarr", Severity: "warning", Detail: "exit code 137"}}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "pending", st.soleActive(t).State)

	clk.t = now + 119
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "pending", st.soleActive(t).State, "not yet 120s")

	clk.t = now + 121
	require.NoError(t, eng.Tick(context.Background()))
	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State)
	require.Equal(t, int64(1), inst.NotifyCount)
	require.Equal(t, []string{"fired"}, notes)
	require.Contains(t, st.eventKinds(), "alert.fired")
}

// TestChurnProbationRetroactivelyResolvesAlreadyFiringBacklogInstance
// pins fix 4's backfill: the same fleet-running check also sweeps an
// already-FIRING instance created before this mechanism existed -- the
// exact backlog of stale exit alerts sitting active on a real box, whose
// containers have long since restarted. Unlike the pending case, this
// instance DID fire (and possibly already notified), so its own history
// still gets an alert.resolved event -- it just never dispatches a
// SECOND notification for something that was already noise.
func TestChurnProbationRetroactivelyResolvesAlreadyFiringBacklogInstance(t *testing.T) {
	now := int64(2_000_000_000)
	rule := exitNonzeroRule()
	st := newFakeStore(rule)
	staleID, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: rule.ID, Kind: "container", Entity: "sonarr", State: "firing",
		Severity: "warning", Summary: "sonarr: container.die (exit code 137)",
		StartedAt: now - 3600, FiredAt: now - 3600, LastNotifiedAt: now - 3600, NotifyCount: 1,
	})
	require.NoError(t, err)

	fleet := func() []FleetMember { return []FleetMember{{Name: "sonarr", State: "running"}} }
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background())) // the first tick after deploying the fix

	active, _ := st.ActiveAlertInstances(context.Background())
	require.Empty(t, active, "the running entity proves this was routine churn -- clean sweep on the first tick")
	require.Equal(t, "restarted", st.instances[staleID].ResolveReason)
	require.Empty(t, notes, "already-delivered junk must not ALSO get a resolved notification")
	require.Contains(t, st.eventKinds(), "alert.resolved", "a firing instance's own history must still show why it closed")
}

// TestChurnProbationDoesNotAffectBootSeedingOfOtherEventRules guards the
// tickEvents restructuring itself: an unrelated probation-enabled rule
// coexisting in the same rule set must not perturb container-unhealthy's
// own boot-seeding, which has nothing to do with container.die at all.
func TestChurnProbationDoesNotAffectBootSeedingOfOtherEventRules(t *testing.T) {
	now := int64(2_000_000_000)
	st := newFakeStore(unhealthyRule(), exitNonzeroRule())
	fleet := func() []FleetMember {
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background()))

	active, _ := st.ActiveAlertInstances(context.Background())
	require.Len(t, active, 1, "container-exit-nonzero's EventKinds never matches the synthetic container.health boot-seed event")
	require.Equal(t, "container-unhealthy", active[0].RuleID)
	require.Equal(t, "firing", active[0].State, "boot-seeding must still fire immediately, unaffected by an unrelated probation-enabled rule")
}

// TestForSecondsSetOnContainerUnhealthyDoesNotArmChurnProbation pins the
// fix for a column-overload bug: churnProbationRules, not a bare
// for_seconds > 0 check, is what decides whether a currently-running
// entity means "routine restart." Before this registry existed, a user
// simply tuning "how long unhealthy before firing" on container-
// unhealthy would have armed the exact fleet-running check
// resolveRestarted uses -- but running is container-unhealthy's own
// NORMAL state for the entire time it's firing, so it would insta-
// resolve as "restarted" on literally the next tick. container-
// unhealthy is not in churnProbationRules, so for_seconds here must
// stay inert: boot-seeding fires it immediately (never pending), and it
// survives a later tick with the entity still reading running+
// unhealthy.
func TestForSecondsSetOnContainerUnhealthyDoesNotArmChurnProbation(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	rule.ForSeconds = 30 // the user edit the bug report names -- must have zero effect here
	st := newFakeStore(rule)
	clk := &clockAt{t: now}
	fleet := func() []FleetMember {
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)

	require.NoError(t, eng.Tick(context.Background())) // boot tick

	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State, "boot-seeding must still fire immediately, not enter pending")
	require.Equal(t, []string{"fired"}, notes)

	// Still running+unhealthy a tick later -- the exact condition that
	// would insta-resolve a REAL probation rule as "restarted".
	clk.t = now + 60
	require.NoError(t, eng.Tick(context.Background()))

	inst = st.soleActive(t)
	require.Equal(t, "firing", inst.State, "must not resolve as restarted just because the entity reads running")
	require.Equal(t, []string{"fired"}, notes, "no resolved notification -- it never resolved")
}

// --- event rule catch-up / renotify sweep ------------------------------

// TestEventRuleCatchesUpSilencedFireOnceSilenceLifts pins N1: the
// tickEvents sweep only ever checked the clear_seconds timeout, so an
// event-rule instance born while silenced (NotifyCount left at 0, the
// same silenced path processEventForRule shares with fire()) stayed
// firing with nobody ever told, forever, even after the silence lifted
// -- nothing else ever revisited it. Mirrors
// TestSilencedFireDispatchesExactlyOnceOnFirstUnsilencedTick's
// threshold-side proof of the same F2 fix, for the event path.
func TestEventRuleCatchesUpSilencedFireOnceSilenceLifts(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule() // RenotifyHours defaults to 0
	st := newFakeStore(rule)
	st.silences = []store.Silence{{RuleID: rule.ID, Entity: "", Until: now + 3600}}
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick: no events yet, cursor clamps

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning", Detail: "unhealthy"}}
	require.NoError(t, eng.Tick(context.Background()))

	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State)
	require.Equal(t, int64(0), inst.NotifyCount, "silenced fire must not stamp notify bookkeeping")
	require.Empty(t, notes, "silenced fire must not dispatch")

	// Silence lapses (the real Store's Silences() simply excludes an
	// expired row; the fake mirrors that by the caller clearing it).
	st.silences = nil
	require.NoError(t, eng.Tick(context.Background()))

	inst = st.instances[inst.ID]
	require.Equal(t, "firing", inst.State)
	require.Equal(t, int64(1), inst.NotifyCount)
	require.Equal(t, []string{"fired"}, notes, "exactly one dispatch, on the first unsilenced tick")

	// Further holding ticks with renotify_hours<=0 must stay silent.
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, []string{"fired"}, notes, "renotify_hours<=0 must not dispatch again")
}

// TestEventRuleCatchUpDoesNotAlsoRenotifySameTick guards the sweep's
// NotifyCount==0 ? catchUpSilencedFire : maybeRenotify pair being an
// if/else, not two independent calls. LastNotifiedAt sits at its zero
// value for as long as NotifyCount==0, so once the silence lifts,
// maybeRenotify's own "not yet due" check (now - LastNotifiedAt against
// renotify_hours) would ALSO look satisfied, purely because `now` is a
// realistic Unix timestamp and LastNotifiedAt is still 0 -- a rule with
// renotify_hours > 0 (container-unhealthy's real default is 24) makes
// that trap live. Catch-up must win alone: exactly one "fired", never a
// same-tick "renotify" too.
func TestEventRuleCatchUpDoesNotAlsoRenotifySameTick(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	rule.RenotifyHours = 24
	st := newFakeStore(rule)
	st.silences = []store.Silence{{RuleID: rule.ID, Entity: "", Until: now + 3600}}
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, nil,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, func() time.Time { return time.Unix(now, 0) })
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: now, Kind: "container.health", Entity: "sonarr", Severity: "warning", Detail: "unhealthy"}}
	require.NoError(t, eng.Tick(context.Background())) // created while silenced: NotifyCount 0, LastNotifiedAt 0

	st.silences = nil
	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, []string{"fired"}, notes, "catch-up must not also renotify in the same pass")
	require.Equal(t, int64(1), st.soleActive(t).NotifyCount)
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

// TestBootSeedingRunsOnlyOnce pins F3's own guarantee: seeding itself --
// the synthetic container.health event that creates the instance -- must
// never repeat. Fleet() is no longer a pure boot-seeding signal, though:
// tickEvents' own sustain sweep (N2) reads it every tick too, for
// exactly this rule (container-unhealthy), so the raw call count climbs
// by one per tick even after booting -- 3 calls over 2 ticks here: 1
// from boot-seeding on tick one, plus 1 from the sweep on each of the
// two ticks. What must NOT happen is a second SEED, which the event/
// instance counts below would catch regardless of the call count.
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
	require.Equal(t, 3, calls, "1 boot-seed read + 1 sustain-sweep read per tick")
	require.Equal(t, 1, st.activeCount(), "seeding must not have run twice")
	require.Equal(t, []string{"alert.fired"}, st.eventKinds(), "and must not have appended a second alert.fired")
}

// TestBootSeedingRetriesUntilFleetIsNonEmpty pins F3: a slow docker probe
// can leave Fleet() returning nothing on the engine's very first tick
// (t+10s can easily beat the first inventory poll). Latching "seeded"
// on that empty read would permanently skip the one thing boot seeding
// exists for -- a container already unhealthy before Gantry started,
// which no new event will ever arrive to report. Seeding must keep
// retrying every tick until Fleet() actually reports something.
//
// The probe's readiness is a bool flipped between Tick() calls, not a
// raw call-count branch: tickEvents' own sustain sweep (N2) now also
// calls Fleet() every tick, so a call-counted branch would flip mid-tick
// once both call sites are live, before the test can observe either one.
func TestBootSeedingRetriesUntilFleetIsNonEmpty(t *testing.T) {
	now := int64(2_000_000_000)
	rule := unhealthyRule()
	st := newFakeStore(rule)
	ready := false
	calls := 0
	fleet := func() []FleetMember {
		calls++
		if !ready {
			return nil // slow probe: nothing yet
		}
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, func() time.Time { return time.Unix(now, 0) })

	require.NoError(t, eng.Tick(context.Background())) // empty fleet: must not latch "seeded"
	require.Equal(t, 0, st.activeCount(), "nothing to seed from an empty fleet")

	ready = true
	require.NoError(t, eng.Tick(context.Background())) // fleet now populated: seeding must retry and fire
	inst := st.soleActive(t)
	require.Equal(t, "sonarr", inst.Entity)
	require.Equal(t, "firing", inst.State)

	// And now that a real seed happened, boot seeding itself must still
	// run only once: further ticks only feed the sustain sweep's own
	// steady one-Fleet()-read-per-tick cadence, never a second seed pass.
	callsBefore := calls
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, callsBefore+1, calls, "only the sustain sweep should read Fleet() now that boot seeding is done")
	require.Equal(t, 1, st.activeCount(), "seeding must not have created a duplicate instance")
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

// --- sustained event conditions (container-unhealthy) ------------------

// unhealthyRuleWithRenotify mirrors the real container-unhealthy default
// exactly (see store.DefaultAlertRules): clear_seconds=6h, renotify_
// hours=24h -- the numbers N2 exists to make coherent.
func unhealthyRuleWithRenotify() store.AlertRule {
	r := unhealthyRule()
	r.RenotifyHours = 24
	return r
}

// TestSustainedUnhealthyStaysFiringPastClearSecondsAndRenotifiesAt24h
// pins N2's preferred fix: container-unhealthy's clear_seconds (6h) is
// shorter than its renotify_hours (24h), so under the old code the
// timeout always resolved it before a renotify could ever fire -- a
// container STILL unhealthy at 6h got silently resolved out from under
// an ongoing problem. Fleet() confirming the condition every tick now
// re-anchors the fallback timeout, and the 24h renotify (measured off
// LastNotifiedAt, untouched by that refresh) fires right on schedule.
func TestSustainedUnhealthyStaysFiringPastClearSecondsAndRenotifiesAt24h(t *testing.T) {
	const t0 = int64(2_000_000_000)
	rule := unhealthyRuleWithRenotify() // ClearSeconds 21600, RenotifyHours 24
	st := newFakeStore(rule)
	clk := &clockAt{t: t0}
	fleet := func() []FleetMember {
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	var notes []string
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet,
		func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // boot seeding fires it off the fleet read

	require.Equal(t, "firing", st.soleActive(t).State)
	require.Equal(t, []string{"fired"}, notes)

	// Past the old 6h clear_seconds boundary: still sustained per
	// Fleet(), so the fallback timeout must not have fired.
	clk.t = t0 + 21600 + 100
	require.NoError(t, eng.Tick(context.Background()))
	inst := st.soleActive(t)
	require.Equal(t, "firing", inst.State, "still unhealthy: must not resolve")
	require.Equal(t, int64(1), inst.NotifyCount, "not yet 24h since the fire: no renotify due")

	// Cross 24h total since the original fire: renotify fires, still firing.
	clk.t = t0 + 24*3600 + 10
	require.NoError(t, eng.Tick(context.Background()))
	inst = st.soleActive(t)
	require.Equal(t, "firing", inst.State)
	require.Equal(t, int64(2), inst.NotifyCount)
	require.Equal(t, []string{"fired", "renotify"}, notes)
}

// TestSustainedUnhealthyResolvesClearSecondsAfterConditionActuallyEnds is
// N2's other half: once Fleet() stops confirming the container
// unhealthy, the refresh stops and the fallback timeout counts
// clear_seconds from the LAST tick the condition was actually seen --
// not from whenever the collector's own clearing event happens to
// arrive (it may never, which is the whole point of a fallback), and
// not never.
func TestSustainedUnhealthyResolvesClearSecondsAfterConditionActuallyEnds(t *testing.T) {
	const t0 = int64(2_000_000_000)
	rule := unhealthyRuleWithRenotify()
	st := newFakeStore(rule)
	clk := &clockAt{t: t0}
	unhealthy := true
	fleet := func() []FleetMember {
		health := "unhealthy"
		if !unhealthy {
			health = "healthy"
		}
		return []FleetMember{{Name: "sonarr", State: "running", Health: health}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // t0: boot seeding fires it, FiredAt=t0
	require.Equal(t, "firing", st.soleActive(t).State)

	clk.t = t0 + 3600 // last tick the condition is still confirmed live: FiredAt refreshes to t0+3600
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State)
	require.Equal(t, t0+3600, st.soleActive(t).FiredAt, "refreshed while still unhealthy")

	// Condition ends now, with no clearing event ever arriving -- the
	// case the fallback timeout exists for. From here FiredAt is frozen:
	// no more refreshes.
	unhealthy = false
	clk.t = t0 + 3600 + 21600 - 10
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State, "not yet clear_seconds since the last confirmed-unhealthy tick")
	require.Equal(t, t0+3600, st.soleActive(t).FiredAt, "frozen: Fleet() no longer confirms it, so no more refreshes")

	clk.t = t0 + 3600 + 21600 + 10
	require.NoError(t, eng.Tick(context.Background()))
	active, _ := st.ActiveAlertInstances(context.Background())
	require.Empty(t, active, "clear_seconds after the condition actually ended: now it resolves")
	for _, i := range st.instances {
		require.Equal(t, "timeout", i.ResolveReason)
	}
}

// TestNonSustainedEventRuleIgnoresFleetAndTimesOutNormally guards
// sustainedEventRules' narrow scope: a true point-in-time event rule
// (container-oom here, standing in for oom/exit/disk-errors/parity-
// errors) must keep its plain clear_seconds-from-the-firing-event
// semantics even when Fleet() happens to describe the same entity in
// whatever shape a sustain predicate would otherwise match against --
// there is no predicate registered for this rule id, so Fleet() is
// never even consulted for it.
func TestNonSustainedEventRuleIgnoresFleetAndTimesOutNormally(t *testing.T) {
	rule := store.AlertRule{
		ID: "container-oom", Enabled: true, Type: "event", Kind: "container", EntityGlob: "*",
		EventKinds: "container.oom", MinSeverity: "alert", ClearSeconds: 100, Severity: "alert",
	}
	st := newFakeStore(rule)
	clk := &clockAt{t: 2_000_000_000}
	fleet := func() []FleetMember {
		// Looks exactly like a live container-unhealthy condition would --
		// irrelevant here, since container-oom has no sustain predicate.
		return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	}
	eng := newEngine(st, func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }, nil, fleet, nil, clk.now)
	require.NoError(t, eng.Tick(context.Background())) // boot tick

	st.events = []store.Event{{ID: 1, TS: clk.t, Kind: "container.oom", Entity: "sonarr", Severity: "alert"}}
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State)

	clk.t += 90
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State, "not yet clear_seconds")

	clk.t += 20
	require.NoError(t, eng.Tick(context.Background()))
	active, _ := st.ActiveAlertInstances(context.Background())
	require.Empty(t, active, "clear_seconds from the ORIGINAL event, unaffected by Fleet()")
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

// --- summary units -----------------------------------------------------------

// TestSummarizeThresholdUnits pins the notification text's unit
// handling: the rule's band family picks the unit first (the same six
// families thresholds.ts renders display bands for), the metric name's
// suffix conventions fill in for custom rules without one, and a metric
// matching neither stays the bare number it always was. Unit renderings
// follow web/src/lib/format.ts' own formatters, so a notification and
// the UI tile it points at never disagree about what a value reads as.
func TestSummarizeThresholdUnits(t *testing.T) {
	cases := []struct {
		name   string
		rule   store.AlertRule
		entity string
		value  float64
		want   string
	}{
		{
			name:   "temperature via band family",
			rule:   store.AlertRule{Metric: "temp.c", Op: ">", Threshold: 55, ForSeconds: 600, BandFamily: "disk.temp"},
			entity: "disk3", value: 58.0,
			want: "disk3 is at 58.0°C (over 55.0°C for 10m0s)",
		},
		{
			name:   "percent via band family",
			rule:   store.AlertRule{Metric: "cpu.total", Op: ">", Threshold: 85, ForSeconds: 600, BandFamily: "host.cpu"},
			entity: "", value: 92.5,
			want: "host is at 92.5% (over 85.0% for 10m0s)",
		},
		{
			name:   "percent via metric suffix, no band family",
			rule:   store.AlertRule{Metric: "swap.used_pct", Op: ">", Threshold: 50, ForSeconds: 300},
			entity: "", value: 61.2,
			want: "host is at 61.2% (over 50.0% for 5m0s)",
		},
		{
			name:   "temperature via metric name, no band family",
			rule:   store.AlertRule{Metric: "temp.c", Op: ">", Threshold: 70, ForSeconds: 600},
			entity: "nvme0n1", value: 74.5,
			want: "nvme0n1 is at 74.5°C (over 70.0°C for 10m0s)",
		},
		{
			name:   "bytes humanized via metric suffix",
			rule:   store.AlertRule{Metric: "share.appdata.used_bytes", Op: ">", Threshold: 500 * 1024 * 1024 * 1024, ForSeconds: 900},
			entity: "appdata", value: 512.5 * 1024 * 1024 * 1024,
			want: "appdata is at 512.5 GiB (over 500.0 GiB for 15m0s)",
		},
		{
			name:   "byte rate in decimal units via metric suffix",
			rule:   store.AlertRule{Metric: "diskio.read_bps", Op: ">", Threshold: 100_000_000, ForSeconds: 300},
			entity: "sda", value: 125_800_000,
			want: "sda is at 125.8 MB/s (over 100.0 MB/s for 5m0s)",
		},
		{
			name:   "unitless metric stays a bare number",
			rule:   store.AlertRule{Metric: "array.started", Op: "<", Threshold: 1, ForSeconds: 300},
			entity: "array", value: 0,
			want: "array is at 0.0 (under 1.0 for 5m0s)",
		},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, summarizeThreshold(tc.rule, tc.entity, tc.value), tc.name)
	}
}

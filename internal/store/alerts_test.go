package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fullRule is a rule literal touching every column with a distinct,
// non-zero-where-possible value, so a round-trip test catches a
// transposed field or a wrong Scan destination that a mostly-zero-valued
// literal would hide.
func fullRule(id string) AlertRule {
	return AlertRule{
		ID: id, Name: "Full rule " + id, Enabled: true, Builtin: true,
		Type: "threshold", Kind: "disk", EntityGlob: "disk*", EntityClass: "nvme",
		Metric: "temp.c", Op: ">", Threshold: 70, ClearThreshold: 65,
		WarnThreshold: 60, CriticalThreshold: 80, BandFamily: "disk.temp.nvme",
		ForSeconds: 600, ClearSeconds: 600, EventKinds: "disk.errors",
		MinSeverity: "warning", ClearEventKinds: "disk.errors", ClearMaxSeverity: "info",
		Severity: "warning", Channels: "notify", RenotifyHours: 12, UpdatedAt: 1756400000,
	}
}

// TestAlertsMigrationCreatesSchema pins migration 003_alerts.sql: all four
// alert tables exist, schema_migrations records version 3, and the partial
// unique index that is the one-active-instance-per-(rule,entity) invariant
// (enforced by the DB, not engine bookkeeping -- see
// TestUpsertAlertInstanceRejectsSecondActiveForSameRuleAndEntity below) is
// actually present.
func TestAlertsMigrationCreatesSchema(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "gantry.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for _, table := range []string{"alert_rules", "alert_instances", "alert_silences", "alert_deliveries"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n))
		require.Equal(t, 1, n, "missing table %s", table)
	}

	var version int
	require.NoError(t, db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version))
	require.Equal(t, 3, version)

	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_alert_active'`).Scan(&n))
	require.Equal(t, 1, n)
}

// TestUpsertAlertRuleRoundTripsEveryField pins the full column set
// end-to-end: every field of fullRule survives an insert and a read back
// unchanged, including the two boolean columns (enabled, builtin) which
// SQLite stores as INTEGER.
func TestUpsertAlertRuleRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := fullRule("disk-temp-nvme-high")
	require.NoError(t, s.UpsertAlertRule(want))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestAlertRulesOrderedBuiltinDescThenID pins the documented ordering:
// builtin rules before user rules, alphabetical by id within each group.
func TestAlertRulesOrderedBuiltinDescThenID(t *testing.T) {
	s := newTestStore(t, nil)
	userRule := fullRule("zzz-user-rule")
	userRule.Builtin = false
	builtinB := fullRule("bbb-builtin")
	builtinA := fullRule("aaa-builtin")

	require.NoError(t, s.UpsertAlertRule(userRule))
	require.NoError(t, s.UpsertAlertRule(builtinB))
	require.NoError(t, s.UpsertAlertRule(builtinA))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{"aaa-builtin", "bbb-builtin", "zzz-user-rule"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

// TestUpsertAlertRuleUpdatesExistingRowOnConflict is the CRUD "U": a
// second UpsertAlertRule call with the same id overwrites the row in
// place rather than adding a second one -- the rule-editor path (Task 11
// PUTs a single edited rule), distinct from SeedAlertRules' INSERT-OR-
// IGNORE-if-absent contract tested separately below.
func TestUpsertAlertRuleUpdatesExistingRowOnConflict(t *testing.T) {
	s := newTestStore(t, nil)
	r := fullRule("host-cpu-high")
	require.NoError(t, s.UpsertAlertRule(r))

	r.Threshold = 90
	r.Enabled = false
	require.NoError(t, s.UpsertAlertRule(r))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "must update in place, not insert a second row")
	require.Equal(t, 90.0, got[0].Threshold)
	require.False(t, got[0].Enabled)
}

// TestReplaceAlertRulesWholeDocumentReplace pins ReplaceAlertRules' own
// contract ("one tx, whole-document replace" -- Task 8's /api/groups-style
// PUT semantics): the rules present after the call are exactly the ones
// passed in, not a merge with whatever was there before.
func TestReplaceAlertRulesWholeDocumentReplace(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.UpsertAlertRule(fullRule("old-rule-1")))
	require.NoError(t, s.UpsertAlertRule(fullRule("old-rule-2")))

	replacement := fullRule("new-rule")
	require.NoError(t, s.ReplaceAlertRules([]AlertRule{replacement}))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "new-rule", got[0].ID)
}

// fullInstance mirrors fullRule's reasoning: every field gets a distinct
// non-zero-where-possible value so a round trip catches a transposed
// column.
func fullInstance(ruleID, entity string) AlertInstance {
	return AlertInstance{
		RuleID: ruleID, Kind: "disk", Entity: entity, Metric: "temp.c",
		State: "firing", Severity: "warning", Value: 57.5, Threshold: 55,
		Summary: "disk3 is at 57.5 C", StartedAt: 1756400000, FiredAt: 1756400600,
		ResolvedAt: 0, ResolveReason: "", LastNotifiedAt: 1756400600, NotifyCount: 1,
	}
}

// TestUpsertAlertInstanceInsertsAndRoundTripsEveryField pins the insert
// half of the CRUD "U": ID==0 always inserts a new row and returns the
// generated id; every other field survives unchanged.
func TestUpsertAlertInstanceInsertsAndRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := fullInstance("disk-temp-high", "disk3")
	id, err := s.UpsertAlertInstance(want)
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	want.ID = id

	got, err := s.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestUpsertAlertInstanceRejectsSecondActiveForSameRuleAndEntity is the
// Task 1 test this schema exists to prove: idx_alert_active (a partial
// unique index on (rule_id, entity) WHERE resolved_at = 0) rejects a
// second concurrently-active instance for the same pair -- enforced by
// the DB itself, not by any Go-side check -- and accepts a new one again
// once the first resolves.
func TestUpsertAlertInstanceRejectsSecondActiveForSameRuleAndEntity(t *testing.T) {
	s := newTestStore(t, nil)
	first := fullInstance("array-stopped", "array")
	firstID, err := s.UpsertAlertInstance(first)
	require.NoError(t, err)

	_, err = s.UpsertAlertInstance(fullInstance("array-stopped", "array"))
	require.Error(t, err, "a second active instance for the same (rule_id, entity) must be rejected")

	require.NoError(t, s.ResolveAlertInstance(firstID, 1756401000, "cleared"))

	thirdID, err := s.UpsertAlertInstance(fullInstance("array-stopped", "array"))
	require.NoError(t, err, "a new active instance is accepted once the prior one has resolved")
	require.NotEqual(t, firstID, thirdID)
}

// TestUpsertAlertInstanceWithIDUpdatesExistingRowInPlace is the CRUD "U"
// path the engine (Task 4) uses on every tick a firing instance's value
// changes: passing a non-zero ID updates that row rather than inserting
// a new one, and the partial unique index is untouched by it (still just
// the one active row).
func TestUpsertAlertInstanceWithIDUpdatesExistingRowInPlace(t *testing.T) {
	s := newTestStore(t, nil)
	inst := fullInstance("disk-temp-high", "disk3")
	id, err := s.UpsertAlertInstance(inst)
	require.NoError(t, err)

	inst.ID = id
	inst.Value = 61.2
	inst.NotifyCount = 2
	returnedID, err := s.UpsertAlertInstance(inst)
	require.NoError(t, err)
	require.Equal(t, id, returnedID)

	got, err := s.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "must update in place, not insert a second row")
	require.Equal(t, 61.2, got[0].Value)
	require.Equal(t, int64(2), got[0].NotifyCount)
}

// TestActiveAlertInstancesExcludesResolved pins the resolved_at = 0
// filter: a resolved instance never appears in ActiveAlertInstances,
// regardless of how many other active ones exist.
func TestActiveAlertInstancesExcludesResolved(t *testing.T) {
	s := newTestStore(t, nil)
	activeID, err := s.UpsertAlertInstance(fullInstance("host-cpu-high", ""))
	require.NoError(t, err)
	resolvedID, err := s.UpsertAlertInstance(fullInstance("host-mem-high", ""))
	require.NoError(t, err)
	require.NoError(t, s.ResolveAlertInstance(resolvedID, 1756401000, "cleared"))

	got, err := s.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, activeID, got[0].ID)
}

// TestResolveAlertInstanceSetsResolvedAtAndReason pins ResolveAlertInstance's
// own two writes, read back through AlertHistory (the only reader of a
// resolved row's resolve_reason).
func TestResolveAlertInstanceSetsResolvedAtAndReason(t *testing.T) {
	s := newTestStore(t, nil)
	id, err := s.UpsertAlertInstance(fullInstance("disk-errors", "disk1"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveAlertInstance(id, 1756402000, "timeout"))

	got, err := s.AlertHistory(context.Background(), 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1756402000), got[0].ResolvedAt)
	require.Equal(t, "timeout", got[0].ResolveReason)
}

// TestAlertHistoryReturnsResolvedNewestFirstAndExcludesActive pins the
// history contract: only resolved instances, newest resolution first, an
// active instance never appears no matter how old it started.
func TestAlertHistoryReturnsResolvedNewestFirstAndExcludesActive(t *testing.T) {
	s := newTestStore(t, nil)
	oldID, err := s.UpsertAlertInstance(fullInstance("disk-errors", "disk1"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveAlertInstance(oldID, 1000, "cleared"))

	newID, err := s.UpsertAlertInstance(fullInstance("disk-errors", "disk2"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveAlertInstance(newID, 2000, "cleared"))

	_, err = s.UpsertAlertInstance(fullInstance("disk-errors", "disk3")) // stays active
	require.NoError(t, err)

	got, err := s.AlertHistory(context.Background(), 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 2, "the active instance must not appear in history")
	require.Equal(t, []int64{newID, oldID}, []int64{got[0].ID, got[1].ID}, "newest resolution first")
}

// TestAlertHistoryRespectsFromToAndLimit pins the three query params
// together: from/to filter on resolved_at, limit caps the result.
func TestAlertHistoryRespectsFromToAndLimit(t *testing.T) {
	s := newTestStore(t, nil)
	for i, resolvedAt := range []int64{1000, 2000, 3000, 4000} {
		id, err := s.UpsertAlertInstance(fullInstance("disk-errors", fmt.Sprintf("disk%d", i)))
		require.NoError(t, err)
		require.NoError(t, s.ResolveAlertInstance(id, resolvedAt, "cleared"))
	}

	windowed, err := s.AlertHistory(context.Background(), 1500, 3500, 100)
	require.NoError(t, err)
	require.Len(t, windowed, 2, "only resolved_at in [1500, 3500] should match")

	limited, err := s.AlertHistory(context.Background(), 0, 0, 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)
	require.Equal(t, int64(4000), limited[0].ResolvedAt, "newest first, limit keeps the two newest")
}

// TestAddSilenceRoundTripsEveryField pins AddSilence/Silences end to end.
func TestAddSilenceRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := Silence{RuleID: "disk-temp-high", Entity: "disk3", Reason: "known-hot-week", Until: 2000, CreatedAt: 1000}
	id, err := s.AddSilence(want)
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	want.ID = id

	got, err := s.Silences(context.Background(), 1500) // now < until: not expired
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestSilencesExcludesExpired pins the read-side half of expiry (Maintain
// prunes expired rows separately -- this proves a call in between the two
// still never sees one).
func TestSilencesExcludesExpired(t *testing.T) {
	s := newTestStore(t, nil)
	expiredID, err := s.AddSilence(Silence{RuleID: "r1", Until: 1000, CreatedAt: 0})
	require.NoError(t, err)
	activeID, err := s.AddSilence(Silence{RuleID: "r2", Until: 3000, CreatedAt: 0})
	require.NoError(t, err)

	got, err := s.Silences(context.Background(), 2000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, activeID, got[0].ID)
	require.NotEqual(t, expiredID, got[0].ID)
}

// TestDeleteSilenceRemovesRow pins the "lift a silence" API path (Task 10's
// Alerts view "lift" control).
func TestDeleteSilenceRemovesRow(t *testing.T) {
	s := newTestStore(t, nil)
	id, err := s.AddSilence(Silence{RuleID: "r1", Until: 3000, CreatedAt: 0})
	require.NoError(t, err)
	require.NoError(t, s.DeleteSilence(id))

	got, err := s.Silences(context.Background(), 0)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestRecordDeliveryRoundTripsEveryField pins RecordDelivery/LastDeliveries
// end to end, including the boolean OK column.
func TestRecordDeliveryRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := Delivery{InstanceID: 42, TS: 1000, Channel: "webhook", Target: "home", Phase: "fired",
		Attempts: 3, OK: false, Status: 500, Error: "server error"}
	require.NoError(t, s.RecordDelivery(want))

	got, err := s.LastDeliveries(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	want.ID = got[0].ID
	require.Equal(t, want, got[0])
}

// TestLastDeliveriesOrdersNewestFirstAndRespectsLimit pins the ordering
// and cap LastDeliveries promises for the Settings channels card.
func TestLastDeliveriesOrdersNewestFirstAndRespectsLimit(t *testing.T) {
	s := newTestStore(t, nil)
	for _, ts := range []int64{1000, 2000, 3000} {
		require.NoError(t, s.RecordDelivery(Delivery{InstanceID: 1, TS: ts, Channel: "notify", Phase: "fired", OK: true}))
	}

	got, err := s.LastDeliveries(context.Background(), 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, []int64{3000, 2000}, []int64{got[0].TS, got[1].TS})
}

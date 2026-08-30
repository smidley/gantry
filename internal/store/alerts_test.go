package store

import (
	"context"
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

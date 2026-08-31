package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInsightsMigrationCreatesSchema pins migration 004_insights.sql: all
// three insight tables exist, schema_migrations records version 4, and
// the partial unique index that is the one-active-finding-per-identity-
// tuple invariant (enforced by the DB, not engine bookkeeping -- see
// TestUpsertInsightRejectsSecondActiveForSameIdentityTuple below) is
// actually present, alongside idx_insight_resolved.
func TestInsightsMigrationCreatesSchema(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "gantry.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for _, table := range []string{"insight_instances", "insight_rule_config", "insight_dismissals"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n))
		require.Equal(t, 1, n, "missing table %s", table)
	}

	var version int
	require.NoError(t, db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version))
	require.Equal(t, 4, version)

	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_insight_active'`).Scan(&n))
	require.Equal(t, 1, n)

	// idx_insight_resolved backs InsightHistory's filter/sort and
	// pruneInsights' age cutoff, both of which key on resolved_at; 004's
	// original started_at index was never actually queried by either and
	// is gone -- the exact idx_alert_instances_resolved precedent
	// (alerts_test.go) one migration earlier.
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_insight_resolved'`).Scan(&n))
	require.Equal(t, 1, n, "missing idx_insight_resolved")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_insight_started'`).Scan(&n))
	require.Equal(t, 0, n, "orphaned idx_insight_started should have been dropped")
}

// fullInsight mirrors fullInstance's reasoning (alerts_test.go): every
// field gets a distinct non-zero-where-possible value so a round trip
// catches a transposed column.
func fullInsight(ruleID, victim, culprit, resource string) InsightInstance {
	return InsightInstance{
		RuleID: ruleID, VictimKind: "container", Victim: victim, Culprit: culprit,
		Culprits: culprit + ",sonarr", Resource: resource, State: "active", Severity: "warning",
		Confidence: "likely", Tier: "proxy",
		Statement:     culprit + " is likely slowing " + victim + " on " + resource,
		Evidence:      `{"window_secs":120,"share_pct":78}`,
		StartedAt:     1756400000,
		FiredAt:       1756400600,
		ResolvedAt:    0,
		ResolveReason: "",
		NotifiedAt:    0,
	}
}

// TestUpsertInsightInsertsAndRoundTripsEveryField pins the insert half of
// the CRUD "U": ID==0 always inserts a new row and returns the generated
// id; every other field survives unchanged -- the exact
// TestUpsertAlertInstanceInsertsAndRoundTripsEveryField contract.
func TestUpsertInsightInsertsAndRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3")
	id, err := s.UpsertInsight(want)
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	want.ID = id

	got, err := s.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestUpsertInsightRejectsSecondActiveForSameIdentityTuple is the Task 4
// test this schema exists to prove: idx_insight_active (a partial unique
// index on (rule_id, victim, culprit, resource) WHERE resolved_at = 0)
// rejects a second concurrently-active instance for the same identity
// tuple -- enforced by the DB itself, not by any Go-side check -- and
// accepts a new one again once the first resolves.
func TestUpsertInsightRejectsSecondActiveForSameIdentityTuple(t *testing.T) {
	s := newTestStore(t, nil)
	first := fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3")
	firstID, err := s.UpsertInsight(first)
	require.NoError(t, err)

	_, err = s.UpsertInsight(fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3"))
	require.Error(t, err, "a second active instance for the same identity tuple must be rejected")

	require.NoError(t, s.ResolveInsight(firstID, 1756401000, "cleared"))

	thirdID, err := s.UpsertInsight(fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3"))
	require.NoError(t, err, "a new active instance is accepted once the prior one has resolved")
	require.NotEqual(t, firstID, thirdID)
}

// TestUpsertInsightAcceptsDifferentResourceForSameVictimAndCulprit pins
// Task 4's own documented case: two devices can genuinely both be
// contended by the same victim/culprit pair at once, so the identity
// tuple's resource column must let a second, distinct-resource row stay
// active alongside the first rather than colliding with it.
func TestUpsertInsightAcceptsDifferentResourceForSameVictimAndCulprit(t *testing.T) {
	s := newTestStore(t, nil)
	_, err := s.UpsertInsight(fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3"))
	require.NoError(t, err)

	_, err = s.UpsertInsight(fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk4"))
	require.NoError(t, err, "a different resource for the same victim/culprit pair must be accepted concurrently")

	got, err := s.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
}

// TestUpsertInsightWithIDUpdatesExistingRowInPlace is the CRUD "U" path
// the engine (Task 7) uses on every tick a firing instance's evidence
// changes: passing a non-zero ID updates that row rather than inserting
// a new one, and the partial unique index is untouched by it (still just
// the one active row) -- the exact
// TestUpsertAlertInstanceWithIDUpdatesExistingRowInPlace contract.
func TestUpsertInsightWithIDUpdatesExistingRowInPlace(t *testing.T) {
	s := newTestStore(t, nil)
	inst := fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3")
	id, err := s.UpsertInsight(inst)
	require.NoError(t, err)

	inst.ID = id
	inst.Confidence = "confirmed"
	inst.Tier = "psi"
	inst.Statement = "qbittorrent is starving jellyfin on disk3"
	returnedID, err := s.UpsertInsight(inst)
	require.NoError(t, err)
	require.Equal(t, id, returnedID)

	got, err := s.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "must update in place, not insert a second row")
	require.Equal(t, "confirmed", got[0].Confidence)
	require.Equal(t, "psi", got[0].Tier)
	require.Equal(t, "qbittorrent is starving jellyfin on disk3", got[0].Statement)
}

// TestActiveInsightsExcludesResolved pins the resolved_at = 0 filter: a
// resolved instance never appears in ActiveInsights, regardless of how
// many other active ones exist -- the exact
// TestActiveAlertInstancesExcludesResolved contract.
func TestActiveInsightsExcludesResolved(t *testing.T) {
	s := newTestStore(t, nil)
	activeID, err := s.UpsertInsight(fullInsight("io-driven-cpu-load", "", "sabnzbd", "cpu"))
	require.NoError(t, err)
	resolvedID, err := s.UpsertInsight(fullInsight("memory-squeeze", "", "plex", "memory"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveInsight(resolvedID, 1756401000, "cleared"))

	got, err := s.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, activeID, got[0].ID)
}

// TestResolveInsightSetsResolvedAtAndReason pins ResolveInsight's three
// writes -- state, resolved_at, and resolve_reason -- read back through
// InsightHistory (the only reader of a resolved row's resolve_reason).
// state must actually flip to "resolved" -- the exact
// TestResolveAlertInstanceSetsResolvedAtAndReason contract.
func TestResolveInsightSetsResolvedAtAndReason(t *testing.T) {
	s := newTestStore(t, nil)
	id, err := s.UpsertInsight(fullInsight("disk-spinup-churn", "disk5", "plex", "disk5"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveInsight(id, 1756402000, "no-data"))

	got, err := s.InsightHistory(context.Background(), 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "resolved", got[0].State)
	require.Equal(t, int64(1756402000), got[0].ResolvedAt)
	require.Equal(t, "no-data", got[0].ResolveReason)
}

// TestResolveInsightErrorsWhenIDDoesNotExist pins the RowsAffected guard:
// resolving an id that isn't in insight_instances must return an error,
// not silently succeed -- the exact
// TestResolveAlertInstanceErrorsWhenIDDoesNotExist contract.
func TestResolveInsightErrorsWhenIDDoesNotExist(t *testing.T) {
	s := newTestStore(t, nil)
	err := s.ResolveInsight(999999, 1756402000, "cleared")
	require.Error(t, err)
}

// TestInsightHistoryReturnsResolvedNewestFirstAndExcludesActive pins the
// history contract: only resolved instances, newest resolution first, an
// active instance never appears no matter how old it started -- the
// exact TestAlertHistoryReturnsResolvedNewestFirstAndExcludesActive
// contract.
func TestInsightHistoryReturnsResolvedNewestFirstAndExcludesActive(t *testing.T) {
	s := newTestStore(t, nil)
	oldID, err := s.UpsertInsight(fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk1"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveInsight(oldID, 1000, "cleared"))

	newID, err := s.UpsertInsight(fullInsight("disk-io-contention", "sonarr", "qbittorrent", "disk2"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveInsight(newID, 2000, "cleared"))

	_, err = s.UpsertInsight(fullInsight("disk-io-contention", "radarr", "qbittorrent", "disk3")) // stays active
	require.NoError(t, err)

	got, err := s.InsightHistory(context.Background(), 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 2, "the active instance must not appear in history")
	require.Equal(t, []int64{newID, oldID}, []int64{got[0].ID, got[1].ID}, "newest resolution first")
}

// TestInsightHistoryRespectsFromToAndLimit pins the three query params
// together: from/to filter on resolved_at, limit caps the result -- the
// exact TestAlertHistoryRespectsFromToAndLimit contract.
func TestInsightHistoryRespectsFromToAndLimit(t *testing.T) {
	s := newTestStore(t, nil)
	for i, resolvedAt := range []int64{1000, 2000, 3000, 4000} {
		id, err := s.UpsertInsight(fullInsight("disk-io-contention", fmt.Sprintf("victim%d", i), "qbittorrent", fmt.Sprintf("disk%d", i)))
		require.NoError(t, err)
		require.NoError(t, s.ResolveInsight(id, resolvedAt, "cleared"))
	}

	windowed, err := s.InsightHistory(context.Background(), 1500, 3500, 100)
	require.NoError(t, err)
	require.Len(t, windowed, 2, "only resolved_at in [1500, 3500] should match")

	limited, err := s.InsightHistory(context.Background(), 0, 0, 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)
	require.Equal(t, int64(4000), limited[0].ResolvedAt, "newest first, limit keeps the two newest")
}

// TestStaleActiveInsightsResolvesActivesWithRestartReason pins Open
// question 5's recommendation: every still-active row is resolved with
// reason 'restart' at boot, since the live ring is empty after a restart
// and a carried-over "active" finding would be asserting something the
// engine cannot currently see. An already-resolved row (with its own,
// different reason) is left completely untouched.
func TestStaleActiveInsightsResolvesActivesWithRestartReason(t *testing.T) {
	s := newTestStore(t, nil)
	activeID, err := s.UpsertInsight(fullInsight("disk-io-contention", "jellyfin", "qbittorrent", "disk3"))
	require.NoError(t, err)

	priorResolvedID, err := s.UpsertInsight(fullInsight("memory-squeeze", "", "plex", "memory"))
	require.NoError(t, err)
	require.NoError(t, s.ResolveInsight(priorResolvedID, 1000, "cleared"))

	require.NoError(t, s.StaleActiveInsights(2000))

	got, err := s.InsightHistory(context.Background(), 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := make(map[int64]InsightInstance, len(got))
	for _, i := range got {
		byID[i.ID] = i
	}
	require.Equal(t, "resolved", byID[activeID].State)
	require.Equal(t, int64(2000), byID[activeID].ResolvedAt)
	require.Equal(t, "restart", byID[activeID].ResolveReason)

	require.Equal(t, "cleared", byID[priorResolvedID].ResolveReason, "an already-resolved row must be untouched")
	require.Equal(t, int64(1000), byID[priorResolvedID].ResolvedAt, "an already-resolved row's resolved_at must be untouched")

	active, err := s.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Empty(t, active, "no active insight should survive a restart")
}

// TestStaleActiveInsightsNoopOnEmptyStore pins the degenerate case: a
// fresh boot with no rows at all must not error.
func TestStaleActiveInsightsNoopOnEmptyStore(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.StaleActiveInsights(1000))
}

// fullRuleConfig mirrors fullRule's reasoning (alerts_test.go): every
// field gets a distinct non-zero-where-possible value so a round trip
// catches a transposed column.
func fullRuleConfig(ruleID string) InsightRuleConfig {
	return InsightRuleConfig{
		RuleID: ruleID, Enabled: true, Notify: false,
		Overrides: `{"util_pct":95}`, UpdatedAt: 1756400000,
	}
}

func insightConfigsByID(cfgs []InsightRuleConfig) map[string]InsightRuleConfig {
	out := make(map[string]InsightRuleConfig, len(cfgs))
	for _, c := range cfgs {
		out[c.RuleID] = c
	}
	return out
}

// TestUpsertInsightRuleConfigRoundTripsEveryField pins the full column
// set end-to-end -- the exact TestUpsertAlertRuleRoundTripsEveryField
// contract, including the two boolean columns (enabled, notify) which
// SQLite stores as INTEGER.
func TestUpsertInsightRuleConfigRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := fullRuleConfig("disk-io-contention")
	require.NoError(t, s.UpsertInsightRuleConfig(want))

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestUpsertInsightRuleConfigUpdatesExistingRowOnConflict is the CRUD
// "U": a second UpsertInsightRuleConfig call with the same rule_id
// overwrites the row in place rather than adding a second one -- the
// rule-editor path (Task 9 PUTs a whole-document replace), distinct from
// SeedInsightRuleConfigs' INSERT-OR-IGNORE-if-absent contract tested
// separately below.
func TestUpsertInsightRuleConfigUpdatesExistingRowOnConflict(t *testing.T) {
	s := newTestStore(t, nil)
	c := fullRuleConfig("cpu-starvation")
	require.NoError(t, s.UpsertInsightRuleConfig(c))

	c.Enabled = false
	c.Notify = true
	c.Overrides = `{"cpu_pct":45}`
	require.NoError(t, s.UpsertInsightRuleConfig(c))

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "must update in place, not insert a second row")
	require.False(t, got[0].Enabled)
	require.True(t, got[0].Notify)
	require.Equal(t, `{"cpu_pct":45}`, got[0].Overrides)
}

// TestInsightRuleConfigsOrderedByRuleID pins a deterministic read order
// (there is no builtin/user split for this table -- every row is a
// tuning knob over a compiled-in rule).
func TestInsightRuleConfigsOrderedByRuleID(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.UpsertInsightRuleConfig(fullRuleConfig("parity-slowdown")))
	require.NoError(t, s.UpsertInsightRuleConfig(fullRuleConfig("cpu-starvation")))
	require.NoError(t, s.UpsertInsightRuleConfig(fullRuleConfig("disk-io-contention")))

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"cpu-starvation", "disk-io-contention", "parity-slowdown"},
		[]string{got[0].RuleID, got[1].RuleID, got[2].RuleID})
}

// TestSeedInsightRuleConfigsInsertsAllDefaultsOnFreshDB pins the basic
// seed path -- the exact TestSeedAlertRulesInsertsAllDefaultsOnFreshDB
// contract, including the updated_at auto-stamp.
func TestSeedInsightRuleConfigsInsertsAllDefaultsOnFreshDB(t *testing.T) {
	s := newTestStore(t, nil)
	defaults := []InsightRuleConfig{
		{RuleID: "disk-io-contention", Enabled: true},
		{RuleID: "cpu-starvation", Enabled: true},
	}
	require.NoError(t, s.SeedInsightRuleConfigs(defaults))

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, c := range got {
		require.True(t, c.Enabled, "every seeded default starts enabled")
		require.NotZero(t, c.UpdatedAt, "SeedInsightRuleConfigs must stamp updated_at")
	}
}

// TestSeedInsightRuleConfigsIsIdempotent pins "seeding twice inserts
// once" -- the exact TestSeedAlertRulesIsIdempotent contract.
func TestSeedInsightRuleConfigsIsIdempotent(t *testing.T) {
	s := newTestStore(t, nil)
	defaults := []InsightRuleConfig{{RuleID: "disk-io-contention", Enabled: true}}
	require.NoError(t, s.SeedInsightRuleConfigs(defaults))
	require.NoError(t, s.SeedInsightRuleConfigs(defaults))

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "a second seed call must not duplicate the row")
}

// TestSeedInsightRuleConfigsNeverOverwritesAnEditedRow pins the
// upgrade-safety contract: SeedInsightRuleConfigs only ever inserts a
// row whose rule_id is absent -- it never touches a row that already
// exists, however the user left it (disabled, notify flipped on,
// thresholds overridden). This is the mechanism, named in the schema's
// own comment, for a rule added by a later Gantry version to get a
// config row on upgrade without ever stomping a threshold the user has
// tuned.
func TestSeedInsightRuleConfigsNeverOverwritesAnEditedRow(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.SeedInsightRuleConfigs([]InsightRuleConfig{{RuleID: "disk-io-contention", Enabled: true}}))

	cfgs, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	edited := cfgs[0]
	edited.Enabled = false
	edited.Notify = true
	edited.Overrides = `{"util_pct":80}`
	require.NoError(t, s.UpsertInsightRuleConfig(edited))

	require.NoError(t, s.SeedInsightRuleConfigs([]InsightRuleConfig{{RuleID: "disk-io-contention", Enabled: true}}))

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "re-seeding must not add a duplicate row for an already-seeded id")
	require.Equal(t, edited, got[0], "re-seeding must not resurrect the enabled state or clear the overrides")
}

// TestSeedInsightRuleConfigsInsertsANewlyAddedDefaultOnNextBoot pins the
// actual upgrade mechanism: seeding is per-rule-id absence, not a global
// version marker, so a default introduced by a later Gantry upgrade is
// simply inserted the next time SeedInsightRuleConfigs runs -- while an
// already-seeded, already-edited row is left untouched -- the exact
// TestSeedAlertRulesInsertsANewlyAddedDefaultOnNextBoot contract.
func TestSeedInsightRuleConfigsInsertsANewlyAddedDefaultOnNextBoot(t *testing.T) {
	s := newTestStore(t, nil)
	oldInstall := []InsightRuleConfig{{RuleID: "disk-io-contention", Enabled: true}}
	upgradeIntroduced := InsightRuleConfig{RuleID: "gpu-engine-contention", Enabled: true}

	require.NoError(t, s.SeedInsightRuleConfigs(oldInstall))

	cfgs, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, cfgs, 1)
	edited := cfgs[0]
	edited.Enabled = false
	require.NoError(t, s.UpsertInsightRuleConfig(edited))

	require.NoError(t, s.SeedInsightRuleConfigs([]InsightRuleConfig{oldInstall[0], upgradeIntroduced})) // "upgrade"

	got, err := s.InsightRuleConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	newRow, ok := insightConfigsByID(got)[upgradeIntroduced.RuleID]
	require.True(t, ok, "the newly-added default must be inserted on this boot")
	require.True(t, newRow.Enabled)
	require.False(t, insightConfigsByID(got)["disk-io-contention"].Enabled, "an already-seeded edited rule must survive the same boot untouched")
}

// TestAddInsightDismissalRoundTripsEveryField pins AddInsightDismissal/
// InsightDismissals end to end -- the exact TestAddSilenceRoundTripsEveryField
// contract.
func TestAddInsightDismissalRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := InsightDismissal{RuleID: "disk-io-contention", Victim: "jellyfin", Culprit: "qbittorrent",
		Resource: "disk3", Until: 2000, CreatedAt: 1000}
	id, err := s.AddInsightDismissal(want)
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	want.ID = id

	got, err := s.InsightDismissals(context.Background(), 1500) // now < until: not expired
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestInsightDismissalsExcludesExpired pins the read-side half of expiry
// (Maintain prunes expired rows separately -- this proves a call in
// between the two still never sees one) -- the exact
// TestSilencesExcludesExpired contract.
func TestInsightDismissalsExcludesExpired(t *testing.T) {
	s := newTestStore(t, nil)
	expiredID, err := s.AddInsightDismissal(InsightDismissal{RuleID: "disk-io-contention", Until: 1000, CreatedAt: 0})
	require.NoError(t, err)
	activeID, err := s.AddInsightDismissal(InsightDismissal{RuleID: "cpu-starvation", Until: 3000, CreatedAt: 0})
	require.NoError(t, err)

	got, err := s.InsightDismissals(context.Background(), 2000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, activeID, got[0].ID)
	require.NotEqual(t, expiredID, got[0].ID)
}

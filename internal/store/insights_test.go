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
// actually present, alongside idx_insight_started.
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

	for _, idx := range []string{"idx_insight_active", "idx_insight_started"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n))
		require.Equal(t, 1, n, "missing index %s", idx)
	}
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

package store

import (
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

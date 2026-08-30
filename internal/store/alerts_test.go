package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

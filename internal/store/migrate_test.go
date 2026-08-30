package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenDBCreatesSchema(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "gantry.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for _, table := range []string{"series", "samples_1m", "samples_10m", "samples_1h", "events", "settings", "schema_migrations"} {
		var n int
		err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		require.NoError(t, err)
		require.Equal(t, 1, n, "missing table %s", table)
	}

	var mode string
	require.NoError(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode)

	var av int
	require.NoError(t, db.QueryRow(`PRAGMA auto_vacuum`).Scan(&av))
	require.Equal(t, 2, av) // 2 = INCREMENTAL
}

func TestOpenDBIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gantry.db")
	db, err := OpenDB(path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db2, err := OpenDB(path) // second open must not fail re-applying migrations
	require.NoError(t, err)
	defer func() { _ = db2.Close() }()

	var v int
	require.NoError(t, db2.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 3, v)
}

func TestMigrationVersionsComeFromFilenamePrefix(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "g.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var versions []int
	for rows.Next() {
		var v int
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	require.Equal(t, []int{1, 2, 3}, versions) // 001_core.sql, 002_ts_indexes.sql, 003_alerts.sql

	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_samples_1m_ts'`).Scan(&n))
	require.Equal(t, 1, n)
}

// TestSortMigrationsOrdersByParsedNumericVersionNotFilenameString pins the
// migration-ordering hardening: a lexicographic sort would place
// "10_add_col.sql" before "2_second.sql" and "9_ninth.sql" (comparing the
// leading "1" byte against "2"/"9"), the classic zero-padding trap.
// sortMigrations must order by the PARSED numeric version instead.
func TestSortMigrationsOrdersByParsedNumericVersionNotFilenameString(t *testing.T) {
	names := []string{"10_add_col.sql", "2_second.sql", "9_ninth.sql", "1_first.sql"}
	require.NoError(t, sortMigrations(names))
	require.Equal(t, []string{"1_first.sql", "2_second.sql", "9_ninth.sql", "10_add_col.sql"}, names)
}

// TestUpgradeFromOnlyCoreAndIndexesAppliesAlertsMigrationCleanly
// simulates the actual upgrade this branch ships into: a real box that
// has only ever run 001_core.sql and 002_ts_indexes.sql (003_alerts.sql
// didn't exist yet when it last booted) opening its existing database
// file against a binary that now also carries 003. Applying 003 must
// succeed, must preserve the row already sitting in events across 003's
// events-table rebuild, and must carry that row's id forward as the new
// AUTOINCREMENT high-water mark -- not just work on a freshly-created,
// empty events table the way every other migration test in this file
// does.
func TestUpgradeFromOnlyCoreAndIndexesAppliesAlertsMigrationCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gantry.db")

	// Reproduce exactly what a pre-003 boot left on disk: 001 and 002
	// applied, schema_migrations recording just those two versions, and
	// one real row already in events.
	func() {
		db, err := sql.Open("sqlite", dsn(path))
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`)
		require.NoError(t, err)
		for _, name := range []string{"001_core.sql", "002_ts_indexes.sql"} {
			body, err := migrationFS.ReadFile("migrations/" + name)
			require.NoError(t, err)
			_, err = db.Exec(string(body))
			require.NoError(t, err)
		}
		_, err = db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (1,0),(2,0)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO events (id, ts, kind, entity) VALUES (50, 1000, 'pre-existing', 'e')`)
		require.NoError(t, err)
	}()

	// Reopen exactly like a real restart: OpenDB finds 003 unapplied and runs it.
	db, err := OpenDB(path)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var version int
	require.NoError(t, db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version))
	require.Equal(t, 3, version)

	var kind string
	require.NoError(t, db.QueryRow(`SELECT kind FROM events WHERE id = 50`).Scan(&kind))
	require.Equal(t, "pre-existing", kind, "003's events-table rebuild must preserve pre-existing rows")

	for _, table := range []string{"alert_rules", "alert_instances", "alert_silences", "alert_deliveries"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n))
		require.Equal(t, 1, n, "missing table %s", table)
	}

	// The high-water mark from the pre-upgrade id must carry forward: a
	// full delete followed by a fresh insert must not reuse id 50 or
	// anything at or below it.
	_, err = db.Exec(`DELETE FROM events`)
	require.NoError(t, err)
	res, err := db.Exec(`INSERT INTO events (ts, kind, entity) VALUES (2000, 'post-upgrade', 'e')`)
	require.NoError(t, err)
	newID, err := res.LastInsertId()
	require.NoError(t, err)
	require.Greater(t, newID, int64(50))
}

// TestSortMigrationsErrorsOnBadVersionPrefix pins the validation half:
// a name that doesn't parse as "<number>_<desc>.sql" is an error, not a
// silently-skipped or zero-valued entry -- matching applyMigrations' own
// pre-existing validation, just surfaced earlier (before sorting, not
// during the apply loop).
func TestSortMigrationsErrorsOnBadVersionPrefix(t *testing.T) {
	require.Error(t, sortMigrations([]string{"1_ok.sql", "not_numeric.sql"}))
	require.Error(t, sortMigrations([]string{"nounderscore.sql"}))
}

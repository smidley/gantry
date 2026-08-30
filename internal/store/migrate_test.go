package store

import (
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

// TestSortMigrationsErrorsOnBadVersionPrefix pins the validation half:
// a name that doesn't parse as "<number>_<desc>.sql" is an error, not a
// silently-skipped or zero-valued entry -- matching applyMigrations' own
// pre-existing validation, just surfaced earlier (before sorting, not
// during the apply loop).
func TestSortMigrationsErrorsOnBadVersionPrefix(t *testing.T) {
	require.Error(t, sortMigrations([]string{"1_ok.sql", "not_numeric.sql"}))
	require.Error(t, sortMigrations([]string{"nounderscore.sql"}))
}

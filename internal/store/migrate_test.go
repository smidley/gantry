package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenDBCreatesSchema(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "gantry.db"))
	require.NoError(t, err)
	defer db.Close()

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
	defer db2.Close()

	var v int
	require.NoError(t, db2.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 2, v)
}

func TestMigrationVersionsComeFromFilenamePrefix(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "g.db"))
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	require.Equal(t, []int{1, 2}, versions) // 001_core.sql, 002_ts_indexes.sql

	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_samples_1m_ts'`).Scan(&n))
	require.Equal(t, 1, n)
}

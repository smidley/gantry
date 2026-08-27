// Package store owns Gantry's metric storage: the live ring buffer,
// SQLite rollup tiers, events, and settings.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// dsn builds the SQLite connection string shared by the write handle
// (OpenDB) and the read pool (openReadPool) — both connect to the same
// file with the same pragmas, so a reader sees exactly the writer's
// journal mode and busy-timeout behavior.
func dsn(path string) string {
	return "file:" + path + "?_pragma=auto_vacuum(INCREMENTAL)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
}

// OpenDB opens (creating if needed) the Gantry SQLite database at path,
// sets connection pragmas, and applies any unapplied embedded migrations.
// This is the single-writer handle (MaxOpenConns(1)); openReadPool opens
// the companion read pool.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openReadPool opens a second handle to the same database for concurrent
// readers (WAL mode allows this safely alongside the single writer).
// Schema is guaranteed present already: Open always calls OpenDB first.
func openReadPool(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	return db, nil
}

// parseMigrationVersion extracts the leading "<number>_" version prefix
// from a migration filename, e.g. "10_add_col.sql" -> 10. Shared by
// sortMigrations (ordering) and applyMigrations (the version actually
// recorded/checked against schema_migrations), so both agree on what
// "the version" of a given filename is.
func parseMigrationVersion(name string) (int, error) {
	numStr, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %s: name must be <number>_<desc>.sql", name)
	}
	version, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("migration %s: bad version prefix: %w", name, err)
	}
	return version, nil
}

// sortMigrations orders names in place by their PARSED numeric version,
// not the filename string: a plain lexicographic sort places
// "10_x.sql" before "2_x.sql" (comparing the leading "1" byte against
// "2"), the classic zero-padding trap. Returns an error, leaving names
// unordered, if any entry's version prefix doesn't parse.
func sortMigrations(names []string) error {
	versions := make(map[string]int, len(names))
	for _, name := range names {
		v, err := parseMigrationVersion(name)
		if err != nil {
			return err
		}
		versions[name] = v
	}
	sort.Slice(names, func(i, j int) bool { return versions[names[i]] < versions[names[j]] })
	return nil
}

func applyMigrations(db *sql.DB) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if err := sortMigrations(names); err != nil {
		return err
	}

	for _, name := range names {
		version, err := parseMigrationVersion(name)
		if err != nil {
			return err
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

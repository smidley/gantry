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
		db.Close()
		return nil, err
	}
	if err := applyMigrations(db); err != nil {
		db.Close()
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

func applyMigrations(db *sql.DB) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		numStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("migration %s: name must be <number>_<desc>.sql", name)
		}
		version, err := strconv.Atoi(numStr)
		if err != nil {
			return fmt.Errorf("migration %s: bad version prefix: %w", name, err)
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
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

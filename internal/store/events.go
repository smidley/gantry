package store

import (
	"context"
	"strings"
)

type Event struct {
	ID       int64
	TS       int64
	Kind     string
	Entity   string
	Severity string
	Detail   string
}

type EventFilter struct {
	Kinds    []string
	Entity   string
	From, To int64
	Limit    int
}

func (s *Store) AppendEvent(e Event) (int64, error) {
	if e.TS == 0 {
		e.TS = s.clock().Unix()
	}
	if e.Severity == "" {
		e.Severity = "info"
	}
	res, err := s.db.Exec(`INSERT INTO events (ts, kind, entity, severity, detail) VALUES (?,?,?,?,?)`,
		e.TS, e.Kind, e.Entity, e.Severity, e.Detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// QueryEvents answers /api/events off the read pool (s.readDB,
// MaxOpenConns(4)), same as QuerySeries/TopEntities in query.go -- never
// s.db, the single-writer handle Maintain's multi-second flush/downsample
// lock can hold for a while. ctx is passed straight through to
// QueryContext so a cancelled request (the entity filter fires on every
// keystroke; see Events.svelte's own debounce for the frontend half of
// this fix) actually stops the query rather than queuing behind that lock
// and running to completion anyway.
func (s *Store) QueryEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	q := `SELECT id, ts, kind, entity, severity, detail FROM events WHERE 1=1`
	var args []any
	if len(f.Kinds) > 0 {
		q += ` AND kind IN (?` + strings.Repeat(",?", len(f.Kinds)-1) + `)`
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.Entity != "" {
		q += ` AND entity = ?`
		args = append(args, f.Entity)
	}
	if f.From > 0 {
		q += ` AND ts >= ?`
		args = append(args, f.From)
	}
	if f.To > 0 {
		q += ` AND ts <= ?`
		args = append(args, f.To)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Entity, &e.Severity, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryEventsSince reads forward by id (NOT ts) so an event rule's
// cursor can never miss a row inserted with an equal or earlier
// timestamp than one it already saw -- the clock is not monotonic
// across an NTP step, but id is: events.id is INTEGER PRIMARY KEY
// AUTOINCREMENT (migrations/003_alerts.sql), so SQLite never reissues an
// id that has ever been used, even across PruneOnce's full-table
// deletes. Defense in depth: a persisted cursor should still be clamped
// to MaxEventID at boot rather than trusted blindly. limit <= 0 defaults
// to 500 (an event rule's Tick reads in bounded pages, not the whole
// backlog at once).
func (s *Store) QueryEventsSince(ctx context.Context, afterID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, ts, kind, entity, severity, detail FROM events WHERE id > ? ORDER BY id ASC LIMIT ?`,
		afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Entity, &e.Severity, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MaxEventID returns the highest event id, or 0 when the events table is
// empty -- Task 4's engine seeds an event rule's cursor here at boot so a
// restart doesn't replay the entire events table as fresh alerts.
func (s *Store) MaxEventID(ctx context.Context) (int64, error) {
	var id int64
	err := s.readDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id)
	return id, err
}

package store

import "strings"

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

func (s *Store) QueryEvents(f EventFilter) ([]Event, error) {
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

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

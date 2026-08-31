package store

import "context"

// OverviewAck is one row of overview_acks -- "stop showing me this for a
// while" on one Overview attention row: a suppressed (kind, entity)
// concern with an expiry, the InsightDismissal shape applied to the
// frame-derived anomalies (unhealthy container, disk usage/errors, array
// stopped, critical source). Kind and Entity are always concrete -- see
// 005_overview_acks.sql for why no global/wildcard shape exists.
type OverviewAck struct {
	ID        int64
	Kind      string
	Entity    string
	Until     int64
	CreatedAt int64
}

// Acks returns every ack whose Until is still in the future relative to
// now -- the exact Silences/InsightDismissals contract: an
// already-expired row is excluded from the read even before Maintain
// prunes it.
func (s *Store) Acks(ctx context.Context, now int64) ([]OverviewAck, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, kind, entity, until, created_at FROM overview_acks WHERE until > ? ORDER BY until`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []OverviewAck
	for rows.Next() {
		var a OverviewAck
		if err := rows.Scan(&a.ID, &a.Kind, &a.Entity, &a.Until, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAck inserts a new ack and returns its generated id.
func (s *Store) AddAck(a OverviewAck) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO overview_acks (kind, entity, until, created_at) VALUES (?,?,?,?)`,
		a.Kind, a.Entity, a.Until, a.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteAck lifts an ack early -- a no-op, not an error, for an id
// that's already gone (the DeleteSilence convention: lifting is
// naturally idempotent from the caller's point of view).
func (s *Store) DeleteAck(id int64) error {
	_, err := s.db.Exec(`DELETE FROM overview_acks WHERE id=?`, id)
	return err
}

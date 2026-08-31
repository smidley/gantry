package store

import (
	"context"
	"fmt"
)

// InsightInstance is one row of insight_instances -- one field per column,
// in schema order. See migrations/004_insights.sql for what each column
// means; the comments live there, not duplicated here.
type InsightInstance struct {
	ID            int64
	RuleID        string
	VictimKind    string
	Victim        string
	Culprit       string
	Culprits      string
	Resource      string
	State         string
	Severity      string
	Confidence    string
	Tier          string
	Statement     string
	Evidence      string
	StartedAt     int64
	FiredAt       int64
	ResolvedAt    int64
	ResolveReason string
	NotifiedAt    int64
}

const insightInstanceColumns = `id, rule_id, victim_kind, victim, culprit, culprits, resource, state, severity,
	confidence, tier, statement, evidence, started_at, fired_at, resolved_at, resolve_reason, notified_at`

func scanInsightInstance(row rowScanner, i *InsightInstance) error {
	return row.Scan(&i.ID, &i.RuleID, &i.VictimKind, &i.Victim, &i.Culprit, &i.Culprits, &i.Resource, &i.State,
		&i.Severity, &i.Confidence, &i.Tier, &i.Statement, &i.Evidence, &i.StartedAt, &i.FiredAt, &i.ResolvedAt,
		&i.ResolveReason, &i.NotifiedAt)
}

// ActiveInsights returns every instance with resolved_at = 0 -- the exact
// ActiveAlertInstances contract: what the engine (Task 7) walks every
// tick and what the frame's `insights` block (Task 9) is filtered from.
func (s *Store) ActiveInsights(ctx context.Context) ([]InsightInstance, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT `+insightInstanceColumns+` FROM insight_instances WHERE resolved_at = 0 ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []InsightInstance
	for rows.Next() {
		var i InsightInstance
		if err := scanInsightInstance(rows, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// UpsertInsight inserts a new instance when ID is 0 (returning the
// generated id) or updates the existing row in place otherwise -- the
// exact UpsertAlertInstance contract. The insert path is where
// idx_insight_active actually does its job: two concurrently-active rows
// for the same (rule_id, victim, culprit, resource) violate that partial
// unique index and this returns the resulting constraint error unchanged
// -- there is no Go-side pre-check, by design (see the index's own
// comment in 004_insights.sql).
func (s *Store) UpsertInsight(i InsightInstance) (int64, error) {
	if i.ID != 0 {
		_, err := s.db.Exec(`UPDATE insight_instances SET rule_id=?, victim_kind=?, victim=?, culprit=?, culprits=?,
			resource=?, state=?, severity=?, confidence=?, tier=?, statement=?, evidence=?, started_at=?, fired_at=?,
			resolved_at=?, resolve_reason=?, notified_at=? WHERE id=?`,
			i.RuleID, i.VictimKind, i.Victim, i.Culprit, i.Culprits, i.Resource, i.State, i.Severity, i.Confidence,
			i.Tier, i.Statement, i.Evidence, i.StartedAt, i.FiredAt, i.ResolvedAt, i.ResolveReason, i.NotifiedAt, i.ID)
		return i.ID, err
	}
	res, err := s.db.Exec(`INSERT INTO insight_instances (rule_id, victim_kind, victim, culprit, culprits, resource,
		state, severity, confidence, tier, statement, evidence, started_at, fired_at, resolved_at, resolve_reason,
		notified_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.RuleID, i.VictimKind, i.Victim, i.Culprit, i.Culprits, i.Resource, i.State, i.Severity, i.Confidence,
		i.Tier, i.Statement, i.Evidence, i.StartedAt, i.FiredAt, i.ResolvedAt, i.ResolveReason, i.NotifiedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ResolveInsight closes out an instance: state, resolved_at, and
// resolve_reason are the only columns a resolve ever touches (the
// evidence bundle keeps whatever it last held while firing -- Open
// question 4's denormalised-at-fire-time numbers are a snapshot, not a
// live view) -- the exact ResolveAlertInstance contract, including its
// not-found guard: a plain UPDATE...WHERE matches zero rows without
// erroring on its own, which would otherwise hide a caller (Task 7's
// engine, which may hold a cached instance id) racing a row that was
// already pruned or never existed.
func (s *Store) ResolveInsight(id, at int64, reason string) error {
	res, err := s.db.Exec(`UPDATE insight_instances SET state='resolved', resolved_at=?, resolve_reason=? WHERE id=?`,
		at, reason, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("insight instance %d: not found", id)
	}
	return nil
}

// InsightHistory returns resolved instances (resolved_at > 0; an active
// instance never appears here regardless of how old it started) with
// resolved_at in [from, to] when either bound is non-zero, newest
// resolution first, capped at limit -- the exact AlertHistory contract.
func (s *Store) InsightHistory(ctx context.Context, from, to int64, limit int) ([]InsightInstance, error) {
	q := `SELECT ` + insightInstanceColumns + ` FROM insight_instances WHERE resolved_at > 0`
	var args []any
	if from > 0 {
		q += ` AND resolved_at >= ?`
		args = append(args, from)
	}
	if to > 0 {
		q += ` AND resolved_at <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY resolved_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []InsightInstance
	for rows.Next() {
		var i InsightInstance
		if err := scanInsightInstance(rows, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// InsightRuleConfig is one row of insight_rule_config -- one field per
// column, in schema order. Overrides is a JSON blob (threshold name ->
// value) at this layer, the same way AlertRule keeps its own
// multi-value columns as plain strings; decoding it is the engine's job
// (Tasks 6/7).
type InsightRuleConfig struct {
	RuleID    string
	Enabled   bool
	Notify    bool
	Overrides string
	UpdatedAt int64
}

// InsightRuleConfigs returns every configured rule, alphabetical by
// rule_id -- there is no builtin/user split here (contrast AlertRules):
// every row is a tuning knob over a rule that is always compiled in.
func (s *Store) InsightRuleConfigs(ctx context.Context) ([]InsightRuleConfig, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT rule_id, enabled, notify, overrides, updated_at FROM insight_rule_config ORDER BY rule_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []InsightRuleConfig
	for rows.Next() {
		var c InsightRuleConfig
		if err := rows.Scan(&c.RuleID, &c.Enabled, &c.Notify, &c.Overrides, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertInsightRuleConfig inserts a new config row or, if rule_id
// already exists, overwrites every column in place -- the rule-editor
// write path (Task 9's PUT /api/insights/rules), the exact
// UpsertAlertRule contract. Contrast SeedInsightRuleConfigs, which never
// overwrites an existing row.
func (s *Store) UpsertInsightRuleConfig(c InsightRuleConfig) error {
	_, err := s.db.Exec(`INSERT INTO insight_rule_config (rule_id, enabled, notify, overrides, updated_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT (rule_id) DO UPDATE SET
			enabled=excluded.enabled, notify=excluded.notify, overrides=excluded.overrides, updated_at=excluded.updated_at`,
		c.RuleID, c.Enabled, c.Notify, c.Overrides, c.UpdatedAt)
	return err
}

// SeedInsightRuleConfigs inserts every config in defaults whose rule_id
// is not already present, leaving any existing row -- edited, disabled,
// or otherwise -- completely untouched. The exact SeedAlertRules
// contract (alert_defaults.go): never updates, never deletes, idempotent
// across any number of calls, and a rule added in a later version gets a
// config row on the next boot of an existing DB with no separate
// seed-version marker to maintain.
func (s *Store) SeedInsightRuleConfigs(defaults []InsightRuleConfig) error {
	now := s.clock().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, c := range defaults {
		if c.UpdatedAt == 0 {
			c.UpdatedAt = now
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO insight_rule_config (rule_id, enabled, notify, overrides, updated_at)
			VALUES (?,?,?,?,?)`,
			c.RuleID, c.Enabled, c.Notify, c.Overrides, c.UpdatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// InsightDismissal is one row of insight_dismissals -- "this wasn't
// useful" feedback without ML (Open question 3): a suppressed identity
// tuple with an expiry, no ranking or learning behind it.
type InsightDismissal struct {
	ID        int64
	RuleID    string
	Victim    string
	Culprit   string
	Resource  string
	Until     int64
	CreatedAt int64
}

// InsightDismissals returns every dismissal whose Until is still in the
// future relative to now -- the exact Silences contract: an
// already-expired row is excluded from the read even before Maintain
// prunes it.
func (s *Store) InsightDismissals(ctx context.Context, now int64) ([]InsightDismissal, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, rule_id, victim, culprit, resource, until, created_at FROM insight_dismissals WHERE until > ? ORDER BY until`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []InsightDismissal
	for rows.Next() {
		var d InsightDismissal
		if err := rows.Scan(&d.ID, &d.RuleID, &d.Victim, &d.Culprit, &d.Resource, &d.Until, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddInsightDismissal inserts a new dismissal and returns its generated id.
func (s *Store) AddInsightDismissal(d InsightDismissal) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO insight_dismissals (rule_id, victim, culprit, resource, until, created_at) VALUES (?,?,?,?,?,?)`,
		d.RuleID, d.Victim, d.Culprit, d.Resource, d.Until, d.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StaleActiveInsights marks every still-active row (resolved_at = 0,
// the same predicate idx_insight_active enforces) resolved with reason
// 'restart' at time at. The live ring is empty after a restart, so no
// rule can be evaluated for the first window and a carried-over "active"
// finding would be asserting something the engine cannot currently see
// (Open question 5) -- if the contention is still happening, the engine
// re-fires within two ticks anyway. An already-resolved row is untouched.
func (s *Store) StaleActiveInsights(at int64) error {
	_, err := s.db.Exec(`UPDATE insight_instances SET state='resolved', resolved_at=?, resolve_reason='restart' WHERE resolved_at = 0`, at)
	return err
}

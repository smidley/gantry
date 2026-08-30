package store

import (
	"context"
	"fmt"
)

// AlertRule is one row of alert_rules -- one field per column, in schema
// order. See migrations/003_alerts.sql for what each column means; the
// comments live there, not duplicated here.
type AlertRule struct {
	ID                string
	Name              string
	Enabled           bool
	Builtin           bool
	Type              string
	Kind              string
	EntityGlob        string
	EntityClass       string
	Metric            string
	Op                string
	Threshold         float64
	ClearThreshold    float64
	WarnThreshold     float64
	CriticalThreshold float64
	BandFamily        string
	ForSeconds        int64
	ClearSeconds      int64
	EventKinds        string
	MinSeverity       string
	ClearEventKinds   string
	ClearMaxSeverity  string
	Severity          string
	Channels          string
	RenotifyHours     int64
	UpdatedAt         int64
}

const alertRuleColumns = `id, name, enabled, builtin, type, kind, entity_glob, entity_class, metric, op,
	threshold, clear_threshold, warn_threshold, critical_threshold, band_family,
	for_seconds, clear_seconds, event_kinds, min_severity, clear_event_kinds,
	clear_max_severity, severity, channels, renotify_hours, updated_at`

func scanAlertRule(row rowScanner, r *AlertRule) error {
	return row.Scan(&r.ID, &r.Name, &r.Enabled, &r.Builtin, &r.Type, &r.Kind, &r.EntityGlob, &r.EntityClass,
		&r.Metric, &r.Op, &r.Threshold, &r.ClearThreshold, &r.WarnThreshold, &r.CriticalThreshold, &r.BandFamily,
		&r.ForSeconds, &r.ClearSeconds, &r.EventKinds, &r.MinSeverity, &r.ClearEventKinds,
		&r.ClearMaxSeverity, &r.Severity, &r.Channels, &r.RenotifyHours, &r.UpdatedAt)
}

// rowScanner is the common subset of *sql.Row and *sql.Rows this package
// needs, so scanAlertRule (and its instance/silence/delivery counterparts
// below) can serve both a single-row QueryRowContext and a Next()-driven
// QueryContext loop without duplicating the column list twice.
type rowScanner interface {
	Scan(dest ...any) error
}

// AlertRules returns every rule, builtin rules first, alphabetical by id
// within each group -- the order the rule editor (Task 11) lists them in.
func (s *Store) AlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT `+alertRuleColumns+` FROM alert_rules ORDER BY builtin DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []AlertRule
	for rows.Next() {
		var r AlertRule
		if err := scanAlertRule(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertAlertRule inserts a new rule or, if id already exists, overwrites
// every column in place -- the rule-editor write path (a user edits one
// rule's numbers and PUTs it back). Contrast SeedAlertRules, which never
// overwrites an existing row.
func (s *Store) UpsertAlertRule(r AlertRule) error {
	_, err := s.db.Exec(`INSERT INTO alert_rules (`+alertRuleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name, enabled=excluded.enabled, builtin=excluded.builtin, type=excluded.type,
			kind=excluded.kind, entity_glob=excluded.entity_glob, entity_class=excluded.entity_class,
			metric=excluded.metric, op=excluded.op, threshold=excluded.threshold,
			clear_threshold=excluded.clear_threshold, warn_threshold=excluded.warn_threshold,
			critical_threshold=excluded.critical_threshold, band_family=excluded.band_family,
			for_seconds=excluded.for_seconds, clear_seconds=excluded.clear_seconds,
			event_kinds=excluded.event_kinds, min_severity=excluded.min_severity,
			clear_event_kinds=excluded.clear_event_kinds, clear_max_severity=excluded.clear_max_severity,
			severity=excluded.severity, channels=excluded.channels, renotify_hours=excluded.renotify_hours,
			updated_at=excluded.updated_at`,
		r.ID, r.Name, r.Enabled, r.Builtin, r.Type, r.Kind, r.EntityGlob, r.EntityClass, r.Metric, r.Op,
		r.Threshold, r.ClearThreshold, r.WarnThreshold, r.CriticalThreshold, r.BandFamily,
		r.ForSeconds, r.ClearSeconds, r.EventKinds, r.MinSeverity, r.ClearEventKinds,
		r.ClearMaxSeverity, r.Severity, r.Channels, r.RenotifyHours, r.UpdatedAt)
	return err
}

// ReplaceAlertRules replaces every user (builtin=0) rule with rules, in
// one transaction -- a mechanical whole-document replace for the rules
// it manages, still not enforcing anything richer than that (further
// validation is the caller's job: Task 8's PUT /api/alerts/rules
// handler, the same division of labor /api/groups already uses between
// its store method and its route). A builtin rule is disable-only and
// never deletable (see alert_rules.builtin in 003_alerts.sql), so this
// is a store-level guarantee, not just a UI convention: builtin rows are
// never touched by the delete, and any builtin-flagged row present in
// rules is silently skipped rather than inserted or used to overwrite --
// editing a builtin is UpsertAlertRule's job.
func (s *Store) ReplaceAlertRules(rules []AlertRule) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM alert_rules WHERE builtin = 0`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, r := range rules {
		if r.Builtin {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO alert_rules (`+alertRuleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Name, r.Enabled, r.Builtin, r.Type, r.Kind, r.EntityGlob, r.EntityClass, r.Metric, r.Op,
			r.Threshold, r.ClearThreshold, r.WarnThreshold, r.CriticalThreshold, r.BandFamily,
			r.ForSeconds, r.ClearSeconds, r.EventKinds, r.MinSeverity, r.ClearEventKinds,
			r.ClearMaxSeverity, r.Severity, r.Channels, r.RenotifyHours, r.UpdatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SaveAlertRules persists a whole PUT /api/alerts/rules submission in a
// single transaction: every Builtin row in rules is upserted in place
// (UpsertAlertRule's own semantics -- the only path a builtin ever
// changes through) and the entire non-builtin set is replaced wholesale
// (ReplaceAlertRules' own semantics), one commit for both halves.
// UpsertAlertRule/ReplaceAlertRules above remain the standalone
// primitives (still exercised directly by this file's own tests); this
// is the one production callers (handleAlertsRulesPut) actually want --
// calling them separately used to mean N+1 commits for N builtins, so a
// failure partway through could leave builtins durably rewritten while
// the non-builtin set stayed on its stale pre-request contents.
func (s *Store) SaveAlertRules(rules []AlertRule) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, r := range rules {
		if !r.Builtin {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO alert_rules (`+alertRuleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (id) DO UPDATE SET
				name=excluded.name, enabled=excluded.enabled, builtin=excluded.builtin, type=excluded.type,
				kind=excluded.kind, entity_glob=excluded.entity_glob, entity_class=excluded.entity_class,
				metric=excluded.metric, op=excluded.op, threshold=excluded.threshold,
				clear_threshold=excluded.clear_threshold, warn_threshold=excluded.warn_threshold,
				critical_threshold=excluded.critical_threshold, band_family=excluded.band_family,
				for_seconds=excluded.for_seconds, clear_seconds=excluded.clear_seconds,
				event_kinds=excluded.event_kinds, min_severity=excluded.min_severity,
				clear_event_kinds=excluded.clear_event_kinds, clear_max_severity=excluded.clear_max_severity,
				severity=excluded.severity, channels=excluded.channels, renotify_hours=excluded.renotify_hours,
				updated_at=excluded.updated_at`,
			r.ID, r.Name, r.Enabled, r.Builtin, r.Type, r.Kind, r.EntityGlob, r.EntityClass, r.Metric, r.Op,
			r.Threshold, r.ClearThreshold, r.WarnThreshold, r.CriticalThreshold, r.BandFamily,
			r.ForSeconds, r.ClearSeconds, r.EventKinds, r.MinSeverity, r.ClearEventKinds,
			r.ClearMaxSeverity, r.Severity, r.Channels, r.RenotifyHours, r.UpdatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM alert_rules WHERE builtin = 0`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, r := range rules {
		if r.Builtin {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO alert_rules (`+alertRuleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Name, r.Enabled, r.Builtin, r.Type, r.Kind, r.EntityGlob, r.EntityClass, r.Metric, r.Op,
			r.Threshold, r.ClearThreshold, r.WarnThreshold, r.CriticalThreshold, r.BandFamily,
			r.ForSeconds, r.ClearSeconds, r.EventKinds, r.MinSeverity, r.ClearEventKinds,
			r.ClearMaxSeverity, r.Severity, r.Channels, r.RenotifyHours, r.UpdatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// AlertInstance is one row of alert_instances -- one field per column, in
// schema order.
type AlertInstance struct {
	ID             int64
	RuleID         string
	Kind           string
	Entity         string
	Metric         string
	State          string
	Severity       string
	Value          float64
	Threshold      float64
	Summary        string
	StartedAt      int64
	FiredAt        int64
	ResolvedAt     int64
	ResolveReason  string
	LastNotifiedAt int64
	NotifyCount    int64
}

const alertInstanceColumns = `id, rule_id, kind, entity, metric, state, severity, value, threshold, summary,
	started_at, fired_at, resolved_at, resolve_reason, last_notified_at, notify_count`

func scanAlertInstance(row rowScanner, i *AlertInstance) error {
	return row.Scan(&i.ID, &i.RuleID, &i.Kind, &i.Entity, &i.Metric, &i.State, &i.Severity, &i.Value, &i.Threshold,
		&i.Summary, &i.StartedAt, &i.FiredAt, &i.ResolvedAt, &i.ResolveReason, &i.LastNotifiedAt, &i.NotifyCount)
}

// ActiveAlertInstances returns every instance with resolved_at = 0 --
// what the engine (Task 4) walks every tick and what the frame's `alerts`
// block (Task 8) is filtered from.
func (s *Store) ActiveAlertInstances(ctx context.Context) ([]AlertInstance, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT `+alertInstanceColumns+` FROM alert_instances WHERE resolved_at = 0 ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []AlertInstance
	for rows.Next() {
		var i AlertInstance
		if err := scanAlertInstance(rows, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// UpsertAlertInstance inserts a new instance when ID is 0 (returning the
// generated id) or updates the existing row in place otherwise. The
// insert path is where idx_alert_active actually does its job: two
// concurrently-active rows for the same (rule_id, entity) violate that
// partial unique index and this returns the resulting constraint error
// unchanged -- there is no Go-side pre-check, by design (see the index's
// own comment in 003_alerts.sql).
func (s *Store) UpsertAlertInstance(i AlertInstance) (int64, error) {
	if i.ID != 0 {
		_, err := s.db.Exec(`UPDATE alert_instances SET rule_id=?, kind=?, entity=?, metric=?, state=?, severity=?,
			value=?, threshold=?, summary=?, started_at=?, fired_at=?, resolved_at=?, resolve_reason=?,
			last_notified_at=?, notify_count=? WHERE id=?`,
			i.RuleID, i.Kind, i.Entity, i.Metric, i.State, i.Severity, i.Value, i.Threshold, i.Summary,
			i.StartedAt, i.FiredAt, i.ResolvedAt, i.ResolveReason, i.LastNotifiedAt, i.NotifyCount, i.ID)
		return i.ID, err
	}
	res, err := s.db.Exec(`INSERT INTO alert_instances (rule_id, kind, entity, metric, state, severity, value,
		threshold, summary, started_at, fired_at, resolved_at, resolve_reason, last_notified_at, notify_count)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.RuleID, i.Kind, i.Entity, i.Metric, i.State, i.Severity, i.Value, i.Threshold, i.Summary,
		i.StartedAt, i.FiredAt, i.ResolvedAt, i.ResolveReason, i.LastNotifiedAt, i.NotifyCount)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ResolveAlertInstance closes out an instance: state, resolved_at, and
// resolve_reason are the only columns a resolve ever touches (value/
// severity/etc. keep whatever they last held while firing -- history is
// a snapshot, not a live view). Returns an error if id doesn't match any
// row -- a plain UPDATE...WHERE matches zero rows without erroring on
// its own, which would otherwise hide a caller (Task 4's engine, which
// may hold a cached instance id) racing a row that was already pruned or
// never existed.
func (s *Store) ResolveAlertInstance(id int64, at int64, reason string) error {
	res, err := s.db.Exec(`UPDATE alert_instances SET state='resolved', resolved_at=?, resolve_reason=? WHERE id=?`,
		at, reason, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("alert instance %d: not found", id)
	}
	return nil
}

// AlertHistory returns resolved instances (resolved_at > 0; an active
// instance never appears here regardless of how old it started) with
// resolved_at in [from, to] when either bound is non-zero, newest
// resolution first, capped at limit.
func (s *Store) AlertHistory(ctx context.Context, from, to int64, limit int) ([]AlertInstance, error) {
	q := `SELECT ` + alertInstanceColumns + ` FROM alert_instances WHERE resolved_at > 0`
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

	var out []AlertInstance
	for rows.Next() {
		var i AlertInstance
		if err := scanAlertInstance(rows, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Silence mutes dispatch (not evaluation -- an active instance under a
// silence still transitions state, see engine.go's Task 4 lifecycle
// table) for a rule, an entity, or both, until Until.
type Silence struct {
	ID        int64
	RuleID    string
	Entity    string
	Reason    string
	Until     int64
	CreatedAt int64
}

// Silences returns every silence whose Until is still in the future
// relative to now -- an already-expired row is excluded from the read
// even before Maintain gets around to pruning it (see pruneAlerts in
// maintain.go).
func (s *Store) Silences(ctx context.Context, now int64) ([]Silence, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, rule_id, entity, reason, until, created_at FROM alert_silences WHERE until > ? ORDER BY until`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Silence
	for rows.Next() {
		var sil Silence
		if err := rows.Scan(&sil.ID, &sil.RuleID, &sil.Entity, &sil.Reason, &sil.Until, &sil.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sil)
	}
	return out, rows.Err()
}

// AddSilence inserts a new silence and returns its generated id.
func (s *Store) AddSilence(sil Silence) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO alert_silences (rule_id, entity, until, reason, created_at) VALUES (?,?,?,?,?)`,
		sil.RuleID, sil.Entity, sil.Until, sil.Reason, sil.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteSilence lifts a silence early (Task 10's Alerts view "lift"
// control) -- a no-op, not an error, if id doesn't exist (already lifted,
// or already pruned as expired).
func (s *Store) DeleteSilence(id int64) error {
	_, err := s.db.Exec(`DELETE FROM alert_silences WHERE id=?`, id)
	return err
}

// Delivery is one attempt at delivering one notification down one
// channel -- the debugging ledger behind the Settings channels card's
// "last delivery failed: ..." text (Task 7), not history (AlertHistory
// reads alert_instances, never this table).
type Delivery struct {
	ID         int64
	InstanceID int64
	TS         int64
	Channel    string
	Target     string
	Phase      string
	Attempts   int64
	OK         bool
	Status     int64
	Error      string
}

// RecordDelivery appends one delivery attempt's outcome.
func (s *Store) RecordDelivery(d Delivery) error {
	_, err := s.db.Exec(`INSERT INTO alert_deliveries (instance_id, ts, channel, target, phase, attempts, ok, status, error)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		d.InstanceID, d.TS, d.Channel, d.Target, d.Phase, d.Attempts, d.OK, d.Status, d.Error)
	return err
}

// LastDeliveries returns the most recent deliveries, newest first,
// capped at limit (default 100 for limit <= 0, matching QueryEvents'
// own convention in events.go).
func (s *Store) LastDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, instance_id, ts, channel, target, phase, attempts, ok, status, error
		FROM alert_deliveries ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.InstanceID, &d.TS, &d.Channel, &d.Target, &d.Phase, &d.Attempts, &d.OK, &d.Status, &d.Error); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

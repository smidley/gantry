package store

import "context"

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

// ReplaceAlertRules replaces the entire alert_rules table with rules, in
// one transaction. This is a mechanical whole-document replace only --
// it does not enforce "a builtin rule can't be removed" or any other
// business rule; that validation is the caller's job (Task 8's PUT
// /api/alerts/rules handler, the same division of labor /api/groups
// already uses between its store method and its route).
func (s *Store) ReplaceAlertRules(rules []AlertRule) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM alert_rules`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, r := range rules {
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

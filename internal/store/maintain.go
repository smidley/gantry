package store

import (
	"context"
	"time"
)

// alertDeliveryRetention is fixed, not a Retention field: alert_deliveries
// is a short debugging ledger (Task 7's Settings-card failure text), not
// history -- AlertHistory reads alert_instances, never this table, so
// there's no user-facing reason to make its own retention configurable.
const alertDeliveryRetention = 7 * 24 * time.Hour

func (s *Store) Maintain(ctx context.Context, now time.Time, ret Retention) error {
	if _, err := s.FlushMinutes(ctx, now); err != nil {
		return err
	}
	if err := s.DownsampleOnce(ctx, now); err != nil {
		return err
	}
	if err := s.PruneOnce(ctx, now, ret); err != nil {
		return err
	}
	return s.pruneAlerts(ctx, now, ret)
}

// pruneAlerts trims the three alert tables that accumulate history:
// resolved instances past ret.R2 (the same knob PruneOnce already uses
// for samples_10m -- alert history is a medium-retention artifact, not
// the raw 1m tier's R1 nor the coarse 1h tier's R3), deliveries past the
// fixed alertDeliveryRetention, and silences whose until has already
// passed. An active instance (resolved_at = 0) is never touched -- the
// age filter looks only at resolved_at, never started_at, so a
// long-running firing alert is never pruned out from under itself.
func (s *Store) pruneAlerts(ctx context.Context, now time.Time, ret Retention) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_instances WHERE resolved_at > 0 AND resolved_at < ?`,
		now.Add(-ret.R2).Unix()); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_deliveries WHERE ts < ?`,
		now.Add(-alertDeliveryRetention).Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_silences WHERE until < ?`, now.Unix())
	return err
}

func RetentionFromConfig(get func(key string, def int) int) Retention {
	d := DefaultRetention()
	return Retention{
		R1:           time.Duration(get("retention.r1_hours", int(d.R1/time.Hour))) * time.Hour,
		R2:           time.Duration(get("retention.r2_days", int(d.R2/(24*time.Hour)))) * 24 * time.Hour,
		R3:           time.Duration(get("retention.r3_days", int(d.R3/(24*time.Hour)))) * 24 * time.Hour,
		SizeCapBytes: int64(get("retention.size_cap_mb", int(d.SizeCapBytes>>20))) << 20,
	}
}

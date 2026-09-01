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

// silenceRetention keeps an expired silence around for a week past its
// own until, for the same reason alert_deliveries gets a debugging
// window: "why didn't I get paged" is exactly the question a silence
// answers, and that question tends to get asked days after the silence
// quietly expired, not the moment it did. Silences() already excludes
// anything expired from what a live caller sees (see its own doc
// comment) regardless of this retention window -- pruneAlerts deleting
// it later is purely about not keeping the row forever.
const silenceRetention = 7 * 24 * time.Hour

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
	if err := s.pruneAlerts(ctx, now, ret); err != nil {
		return err
	}
	if err := s.pruneInsights(ctx, now, ret); err != nil {
		return err
	}
	if err := s.pruneAcks(ctx, now); err != nil {
		return err
	}
	return s.PruneSessions(ctx, now)
}

// pruneAlerts trims the three alert tables that accumulate history:
// resolved instances past ret.R2 (the same knob PruneOnce already uses
// for samples_10m -- alert history is a medium-retention artifact, not
// the raw 1m tier's R1 nor the coarse 1h tier's R3), deliveries past the
// fixed alertDeliveryRetention, and silences whose until passed more
// than silenceRetention ago (not the moment they expire -- see its own
// doc comment). An active instance (resolved_at = 0) is never touched --
// the age filter looks only at resolved_at, never started_at, so a
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_silences WHERE until < ?`, now.Add(-silenceRetention).Unix())
	return err
}

// pruneInsights trims the two insight tables that accumulate history:
// resolved instances past ret.R2 (the same knob pruneAlerts already uses
// for alert_instances -- insight history is the same medium-retention
// artifact class, not the raw 1m tier's R1 nor the coarse 1h tier's R3),
// and dismissals past their own until (no grace window the way
// silenceRetention gives an expired silence -- a dismissal has no
// "why didn't I get paged" debugging use once it lapses). An active
// instance (resolved_at = 0) is never touched -- the age filter looks
// only at resolved_at, never started_at, the exact pruneAlerts guarantee.
func (s *Store) pruneInsights(ctx context.Context, now time.Time, ret Retention) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM insight_instances WHERE resolved_at > 0 AND resolved_at < ?`,
		now.Add(-ret.R2).Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM insight_dismissals WHERE until < ?`, now.Unix())
	return err
}

// pruneAcks trims overview_acks past their own until -- the exact
// insight_dismissals treatment (no grace window the way silenceRetention
// gives an expired silence: like a lapsed dismissal, a lapsed ack has no
// "why didn't I get paged" debugging use -- it never suppressed
// notification of anything, only an attention row on one view). Acks()
// already excludes anything expired from what a live caller sees
// regardless of this prune.
func (s *Store) pruneAcks(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM overview_acks WHERE until < ?`, now.Unix())
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

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The I2 regression guard: samples recorded up to the very end of a 10m window
// must reach the 10m rollup when Maintain runs at the window boundary.
func TestMaintainFlushesBeforeDownsampling(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	base := at("12:00:00")
	_, err := s.FlushMinutes(context.Background(), base) // baseline
	require.NoError(t, err)

	for m := int64(0); m < 10; m++ { // one sample per minute, values 0..9
		s.Record(k, base.Unix()+m*60+30, float64(m))
	}
	// Maintain at the boundary: minute 12:09 is only in the ring at this point.
	require.NoError(t, s.Maintain(context.Background(), at("12:10:05"), DefaultRetention()))

	var avg, max float64
	require.NoError(t, s.DB().QueryRow(`SELECT avg, max FROM samples_10m WHERE ts=?`, base.Unix()).Scan(&avg, &max))
	require.InDelta(t, 4.5, avg, 0.001)
	require.Equal(t, 9.0, max)
}

// TestMaintainPrunesAlertTablesButLeavesActiveInstancesAlone pins
// pruneAlerts' cutoffs in one pass: resolved instances past ret.R2 (the
// same knob PruneOnce uses for samples_10m), deliveries past a fixed 7
// days, and silences past a fixed 7 days from their own until (not from
// the moment they expire -- see silenceRetention) -- while an active
// instance (resolved_at = 0) survives regardless of how old its
// started_at is, since the age filter only ever looks at resolved_at.
func TestMaintainPrunesAlertTablesButLeavesActiveInstancesAlone(t *testing.T) {
	s := newTestStore(t, nil)
	ret := DefaultRetention()
	now := at("12:00:00")
	nowUnix := now.Unix()

	oldResolvedID, err := s.UpsertAlertInstance(AlertInstance{RuleID: "r1", Kind: "disk", Entity: "d1", State: "resolved", Severity: "warning", StartedAt: 1})
	require.NoError(t, err)
	require.NoError(t, s.ResolveAlertInstance(oldResolvedID, nowUnix-int64(ret.R2.Seconds())-100, "cleared"))

	recentResolvedID, err := s.UpsertAlertInstance(AlertInstance{RuleID: "r2", Kind: "disk", Entity: "d2", State: "resolved", Severity: "warning", StartedAt: 1})
	require.NoError(t, err)
	require.NoError(t, s.ResolveAlertInstance(recentResolvedID, nowUnix-100, "cleared"))

	// Active, with a StartedAt far older than R2 -- proving the prune
	// filter keys on resolved_at (0 here), never on started_at's age.
	activeID, err := s.UpsertAlertInstance(AlertInstance{RuleID: "r3", Kind: "disk", Entity: "d3", State: "firing", Severity: "warning", StartedAt: 1})
	require.NoError(t, err)

	require.NoError(t, s.RecordDelivery(Delivery{InstanceID: oldResolvedID, TS: nowUnix - 7*24*3600 - 100, Channel: "notify", Phase: "fired", OK: true}))
	require.NoError(t, s.RecordDelivery(Delivery{InstanceID: recentResolvedID, TS: nowUnix - 100, Channel: "notify", Phase: "fired", OK: true}))

	// Mirrors the instance/delivery pairing above: one silence expired
	// long enough ago to be pruned, one expired recently enough to still
	// be kept as "why didn't I get paged" evidence, one not expired yet.
	longExpiredSilenceID, err := s.AddSilence(Silence{RuleID: "r1", Until: nowUnix - int64(silenceRetention.Seconds()) - 100})
	require.NoError(t, err)
	recentlyExpiredSilenceID, err := s.AddSilence(Silence{RuleID: "r2", Until: nowUnix - 100})
	require.NoError(t, err)
	activeSilenceID, err := s.AddSilence(Silence{RuleID: "r3", Until: nowUnix + 100})
	require.NoError(t, err)

	require.NoError(t, s.Maintain(context.Background(), now, ret))

	remainingInstances, err := allAlertInstanceIDs(s)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{recentResolvedID, activeID}, remainingInstances, "old resolved instance pruned; recent resolved and active survive")

	var deliveryCount int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM alert_deliveries`).Scan(&deliveryCount))
	require.Equal(t, 1, deliveryCount, "only the recent delivery survives")

	remainingSilences, err := allSilenceIDs(s)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{recentlyExpiredSilenceID, activeSilenceID}, remainingSilences,
		"a silence expired under 7 days ago is kept as paging evidence; only one expired over 7 days ago is pruned")
	require.NotContains(t, remainingSilences, longExpiredSilenceID)
}

// TestMaintainKeepsRecentlyExpiredSilenceButSilencesExcludesItFromReads
// is the read-side half of the same guarantee: pruneAlerts keeps an
// expired silence around for silenceRetention as "why didn't I get
// paged" evidence, but Silences() must still never hand it back to a
// live caller (Task 4's engine, the Alerts view's active-silences list)
// just because it hasn't been physically deleted yet.
func TestMaintainKeepsRecentlyExpiredSilenceButSilencesExcludesItFromReads(t *testing.T) {
	s := newTestStore(t, nil)
	now := at("12:00:00")
	nowUnix := now.Unix()

	id, err := s.AddSilence(Silence{RuleID: "r1", Until: nowUnix - 100})
	require.NoError(t, err)

	require.NoError(t, s.Maintain(context.Background(), now, DefaultRetention()))

	remaining, err := allSilenceIDs(s)
	require.NoError(t, err)
	require.Contains(t, remaining, id, "recently expired silence must still be on disk as evidence")

	got, err := s.Silences(context.Background(), nowUnix)
	require.NoError(t, err)
	require.Empty(t, got, "Silences() must exclude an expired silence from reads even while pruneAlerts still keeps it on disk")
}

func allAlertInstanceIDs(s *Store) ([]int64, error) {
	rows, err := s.DB().Query(`SELECT id FROM alert_instances`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func allSilenceIDs(s *Store) ([]int64, error) {
	rows, err := s.DB().Query(`SELECT id FROM alert_silences`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func TestRetentionFromConfig(t *testing.T) {
	vals := map[string]int{"retention.r1_hours": 24, "retention.r3_days": 60, "retention.size_cap_mb": 128}
	get := func(key string, def int) int {
		if v, ok := vals[key]; ok {
			return v
		}
		return def
	}
	ret := RetentionFromConfig(get)
	require.Equal(t, 24*time.Hour, ret.R1)
	require.Equal(t, DefaultRetention().R2, ret.R2) // untouched keys keep defaults
	require.Equal(t, 60*24*time.Hour, ret.R3, "r3_days override")
	require.Equal(t, int64(128<<20), ret.SizeCapBytes)

	// R3 default: with retention.r3_days untouched, RetentionFromConfig
	// must fall back to DefaultRetention's R3, the same way R2 does above.
	defaultOnly := func(_ string, def int) int { return def }
	require.Equal(t, DefaultRetention().R3, RetentionFromConfig(defaultOnly).R3)
}

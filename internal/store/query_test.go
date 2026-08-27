package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fixedClock returns a clock func pinned to unix, for controlling
// QuerySeries' "is `to` recent enough for the live ring" check
// deterministically.
func fixedClock(unix int64) func() time.Time {
	return func() time.Time { return time.Unix(unix, 0).UTC() }
}

// seedTable inserts one row per minute over [from, to) into the named tier
// table (samples_1m/_10m/_1h all share the same schema), for a fresh series.
func seedTable(t *testing.T, s *Store, table string, key SeriesKey, from, to int64, val float64) {
	t.Helper()
	id, err := s.seriesID(context.Background(), key)
	require.NoError(t, err)
	for ts := from; ts < to; ts += 60 {
		_, err := s.DB().Exec(`INSERT OR REPLACE INTO `+table+` (series_id, ts, avg, max) VALUES (?,?,?,?)`,
			id, ts, val, val)
		require.NoError(t, err)
	}
}

// --- QuerySeries: live-ring path ---------------------------------------

// TestQuerySeriesUsesLiveRingWhenRecentAndNarrow pins the live-ring branch:
// a narrow (<=15m), recent (to within 30s of now) range must be served
// straight from the Live ring, with Avg==Max==Val (a live sample carries no
// aggregation) -- NOT from samples_1m, even though samples_1m also has data
// in range with a DIFFERENT value, proving the ring (not the table) won.
func TestQuerySeriesUsesLiveRingWhenRecentAndNarrow(t *testing.T) {
	now := at("12:10:00").Unix()
	s := newTestStore(t, fixedClock(now))
	k := SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}

	s.Record(k, now-120, 42)                                // lands in the ring
	seedTable(t, s, "samples_1m", k, now-120, now-119, 999) // decoy: same instant, wrong table, different value

	got, err := s.QuerySeries(context.Background(), "container", "web", []string{"cpu.pct"}, now-300, now)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "cpu.pct", got[0].Metric)
	require.Equal(t, []SeriesPoint{{TS: now - 120, Avg: 42, Max: 42}}, got[0].Points, "must come from the ring (avg==max==val), not samples_1m's decoy 999")
}

// TestQuerySeriesFallsBackToTableWhenRangeNotRecent pins the "second half"
// of the live-ring rule the dispatch calls out explicitly: span<=15m alone
// is not enough -- `to` must also be within the last 30s of now. A
// historical 5-minute window from days ago must use samples_1m even though
// its span easily qualifies as "narrow".
func TestQuerySeriesFallsBackToTableWhenRangeNotRecent(t *testing.T) {
	now := at("12:10:00").Unix()
	s := newTestStore(t, fixedClock(now))
	k := SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}

	historicalTo := now - 3*3600 // 3h in the past: span is narrow but not recent
	historicalFrom := historicalTo - 300
	seedTable(t, s, "samples_1m", k, historicalFrom, historicalTo, 7)
	// A decoy in the live ring at the same timestamps would prove nothing
	// either way (rings don't hold 3h-old data in practice), so the table
	// is the only place this data could have come from.

	got, err := s.QuerySeries(context.Background(), "container", "web", []string{"cpu.pct"}, historicalFrom, historicalTo)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotEmpty(t, got[0].Points, "must fall back to samples_1m for a non-recent range")
	for _, p := range got[0].Points {
		require.Equal(t, 7.0, p.Avg)
	}
}

// --- QuerySeries: tier boundaries, tested exactly -----------------------

func TestQuerySeriesTierBoundaries(t *testing.T) {
	now := at("12:00:00").Unix()

	cases := []struct {
		name  string
		span  int64
		table string
	}{
		{"15m exactly, recent -> live ring handled separately", 15 * 60, "live"},
		{"15m+1s, recent -> samples_1m (too wide for the ring)", 15*60 + 1, "samples_1m"},
		{"48h exactly -> samples_1m", 48 * 3600, "samples_1m"},
		{"48h+1s -> samples_10m", 48*3600 + 1, "samples_10m"},
		{"30d exactly -> samples_10m", 30 * 24 * 3600, "samples_10m"},
		{"30d+1s -> samples_1h", 30*24*3600 + 1, "samples_1h"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t, fixedClock(now))
			k := SeriesKey{Kind: "host", Metric: "cpu.total"}
			to := now
			from := to - tc.span

			if tc.table == "live" {
				s.Record(k, to-1, 55)
			} else {
				seedTable(t, s, tc.table, k, from, from+60, 55)
			}

			got, err := s.QuerySeries(context.Background(), "host", "", []string{"cpu.total"}, from, to)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.NotEmpty(t, got[0].Points, "case %s: expected data from %s", tc.name, tc.table)
			require.Equal(t, 55.0, got[0].Points[0].Avg)
		})
	}
}

// --- QuerySeries: rejection / unknown-series rules ----------------------

// TestQuerySeriesRejectsLivePrefixedMetrics pins the "`live:`-prefixed
// metrics rejected" rule: requesting one must not error, and must not leak
// ring data -- it comes back with empty Points, same as an unknown series.
func TestQuerySeriesRejectsLivePrefixedMetrics(t *testing.T) {
	now := at("12:10:00").Unix()
	s := newTestStore(t, fixedClock(now))
	k := SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}
	s.Record(k, now-10, 12345) // present in the ring -- must still be rejected

	got, err := s.QuerySeries(context.Background(), "container", "web", []string{"live:io.sda.read_bps"}, now-300, now)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "live:io.sda.read_bps", got[0].Metric)
	require.Empty(t, got[0].Points, "live:-prefixed metrics must never be served")
}

// TestQuerySeriesUnknownSeriesIsEmptyNotError covers both the live-ring
// path and the table path: an unknown kind/entity/metric combination must
// come back as an empty result for that metric, never an error.
func TestQuerySeriesUnknownSeriesIsEmptyNotError(t *testing.T) {
	now := at("12:10:00").Unix()
	s := newTestStore(t, fixedClock(now))

	// Live-ring path (recent, narrow range) -- series never recorded at all.
	got, err := s.QuerySeries(context.Background(), "container", "ghost", []string{"cpu.pct"}, now-300, now)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Empty(t, got[0].Points)

	// Table path (wide range) -- series never flushed to any tier table.
	got, err = s.QuerySeries(context.Background(), "container", "ghost", []string{"cpu.pct"}, now-3*24*3600, now)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Empty(t, got[0].Points)
}

// TestQuerySeriesMultipleMetricsPreserveOrder confirms the returned slice is
// one SeriesResult per requested metric, in request order, so a caller can
// zip metrics[i] with got[i] -- not a shorter slice that drops unknowns.
func TestQuerySeriesMultipleMetricsPreserveOrder(t *testing.T) {
	now := at("12:10:00").Unix()
	s := newTestStore(t, fixedClock(now))
	s.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}, now-10, 5)
	s.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "mem.bytes"}, now-10, 900)

	got, err := s.QuerySeries(context.Background(), "container", "web",
		[]string{"cpu.pct", "unknown.metric", "mem.bytes"}, now-300, now)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "cpu.pct", got[0].Metric)
	require.Equal(t, "unknown.metric", got[1].Metric)
	require.Equal(t, "mem.bytes", got[2].Metric)
	require.NotEmpty(t, got[0].Points)
	require.Empty(t, got[1].Points)
	require.NotEmpty(t, got[2].Points)
}

// --- TopEntities ---------------------------------------------------------

func TestTopEntitiesAvgOrdersDescendingAndLimits(t *testing.T) {
	s := newTestStore(t, nil)
	base := at("12:00:00")
	to := at("12:10:00")

	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web-a", Metric: "cpu.pct"}, base, to, 10)
	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web-b", Metric: "cpu.pct"}, base, to, 30)
	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web-c", Metric: "cpu.pct"}, base, to, 20)

	got, err := s.TopEntities(context.Background(), "container", "cpu.pct", base.Unix(), to.Unix(), "avg", 2)
	require.NoError(t, err)
	require.Len(t, got, 2, "limit must cap the result")
	require.Equal(t, "web-b", got[0].Entity)
	require.InDelta(t, 30.0, got[0].Value, 0.001)
	require.Equal(t, "web-c", got[1].Entity)
	require.InDelta(t, 20.0, got[1].Value, 0.001)
}

// TestTopEntitiesTiesBreakByEntityName pins the deterministic secondary
// sort: entities with an EXACTLY equal aggregated value must still come
// back in a fixed (entity ascending), reproducible order -- not whatever
// order SQLite's GROUP BY happens to produce, which is unspecified.
func TestTopEntitiesTiesBreakByEntityName(t *testing.T) {
	s := newTestStore(t, nil)
	base := at("12:00:00")
	to := at("12:01:00")

	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web-c", Metric: "cpu.pct"}, base, to, 10)
	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web-a", Metric: "cpu.pct"}, base, to, 10)
	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web-b", Metric: "cpu.pct"}, base, to, 10)

	got, err := s.TopEntities(context.Background(), "container", "cpu.pct", base.Unix(), to.Unix(), "avg", 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{"web-a", "web-b", "web-c"}, []string{got[0].Entity, got[1].Entity, got[2].Entity})
}

// TestTopEntitiesPeakUsesMax pins agg="peak" -> MAX(max), distinguished from
// avg by seeding a series whose max is much larger than its avg.
func TestTopEntitiesPeakUsesMax(t *testing.T) {
	s := newTestStore(t, nil)
	base := at("12:00:00")
	to := at("12:02:00")
	id, err := s.seriesID(context.Background(), SeriesKey{Kind: "container", Entity: "web-a", Metric: "cpu.pct"})
	require.NoError(t, err)
	_, err = s.DB().Exec(`INSERT INTO samples_1m (series_id, ts, avg, max) VALUES (?,?,?,?)`, id, base.Unix(), 10.0, 90.0)
	require.NoError(t, err)

	gotAvg, err := s.TopEntities(context.Background(), "container", "cpu.pct", base.Unix(), to.Unix(), "avg", 10)
	require.NoError(t, err)
	require.Len(t, gotAvg, 1)
	require.InDelta(t, 10.0, gotAvg[0].Value, 0.001)

	gotPeak, err := s.TopEntities(context.Background(), "container", "cpu.pct", base.Unix(), to.Unix(), "peak", 10)
	require.NoError(t, err)
	require.Len(t, gotPeak, 1)
	require.InDelta(t, 90.0, gotPeak[0].Value, 0.001)
}

// TestTopEntitiesFiltersByKindAndMetric confirms grouping never leaks
// samples from a different kind or a different metric into the same
// leaderboard.
func TestTopEntitiesFiltersByKindAndMetric(t *testing.T) {
	s := newTestStore(t, nil)
	base := at("12:00:00")
	to := at("12:01:00")

	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}, base, to, 10)
	seed1m(t, s, SeriesKey{Kind: "host", Entity: "", Metric: "cpu.pct"}, base, to, 999)           // different kind
	seed1m(t, s, SeriesKey{Kind: "container", Entity: "web", Metric: "mem.bytes"}, base, to, 999) // different metric

	got, err := s.TopEntities(context.Background(), "container", "cpu.pct", base.Unix(), to.Unix(), "avg", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "web", got[0].Entity)
	require.InDelta(t, 10.0, got[0].Value, 0.001)
}

// TestTopEntitiesUsesRangeTierNotLiveRing pins the store-layer contract
// that TopEntities never touches the Live ring at all -- a "now" window is
// the API layer's job. A value sitting only in the ring must not surface
// even though it would trivially satisfy a live-ring style check.
func TestTopEntitiesUsesRangeTierNotLiveRing(t *testing.T) {
	now := at("12:10:00").Unix()
	s := newTestStore(t, fixedClock(now))
	s.Record(SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}, now-10, 500)

	got, err := s.TopEntities(context.Background(), "container", "cpu.pct", now-300, now, "avg", 10)
	require.NoError(t, err)
	require.Empty(t, got, "TopEntities must be SQL-only; a live-ring-only sample must not appear")
}

// TestTopEntitiesWideRangeUsesWiderTier confirms the SQL tier for
// TopEntities is picked by range the same way QuerySeries' table path is
// (a >48h span must read samples_10m, not samples_1m).
func TestTopEntitiesWideRangeUsesWiderTier(t *testing.T) {
	s := newTestStore(t, nil)
	from := at("12:00:00").Unix()
	to := from + 49*3600 // >48h -> samples_10m tier
	k := SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}
	id, err := s.seriesID(context.Background(), k)
	require.NoError(t, err)
	_, err = s.DB().Exec(`INSERT INTO samples_10m (series_id, ts, avg, max) VALUES (?,?,?,?)`, id, from+60, 40.0, 40.0)
	require.NoError(t, err)

	got, err := s.TopEntities(context.Background(), "container", "cpu.pct", from, to, "avg", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "web", got[0].Entity)
	require.InDelta(t, 40.0, got[0].Value, 0.001)
}

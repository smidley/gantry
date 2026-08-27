package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seed1m inserts one samples_1m row per minute over [from, to) for a fresh series.
func seed1m(t *testing.T, s *Store, key SeriesKey, from, to time.Time, val float64) {
	t.Helper()
	id, err := s.seriesID(context.Background(), key)
	require.NoError(t, err)
	for m := from.Unix(); m < to.Unix(); m += 60 {
		_, err := s.DB().Exec(`INSERT OR REPLACE INTO samples_1m (series_id, ts, avg, max) VALUES (?,?,?,?)`,
			id, m, val, val*2)
		require.NoError(t, err)
	}
}

func TestDownsample1mTo10m(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	seed1m(t, s, k, at("12:00:00"), at("12:20:00"), 10) // 20 minutes of avg=10, max=20

	require.NoError(t, s.DownsampleOnce(context.Background(), at("12:21:00")))

	rows, err := s.DB().Query(`SELECT ts, avg, max FROM samples_10m ORDER BY ts`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var ts int64
		var avg, max float64
		require.NoError(t, rows.Scan(&ts, &avg, &max))
		got = append(got, fmt.Sprintf("%d avg=%.0f max=%.0f", ts, avg, max))
	}
	// Two complete 10m windows: 12:00 and 12:10.
	require.Equal(t, []string{
		fmt.Sprintf("%d avg=10 max=20", at("12:00:00").Unix()),
		fmt.Sprintf("%d avg=10 max=20", at("12:10:00").Unix()),
	}, got)

	// Idempotent: watermark advanced, re-run adds nothing.
	require.NoError(t, s.DownsampleOnce(context.Background(), at("12:21:30")))
	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_10m`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestDownsampleFullWindowFidelity(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	id, err := s.seriesID(context.Background(), k)
	require.NoError(t, err)

	base := at("12:00:00")
	for i := 0; i < 10; i++ { // ts = base+0m..base+9m; avg and max both 0..9, distinct per row
		_, err := s.DB().Exec(`INSERT OR REPLACE INTO samples_1m (series_id, ts, avg, max) VALUES (?,?,?,?)`,
			id, base.Unix()+int64(i)*60, float64(i), float64(i))
		require.NoError(t, err)
	}

	require.NoError(t, s.DownsampleOnce(context.Background(), base.Add(11*time.Minute)))

	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_10m`).Scan(&n))
	require.Equal(t, 1, n)

	var ts int64
	var avg, max float64
	require.NoError(t, s.DB().QueryRow(`SELECT ts, avg, max FROM samples_10m`).Scan(&ts, &avg, &max))
	require.Equal(t, base.Unix(), ts)
	require.InDelta(t, 4.5, avg, 0.001) // avg-of-avgs over 0..9; only correct if the full window is present
	require.Equal(t, 9.0, max)
}

func TestPruneEnforcesAges(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	now := at("12:00:00")
	seed1m(t, s, k, now.Add(-50*time.Hour), now.Add(-49*time.Hour), 5) // older than R1=48h
	seed1m(t, s, k, now.Add(-1*time.Hour), now, 5)                     // fresh

	require.NoError(t, s.PruneOnce(context.Background(), now, DefaultRetention()))

	var minTS int64
	require.NoError(t, s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&minTS))
	require.GreaterOrEqual(t, minTS, now.Add(-48*time.Hour).Unix())
}

func TestPruneEnforcesSizeCap(t *testing.T) {
	s := newTestStore(t, nil)
	now := at("12:00:00")

	// Seed ~4000 minutes across 4 series to exceed the size cap over multiple calls.
	// Single PruneOnce call trims at most 8 × 6h = 48h, so multiple calls are needed.
	for i := 0; i < 4; i++ {
		k := SeriesKey{Kind: "host", Metric: fmt.Sprintf("metric_%d", i)}
		// 4000 minutes = ~67 hours of data; avg=100, max=200
		from := now.Add(-4000 * time.Minute)
		to := now
		seed1m(t, s, k, from, to, 100)
	}

	// Verify we have many rows before pruning.
	var beforeCount int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_1m`).Scan(&beforeCount))
	require.Greater(t, beforeCount, 4000, "should have seeded > 4000 rows")

	var beforeMinTS sql.NullInt64
	require.NoError(t, s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&beforeMinTS))
	require.True(t, beforeMinTS.Valid, "should have seeded rows")

	// Prune with a small size cap (256 KB) and huge ages so only the size branch acts.
	// The size-cap loop trims 6h at a time (max 8 iterations per call).
	// Production calls this every 10 minutes, so we loop here to simulate that.
	ret := Retention{
		R1:           720 * time.Hour, // 30 days, won't affect the old data
		R2:           720 * time.Hour, // 30 days
		R3:           720 * time.Hour, // 30 days
		SizeCapBytes: 256 * 1024,      // 256 KB cap
	}

	// Drive PruneOnce to convergence (up to 25 calls), measuring occupied bytes after each.
	converged := false
	for callNum := 1; callNum <= 25; callNum++ {
		require.NoError(t, s.PruneOnce(context.Background(), now, ret), "PruneOnce call %d failed", callNum)

		// Measure occupied bytes: (page_count - freelist_count) * page_size
		var pages, pageSize, freelistCount int64
		require.NoError(t, s.DB().QueryRow(`PRAGMA page_count`).Scan(&pages))
		require.NoError(t, s.DB().QueryRow(`PRAGMA page_size`).Scan(&pageSize))
		require.NoError(t, s.DB().QueryRow(`PRAGMA freelist_count`).Scan(&freelistCount))
		occupiedBytes := (pages - freelistCount) * pageSize

		// Check row count
		var rowCount int
		require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_1m`).Scan(&rowCount))

		// Break if converged: occupied <= cap or no rows remain
		if occupiedBytes <= ret.SizeCapBytes || rowCount == 0 {
			converged = true
			break
		}
	}

	// Must converge to a valid state
	require.True(t, converged, "never converged: occupied > cap and rows remain after 25 calls")

	// Final state: verify rows were deleted from oldest end
	var afterCount int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_1m`).Scan(&afterCount))
	require.Less(t, afterCount, beforeCount, "rows should have been deleted")

	// Verify oldest timestamp advanced (or all data is gone)
	var afterMinTS sql.NullInt64
	require.NoError(t, s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&afterMinTS))
	if afterMinTS.Valid {
		require.Greater(t, afterMinTS.Int64, beforeMinTS.Int64, "oldest timestamp should have advanced")
	}
	// If afterMinTS is NULL, all rows were deleted — also acceptable
}

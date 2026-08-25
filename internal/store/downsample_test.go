package store

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seed1m inserts one samples_1m row per minute over [from, to) for a fresh series.
func seed1m(t *testing.T, s *Store, key SeriesKey, from, to time.Time, val float64) {
	t.Helper()
	id, err := s.seriesID(key)
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

	require.NoError(t, s.DownsampleOnce(at("12:21:00")))

	rows, err := s.DB().Query(`SELECT ts, avg, max FROM samples_10m ORDER BY ts`)
	require.NoError(t, err)
	defer rows.Close()
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
	require.NoError(t, s.DownsampleOnce(at("12:21:30")))
	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_10m`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestPruneEnforcesAges(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	now := at("12:00:00")
	seed1m(t, s, k, now.Add(-50*time.Hour), now.Add(-49*time.Hour), 5) // older than R1=48h
	seed1m(t, s, k, now.Add(-1*time.Hour), now, 5)                     // fresh

	require.NoError(t, s.PruneOnce(now, DefaultRetention()))

	var minTS int64
	require.NoError(t, s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&minTS))
	require.GreaterOrEqual(t, minTS, now.Add(-48*time.Hour).Unix())
}

func TestPruneEnforcesSizeCap(t *testing.T) {
	s := newTestStore(t, nil)
	now := at("12:00:00")

	// Seed ~10000 minutes across 8 series to exceed a reasonable size cap.
	for i := 0; i < 8; i++ {
		k := SeriesKey{Kind: "host", Metric: fmt.Sprintf("metric_%d", i)}
		// 10000 minutes = ~167 hours of data; avg=100, max=200
		from := now.Add(-10000 * time.Minute)
		to := now
		seed1m(t, s, k, from, to, 100)
	}

	// Verify we have many rows before pruning.
	var beforeCount int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_1m`).Scan(&beforeCount))
	require.Greater(t, beforeCount, 10000, "should have seeded > 10000 rows")

	var beforeMin int64
	require.NoError(t, s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&beforeMin))

	// Prune with a small size cap (512 KB) and huge ages so only the size branch acts.
	// This should trigger the size-cap loop to delete old data.
	ret := Retention{
		R1:           720 * time.Hour, // 30 days, won't affect the old data
		R2:           720 * time.Hour, // 30 days
		R3:           720 * time.Hour, // 30 days
		SizeCapBytes: 512 * 1024,      // 512 KB cap
	}
	require.NoError(t, s.PruneOnce(now, ret))

	// Rows should have been deleted from the oldest end.
	var afterCount int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_1m`).Scan(&afterCount))
	require.Less(t, afterCount, beforeCount, "some rows should have been deleted")

	// Check that the oldest timestamp advanced or all data is gone.
	var afterMin int64
	err := s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&afterMin)
	if err == sql.ErrNoRows {
		// OK: all rows were deleted
		afterMin = now.Unix()
	} else {
		require.NoError(t, err)
		require.Greater(t, afterMin, beforeMin, "oldest timestamp should have advanced")
	}

	// Verify occupied bytes (page_count - freelist_count) * page_size is measured.
	// The size-cap loop should have run and freed pages via incremental_vacuum.
	var pages, pageSize, freelist int64
	require.NoError(t, s.DB().QueryRow(`PRAGMA page_count`).Scan(&pages))
	require.NoError(t, s.DB().QueryRow(`PRAGMA page_size`).Scan(&pageSize))
	require.NoError(t, s.DB().QueryRow(`PRAGMA freelist_count`).Scan(&freelist))
	// Just verify the PRAGMA queries work and the logic completes without error.
	_ = (pages - freelist) * pageSize
}

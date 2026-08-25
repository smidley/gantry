package store

import (
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

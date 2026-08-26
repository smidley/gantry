package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func at(s string) time.Time { // "15:04:05" on a fixed day, UTC
	tm, err := time.Parse("2006-01-02 15:04:05", "2026-08-25 "+s)
	if err != nil {
		panic(err)
	}
	return tm.UTC()
}

func TestFlushMinutesAggregatesAvgAndMax(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}

	base := at("12:04:00")
	// Baseline call: establishes lastFlushed, writes nothing.
	n, err := s.FlushMinutes(context.Background(), base)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	// Samples inside [12:04:00, 12:05:00): values 10, 20, 60.
	s.Record(k, base.Unix()+2, 10)
	s.Record(k, base.Unix()+30, 20)
	s.Record(k, base.Unix()+58, 60)

	n, err = s.FlushMinutes(context.Background(), at("12:05:07")) // 12:04 window is now complete
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var avg, max float64
	var ts int64
	require.NoError(t, s.DB().QueryRow(
		`SELECT ts, avg, max FROM samples_1m LIMIT 1`).Scan(&ts, &avg, &max))
	require.Equal(t, base.Unix(), ts)
	require.InDelta(t, 30.0, avg, 0.001)
	require.Equal(t, 60.0, max)
}

func TestFlushMinutesCatchesUpMultipleWindows(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "mem.used"}
	base := at("12:00:00")
	_, err := s.FlushMinutes(context.Background(), base)
	require.NoError(t, err)

	for m := int64(0); m < 3; m++ { // one sample in each of 12:00, 12:01, 12:02
		s.Record(k, base.Unix()+m*60+5, float64(m))
	}
	n, err := s.FlushMinutes(context.Background(), at("12:03:30")) // three complete windows at once
	require.NoError(t, err)
	require.Equal(t, 3, n)

	// Idempotent: nothing new without new samples/minutes.
	n, err = s.FlushMinutes(context.Background(), at("12:03:45"))
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestFlushSkipsEmptyWindows(t *testing.T) {
	s := newTestStore(t, nil)
	_, err := s.FlushMinutes(context.Background(), at("12:00:00"))
	require.NoError(t, err)
	n, err := s.FlushMinutes(context.Background(), at("12:02:00")) // no samples at all
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestFlushSkipsLivePrefixedMetrics(t *testing.T) {
	s := newTestStore(t, nil)
	live := SeriesKey{Kind: "container", Entity: "web", Metric: "live:io.sda.read_bps"}
	normal := SeriesKey{Kind: "container", Entity: "web", Metric: "cpu.pct"}

	base := at("12:04:00")
	_, err := s.FlushMinutes(context.Background(), base)
	require.NoError(t, err)

	s.Record(live, base.Unix()+2, 12345)
	s.Record(normal, base.Unix()+2, 50)

	n, err := s.FlushMinutes(context.Background(), at("12:05:00"))
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the non-live: series should flush to samples_1m")

	var count int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_1m`).Scan(&count))
	require.Equal(t, 1, count)
}

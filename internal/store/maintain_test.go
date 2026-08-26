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

func TestRetentionFromConfig(t *testing.T) {
	vals := map[string]int{"retention.r1_hours": 24, "retention.size_cap_mb": 128}
	get := func(key string, def int) int {
		if v, ok := vals[key]; ok {
			return v
		}
		return def
	}
	ret := RetentionFromConfig(get)
	require.Equal(t, 24*time.Hour, ret.R1)
	require.Equal(t, DefaultRetention().R2, ret.R2) // untouched keys keep defaults
	require.Equal(t, int64(128<<20), ret.SizeCapBytes)
}

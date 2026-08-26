package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingAppendAndSince(t *testing.T) {
	r := NewRing(4)
	for i := int64(1); i <= 3; i++ {
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}
	require.Equal(t, 3, r.Len())
	got := r.Since(20)
	require.Equal(t, []Sample{{TS: 20, Val: 2}, {TS: 30, Val: 3}}, got)
}

func TestRingWraparoundEvictsOldest(t *testing.T) {
	r := NewRing(3)
	for i := int64(1); i <= 5; i++ { // capacity 3, appending 5 → keeps ts 30,40,50
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}
	require.Equal(t, 3, r.Len())
	require.Equal(t, []Sample{{TS: 30, Val: 3}, {TS: 40, Val: 4}, {TS: 50, Val: 5}}, r.Since(0))

	latest, ok := r.Latest()
	require.True(t, ok)
	require.Equal(t, Sample{TS: 50, Val: 5}, latest)
}

func TestRingEmpty(t *testing.T) {
	r := NewRing(3)
	_, ok := r.Latest()
	require.False(t, ok)
	require.Empty(t, r.Since(0))
}

func TestNewRingClampsNonPositiveCapacity(t *testing.T) {
	r := NewRing(0)
	r.Append(Sample{TS: 1, Val: 1}) // must not panic
	require.Equal(t, 1, r.Len())
}

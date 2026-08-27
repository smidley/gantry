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

func TestRingAppendSinceAppendsOntoProvidedSlice(t *testing.T) {
	r := NewRing(4)
	for i := int64(1); i <= 3; i++ {
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}

	dst := []Sample{{TS: -1, Val: -1}} // pre-existing element must be kept, not overwritten
	got := r.AppendSince(20, dst)
	require.Equal(t, []Sample{{TS: -1, Val: -1}, {TS: 20, Val: 2}, {TS: 30, Val: 3}}, got)
}

// TestRingAppendSinceReusesCapacityAcrossCalls pins the whole point of
// Task 3's Ring.AppendSince: a caller (FlushMinutes) that walks many
// rings for the same ts can reuse one growing buffer instead of Since's
// fresh ring-capacity allocation per ring. Passing the same backing slice
// back in, reset to length 0, must not require a new allocation once its
// capacity already covers the result.
func TestRingAppendSinceReusesCapacityAcrossCalls(t *testing.T) {
	r := NewRing(4)
	for i := int64(1); i <= 4; i++ {
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}

	buf := make([]Sample, 0, 4)
	buf = r.AppendSince(0, buf[:0])
	require.Len(t, buf, 4)
	capAfterFirst := cap(buf)

	buf = r.AppendSince(0, buf[:0])
	require.Len(t, buf, 4)
	require.Equal(t, capAfterFirst, cap(buf), "reslicing to [:0] and appending again must not grow the backing array")
}

func TestRingSinceIsAppendSinceWithNilDst(t *testing.T) {
	r := NewRing(4)
	for i := int64(1); i <= 3; i++ {
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}
	require.Equal(t, r.AppendSince(20, nil), r.Since(20))
}

package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveRecordAndRead(t *testing.T) {
	l := NewLive(8)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	l.Record(k, 100, 42.0)
	l.Record(k, 102, 43.0)

	latest, ok := l.Latest(k)
	require.True(t, ok)
	require.Equal(t, Sample{TS: 102, Val: 43.0}, latest)
	require.Len(t, l.Since(k, 0), 2)
	require.Equal(t, []SeriesKey{k}, l.Keys())
}

func TestLiveConcurrentRecord(t *testing.T) {
	l := NewLive(512)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := SeriesKey{Kind: "container", Entity: string(rune('a' + g)), Metric: "cpu"}
			for i := int64(0); i < 100; i++ {
				l.Record(k, i, float64(i))
			}
		}(g)
	}
	wg.Wait()
	require.Len(t, l.Keys(), 8)
	for _, k := range l.Keys() {
		require.Len(t, l.Since(k, 0), 100)
	}
}

package fake

import (
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

type capture struct {
	recs map[store.SeriesKey][]store.Sample
}

func (c *capture) Record(k store.SeriesKey, ts int64, v float64) {
	if c.recs == nil {
		c.recs = map[store.SeriesKey][]store.Sample{}
	}
	c.recs[k] = append(c.recs[k], store.Sample{TS: ts, Val: v})
}

func TestTickEmitsHostAndContainerSeries(t *testing.T) {
	sink := &capture{}
	g := New(sink, 1)
	now := time.Unix(1_000_000, 0)
	g.Tick(now)
	g.Tick(now.Add(2 * time.Second))

	require.Len(t, sink.recs[store.SeriesKey{Kind: "host", Metric: "cpu.total"}], 2)

	containers := map[string]bool{}
	for k := range sink.recs {
		if k.Kind == "container" {
			containers[k.Entity] = true
		}
	}
	require.GreaterOrEqual(t, len(containers), 15, "want a fleet worth rendering")

	for k, samples := range sink.recs {
		for _, s := range samples {
			if k.Metric == "cpu.total" || k.Metric == "cpu.pct" || k.Metric == "mem.used_pct" {
				require.GreaterOrEqual(t, s.Val, 0.0, "%v", k)
				require.LessOrEqual(t, s.Val, 100.0, "%v", k)
			}
		}
	}
}

func TestDeterministicWithSameSeed(t *testing.T) {
	a, b := &capture{}, &capture{}
	now := time.Unix(1_000_000, 0)
	New(a, 42).Tick(now)
	New(b, 42).Tick(now)
	require.Equal(t, a.recs, b.recs)
}

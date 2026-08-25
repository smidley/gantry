// Package fake synthesizes a plausible Unraid box (host + container fleet)
// through the production MetricSink path, for UI development and demos.
// Enabled by GANTRY_FAKE_DATA=1. Never active by default.
package fake

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/smidley/gantry/internal/store"
)

type archetype struct {
	name     string
	cpuBase  float64 // steady CPU %
	cpuAmp   float64 // sinusoidal swing
	cpuSpike float64 // probability per tick of a hard spike
	memBytes float64
	netScale float64 // bytes/s magnitude
}

var fleet = []archetype{
	{"jellyfin", 4, 3, 0.02, 900e6, 4e6},
	{"plex", 3, 2, 0.02, 800e6, 3e6},
	{"radarr", 1, 1, 0.005, 300e6, 2e5},
	{"sonarr", 1, 1, 0.005, 320e6, 2e5},
	{"prowlarr", 0.5, 0.5, 0.002, 150e6, 5e4},
	{"qbittorrent", 6, 4, 0.01, 500e6, 8e6},
	{"sabnzbd", 2, 6, 0.01, 400e6, 9e6},
	{"postgres", 2, 0.5, 0.001, 1.2e9, 1e5},
	{"redis", 0.5, 0.2, 0.001, 200e6, 8e4},
	{"homeassistant", 3, 1, 0.005, 700e6, 1e5},
	{"grafana", 1, 0.5, 0.002, 250e6, 6e4},
	{"pihole", 0.5, 0.3, 0.001, 120e6, 4e4},
	{"nginx", 0.3, 0.2, 0.001, 80e6, 5e5},
	{"vaultwarden", 0.2, 0.1, 0.001, 90e6, 1e4},
	{"immich", 5, 4, 0.02, 1.5e9, 1e6},
	{"paperless", 1, 2, 0.01, 400e6, 8e4},
	{"gitea", 0.5, 0.5, 0.002, 300e6, 6e4},
	{"minecraft", 8, 5, 0.01, 2.5e9, 3e5},
	{"frigate", 12, 4, 0.02, 1.1e9, 5e6},
	{"unifi-controller", 2, 1, 0.005, 900e6, 2e5},
}

type Generator struct {
	sink store.MetricSink
	rng  *rand.Rand
}

func New(sink store.MetricSink, seed int64) *Generator {
	return &Generator{sink: sink, rng: rand.New(rand.NewSource(seed))}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Tick emits one sample per series for the instant `now`.
func (g *Generator) Tick(now time.Time) {
	ts := now.Unix()
	phase := float64(ts) / 300.0 // slow 5-minute swells

	hostCPU := 0.0
	for i, a := range fleet {
		cpu := a.cpuBase + a.cpuAmp*math.Sin(phase+float64(i)) + g.rng.Float64()
		if g.rng.Float64() < a.cpuSpike {
			cpu += 30 + g.rng.Float64()*40
		}
		cpu = clamp(cpu, 0, 100)
		hostCPU += cpu

		mem := a.memBytes * (0.9 + 0.2*g.rng.Float64())
		rx := a.netScale * (0.5 + g.rng.Float64())
		tx := a.netScale * 0.2 * (0.5 + g.rng.Float64())

		e := a.name
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "cpu.pct"}, ts, cpu)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "mem.bytes"}, ts, mem)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "net.rx_bps"}, ts, rx)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "net.tx_bps"}, ts, tx)
	}

	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "cpu.total"}, ts, clamp(hostCPU/3+5, 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.used_pct"}, ts, clamp(55+10*math.Sin(phase/3)+2*g.rng.Float64(), 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "net.rx_bps"}, ts, 20e6*(0.5+g.rng.Float64()))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "net.tx_bps"}, ts, 5e6*(0.5+g.rng.Float64()))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio.read_bps"}, ts, 30e6*g.rng.Float64())
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio.write_bps"}, ts, 15e6*g.rng.Float64())
}

// Run ticks until ctx is done. clock defaults to time.Now when nil.
func (g *Generator) Run(ctx context.Context, interval time.Duration, clock func() time.Time) {
	if clock == nil {
		clock = time.Now
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Tick(clock())
		}
	}
}

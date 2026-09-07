package insight

import (
	"math"
	"sort"
	"strings"

	"github.com/smidley/gantry/internal/store"
)

// RecordedSeries is a bounded excerpt captured when an incident first fires.
// Live-only device metrics would otherwise disappear with the in-memory ring.
type RecordedSeries struct {
	Kind   string       `json:"kind"`
	Entity string       `json:"entity"`
	Metric string       `json:"metric"`
	Points [][2]float64 `json:"points"`
}

const maxEvidenceSeries = 16
const maxEvidencePoints = 120

// evidencePoints preserves the endpoints and each bucket's extrema, so a
// brief peak remains visible when a high-frequency source needs thinning.
func evidencePoints(samples []store.Sample, from, to int64) [][2]float64 {
	points := make([][2]float64, 0, len(samples))
	for _, s := range samples {
		if s.TS >= from && s.TS <= to && !math.IsNaN(s.Val) && !math.IsInf(s.Val, 0) {
			points = append(points, [2]float64{float64(s.TS), s.Val})
		}
	}
	if len(points) <= maxEvidencePoints {
		return points
	}
	out := [][2]float64{points[0]}
	const buckets = (maxEvidencePoints - 2) / 2
	for i := 0; i < buckets; i++ {
		start, end := 1+i*(len(points)-2)/buckets, 1+(i+1)*(len(points)-2)/buckets
		minAt, maxAt := start, start
		for j := start + 1; j < end; j++ {
			if points[j][1] < points[minAt][1] {
				minAt = j
			}
			if points[j][1] > points[maxAt][1] {
				maxAt = j
			}
		}
		if minAt > maxAt {
			minAt, maxAt = maxAt, minAt
		}
		out = append(out, points[minAt])
		if maxAt != minAt {
			out = append(out, points[maxAt])
		}
	}
	return append(out, points[len(points)-1])
}

func (e *Engine) captureExcerpt(f Finding, now int64) ([]RecordedSeries, string) {
	if e.MatchSince == nil {
		return nil, ""
	}
	from := now - EvidenceWindowSecs
	if f.RuleID == RuleDiskSpinupChurn {
		from = now - SpinupLookbackSecs
	}
	device := f.Resource
	if e.Slots != nil {
		if meta, ok := e.Slots()[f.Resource]; ok && meta.Device != "" {
			device = meta.Device
		}
	}
	var recorded []RecordedSeries
	add := func(kind, entity, metric string) {
		if len(recorded) >= maxEvidenceSeries {
			return
		}
		samples, _ := e.MatchSince(kind, metric, from)
		if points := evidencePoints(samples[entity], from, now); len(points) > 0 {
			recorded = append(recorded, RecordedSeries{kind, entity, metric, points})
		}
	}
	var culpritMetrics []string
	switch f.RuleID {
	case RuleDiskIOContention:
		add("host", "", "diskio."+device+".util_pct")
		add("host", "", "diskio."+device+".await_ms")
		culpritMetrics = []string{"io.read_bps", "io.write_bps"}
	case RuleIODrivenCPULoad:
		add("host", "", "cpu.iowait_pct")
		culpritMetrics = []string{"io.read_bps", "io.write_bps"}
	case RuleCPUStarvation:
		add("container", f.Victim, "cpu.throttled_pct")
		add("host", "", "cpu.total")
		culpritMetrics = []string{"cpu.pct"}
	case RuleMemorySqueeze:
		add("host", "", "mem.used_pct")
		if f.VictimKind == "container" {
			add("container", f.Victim, "mem.pct")
		}
		culpritMetrics = []string{"mem.pct"}
	case RuleParitySlowdown:
		add("unraid", "array", "parity.speed_bps")
		culpritMetrics = []string{"io.read_bps", "io.write_bps"}
	case RuleDiskSpinupChurn:
		add("disk", f.Resource, "spun_up")
		culpritMetrics = []string{"io.read_bps", "io.write_bps"}
	case RuleGPUEngineContention:
		metric := "engine." + f.Victim + ".busy_pct"
		samples, _ := e.MatchSince("gpu", metric, from)
		names := make([]string, 0, len(samples))
		for name := range samples {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add("gpu", name, metric)
		}
		culpritMetrics = []string{"gpu." + f.Victim + ".busy_pct"}
	}
	for i, name := range f.Culprit.Names {
		if i >= 4 {
			break
		}
		for _, metric := range culpritMetrics {
			add("container", name, metric)
		}
	}
	// Keep the actual device-specific evidence too, without inventing a
	// partition/alias relationship when the collector did not record it.
	if f.RuleID == RuleDiskIOContention && e.MatchPrefixSince != nil {
		byEntity, _ := e.MatchPrefixSince("container", "live:io.", from)
		topology := NewTopology(e.DeviceName, nil)
		if e.Slots != nil {
			topology = NewTopology(e.DeviceName, e.Slots())
		}
		for i, name := range f.Culprit.Names {
			if i >= 4 {
				break
			}
			metrics := make([]string, 0, len(byEntity[name]))
			for metric := range byEntity[name] {
				metrics = append(metrics, metric)
			}
			sort.Strings(metrics)
			for _, metric := range metrics {
				raw := strings.TrimPrefix(metric, "live:io.")
				raw, _, _ = strings.Cut(raw, ".")
				resolved, known := topology.ResolveName(raw)
				if raw == device || (known && resolved.Slot == f.Resource) {
					add("container", name, metric)
				}
			}
		}
	}
	return recorded, device
}

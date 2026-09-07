package insight

import (
	"encoding/json"
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestEvidenceExcerptPreservesBriefPeakWithinBudget(t *testing.T) {
	samples := make([]store.Sample, 1000)
	for i := range samples {
		samples[i] = store.Sample{TS: int64(i), Val: 1}
	}
	samples[501].Val = 1000
	points := evidencePoints(samples, 0, 999)
	require.LessOrEqual(t, len(points), maxEvidencePoints)
	require.Equal(t, [2]float64{0, 1}, points[0])
	require.Equal(t, [2]float64{999, 1}, points[len(points)-1])
	require.Contains(t, points, [2]float64{501, 1000})
}

func TestRecordedEvidenceSurvivesSerializationAndIsBounded(t *testing.T) {
	e := &Engine{MatchSince: func(kind, metric string, since int64) (map[string][]store.Sample, map[string]int64) {
		return map[string][]store.Sample{"neighbor": {{TS: 50, Val: 8}, {TS: 999, Val: 99}}}, nil
	}}
	f := Finding{RuleID: RuleCPUStarvation, Culprit: Culprits{Names: []string{"neighbor"}}}
	recorded, _ := e.captureExcerpt(f, 100)
	require.Len(t, recorded, 1)
	require.Equal(t, "cpu.pct", recorded[0].Metric)
	encoded, err := json.Marshal(Evidence{RecordedSeries: recorded})
	require.NoError(t, err)
	var saved Evidence
	require.NoError(t, json.Unmarshal(encoded, &saved))
	require.Equal(t, [][2]float64{{50, 8}}, saved.RecordedSeries[0].Points)
}

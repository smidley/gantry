package insight

import (
	"testing"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func sampleSeries(startTS, stepSecs int64, vals ...float64) []store.Sample {
	out := make([]store.Sample, len(vals))
	for i, v := range vals {
		out[i] = store.Sample{TS: startTS + int64(i)*stepSecs, Val: v}
	}
	return out
}

// TestSustainedBreachingWhenEverySampleInWindowCrosses pins the basic
// positive case: every sample in the trailing forSecs window is above
// threshold, and the ring's oldest retained sample proves the window is
// fully covered.
func TestSustainedBreachingWhenEverySampleInWindowCrosses(t *testing.T) {
	samples := sampleSeries(100, 10, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100) // TS 100..190
	v, latest := Sustained(Window{To: 190, Samples: samples}, Above, 90, 90, 100)

	require.Equal(t, VerdictBreaching, v)
	require.Equal(t, 100.0, latest)
}

// TestSustainedOneDipResetsSustain pins allCross's own semantics carried
// over: sustained means EVERY sample, not "mostly" -- one sample back
// under threshold anywhere in the trailing window means not breaching,
// even though the latest sample is still above it.
func TestSustainedOneDipResetsSustain(t *testing.T) {
	samples := sampleSeries(100, 10, 91, 92, 60, 94, 95, 96, 97, 98, 99, 100) // one dip at TS 120
	v, _ := Sustained(Window{To: 190, Samples: samples}, Above, 90, 90, 100)

	require.Equal(t, VerdictHolding, v)
}

// TestSustainedExactlyOnThresholdDoesNotBreach pins the strict-crossing
// rule (thresholds.ts' documented "on a threshold reads as the band
// below it"): a value sitting exactly at threshold never crosses.
func TestSustainedExactlyOnThresholdDoesNotBreach(t *testing.T) {
	samples := sampleSeries(100, 10, 91, 92, 93, 94, 95, 96, 97, 98, 99, 90) // last sample == threshold
	v, _ := Sustained(Window{To: 190, Samples: samples}, Above, 90, 90, 100)

	require.Equal(t, VerdictHolding, v)
}

// TestSustainedUncoveredWindowIsInsufficient pins the coverage gate: the
// ring's oldest sample is younger than the trailing window needs, so
// there is no way to know whether the FULL window would have crossed --
// this is not a breach, and (see the next test) never reads as a clear
// either.
func TestSustainedUncoveredWindowIsInsufficient(t *testing.T) {
	samples := sampleSeries(50, 10, 91, 92, 93, 94) // oldest sample at TS 50
	v, _ := Sustained(Window{To: 90, Samples: samples}, Above, 90, 90, 50)

	require.Equal(t, VerdictInsufficient, v)
}

// TestSustainedEmptyWindowIsInsufficientNeverAClear pins the same
// no-data-is-not-evidence rule EvaluateThreshold documents: an entity
// with a long-covered ring but literally zero samples in the trailing
// window (e.g. a gap in collection) must never read as "the value must
// be fine, nothing crossed" -- it stays Insufficient.
func TestSustainedEmptyWindowIsInsufficientNeverAClear(t *testing.T) {
	v, latest := Sustained(Window{To: 90, Samples: nil}, Above, 90, 90, 0)

	require.Equal(t, VerdictInsufficient, v)
	require.Equal(t, 0.0, latest)
}

// TestSustainedZeroOldestIsInsufficientRegardlessOfSamples pins oldest==0
// as "unknown ring floor" (the Live.MatchSince convention: an entity
// absent from oldestTS reports 0) -- never treated as "the ring covers
// back to TS 0", which would let a freshly-appeared series masquerade as
// long-sustained.
func TestSustainedZeroOldestIsInsufficientRegardlessOfSamples(t *testing.T) {
	samples := sampleSeries(0, 10, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100)
	v, _ := Sustained(Window{To: 90, Samples: samples}, Above, 90, 90, 0)

	require.Equal(t, VerdictInsufficient, v)
}

// TestSustainedHoldingWhenCoveredButNotCrossing is the plain negative
// case: window fully covered, latest value not even crossing.
func TestSustainedHoldingWhenCoveredButNotCrossing(t *testing.T) {
	samples := sampleSeries(100, 10, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19)
	v, latest := Sustained(Window{To: 190, Samples: samples}, Above, 90, 90, 100)

	require.Equal(t, VerdictHolding, v)
	require.Equal(t, 19.0, latest)
}

// TestSustainedBelowDirectionCrossesUnderThreshold exercises the Below
// op -- the shape the engine's clear-side check uses (see engine.go):
// calling Sustained with the rule's clear op/threshold/for is how a
// caller asks "has this been clearing", without Sustained needing its
// own dedicated clear-verdict value.
func TestSustainedBelowDirectionCrossesUnderThreshold(t *testing.T) {
	samples := sampleSeries(100, 10, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69)
	v, _ := Sustained(Window{To: 190, Samples: samples}, Below, 70, 90, 100)

	require.Equal(t, VerdictBreaching, v)
}

// TestSustainedAgreesWithAlertEvaluateThresholdEdgeMatrix is the plan's
// own required cross-check: Sustained re-implements EvaluateThreshold's
// semantics rather than calling it (an insight rule has no store.AlertRule
// to bind the signature to), so this proves the two independently-written
// functions agree case for case on the FIRE side (both see the same
// samples/threshold/for/oldest/now and must both breach, hold, or read
// insufficient together) and on the CLEAR side (EvaluateThreshold folds
// fire+clear into one Verdict off one store.AlertRule; Sustained has no
// such rule, so a caller checks "is this clearing" by calling Sustained a
// second time with the clear op/threshold/for -- EvaluateThreshold's
// VerdictClearing must line up with THAT second call's VerdictBreaching).
func TestSustainedAgreesWithAlertEvaluateThresholdEdgeMatrix(t *testing.T) {
	const now = int64(1000)
	rule := store.AlertRule{
		Op: ">", Threshold: 90, ForSeconds: 90,
		ClearThreshold: 70, ClearSeconds: 90,
	}

	cases := []struct {
		name    string
		samples []store.Sample
		oldest  int64
		side    string // "fire" or "clear"
	}{
		{"fire breaching", sampleSeries(910, 10, 91, 92, 93, 94, 95, 96, 97, 98, 99), 910, "fire"},
		{"fire one dip resets", sampleSeries(910, 10, 91, 60, 93, 94, 95, 96, 97, 98, 99), 910, "fire"},
		{"fire exactly on threshold does not breach", sampleSeries(910, 10, 91, 92, 93, 94, 95, 96, 97, 98, 90), 910, "fire"},
		{"fire uncovered window is insufficient", sampleSeries(950, 10, 91, 92, 93, 94, 95), 950, "fire"},
		{"fire empty window is insufficient never a clear", nil, 0, "fire"},
		{"clear breaching", sampleSeries(910, 10, 60, 61, 62, 63, 64, 65, 66, 67, 68), 910, "clear"},
		{"clear one spike resets", sampleSeries(910, 10, 60, 95, 62, 63, 64, 65, 66, 67, 68), 910, "clear"},
		{"clear uncovered window is insufficient", sampleSeries(950, 10, 60, 61, 62, 63, 64), 950, "clear"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alertVerdict, _ := alert.EvaluateThreshold(rule, tc.samples, tc.oldest, now)

			var got Verdict
			if tc.side == "fire" {
				got, _ = Sustained(Window{To: now, Samples: tc.samples}, Above, rule.Threshold, rule.ForSeconds, tc.oldest)
				switch alertVerdict {
				case alert.VerdictBreaching:
					require.Equal(t, VerdictBreaching, got)
				case alert.VerdictInsufficient:
					require.Equal(t, VerdictInsufficient, got)
				case alert.VerdictHolding:
					require.Equal(t, VerdictHolding, got)
				default:
					t.Fatalf("fire side produced unexpected alert verdict %v", alertVerdict)
				}
				return
			}

			got, _ = Sustained(Window{To: now, Samples: tc.samples}, Below, rule.ClearThreshold, rule.ClearSeconds, tc.oldest)
			switch alertVerdict {
			case alert.VerdictClearing:
				require.Equal(t, VerdictBreaching, got, "alert Clearing must line up with Sustained's clear-side Breaching")
			case alert.VerdictInsufficient:
				require.Equal(t, VerdictInsufficient, got)
			case alert.VerdictHolding:
				require.Equal(t, VerdictHolding, got)
			default:
				t.Fatalf("clear side produced unexpected alert verdict %v", alertVerdict)
			}
		})
	}
}

package insight

import "github.com/smidley/gantry/internal/store"

// Op is a threshold comparison direction: ">" fires above a value, "<"
// fires below it. A fresh, insight-local type rather than a reuse of
// alert.AlertRule's own Op string field -- Sustained re-implements
// alert.EvaluateThreshold's semantics rather than calling it (that
// function's signature is bound to a store.AlertRule, which an insight
// rule is not), so this package owns its own small vocabulary end to end.
type Op string

const (
	Above Op = ">"
	Below Op = "<"
)

// Verdict is Sustained's answer for one series over one trailing window.
// The verdict vocabulary deliberately echoes alert.Verdict's own names
// (internal/alert/eval.go:5-36) -- one lifecycle model in the codebase,
// two independent entry points into it (window_test.go's edge-matrix
// test asserts the two agree case for case) -- but this is its own type:
// an insight rule has no store.AlertRule for alert.Verdict's producer to
// bind against. There is no VerdictClearing here: Sustained only ever
// checks ONE direction per call (see its own doc for why "is this
// clearing" is a second call, not a fourth verdict).
type Verdict int

const (
	// VerdictInsufficient: the ring's oldest retained sample cannot prove
	// coverage of the trailing `for` window -- not a breach, and (unlike
	// alert.Verdict, which folds fire+clear into one call) not evidence
	// either way about the opposite direction: a caller must not read
	// insufficiency as "safe to clear" just because it isn't "breaching".
	VerdictInsufficient Verdict = iota
	// VerdictBreaching: every sample in the trailing `for` seconds
	// crosses threshold in the direction op names.
	VerdictBreaching
	// VerdictHolding: the window is covered but not every sample
	// crosses -- neither confirmed nor ruled out.
	VerdictHolding
)

func (v Verdict) String() string {
	switch v {
	case VerdictInsufficient:
		return "insufficient"
	case VerdictBreaching:
		return "breaching"
	case VerdictHolding:
		return "holding"
	default:
		return "unknown"
	}
}

// Window is the evidence window every insight rule shares: samples
// (ascending by TS, the Live.MatchSince/Ring.Since convention) covering
// up to the 120s plan-wide evidence span, evaluated as of To (the tick's
// `now`). From is carried for evidence-bundle rendering (Task 9); only To
// and Samples drive Sustained itself.
type Window struct {
	From, To int64
	Samples  []store.Sample
}

// crosses reports whether val crosses threshold in the direction op
// names, strictly -- a value sitting exactly on threshold reads as the
// band below it (thresholds.ts' documented rule, mirrored from
// alert/eval.go's own crosses).
func crosses(op Op, val, threshold float64) bool {
	if op == Below {
		return val < threshold
	}
	return val > threshold
}

// windowSince returns the suffix of samples (already TS-ascending) at or
// after start.
func windowSince(samples []store.Sample, start int64) []store.Sample {
	for i, s := range samples {
		if s.TS >= start {
			return samples[i:]
		}
	}
	return nil
}

// allCross reports whether every sample in the window crosses threshold.
// An empty window is never a vacuous true: Sustained checks emptiness
// itself (as VerdictInsufficient) before ever calling this.
func allCross(samples []store.Sample, op Op, threshold float64) bool {
	if len(samples) == 0 {
		return false
	}
	for _, s := range samples {
		if !crosses(op, s.Val, threshold) {
			return false
		}
	}
	return true
}

// Sustained reports whether EVERY sample in the trailing forSecs seconds
// (measured back from w.To) crosses threshold in the direction op names:
// sustained means sustained, not "on average" -- one dip anywhere in the
// window resets it. oldest is the ring's true retention floor for this
// series (Live.MatchSince/MatchPrefixSince's own oldestTS, 0 when
// unknown); when it cannot prove the trailing window is fully covered,
// the answer is VerdictInsufficient, never a Breaching or a Holding
// masquerading as "the value must be fine."
//
// This has no clear-side twin of its own: a caller asking "has this been
// clearing" calls Sustained a second time with the clear op, clear
// threshold, and clear-for duration, and reads VerdictBreaching from that
// call as "yes" -- see engine.go's lifecycle sweep. That is what lets one
// pure primitive serve both directions without needing a store.AlertRule
// to bind a fire/clear pair together the way alert.EvaluateThreshold
// does.
func Sustained(w Window, op Op, threshold float64, forSecs int64, oldest int64) (Verdict, float64) {
	var latest float64
	if n := len(w.Samples); n > 0 {
		latest = w.Samples[n-1].Val
	}

	start := w.To - forSecs
	if oldest == 0 || oldest > start {
		return VerdictInsufficient, latest
	}
	win := windowSince(w.Samples, start)
	if len(win) == 0 {
		return VerdictInsufficient, latest
	}
	if allCross(win, op, threshold) {
		return VerdictBreaching, latest
	}
	return VerdictHolding, latest
}

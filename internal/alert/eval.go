package alert

import "github.com/smidley/gantry/internal/store"

// Verdict is EvaluateThreshold's answer for one (rule, entity) pair on
// one tick.
type Verdict int

const (
	// VerdictInsufficient: the series is younger than the window -- NOT
	// a breach, NOT a clear. Also returned for a window the ring claims
	// to cover but that holds zero samples (see EvaluateThreshold) --
	// silence is not evidence of recovery.
	VerdictInsufficient Verdict = iota
	// VerdictBreaching: every sample in [now-ForSeconds, now] crosses Threshold.
	VerdictBreaching
	// VerdictClearing: every sample in [now-ClearSeconds, now] is past ClearThreshold.
	VerdictClearing
	// VerdictHolding: neither -- an existing instance keeps its state.
	VerdictHolding
)

func (v Verdict) String() string {
	switch v {
	case VerdictInsufficient:
		return "insufficient"
	case VerdictBreaching:
		return "breaching"
	case VerdictClearing:
		return "clearing"
	case VerdictHolding:
		return "holding"
	default:
		return "unknown"
	}
}

// crosses reports whether val crosses threshold in the direction op
// names: ">" fires above, "<" fires below. Strict, matching
// thresholds.ts's own documented rule: a value sitting exactly on
// threshold reads as the band below it, so it does not cross.
func crosses(op string, val, threshold float64) bool {
	if op == "<" {
		return val < threshold
	}
	return val > threshold
}

// clearOp is the comparison a clear check uses: the opposite direction
// from the rule's own fire op, applied to ClearThreshold instead of
// Threshold.
func clearOp(op string) string {
	if op == "<" {
		return ">"
	}
	return "<"
}

// windowSince returns the suffix of samples (already TS-ascending, the
// Live.MatchSince/Ring.Since convention) at or after start.
func windowSince(samples []store.Sample, start int64) []store.Sample {
	for i, s := range samples {
		if s.TS >= start {
			return samples[i:]
		}
	}
	return nil
}

// allCross reports whether every sample in the window crosses threshold.
// An empty window is never a vacuous true here -- EvaluateThreshold
// checks window emptiness itself (as VerdictInsufficient) before ever
// calling this, so allCross only ever sees a window it already knows is
// non-empty; the explicit false is a second line of defense, not the
// primary guard.
func allCross(samples []store.Sample, op string, threshold float64) bool {
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

// EvaluateThreshold is pure: no clock, no store, no I/O. samples is
// whatever the caller fetched (ascending by TS); oldest is the ring's
// oldest retained TS for this series, 0 if unknown. The returned float64
// is the latest sample's value regardless of verdict, for snapshotting
// onto an instance.
//
// Order of checks, and why: the fire window (ForSeconds) is checked for
// coverage FIRST and unconditionally, even when the caller only cares
// about clearing -- a rule can only ever reach a clearing verdict once
// its primary window is provably covered by ring history, matching verdict
// 1's "the ring cannot cover the window" gate. The clear window then gets
// its own, independent coverage + emptiness check: it's usually shorter
// than the fire window (so this second check is normally a no-op) but a
// rule may configure it longer, and a partially-covered clear window must
// never be trusted either.
func EvaluateThreshold(r store.AlertRule, samples []store.Sample, oldest, now int64) (Verdict, float64) {
	var latest float64
	if n := len(samples); n > 0 {
		latest = samples[n-1].Val
	}

	breachStart := now - r.ForSeconds
	if oldest == 0 || oldest > breachStart {
		return VerdictInsufficient, latest
	}
	breachWindow := windowSince(samples, breachStart)
	if len(breachWindow) == 0 {
		return VerdictInsufficient, latest
	}
	if allCross(breachWindow, r.Op, r.Threshold) {
		return VerdictBreaching, latest
	}

	clearStart := now - r.ClearSeconds
	if oldest > clearStart {
		return VerdictInsufficient, latest
	}
	clearWindow := windowSince(samples, clearStart)
	if len(clearWindow) == 0 {
		return VerdictInsufficient, latest
	}
	if allCross(clearWindow, clearOp(r.Op), r.ClearThreshold) {
		return VerdictClearing, latest
	}
	return VerdictHolding, latest
}

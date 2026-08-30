// Package alert is Gantry's alert engine: rule model, pure threshold/
// event evaluators, and the lifecycle state machine that turns a verdict
// into a pending/firing/resolved alert_instances row. Delivery (the
// notify-spool and webhook channels) is a later phase; this package only
// decides WHAT should alert, never how it reaches a person.
package alert

import (
	"fmt"
	"strings"

	"github.com/smidley/gantry/internal/store"
)

// MatchEntity glob-matches an entity name: "*" any, a trailing "*" is a
// prefix match, otherwise exact. Deliberately NOT full glob/regex --
// docker names and Unraid slot names have no charset that needs it, and a
// regex in a user-editable rule is a support burden.
func MatchEntity(glob, entity string) bool {
	switch {
	case glob == "*":
		return true
	case strings.HasSuffix(glob, "*"):
		return strings.HasPrefix(entity, strings.TrimSuffix(glob, "*"))
	default:
		return glob == entity
	}
}

// MatchClass: "" matches anything; "nvme" requires class=="nvme"; "!nvme"
// requires class!="nvme". Leading "!" is the only operator. An empty
// class (a kind ClassOf never resolves, e.g. anything but "disk") is
// simply not "nvme", so a negated spec still matches it -- a "not nvme"
// rule is meant to cover every disk that isn't specifically nvme,
// including one whose class can't be determined.
func MatchClass(spec, class string) bool {
	if spec == "" {
		return true
	}
	if neg, ok := strings.CutPrefix(spec, "!"); ok {
		return class != neg
	}
	return class == spec
}

// kindSampleIntervalSeconds is a conservative map of each entity kind's
// own collector cadence (internal/collect/{host,docker,gpu,pressure,
// unraid}'s tickInterval consts), duplicated here rather than imported --
// importing collect from alert would invert the dependency, and this
// package stays free of I/O. host/container/gpu record at 2s; disk/
// unraid (the unraid collector's own single 15s tick) at 15s. A kind not
// listed here falls back to the fastest cadence, the tightest and safest
// bound when the real one isn't known.
var kindSampleIntervalSeconds = map[string]int64{
	"host":      2,
	"container": 2,
	"gpu":       2,
	"disk":      15,
	"unraid":    15,
}

// maxProvableWindowSeconds is the longest span a full ring of
// store.DefaultRingCap samples at this kind's own cadence can ever prove
// coverage for. N samples spaced `interval` seconds apart cover
// (N-1)*interval seconds between the oldest and the newest, not N*
// interval -- one second more than that and EvaluateThreshold's coverage
// check (oldest > breachStart) can never be satisfied, no matter how
// long the rule has actually been sustained (see F5).
func maxProvableWindowSeconds(kind string) int64 {
	interval, ok := kindSampleIntervalSeconds[kind]
	if !ok {
		interval = 2
	}
	return (store.DefaultRingCap - 1) * interval
}

// validBandFamilies mirrors thresholds.ts' six display-band families
// (Task 12 unifies them; until that frontend file exists this branch's
// own alert_defaults.go is the source of truth for the exact six names --
// see DefaultAlertRules). "" (no band family) is valid too and checked
// separately below, not folded into this set.
var validBandFamilies = map[string]bool{
	"host.cpu":                true,
	"host.mem":                true,
	"disk.capacity":           true,
	"disk.temp":               true,
	"disk.temp.nvme":          true,
	"container.mem_limit_pct": true,
}

// ValidateRule checks the invariants the store schema can't express on
// its own. It does not check id/name non-emptiness or entity_glob/
// entity_class syntax -- MatchEntity/MatchClass already treat any string
// as a well-formed pattern, so there is nothing to reject there. Nor does
// it check that an event rule's Kind agrees with its EventKinds (F11):
// scoping which events reach an event rule is EventKinds' job alone (a
// dot-namespaced event kind like "container.health" or "parity.finish"
// already names its own entity domain unambiguously) -- see
// processEventForRule's own doc for why deriving that from Kind instead
// would either be redundant or actively wrong.
func ValidateRule(r store.AlertRule) error {
	switch r.Type {
	case "threshold", "event":
	default:
		return fmt.Errorf("alert rule %q: invalid type %q", r.ID, r.Type)
	}
	// op is only meaningful for a threshold rule -- the seeded event
	// rules (alert_defaults.go) never set it, so it round-trips as ""
	// through the store, and that must validate clean.
	if r.Type == "threshold" {
		switch r.Op {
		case ">", "<":
		default:
			return fmt.Errorf("alert rule %q: invalid op %q", r.ID, r.Op)
		}
	}
	switch r.Severity {
	case "info", "warning", "alert":
	default:
		return fmt.Errorf("alert rule %q: invalid severity %q", r.ID, r.Severity)
	}
	if r.Type == "threshold" && r.Metric == "" {
		return fmt.Errorf("alert rule %q: threshold rule needs a metric", r.ID)
	}
	if r.Type == "event" && r.EventKinds == "" {
		return fmt.Errorf("alert rule %q: event rule needs event_kinds", r.ID)
	}
	// Equal thresholds with a live sustained-for clear window would strand
	// a value sitting exactly at the boundary: strict comparison
	// (thresholds.ts' documented "on a threshold reads as the band below
	// it") means it never breaches AND never clears for the window's
	// entire duration, an instance that can only leave firing by the rule
	// being disabled. clear_seconds==0 is exempted -- that shape checks
	// only the single latest sample on every tick (see EvaluateThreshold),
	// never a sustained window, so an exact tie there is re-evaluated
	// fresh next tick rather than accumulating into a permanent stall.
	if r.Type == "threshold" && r.Threshold == r.ClearThreshold && r.ClearSeconds > 0 {
		return fmt.Errorf("alert rule %q: threshold and clear_threshold must differ when clear_seconds > 0", r.ID)
	}
	if r.Type == "threshold" {
		if max := maxProvableWindowSeconds(r.Kind); r.ForSeconds > max {
			return fmt.Errorf("alert rule %q: for_seconds %d exceeds %ds, the longest window a %q ring can prove coverage for", r.ID, r.ForSeconds, max, r.Kind)
		}
	}
	if r.ForSeconds > 3600 {
		return fmt.Errorf("alert rule %q: for_seconds %d exceeds the 3600s cap", r.ID, r.ForSeconds)
	}
	if r.RenotifyHours < 0 || r.RenotifyHours > 168 {
		return fmt.Errorf("alert rule %q: renotify_hours %d outside 0-168", r.ID, r.RenotifyHours)
	}
	if r.BandFamily != "" && !validBandFamilies[r.BandFamily] {
		return fmt.Errorf("alert rule %q: unknown band_family %q", r.ID, r.BandFamily)
	}
	return nil
}

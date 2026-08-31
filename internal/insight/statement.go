package insight

import (
	"fmt"
	"strconv"
	"strings"
)

// Confidence is a finding's evidentiary tier -- the plan's own two-tier,
// two-vocabulary contract (Global Constraints): ConfidenceLikely is
// tier-1 proxy evidence and ConfidenceConfirmed is a measured PSI stall
// on the victim, or a hard event like an OOM kill. Nothing else may ever
// produce ConfidenceConfirmed -- see each rule's own Eval in rules.go.
type Confidence int

const (
	ConfidenceLikely Confidence = iota
	ConfidenceConfirmed
)

func (c Confidence) String() string {
	if c == ConfidenceConfirmed {
		return "confirmed"
	}
	return "likely"
}

// Tier reports which evidence family produced a Rule or a Finding:
// TierProxy works on stock Unraid; TierPSI needs psi=1. Mirrors
// pressure.Collector.Tier()'s own two return values so the engine never
// invents a third spelling of the same idea.
type Tier int

const (
	TierProxy Tier = iota
	TierPSI
)

func (t Tier) String() string {
	if t == TierPSI {
		return "psi"
	}
	return "proxy"
}

// Shape distinguishes the RELATIONSHIP a finding describes -- not the
// rule that produced it -- because Verb's causal-language choice depends
// on shape, not on which of the seven rules is asking. A closed,
// enumerable set: Verb is property-tested over every value of it.
type Shape int

const (
	// ShapeSlowing: the culprit's share of a contended resource is
	// slowing (likely) or starving (confirmed) one or more OTHER
	// entities sharing it -- disk-io-contention, cpu-starvation,
	// gpu-engine-contention, and memory-squeeze's container-victim path.
	ShapeSlowing Shape = iota
	// ShapeLoading: the culprit's activity is loading (likely) or
	// causing (confirmed) load onto a HOST- or ARRAY-WIDE aggregate,
	// not a specific sibling entity -- io-driven-cpu-load,
	// parity-slowdown, and memory-squeeze's host-wide path.
	ShapeLoading
	// ShapeKeepingAwake: the culprit's activity is keeping (likely) or
	// forcing (confirmed) a device to stay spun up -- disk-spinup-churn.
	// Confirmed is never actually produced for this shape today (the
	// rule is tier-1 native, Task 6's own table), but Verb still answers
	// it, matching the property test's "over every shape" contract.
	ShapeKeepingAwake
)

// Verb is the ONLY place causal language is chosen (Global Constraints):
// ConfidenceLikely never yields a causal verb; ConfidenceConfirmed always
// does. A rule (via Statement's per-rule builders below) earns "causing"
// or "starving" or "forcing" only by supplying a measured victim stall or
// a hard event -- there is no third path, and no rule hand-writes any of
// these words itself.
func Verb(c Confidence, shape Shape) string {
	switch shape {
	case ShapeLoading:
		if c == ConfidenceConfirmed {
			return "is causing"
		}
		return "is loading"
	case ShapeKeepingAwake:
		if c == ConfidenceConfirmed {
			return "is forcing"
		}
		return "is keeping"
	default: // ShapeSlowing
		if c == ConfidenceConfirmed {
			return "is starving"
		}
		return "is likely slowing"
	}
}

// pluralizeIs turns a Verb() phrase's leading "is" into "are" for a
// shared (multi-name) culprit -- Verb itself stays singular/plural
// agnostic (it answers "which words", not "which conjugation"), so
// subject-verb agreement is a presentation-layer concern handled once,
// here, rather than inside Verb.
func pluralizeIs(phrase string) string {
	if rest, ok := strings.CutPrefix(phrase, "is "); ok {
		return "are " + rest
	}
	return phrase
}

// Evidence carries every number a rendered Statement might quote, plus
// the API/evidence-drawer's own "show your working" numbers (Task 9).
// Not every field applies to every rule/shape/confidence combination;
// a field the current finding's template doesn't use is simply left at
// its zero value and never rendered.
type Evidence struct {
	CulpritSharePct   float64  // culprit's (or culprit set's combined) share of the resource, 0-100
	DeviceUtilPct     float64  // host diskio.<dev>.util_pct
	AwaitMs           float64  // host diskio.<dev>.await_ms
	VictimStallPct    float64  // PSI some_pct, confirmed-tier victim evidence
	WindowMinutes     int      // the "last N minutes" a stall/rate figure covers
	OtherUsers        []string // other entities also touching the resource (likely-tier co-tenancy witnesses)
	IowaitPct         float64  // host/cpu.iowait_pct
	HostCPUPct        float64  // host/cpu.total
	SpinCount         int      // disk-spinup-churn: transitions observed
	SpinWindowMinutes int      // disk-spinup-churn: the observation window
	EngineBusyPct     float64  // gpu engine busy_pct
	BaselinePct       float64  // parity-slowdown: current speed as % of baseline
}

// Finding is EvaluateRule's answer for one (rule, victim, resource) tuple
// on one tick. Every field is populated by construction -- there is no
// zero-valued path that yields a "finding" with an empty Culprit or an
// empty Resource, which is what the plan's "both sides or nothing" rule
// (Global Constraints) means by enforcing the contract in the type: a
// rule's Eval either returns no Finding for a candidate, or a fully
// populated one.
type Finding struct {
	RuleID     string
	VictimKind string // container|host|array|disk|gpu
	Victim     string // "" for a host/array-wide finding
	Culprit    Culprits
	Resource   string
	Confidence Confidence
	Tier       Tier
	Shape      Shape
	Severity   string // info|warning|alert
	Evidence   Evidence
}

// pct formats a percentage with the plan's own convention (whole numbers
// read cleaner than "78.0%" in a one-sentence finding; a genuinely
// fractional share still shows one decimal so a near-floor culprit isn't
// rounded into looking like it cleared by more than it did).
func pct(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

func culpritSubject(c Culprits) string { return joinAnd(c.Names) }

// verbFor renders Verb's answer with the right subject-verb agreement
// for a possibly-shared culprit -- see pluralizeIs's own doc.
func verbFor(f Finding) string {
	v := Verb(f.Confidence, f.Shape)
	if f.Culprit.Shared {
		return pluralizeIs(v)
	}
	return v
}

// Statement renders a Finding's one-sentence, human-readable claim.
// Verb (above) is the only place causal language is chosen; everything
// else here is sentence assembly around that one word choice, switched
// per rule because the SHAPE of the supporting clause (which numbers,
// which nouns) is necessarily rule-specific even when two rules share a
// Shape's verb pair.
func Statement(f Finding) string {
	switch f.RuleID {
	case RuleDiskIOContention:
		return statementDiskIOContention(f)
	case RuleIODrivenCPULoad:
		return statementIODrivenCPULoad(f)
	case RuleCPUStarvation:
		return statementCPUStarvation(f)
	case RuleParitySlowdown:
		return statementParitySlowdown(f)
	case RuleDiskSpinupChurn:
		return statementDiskSpinupChurn(f)
	case RuleGPUEngineContention:
		return statementGPUEngineContention(f)
	case RuleMemorySqueeze:
		return statementMemorySqueeze(f)
	default:
		return fmt.Sprintf("%s %s %s", culpritSubject(f.Culprit), verbFor(f), f.Resource)
	}
}

// statementDiskIOContention matches the plan's own verbatim golden
// examples (Task 6):
//
//	likely:    "qbittorrent is likely slowing other containers on disk3 —
//	           it's driving 78% of the disk's IO while the device sits at
//	           98% utilisation and 42ms average latency. jellyfin and
//	           sonarr are also reading from disk3."
//	confirmed: "qbittorrent is starving jellyfin on disk3 — jellyfin's IO
//	           was stalled 38% of the last 10 minutes while qbittorrent
//	           drove 78% of the disk's IO."
func statementDiskIOContention(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	if f.Confidence == ConfidenceConfirmed {
		return fmt.Sprintf("%s %s %s on %s — %s's IO was stalled %s%% of the last %d minutes while %s drove %s%% of the disk's IO.",
			culprit, verbFor(f), f.Victim, f.Resource,
			f.Victim, pct(f.Evidence.VictimStallPct), f.Evidence.WindowMinutes,
			culprit, pct(f.Evidence.CulpritSharePct))
	}
	victim := f.Victim
	if victim == "" {
		victim = "other containers"
	}
	s := fmt.Sprintf("%s %s %s on %s — it's driving %s%% of the disk's IO while the device sits at %s%% utilisation and %sms average latency.",
		culprit, verbFor(f), victim, f.Resource,
		pct(f.Evidence.CulpritSharePct), pct(f.Evidence.DeviceUtilPct), pct(f.Evidence.AwaitMs))
	if len(f.Evidence.OtherUsers) > 0 {
		verb := "is"
		if len(f.Evidence.OtherUsers) > 1 {
			verb = "are"
		}
		s += fmt.Sprintf(" %s %s also reading from %s.", joinAnd(f.Evidence.OtherUsers), verb, f.Resource)
	}
	return s
}

// statementIODrivenCPULoad matches the plan's own verbatim golden
// (Scott's founding example, verbatim in shape):
//
//	likely: "sabnzbd's storage IO is loading the host CPU — it's driving
//	        63% of all disk IO while the CPU spends 24% of its time
//	        waiting on IO."
//
// confirmed (the psi.io.some_pct upgrade) swaps the victim clause for the
// measured host stall, the same likely->confirmed shape every other rule
// uses.
func statementIODrivenCPULoad(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	if f.Confidence == ConfidenceConfirmed {
		return fmt.Sprintf("%s's storage IO %s host CPU starvation — the host was stalled on IO %s%% of the last %d minutes while %s drove %s%% of all disk IO.",
			culprit, verbFor(f), pct(f.Evidence.VictimStallPct), f.Evidence.WindowMinutes, culprit, pct(f.Evidence.CulpritSharePct))
	}
	return fmt.Sprintf("%s's storage IO %s the host CPU — it's driving %s%% of all disk IO while the CPU spends %s%% of its time waiting on IO.",
		culprit, verbFor(f), pct(f.Evidence.CulpritSharePct), pct(f.Evidence.IowaitPct))
}

// statementCPUStarvation: the victim is a specific throttled/stalled
// container, the culprit a different container holding a large CPU
// share while the host itself runs hot.
func statementCPUStarvation(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	if f.Confidence == ConfidenceConfirmed {
		return fmt.Sprintf("%s %s %s of CPU — %s's CPU was stalled %s%% of the last %d minutes while %s holds %s%% of host CPU.",
			culprit, verbFor(f), f.Victim, f.Victim, pct(f.Evidence.VictimStallPct), f.Evidence.WindowMinutes, culprit, pct(f.Evidence.CulpritSharePct))
	}
	return fmt.Sprintf("%s %s %s's CPU — %s was throttled %s%% of the window while %s holds %s%% of host CPU and the host sits at %s%% overall.",
		culprit, verbFor(f), f.Victim, f.Victim, pct(f.Evidence.VictimStallPct), culprit, pct(f.Evidence.CulpritSharePct), pct(f.Evidence.HostCPUPct))
}

// statementParitySlowdown: tier-1 native (no PSI upgrade path exists for
// this rule -- Task 6's own table), so Confidence is always Likely in
// practice; the Confirmed branch still renders something coherent for
// Statement's own completeness/golden-string tests.
func statementParitySlowdown(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	verb := verbFor(f)
	if f.Confidence == ConfidenceConfirmed {
		verb = strings.Replace(verb, "starving", "stalling", 1)
	}
	return fmt.Sprintf("%s %s the parity check — parity speed has dropped to %s%% of its usual baseline while %s drives %s%% of the array's data IO.",
		culprit, verb, pct(f.Evidence.BaselinePct), culprit, pct(f.Evidence.CulpritSharePct))
}

// statementDiskSpinupChurn matches the plan's own verbatim golden:
//
//	likely: "plex is keeping disk5 awake — it has spun up 5 times in the
//	        last hour, each within a minute of plex reading from it."
func statementDiskSpinupChurn(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	return fmt.Sprintf("%s %s %s awake — it has spun up %d times in the last hour, each within a minute of %s reading from it.",
		culprit, verbFor(f), f.Resource, f.Evidence.SpinCount, culprit)
}

// statementGPUEngineContention: co-tenancy is the finding itself (>=2
// containers sharing one saturated engine), so this always renders
// through the Shared culprit-set path in practice; the single-culprit
// branch stays for Statement's own completeness.
func statementGPUEngineContention(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	return fmt.Sprintf("%s %s %s at %s%% busy — %s together drive %s%% of its usage.",
		culprit, verbFor(f), f.Resource, pct(f.Evidence.EngineBusyPct), culprit, pct(f.Evidence.CulpritSharePct))
}

// statementMemorySqueeze branches on VictimKind: an OOM'd container (the
// hard-event path, always Confirmed+alert) gets the container-victim
// ShapeSlowing rendering; the host-wide mem.used_pct threshold path gets
// the ShapeLoading rendering, since there is no single sibling container
// being named as the one starved -- the whole host is under pressure.
func statementMemorySqueeze(f Finding) string {
	culprit := culpritSubject(f.Culprit)
	if f.VictimKind == "container" {
		if f.Confidence == ConfidenceConfirmed {
			return fmt.Sprintf("%s %s %s of memory — %s was OOM-killed while %s holds %s%% of host memory.",
				culprit, verbFor(f), f.Victim, f.Victim, culprit, pct(f.Evidence.CulpritSharePct))
		}
		return fmt.Sprintf("%s %s %s's memory — %s is under memory pressure while %s holds %s%% of host memory.",
			culprit, verbFor(f), f.Victim, f.Victim, culprit, pct(f.Evidence.CulpritSharePct))
	}
	if f.Confidence == ConfidenceConfirmed {
		return fmt.Sprintf("%s %s host memory pressure — the host was stalled on memory %s%% of the last %d minutes while %s holds %s%% of host memory.",
			culprit, verbFor(f), pct(f.Evidence.VictimStallPct), f.Evidence.WindowMinutes, culprit, pct(f.Evidence.CulpritSharePct))
	}
	return fmt.Sprintf("%s %s host memory — the host is at %s%% memory used while %s holds %s%% of it.",
		culprit, verbFor(f), pct(f.Evidence.VictimStallPct), culprit, pct(f.Evidence.CulpritSharePct))
}

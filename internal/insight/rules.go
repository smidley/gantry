// Package insight is the cross-container impact correlation engine
// (spec §16): a fixed, compiled-in library of rules that each turn a
// tick's already-gathered evidence (In) into zero or more Finding
// values, requiring BOTH a victim's measured distress and a culprit's
// dominant share of the same contended resource before ever claiming one
// impacts the other (Global Constraints' "both sides or nothing").
package insight

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/smidley/gantry/internal/store"
)

// Rule ids -- the compiled-in library's identity strings, matching
// insight_instances.rule_id (004_insights.sql) and this plan's own task
// table verbatim.
const (
	RuleDiskIOContention    = "disk-io-contention"
	RuleIODrivenCPULoad     = "io-driven-cpu-load"
	RuleCPUStarvation       = "cpu-starvation"
	RuleParitySlowdown      = "parity-slowdown"
	RuleDiskSpinupChurn     = "disk-spinup-churn"
	RuleGPUEngineContention = "gpu-engine-contention"
	RuleMemorySqueeze       = "memory-squeeze"
)

// Engine-wide cadence constants (Global Constraints). EvidenceWindowSecs
// bounds every gather-step fetch that doesn't need its own longer
// lookback (BaselineLookbackSecs/SpinupLookbackSecs below). SustainForSecs
// is the "sustain_secs" threshold DEFAULT every rule but disk-spinup-churn
// carries in its own Thresholds map (librarySpecs) rather than reading
// this constant directly at Eval time: fake mode needs to compress how
// long a breach must sustain before firing (Task 10's own compressed
// demo schedule), and the existing per-rule threshold-override mechanism
// (Rules(overrides)) is what already lets a caller change a number
// without a second, engine-level compression path -- see rules_test.go's
// TestRulesThresholdOverrideChangesTheFiringPoint for the exact same
// override plumbing this reuses. parity-slowdown's own default (120s)
// is the one rule that genuinely needs a different sustain-for than the
// shared 90s baseline.
const (
	EvidenceWindowSecs int64 = 120
	SustainForSecs     int64 = 90
	// BaselineLookbackSecs is how far back the engine's gather step reads
	// a series that needs a rolling-median Baseline rather than a plain
	// threshold: disk-io-contention's diskio.<dev>.await_ms and
	// parity-slowdown's parity.speed_bps both split their fetched window
	// at Now-EvidenceWindowSecs into "history" (everything older, fed to
	// Baseline) and "recent" (the evidence window itself) -- so both need
	// real history beyond the 120s window to compute a baseline from at
	// all, not just the window Sustained itself checks.
	BaselineLookbackSecs int64 = 600
	// SpinupLookbackSecs is disk-spinup-churn's own required window
	// (Task 6's table: "3 within 60m") -- comfortably inside the 15s
	// unraid collector's ~112-minute ring coverage (Global Constraints).
	SpinupLookbackSecs int64 = 3600
)

// MatchResult mirrors one Live.MatchSince call's pair of returns.
type MatchResult struct {
	Samples map[string][]store.Sample
	Oldest  map[string]int64
}

// PrefixResult mirrors one Live.MatchPrefixSince call's pair of returns.
type PrefixResult struct {
	Samples map[string]map[string][]store.Sample
	Oldest  map[string]map[string]int64
}

// In carries one tick's already-gathered inputs: every MatchSince/
// MatchPrefixSince result the seven rules collectively need, the
// topology snapshot, recent hard events, and which PSI tier is live --
// so no Eval function below performs any I/O of its own. The engine
// (engine.go) builds exactly one In per tick, making each Live call
// below exactly once regardless of how many rules are enabled.
type In struct {
	Now int64 // the tick's `now`, unix seconds

	HostDiskIO      PrefixResult // kind=host,      prefix="diskio."  -- util_pct/await_ms/... per device
	ContainerLiveIO PrefixResult // kind=container, prefix="live:io." -- read_bps/write_bps per device per container
	GPUEngine       PrefixResult // kind=gpu,       prefix="engine."  -- busy_pct per engine per GPU entity
	ContainerGPU    PrefixResult // kind=container, prefix="gpu."     -- busy_pct per engine (mem_mib entries ignored)

	HostPSI      map[string]MatchResult // metric ("psi.cpu.some_pct" etc) -> host-kind result (single "" entity key)
	ContainerPSI map[string]MatchResult // metric -> per-container result

	HostCPUIowait  MatchResult
	HostCPUTotal   MatchResult
	HostMemUsedPct MatchResult

	ContainerCPUThrottled  MatchResult
	ContainerCPUAllocCores MatchResult
	ContainerCPUPct        MatchResult
	ContainerMemPct        MatchResult

	ParitySpeedBps    MatchResult
	ParityProgressPct MatchResult

	DiskSpunUp     MatchResult
	DiskRotational MatchResult

	OOMEvents []store.Event // container.oom events within the evidence window

	Topology *Topology
	Tier     string // "psi" | "proxy" -- pressure.Collector.Tier()
}

// Rule is one compiled-in evaluator plus its tunable thresholds --
// SHAPE (the Eval closure's logic) is fixed; only the named entries in
// Thresholds can differ from DefaultRules' own, via
// store.InsightRuleConfig.Overrides (see Rules below).
type Rule struct {
	ID         string
	Title      string
	Tier       Tier
	PSIUpgrade bool
	Thresholds map[string]float64
	Eval       func(In) []Finding
}

// ruleSpec is the compiled-in library's own private shape: the pure
// evaluator function plus its DEFAULT thresholds, before any per-rule
// override is merged in. Rules (below) is what turns this into the
// public, engine-facing []Rule.
type ruleSpec struct {
	id         string
	title      string
	tier       Tier
	psiUpgrade bool
	defaults   map[string]float64
	eval       func(In, map[string]float64) []Finding
}

var librarySpecs = []ruleSpec{
	{
		id: RuleDiskIOContention, title: "Disk IO contention", tier: TierProxy, psiUpgrade: true,
		defaults: map[string]float64{
			"util_pct_floor": 90, "await_multiplier": 2, "culprit_share_floor_pct": 60, "psi_stall_floor": 20,
			"sustain_secs": float64(SustainForSecs),
		},
		eval: evalDiskIOContention,
	},
	{
		id: RuleIODrivenCPULoad, title: "IO-driven CPU load", tier: TierProxy, psiUpgrade: true,
		defaults: map[string]float64{
			"iowait_pct_floor": 15, "psi_io_some_floor": 20, "psi_io_full_floor": 10, "culprit_share_floor_pct": 50,
			"sustain_secs": float64(SustainForSecs),
		},
		eval: evalIODrivenCPULoad,
	},
	{
		id: RuleCPUStarvation, title: "CPU starvation", tier: TierProxy, psiUpgrade: true,
		defaults: map[string]float64{
			"throttled_pct_floor": 5, "psi_cpu_some_floor": 20, "culprit_cpu_pct_floor": 40, "host_cpu_total_floor": 85,
			"sustain_secs": float64(SustainForSecs),
		},
		eval: evalCPUStarvation,
	},
	{
		id: RuleParitySlowdown, title: "Parity check slowdown", tier: TierProxy, psiUpgrade: false,
		defaults: map[string]float64{
			"speed_floor_fraction_of_baseline": 0.75, "culprit_share_floor_pct": 25, "sustain_secs": 120,
		},
		eval: evalParitySlowdown,
	},
	{
		id: RuleDiskSpinupChurn, title: "Disk spin-up churn", tier: TierProxy, psiUpgrade: false,
		defaults: map[string]float64{
			"min_transitions": 3, "window_minutes": 60, "attribution_window_secs": 60,
		},
		eval: evalDiskSpinupChurn,
	},
	{
		id: RuleGPUEngineContention, title: "GPU engine contention", tier: TierProxy, psiUpgrade: false,
		defaults: map[string]float64{
			"engine_busy_floor": 90, "culprit_share_floor_pct": 10, "min_culprits": 2, "sustain_secs": float64(SustainForSecs),
		},
		eval: evalGPUEngineContention,
	},
	{
		id: RuleMemorySqueeze, title: "Memory squeeze", tier: TierProxy, psiUpgrade: true,
		defaults: map[string]float64{
			"mem_used_pct_floor": 92, "psi_mem_some_floor": 10, "culprit_mem_pct_floor": 30, "sustain_secs": float64(SustainForSecs),
		},
		eval: evalMemorySqueeze,
	},
}

// mergeThresholds returns a fresh map: every default, with override's
// entries applied on top -- an override may only ever change a VALUE for
// an already-named threshold; it can neither add a new knob an evaluator
// doesn't read nor remove one the evaluator needs, since the evaluator
// always reads by name with the default map as its own fallback base.
func mergeThresholds(defaults, overrides map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(defaults))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range overrides {
		if _, known := defaults[k]; known {
			out[k] = v
		}
	}
	return out
}

// Rules returns the compiled-in library, one Rule per ruleSpec, with
// each rule's Thresholds merged from its own defaults plus
// overrides[rule.ID] (store.InsightRuleConfig.Overrides, decoded by the
// caller -- this package does no JSON of its own). Passing a nil or
// empty overrides map is DefaultRules' own behavior: every threshold at
// its compiled-in default.
func Rules(overrides map[string]map[string]float64) []Rule {
	out := make([]Rule, len(librarySpecs))
	for i, spec := range librarySpecs {
		th := mergeThresholds(spec.defaults, overrides[spec.id])
		eval := spec.eval
		out[i] = Rule{
			ID: spec.id, Title: spec.title, Tier: spec.tier, PSIUpgrade: spec.psiUpgrade,
			Thresholds: th,
			Eval:       func(in In) []Finding { return eval(in, th) },
		}
	}
	return out
}

// DefaultRules is Rules(nil) -- every threshold at its compiled-in
// default, for callers with no store.InsightRuleConfig overrides on hand
// (tests, and the engine's own first tick before it has read any).
func DefaultRules() []Rule { return Rules(nil) }

// fastSustainSecsOverride is every sustain-bearing rule's own
// "sustain_secs" threshold, compressed for fake-data mode -- the
// DefaultAlertRules(fast) counterpart for this schema (Task 10): a real
// box always seeds the true 90s/120s numbers (DefaultRuleConfigs(false)),
// but a demo session needs a rule to count as sustained within seconds,
// not minutes, to complete its whole scripted story in a few short
// minutes of wall-clock time.
const fastSustainSecsOverride = 10

// DefaultRuleConfigs returns one store.InsightRuleConfig per compiled-in
// rule -- enabled, notify off (Global Constraints: no seeded rule may
// ever page by default) -- for main.go's boot-time SeedInsightRuleConfigs
// call, the exact store.DefaultAlertRules counterpart for this schema.
// UpdatedAt is left 0; SeedInsightRuleConfigs stamps it at insert time,
// matching DefaultAlertRules' own convention. Lives here, not in package
// store, because the rule ID list's one authoritative source is this
// compiled-in library (librarySpecs) -- duplicating those seven strings
// into a second, store-side list would be exactly the kind of
// hand-maintained copy this phase's own review keeps flagging.
//
// fast, true only in fake-data mode, overrides every rule's own
// "sustain_secs" threshold (the ones that have one -- disk-spinup-churn
// doesn't) down to fastSustainSecsOverride, mirroring
// DefaultAlertRules(fast)'s identical compression of for_seconds/
// clear_seconds. This is SeedInsightRuleConfigs' own insert-or-ignore
// contract: it only ever takes effect on the very first boot of a fresh
// database, exactly like the alert rules' own fast seed.
func DefaultRuleConfigs(fast bool) []store.InsightRuleConfig {
	out := make([]store.InsightRuleConfig, len(librarySpecs))
	for i, spec := range librarySpecs {
		c := store.InsightRuleConfig{RuleID: spec.id, Enabled: true, Notify: false}
		if fast {
			if _, hasSustain := spec.defaults["sustain_secs"]; hasSustain {
				if b, err := json.Marshal(map[string]float64{"sustain_secs": fastSustainSecsOverride}); err == nil {
					c.Overrides = string(b)
				}
			}
		}
		out[i] = c
	}
	return out
}

// --- shared helpers used by more than one rule's Eval -------------------

// splitPrefixedMetric splits a metric of the shape "<prefix><middle>.<suffix>"
// into middle and suffix, where middle itself never contains a dot --
// device names (wholeDeviceRe) and GPU engine names are both dot-free by
// construction, so a plain Cut on the remainder after prefix is safe and
// exact. ok is false when metric doesn't start with prefix, or has no
// further dot to cut on.
func splitPrefixedMetric(prefix, metric string) (middle, suffix string, ok bool) {
	rest, hasPrefix := strings.CutPrefix(metric, prefix)
	if !hasPrefix {
		return "", "", false
	}
	middle, suffix, ok = strings.Cut(rest, ".")
	return middle, suffix, ok
}

func latestVal(samples []store.Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	return samples[len(samples)-1].Val
}

func valuesOf(samples []store.Sample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.Val
	}
	return out
}

// splitWindow divides samples (TS-ascending) at cutoff: older holds
// everything before it, recent everything at or after -- Baseline's own
// history/opening split for a rolling-median comparison against the
// current evidence window.
func splitWindow(samples []store.Sample, cutoff int64) (older, recent []store.Sample) {
	idx := len(samples)
	for i, s := range samples {
		if s.TS >= cutoff {
			idx = i
			break
		}
	}
	return samples[:idx], samples[idx:]
}

// otherEntities returns every key of all not present in exclude,
// sorted -- the "who else is here" complement Dominant's own culprit set
// needs for both the co-tenancy witness list (likely-tier "X and Y are
// also reading from disk3") and PSI-upgrade victim candidates.
func otherEntities(all map[string][]store.Sample, exclude []string) []string {
	ex := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		ex[n] = true
	}
	out := make([]string, 0, len(all))
	for n := range all {
		if !ex[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// bestSustainedVictim finds the highest-latest-value entity among
// candidates whose series in res has sustained past floor for
// sustainSecs (the caller's own "sustain_secs" threshold, so a
// PSI-upgrade victim check compresses the same way its rule's tier-1
// check does) -- the confirmed-tier "which specific entity is actually
// measured stalled" step every PSI (or hard-metric) upgrade path uses.
// ok is false when none of candidates clears the floor.
func bestSustainedVictim(res MatchResult, candidates []string, now, sustainSecs int64, floor float64) (victim string, value float64, ok bool) {
	best := -1.0
	for _, c := range candidates {
		v, latest := Sustained(Window{To: now, Samples: res.Samples[c]}, Above, floor, sustainSecs, res.Oldest[c])
		if v == VerdictBreaching && latest > best {
			best, victim, ok = latest, c, true
		}
	}
	return victim, best, ok
}

// combineReadWrite sums read_bps and write_bps samples sharing a
// timestamp into one bytes/sec series: a culprit's true IO load on a
// device is read+write together, not either alone, and Share's own
// window-mean math needs one series per entity, not two.
func combineReadWrite(read, write []store.Sample) []store.Sample {
	byTS := make(map[int64]float64, len(read)+len(write))
	for _, s := range read {
		byTS[s.TS] += s.Val
	}
	for _, s := range write {
		byTS[s.TS] += s.Val
	}
	out := make([]store.Sample, 0, len(byTS))
	for ts, v := range byTS {
		out = append(out, store.Sample{TS: ts, Val: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// hostDevice is one canonical device's resolved identity plus which raw
// diskio.<RawName>.* series to actually read samples from.
type hostDevice struct {
	Device  Device
	RawName string
}

// hostDiskDevices reduces a tick's raw diskio.<dev>.<suffix> samples
// (keyed by whatever name /proc/diskstats reports -- which, for a
// protected array's data disks, includes BOTH the raw member, e.g. sdc,
// and its md-level aggregate, e.g. md1) to one candidate per REAL
// resource, keyed by canonical device name. A data disk's own md-level
// row is preferred over its raw member's when both are present in the
// same tick: "disk3" is one resource, reported once, not twice from two
// different vantage points (see canonicalDevice's own doc for why the
// join happens at all). Every other role has no such duplication
// (Canonical is a no-op for it), so it passes through keyed by its own
// name with no preference to apply.
func hostDiskDevices(topo *Topology, metrics map[string][]store.Sample) map[string]hostDevice {
	rawSet := map[string]bool{}
	for metric := range metrics {
		if dev, _, ok := splitPrefixedMetric("diskio.", metric); ok {
			rawSet[dev] = true
		}
	}
	rawNames := make([]string, 0, len(rawSet))
	for dev := range rawSet {
		rawNames = append(rawNames, dev)
	}
	sort.Strings(rawNames)

	out := make(map[string]hostDevice, len(rawNames))
	for _, dev := range rawNames {
		d := canonicalDevice(topo, dev)
		existing, ok := out[d.Name]
		if !ok || (dev == d.Name && existing.RawName != d.Name) {
			out[d.Name] = hostDevice{Device: d, RawName: dev}
		}
	}
	return out
}

// containerDeviceIO returns, for every container with live:io.* activity
// on the given canonical device (partitions folded, raw members
// canonicalized -- see canonicalDevice), its combined read+write bytes/
// sec series -- Share's own per-entity input for a disk-io-contention
// candidate device.
func containerDeviceIO(topo *Topology, byContainer map[string]map[string][]store.Sample, canonicalName string) map[string][]store.Sample {
	parts := map[string][]store.Sample{}
	for container, metrics := range byContainer {
		var reads, writes []store.Sample
		for metric, samples := range metrics {
			dev, suffix, ok := splitPrefixedMetric("live:io.", metric)
			if !ok || canonicalDevice(topo, dev).Name != canonicalName {
				continue
			}
			switch suffix {
			case "read_bps":
				reads = append(reads, samples...)
			case "write_bps":
				writes = append(writes, samples...)
			}
		}
		if len(reads) == 0 && len(writes) == 0 {
			continue
		}
		parts[container] = combineReadWrite(reads, writes)
	}
	return parts
}

const windowMinutes = int(EvidenceWindowSecs / 60)

// --- disk-io-contention ---------------------------------------------------

// evalDiskIOContention: victim evidence is the DEVICE's own measured
// saturation (util_pct sustained, await_ms sustained past a rolling
// multiple of its own baseline) -- never inferred from "someone is doing
// a lot of IO" -- with a PSI upgrade naming the actual stalled sibling
// container when psi=1 is live. Culprit attribution is Dominant() over
// every OTHER container's own IO share of the SAME canonical device,
// which is also where the co-tenancy requirement lives: a lone user of a
// saturated device is just busy, not evidence of contention, so this
// never fires with fewer than two containers present on the device.
// Topology's Contended gate excludes parity devices outright (Open
// question 1): a parity row never even becomes a hostDiskDevices
// candidate resource in its own right.
func evalDiskIOContention(in In, th map[string]float64) []Finding {
	metrics := in.HostDiskIO.Samples[""]
	if len(metrics) == 0 || in.Topology == nil {
		return nil
	}
	oldestFor := func(metric string) int64 { return in.HostDiskIO.Oldest[""][metric] }

	var findings []Finding
	for canonicalName, hd := range hostDiskDevices(in.Topology, metrics) {
		if !in.Topology.Contended(hd.Device) {
			continue // parity: folded into the array-write finding, never named itself
		}

		utilMetric := "diskio." + hd.RawName + ".util_pct"
		utilVerdict, utilLatest := Sustained(Window{To: in.Now, Samples: metrics[utilMetric]}, Above, th["util_pct_floor"], int64(th["sustain_secs"]), oldestFor(utilMetric))
		if utilVerdict != VerdictBreaching {
			continue
		}

		awaitMetric := "diskio." + hd.RawName + ".await_ms"
		awaitSamples := metrics[awaitMetric]
		older, recent := splitWindow(awaitSamples, in.Now-EvidenceWindowSecs)
		baseline, haveBaseline := Baseline(valuesOf(older), valuesOf(recent))
		if !haveBaseline {
			continue
		}
		awaitVerdict, awaitLatest := Sustained(Window{To: in.Now, Samples: awaitSamples}, Above, baseline*th["await_multiplier"], int64(th["sustain_secs"]), oldestFor(awaitMetric))
		if awaitVerdict != VerdictBreaching {
			continue
		}

		parts := containerDeviceIO(in.Topology, in.ContainerLiveIO.Samples, canonicalName)
		if len(parts) < 2 {
			continue // co-tenancy requirement: a lone user isn't contention
		}
		ranked, _ := Share(parts)
		culprits, ok := Dominant(ranked, th["culprit_share_floor_pct"]/100, 3)
		if !ok {
			continue
		}
		others := otherEntities(parts, culprits.Names)

		f := Finding{
			RuleID: RuleDiskIOContention, VictimKind: "disk", Resource: resourceLabel(hd.Device),
			Culprit: culprits, Confidence: ConfidenceLikely, Tier: TierProxy, Shape: ShapeSlowing, Severity: "warning",
			Evidence: Evidence{
				CulpritSharePct: culprits.Fraction * 100, DeviceUtilPct: utilLatest, AwaitMs: awaitLatest, OtherUsers: others,
			},
		}
		if in.Tier == "psi" {
			if victim, stall, ok := bestSustainedVictim(in.ContainerPSI["psi.io.some_pct"], others, in.Now, int64(th["sustain_secs"]), th["psi_stall_floor"]); ok {
				f.VictimKind, f.Victim = "container", victim
				f.Confidence, f.Tier = ConfidenceConfirmed, TierPSI
				f.Evidence.VictimStallPct, f.Evidence.WindowMinutes = stall, windowMinutes
			}
		}
		findings = append(findings, f)
	}
	return findings
}

// --- io-driven-cpu-load ----------------------------------------------------

// evalIODrivenCPULoad is Scott's founding example (plan intro): the
// host's own iowait_pct (tier 1) or psi.io stall (PSI upgrade) is the
// victim evidence; the culprit is whichever container(s) dominate TOTAL
// host disk IO (every canonical device summed, not per-device), since
// this rule is about the CPU-wide consequence of storage load, not any
// one device.
func evalIODrivenCPULoad(in In, th map[string]float64) []Finding {
	iowaitVerdict, iowaitLatest := Sustained(Window{To: in.Now, Samples: in.HostCPUIowait.Samples[""]}, Above, th["iowait_pct_floor"], int64(th["sustain_secs"]), in.HostCPUIowait.Oldest[""])
	if iowaitVerdict != VerdictBreaching {
		return nil
	}

	parts := totalContainerIO(in)
	if len(parts) == 0 {
		return nil
	}
	ranked, _ := Share(parts)
	culprits, ok := Dominant(ranked, th["culprit_share_floor_pct"]/100, 3)
	if !ok {
		return nil
	}

	f := Finding{
		RuleID: RuleIODrivenCPULoad, VictimKind: "host", Culprit: culprits, Resource: "cpu",
		Confidence: ConfidenceLikely, Tier: TierProxy, Shape: ShapeLoading, Severity: "warning",
		Evidence: Evidence{CulpritSharePct: culprits.Fraction * 100, IowaitPct: iowaitLatest},
	}
	if in.Tier == "psi" {
		someV, someLatest := Sustained(Window{To: in.Now, Samples: in.HostPSI["psi.io.some_pct"].Samples[""]}, Above, th["psi_io_some_floor"], int64(th["sustain_secs"]), in.HostPSI["psi.io.some_pct"].Oldest[""])
		if someV == VerdictBreaching {
			f.Confidence, f.Tier = ConfidenceConfirmed, TierPSI
			f.Evidence.VictimStallPct, f.Evidence.WindowMinutes = someLatest, windowMinutes
			fullV, _ := Sustained(Window{To: in.Now, Samples: in.HostPSI["psi.io.full_pct"].Samples[""]}, Above, th["psi_io_full_floor"], int64(th["sustain_secs"]), in.HostPSI["psi.io.full_pct"].Oldest[""])
			if fullV == VerdictBreaching {
				f.Severity = "alert"
			}
		}
	}
	return []Finding{f}
}

// totalContainerIO sums each container's live:io.* across EVERY
// canonical device into one series -- io-driven-cpu-load's own
// denominator is total host disk IO, not any one device's.
func totalContainerIO(in In) map[string][]store.Sample {
	byContainerTS := map[string]map[int64]float64{}
	for container, metrics := range in.ContainerLiveIO.Samples {
		for metric, samples := range metrics {
			_, suffix, ok := splitPrefixedMetric("live:io.", metric)
			if !ok || (suffix != "read_bps" && suffix != "write_bps") {
				continue
			}
			if byContainerTS[container] == nil {
				byContainerTS[container] = map[int64]float64{}
			}
			for _, s := range samples {
				byContainerTS[container][s.TS] += s.Val
			}
		}
	}
	out := make(map[string][]store.Sample, len(byContainerTS))
	for container, byTS := range byContainerTS {
		samples := make([]store.Sample, 0, len(byTS))
		for ts, v := range byTS {
			samples = append(samples, store.Sample{TS: ts, Val: v})
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i].TS < samples[j].TS })
		out[container] = samples
	}
	return out
}

// --- cpu-starvation ---------------------------------------------------

// evalCPUStarvation: tier-1 victim evidence (cpu.throttled_pct) only
// applies to a container with an actual CPU limit (cpu.alloc_cores > 0)
// -- the Deviations table's own honest gate, since throttling is
// structurally zero for an unlimited container regardless of contention.
// The PSI upgrade lifts that gate entirely: psi.cpu.some_pct measures
// starvation for ANY container, limited or not.
func evalCPUStarvation(in In, th map[string]float64) []Finding {
	var findings []Finding
	for victim, throttled := range in.ContainerCPUThrottled.Samples {
		if latestVal(in.ContainerCPUAllocCores.Samples[victim]) <= 0 {
			continue // no CPU limit: throttled_pct is structurally zero, not evidence either way
		}
		verdict, latest := Sustained(Window{To: in.Now, Samples: throttled}, Above, th["throttled_pct_floor"], int64(th["sustain_secs"]), in.ContainerCPUThrottled.Oldest[victim])
		if verdict != VerdictBreaching {
			continue
		}
		if f, ok := cpuStarvationFinding(in, th, victim, ConfidenceLikely, latest); ok {
			findings = append(findings, f)
		}
	}

	if in.Tier == "psi" {
		for victim, some := range in.ContainerPSI["psi.cpu.some_pct"].Samples {
			verdict, latest := Sustained(Window{To: in.Now, Samples: some}, Above, th["psi_cpu_some_floor"], int64(th["sustain_secs"]), in.ContainerPSI["psi.cpu.some_pct"].Oldest[victim])
			if verdict != VerdictBreaching {
				continue
			}
			if alreadyFound(findings, victim) {
				continue
			}
			if f, ok := cpuStarvationFinding(in, th, victim, ConfidenceConfirmed, latest); ok {
				findings = append(findings, f)
			}
		}
	}
	return findings
}

func alreadyFound(findings []Finding, victim string) bool {
	for _, f := range findings {
		if f.Victim == victim {
			return true
		}
	}
	return false
}

// cpuStarvationFinding shares the culprit-attribution step (a DIFFERENT
// container holding a large CPU share while the host itself runs hot)
// between the tier-1 and PSI-upgrade paths above.
func cpuStarvationFinding(in In, th map[string]float64, victim string, confidence Confidence, victimValue float64) (Finding, bool) {
	hostTotal := latestVal(in.HostCPUTotal.Samples[""])
	if hostTotal < th["host_cpu_total_floor"] {
		return Finding{}, false
	}
	parts := map[string][]store.Sample{}
	for c, samples := range in.ContainerCPUPct.Samples {
		if c != victim {
			parts[c] = samples
		}
	}
	// maxN 1: cpu-starvation names a single dominant culprit, never a
	// shared set -- the plan's own table has no joint-culprit language
	// for this rule the way disk-io-contention's Open-question-2 example
	// does, so a leading SET is deliberately not attempted here.
	ranked, _ := Share(parts)
	culprits, ok := Dominant(ranked, th["culprit_cpu_pct_floor"]/100, 1)
	if !ok {
		return Finding{}, false
	}

	tier := TierProxy
	if confidence == ConfidenceConfirmed {
		tier = TierPSI
	}
	return Finding{
		RuleID: RuleCPUStarvation, VictimKind: "container", Victim: victim, Culprit: culprits, Resource: "cpu",
		Confidence: confidence, Tier: tier, Shape: ShapeSlowing, Severity: "warning",
		Evidence: Evidence{
			CulpritSharePct: culprits.Fraction * 100, HostCPUPct: hostTotal, VictimStallPct: victimValue, WindowMinutes: windowMinutes,
		},
	}, true
}

// --- parity-slowdown ---------------------------------------------------

// evalParitySlowdown is tier-1 native (no PSI signal applies to array
// throughput): the victim evidence is the parity check's OWN speed
// against Baseline, sustained for this rule's own 120s (Task 6's table;
// longer than the shared 90s default, hence its own "sustain_secs"
// threshold rather than the package constant). Culprit attribution is
// Dominant() over containers' IO share of the array's DATA devices
// (Contended, RoleData) -- parity itself is never a culprit-attribution
// target, matching Contended's own doc.
func evalParitySlowdown(in In, th map[string]float64) []Finding {
	speedSamples := in.ParitySpeedBps.Samples["array"]
	older, recent := splitWindow(speedSamples, in.Now-EvidenceWindowSecs)
	baseline, ok := Baseline(valuesOf(older), valuesOf(recent))
	if !ok || baseline <= 0 {
		return nil
	}
	verdict, latest := Sustained(Window{To: in.Now, Samples: speedSamples}, Below, baseline*th["speed_floor_fraction_of_baseline"], int64(th["sustain_secs"]), in.ParitySpeedBps.Oldest["array"])
	if verdict != VerdictBreaching {
		return nil
	}

	parts := map[string][]store.Sample{}
	for container, metrics := range in.ContainerLiveIO.Samples {
		var reads, writes []store.Sample
		for metric, samples := range metrics {
			dev, suffix, ok := splitPrefixedMetric("live:io.", metric)
			if !ok {
				continue
			}
			d := canonicalDevice(in.Topology, dev)
			if d.Role != RoleData {
				continue
			}
			switch suffix {
			case "read_bps":
				reads = append(reads, samples...)
			case "write_bps":
				writes = append(writes, samples...)
			}
		}
		if len(reads) > 0 || len(writes) > 0 {
			parts[container] = combineReadWrite(reads, writes)
		}
	}
	ranked, _ := Share(parts)
	culprits, ok := Dominant(ranked, th["culprit_share_floor_pct"]/100, 3)
	if !ok {
		return nil
	}

	return []Finding{{
		RuleID: RuleParitySlowdown, VictimKind: "array", Victim: "", Culprit: culprits, Resource: "parity",
		Confidence: ConfidenceLikely, Tier: TierProxy, Shape: ShapeSlowing, Severity: "warning",
		Evidence: Evidence{CulpritSharePct: culprits.Fraction * 100, BaselinePct: 100 * latest / baseline},
	}}
}

// --- disk-spinup-churn ---------------------------------------------------

// evalDiskSpinupChurn counts 0->1 transitions of disk/<slot>/spun_up
// over the rule's own 60-minute window (never the shared 120s evidence
// window -- churn is a slow, cumulative signal, not a short spike) on a
// KNOWN-rotational disk (RotationalKnown gates this: an unplaced disk
// must never read as "spins up" just because a map lookup defaulted
// false -- see Device.RotationalKnown's own doc), then attributes to
// whichever container dominated that disk's live:io.* in the 60s
// surrounding each transition (Task 6's table).
func evalDiskSpinupChurn(in In, th map[string]float64) []Finding {
	var findings []Finding
	for slot, samples := range in.DiskSpunUp.Samples {
		dev, ok := in.Topology.ResolveSlot(slot)
		if !ok || !dev.RotationalKnown || !dev.Rotational {
			continue
		}
		// Canonicalize before using dev.Name as a join key: ResolveSlot
		// (unlike canonicalDevice, which every container-side lookup
		// below goes through) returns the RAW device, and a data slot's
		// raw name and its md alias must resolve to the identical join
		// key or containerDeviceIO's own canonicalization on the
		// culprit side can never match it.
		dev = in.Topology.Canonical(dev)
		transitions := countRisingEdges(samples)
		if len(transitions) < int(th["min_transitions"]) {
			continue
		}

		attribWindow := int64(th["attribution_window_secs"])
		parts := containerDeviceIO(in.Topology, in.ContainerLiveIO.Samples, dev.Name)
		culpritTotals := map[string]float64{}
		for _, t := range transitions {
			for container, cSamples := range parts {
				culpritTotals[container] += sumInWindow(cSamples, t-attribWindow, t+attribWindow)
			}
		}
		culprit, ok := topCulprit(culpritTotals)
		if !ok {
			continue
		}

		findings = append(findings, Finding{
			RuleID: RuleDiskSpinupChurn, VictimKind: "disk", Victim: "", Resource: resourceLabel(dev),
			Culprit:    Culprits{Names: []string{culprit}, Fraction: 1, Shared: false},
			Confidence: ConfidenceLikely, Tier: TierProxy, Shape: ShapeKeepingAwake, Severity: "info",
			Evidence: Evidence{SpinCount: len(transitions), SpinWindowMinutes: int(th["window_minutes"])},
		})
	}
	return findings
}

// countRisingEdges returns the timestamp of every 0->1 transition in a
// 0/1 gauge series (spun_up), TS-ascending.
func countRisingEdges(samples []store.Sample) []int64 {
	var out []int64
	prev := -1.0
	for _, s := range samples {
		if prev == 0 && s.Val != 0 {
			out = append(out, s.TS)
		}
		prev = s.Val
	}
	return out
}

func sumInWindow(samples []store.Sample, from, to int64) float64 {
	sum := 0.0
	for _, s := range samples {
		if s.TS >= from && s.TS <= to {
			sum += s.Val
		}
	}
	return sum
}

func topCulprit(totals map[string]float64) (string, bool) {
	best, bestName := 0.0, ""
	names := make([]string, 0, len(totals))
	for n := range totals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if totals[n] > best {
			best, bestName = totals[n], n
		}
	}
	return bestName, bestName != ""
}

// --- gpu-engine-contention ---------------------------------------------------

// evalGPUEngineContention: victim evidence is the GPU engine's own
// busy_pct sustained past floor; culprit attribution requires at least
// min_culprits containers each independently clearing their own share
// floor on that engine (the rule's own co-tenancy shape: a single
// container driving a busy engine alone is just a busy container, same
// principle as disk-io-contention's device co-tenancy gate).
func evalGPUEngineContention(in In, th map[string]float64) []Finding {
	var findings []Finding
	for gpuEntity, metrics := range in.GPUEngine.Samples {
		for metric, samples := range metrics {
			eng, suffix, ok := splitPrefixedMetric("engine.", metric)
			if !ok || suffix != "busy_pct" {
				continue
			}
			verdict, latest := Sustained(Window{To: in.Now, Samples: samples}, Above, th["engine_busy_floor"], int64(th["sustain_secs"]), in.GPUEngine.Oldest[gpuEntity][metric])
			if verdict != VerdictBreaching {
				continue
			}

			parts := map[string][]store.Sample{}
			for container, cMetrics := range in.ContainerGPU.Samples {
				for cMetric, cSamples := range cMetrics {
					cEng, cSuffix, ok := splitPrefixedMetric("gpu.", cMetric)
					if ok && cSuffix == "busy_pct" && cEng == eng {
						parts[container] = cSamples
					}
				}
			}
			ranked, _ := Share(parts)
			qualifying := make([]EntityShare, 0, len(ranked))
			for _, r := range ranked {
				if r.Value >= th["culprit_share_floor_pct"] {
					qualifying = append(qualifying, r)
				}
			}
			if len(qualifying) < int(th["min_culprits"]) {
				continue
			}
			names := make([]string, len(qualifying))
			combined := 0.0
			for i, r := range qualifying {
				names[i] = r.Entity
				combined += r.Fraction
			}

			findings = append(findings, Finding{
				RuleID: RuleGPUEngineContention, VictimKind: "gpu", Victim: eng,
				Resource:   "gpu:" + eng,
				Culprit:    Culprits{Names: names, Fraction: combined, Shared: len(names) > 1},
				Confidence: ConfidenceLikely, Tier: TierProxy, Shape: ShapeSlowing, Severity: "info",
				Evidence: Evidence{CulpritSharePct: combined * 100, EngineBusyPct: latest},
			})
		}
	}
	return findings
}

// --- memory-squeeze ---------------------------------------------------

// evalMemorySqueeze has two independent victim paths (Task 6's table):
// a container.oom hard event (always Confirmed + severity alert -- a
// kill is not a correlation, it is a fact) and the host-wide
// mem.used_pct threshold (tier 1) with a psi.mem.some_pct upgrade. Both
// share the same culprit attribution: Dominant() over every container's
// mem.pct.
func evalMemorySqueeze(in In, th map[string]float64) []Finding {
	var findings []Finding

	for _, ev := range in.OOMEvents {
		culprits, ok := memorySqueezeCulprits(in, th, ev.Entity)
		if !ok {
			continue
		}
		findings = append(findings, Finding{
			RuleID: RuleMemorySqueeze, VictimKind: "container", Victim: ev.Entity, Culprit: culprits, Resource: "memory",
			Confidence: ConfidenceConfirmed, Tier: TierProxy, Shape: ShapeSlowing, Severity: "alert",
			Evidence: Evidence{CulpritSharePct: culprits.Fraction * 100},
		})
	}

	hostVerdict, hostLatest := Sustained(Window{To: in.Now, Samples: in.HostMemUsedPct.Samples[""]}, Above, th["mem_used_pct_floor"], int64(th["sustain_secs"]), in.HostMemUsedPct.Oldest[""])
	if hostVerdict == VerdictBreaching {
		if culprits, ok := memorySqueezeCulprits(in, th, ""); ok {
			f := Finding{
				RuleID: RuleMemorySqueeze, VictimKind: "host", Culprit: culprits, Resource: "memory",
				Confidence: ConfidenceLikely, Tier: TierProxy, Shape: ShapeLoading, Severity: "warning",
				Evidence: Evidence{CulpritSharePct: culprits.Fraction * 100, VictimStallPct: hostLatest},
			}
			if in.Tier == "psi" {
				psiV, psiLatest := Sustained(Window{To: in.Now, Samples: in.HostPSI["psi.mem.some_pct"].Samples[""]}, Above, th["psi_mem_some_floor"], int64(th["sustain_secs"]), in.HostPSI["psi.mem.some_pct"].Oldest[""])
				if psiV == VerdictBreaching {
					f.Confidence, f.Tier = ConfidenceConfirmed, TierPSI
					f.Evidence.VictimStallPct, f.Evidence.WindowMinutes = psiLatest, windowMinutes
				}
			}
			findings = append(findings, f)
		}
	}
	return findings
}

func memorySqueezeCulprits(in In, th map[string]float64, excludeVictim string) (Culprits, bool) {
	parts := map[string][]store.Sample{}
	for c, samples := range in.ContainerMemPct.Samples {
		if c != excludeVictim {
			parts[c] = samples
		}
	}
	ranked, _ := Share(parts)
	return Dominant(ranked, th["culprit_mem_pct_floor"]/100, 3)
}

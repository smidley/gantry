package insight

import (
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

const testNow int64 = 1_000_000

// --- fixture builders ----------------------------------------------------

// seriesRange builds a flat (constant-value) series from fromTS to toTS
// inclusive, step apart, TS-ascending -- every rule test below uses
// generously-long, generously-covered windows so a fixture's intent
// (breaching/not, covered/not) is never accidentally confused with a
// coverage edge case Sustained/Baseline already have their own dedicated
// tests for (window_test.go, evidence_test.go).
func seriesRange(fromTS, toTS, step int64, val float64) []store.Sample {
	var out []store.Sample
	for ts := fromTS; ts <= toTS; ts += step {
		out = append(out, store.Sample{TS: ts, Val: val})
	}
	return out
}

// mkMatch builds a MatchResult, deriving Oldest from each entity's own
// first (earliest) sample -- the ring's-true-floor convention every
// fixture below wants "fully covered" for, unless a test explicitly
// overrides it to exercise a coverage edge (none do here; window_test.go
// and evidence_test.go already own that ground).
func mkMatch(entries map[string][]store.Sample) MatchResult {
	oldest := map[string]int64{}
	for e, s := range entries {
		if len(s) > 0 {
			oldest[e] = s[0].TS
		}
	}
	return MatchResult{Samples: entries, Oldest: oldest}
}

func mkPrefix(entries map[string]map[string][]store.Sample) PrefixResult {
	oldest := map[string]map[string]int64{}
	for e, byMetric := range entries {
		oldest[e] = map[string]int64{}
		for m, s := range byMetric {
			if len(s) > 0 {
				oldest[e][m] = s[0].TS
			}
		}
	}
	return PrefixResult{Samples: entries, Oldest: oldest}
}

// diskTopology is the shared Topology every disk-related rule test below
// resolves against: disk1/disk3 data members, a parity disk, and a cache
// pool -- enough of a real array shape to exercise Contended/Canonical
// without each test hand-rolling its own.
func diskTopology() *Topology {
	return NewTopology(nil, map[string]SlotMeta{
		"parity": {Device: "sdb", Rotational: true},
		"disk1":  {Device: "sdc", Rotational: true},
		"disk3":  {Device: "sde", Rotational: true},
		"disk5":  {Device: "sdg", Rotational: true},
		"cache":  {Device: "nvme0n1", Rotational: false},
	})
}

// --- disk-io-contention ---------------------------------------------------

func diskIOContentionIn(now int64, withVictim, withCulprit bool) In {
	in := In{Now: now, Topology: diskTopology(), Tier: "proxy"}

	util := seriesRange(now-100, now, 10, 50) // below the 90 floor by default
	older := seriesRange(now-700, now-130, 10, 5)
	recent := seriesRange(now-120, now, 10, 8) // below 2x baseline (10) by default
	if withVictim {
		util = seriesRange(now-100, now, 10, 97)
		recent = seriesRange(now-120, now, 10, 45) // >> 2x the 5ms baseline
	}
	await := append(append([]store.Sample{}, older...), recent...)
	in.HostDiskIO = mkPrefix(map[string]map[string][]store.Sample{
		"": {"diskio.sde.util_pct": util, "diskio.sde.await_ms": await},
	})

	live := map[string]map[string][]store.Sample{
		"qbittorrent": {"live:io.sde.read_bps": seriesRange(now-100, now, 10, 800)},
	}
	if withCulprit {
		live["jellyfin"] = map[string][]store.Sample{"live:io.sde.read_bps": seriesRange(now-100, now, 10, 100)}
		live["sonarr"] = map[string][]store.Sample{"live:io.sde.write_bps": seriesRange(now-100, now, 10, 100)}
	}
	in.ContainerLiveIO = mkPrefix(live)
	in.ContainerPSI = map[string]MatchResult{}
	return in
}

func TestEvalDiskIOContentionFiresWithBothSides(t *testing.T) {
	findings := evalDiskIOContention(diskIOContentionIn(testNow, true, true), librarySpecs[0].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, RuleDiskIOContention, f.RuleID)
	require.Equal(t, "disk3", f.Resource, "seam invariant 3: resource is Device.Slot, never the kernel name")
	require.Equal(t, Culprits{Names: []string{"qbittorrent"}, Fraction: 0.8, Shared: false}, f.Culprit)
	require.Equal(t, ConfidenceLikely, f.Confidence)
	require.ElementsMatch(t, []string{"jellyfin", "sonarr"}, f.Evidence.OtherUsers)
}

// TestEvalDiskIOContentionRequiresGenuineHistoryForBaselineNotSelfReferential
// pins I6 (review): with no await_ms samples OLDER than the evidence
// window itself (a cold-started device, or a ring that hasn't
// accumulated BaselineLookbackSecs yet), Baseline's own opening-samples
// fallback would use `recent` -- the SAME window Sustained is about to
// test -- as its own baseline. That's safe only by arithmetic accident
// with the default await_multiplier (2): here the window's first 30s
// sits at 10ms (pulling the window's own median down to 10) while the
// trailing 90s spikes to 45ms -- comfortably "sustained above 2x" a
// baseline computed from the very data being tested. A genuine cold
// start must report no verdict, not a baseline computed from the thing
// it's testing.
func TestEvalDiskIOContentionRequiresGenuineHistoryForBaselineNotSelfReferential(t *testing.T) {
	in := diskIOContentionIn(testNow, true, true)
	low := seriesRange(testNow-120, testNow-91, 1, 10)
	high := seriesRange(testNow-90, testNow, 30, 45)
	await := append(append([]store.Sample{}, low...), high...)
	in.HostDiskIO.Samples[""]["diskio.sde.await_ms"] = await
	in.HostDiskIO.Oldest[""]["diskio.sde.await_ms"] = await[0].TS

	findings := evalDiskIOContention(in, librarySpecs[0].defaults)

	require.Empty(t, findings, "no history older than the evidence window itself must yield no verdict, not a self-referential baseline")
}

func TestEvalDiskIOContentionDoesNotFireWithoutVictimEvidence(t *testing.T) {
	findings := evalDiskIOContention(diskIOContentionIn(testNow, false, true), librarySpecs[0].defaults)
	require.Empty(t, findings)
}

func TestEvalDiskIOContentionDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	// withCulprit=false leaves only qbittorrent on the device -- a lone
	// user of a saturated device is just busy, not contention (the
	// co-tenancy requirement), so this must not fire even though the
	// device itself is genuinely saturated (both victim conditions hold).
	findings := evalDiskIOContention(diskIOContentionIn(testNow, true, false), librarySpecs[0].defaults)
	require.Empty(t, findings)
}

func TestEvalDiskIOContentionNeverFiresOnParityDeviceSeamTopologyContendedGate(t *testing.T) {
	topo := diskTopology()
	in := In{
		Now: testNow, Topology: topo, Tier: "proxy",
		HostDiskIO: mkPrefix(map[string]map[string][]store.Sample{
			"": {
				"diskio.sdb.util_pct": seriesRange(testNow-100, testNow, 10, 99),
				"diskio.sdb.await_ms": seriesRange(testNow-700, testNow, 10, 90),
			},
		}),
		ContainerLiveIO: mkPrefix(map[string]map[string][]store.Sample{
			"qbittorrent": {"live:io.sdb.read_bps": seriesRange(testNow-100, testNow, 10, 800)},
			"jellyfin":    {"live:io.sdb.read_bps": seriesRange(testNow-100, testNow, 10, 100)},
		}),
	}

	findings := evalDiskIOContention(in, librarySpecs[0].defaults)

	require.Empty(t, findings, "a parity device must never be named as a contended resource in its own right")
}

func TestEvalDiskIOContentionPSIEvidenceUpgradesConfidenceAndVerb(t *testing.T) {
	in := diskIOContentionIn(testNow, true, true)
	in.Tier = "psi"
	in.ContainerPSI = map[string]MatchResult{
		"psi.io.some_pct": mkMatch(map[string][]store.Sample{
			"jellyfin": seriesRange(testNow-100, testNow, 10, 38),
		}),
	}

	findings := evalDiskIOContention(in, librarySpecs[0].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, ConfidenceConfirmed, f.Confidence)
	require.Equal(t, TierPSI, f.Tier)
	require.Equal(t, "jellyfin", f.Victim)
	require.Equal(t, 38.0, f.Evidence.VictimStallPct)
	require.Contains(t, Statement(f), "is starving", "the verb must change with the confidence upgrade")
	require.NotContains(t, Statement(f), "likely")
}

func TestRulesThresholdOverrideChangesTheFiringPoint(t *testing.T) {
	in := diskIOContentionIn(testNow, true, true)
	// Lower the device's util_pct so it clears 70 but not the 90 default.
	in.HostDiskIO.Samples[""]["diskio.sde.util_pct"] = seriesRange(testNow-100, testNow, 10, 80)

	require.Empty(t, DefaultRules()[0].Eval(in), "80%% util must not breach the default 90%% floor")

	overridden := Rules(map[string]map[string]float64{RuleDiskIOContention: {"util_pct_floor": 70}})
	require.NotEmpty(t, overridden[0].Eval(in), "the SAME 80%% util must breach an overridden 70%% floor")
}

// --- io-driven-cpu-load ----------------------------------------------------

func ioDrivenCPULoadIn(now int64, withVictim, withCulprit bool) In {
	iowait := seriesRange(now-100, now, 10, 5)
	if withVictim {
		iowait = seriesRange(now-100, now, 10, 24)
	}
	live := map[string]map[string][]store.Sample{}
	if withCulprit {
		live["sabnzbd"] = map[string][]store.Sample{"live:io.sde.read_bps": seriesRange(now-100, now, 10, 630)}
		live["jellyfin"] = map[string][]store.Sample{"live:io.sde.read_bps": seriesRange(now-100, now, 10, 370)}
	}
	return In{
		Now: now, Topology: diskTopology(), Tier: "proxy",
		HostCPUIowait:   mkMatch(map[string][]store.Sample{"": iowait}),
		ContainerLiveIO: mkPrefix(live),
		HostPSI:         map[string]MatchResult{},
	}
}

func TestEvalIODrivenCPULoadFiresWithBothSides(t *testing.T) {
	findings := evalIODrivenCPULoad(ioDrivenCPULoadIn(testNow, true, true), librarySpecs[1].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "host", f.VictimKind)
	require.Equal(t, "cpu", f.Resource)
	require.Equal(t, ConfidenceLikely, f.Confidence)
	require.Equal(t, []string{"sabnzbd"}, f.Culprit.Names)
}

func TestEvalIODrivenCPULoadDoesNotFireWithoutVictimEvidence(t *testing.T) {
	require.Empty(t, evalIODrivenCPULoad(ioDrivenCPULoadIn(testNow, false, true), librarySpecs[1].defaults))
}

func TestEvalIODrivenCPULoadDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalIODrivenCPULoad(ioDrivenCPULoadIn(testNow, true, false), librarySpecs[1].defaults))
}

func TestEvalIODrivenCPULoadPSIUpgradesConfidenceAndSeverity(t *testing.T) {
	in := ioDrivenCPULoadIn(testNow, true, true)
	in.Tier = "psi"
	in.HostPSI["psi.io.some_pct"] = mkMatch(map[string][]store.Sample{"": seriesRange(testNow-100, testNow, 10, 32)})
	in.HostPSI["psi.io.full_pct"] = mkMatch(map[string][]store.Sample{"": seriesRange(testNow-100, testNow, 10, 15)})

	findings := evalIODrivenCPULoad(in, librarySpecs[1].defaults)

	require.Len(t, findings, 1)
	require.Equal(t, ConfidenceConfirmed, findings[0].Confidence)
	require.Equal(t, "alert", findings[0].Severity, "full_pct clearing its own floor bumps severity to alert")
}

// --- cpu-starvation ---------------------------------------------------

func cpuStarvationIn(now int64, withVictim, withCulprit bool, allocCores float64) In {
	throttled := seriesRange(now-100, now, 10, 2)
	if withVictim {
		throttled = seriesRange(now-100, now, 10, 8)
	}
	cpuPct := map[string][]store.Sample{}
	if withCulprit {
		cpuPct["sabnzbd"] = seriesRange(now-100, now, 10, 46)
	}
	return In{
		Now: now, Topology: diskTopology(), Tier: "proxy",
		ContainerCPUThrottled:  mkMatch(map[string][]store.Sample{"minecraft": throttled}),
		ContainerCPUAllocCores: mkMatch(map[string][]store.Sample{"minecraft": {{TS: now, Val: allocCores}}}),
		HostCPUTotal:           mkMatch(map[string][]store.Sample{"": {{TS: now, Val: 91}}}),
		ContainerCPUPct:        mkMatch(cpuPct),
		ContainerPSI:           map[string]MatchResult{},
	}
}

func TestEvalCPUStarvationFiresWithBothSides(t *testing.T) {
	findings := evalCPUStarvation(cpuStarvationIn(testNow, true, true, 2), librarySpecs[2].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "minecraft", f.Victim)
	require.Equal(t, []string{"sabnzbd"}, f.Culprit.Names)
	require.Equal(t, ConfidenceLikely, f.Confidence)
}

func TestEvalCPUStarvationDoesNotFireWithoutVictimEvidence(t *testing.T) {
	require.Empty(t, evalCPUStarvation(cpuStarvationIn(testNow, false, true, 2), librarySpecs[2].defaults))
}

func TestEvalCPUStarvationDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalCPUStarvation(cpuStarvationIn(testNow, true, false, 2), librarySpecs[2].defaults))
}

// TestEvalCPUStarvationDoesNotFireForUnlimitedContainerEvenWithThrottlingPresent
// is the plan's own explicitly-required case: cpu.alloc_cores == 0 means
// throttled_pct is structurally zero on a real box (no limit, nothing to
// throttle against) -- a rule that fired here would be reacting to noise,
// not evidence, regardless of what the number happens to read.
func TestEvalCPUStarvationDoesNotFireForUnlimitedContainerEvenWithThrottlingPresent(t *testing.T) {
	in := cpuStarvationIn(testNow, true, true, 0)

	findings := evalCPUStarvation(in, librarySpecs[2].defaults)

	require.Empty(t, findings)
}

func TestEvalCPUStarvationPSIUpgradeNeedsNoAllocCores(t *testing.T) {
	in := cpuStarvationIn(testNow, false, true, 0) // no tier-1 victim evidence, no CPU limit at all
	in.Tier = "psi"
	in.ContainerPSI["psi.cpu.some_pct"] = mkMatch(map[string][]store.Sample{"minecraft": seriesRange(testNow-100, testNow, 10, 27)})

	findings := evalCPUStarvation(in, librarySpecs[2].defaults)

	require.Len(t, findings, 1, "PSI lifts the alloc_cores>0 gate entirely -- that is the whole argument for psi=1")
	require.Equal(t, ConfidenceConfirmed, findings[0].Confidence)
}

// TestEvalCPUStarvationPSIUpgradesAVictimTheTier1LoopAlreadyFound pins I1
// (review): a container with a real CPU limit that ALSO clears the
// tier-1 throttled_pct floor must still reach Confirmed when
// psi.cpu.some_pct breaches for that same victim -- alreadyFound exists
// to stop a duplicate Finding for one victim, not to freeze the first
// one at Likely forever. Before this fix the PSI loop simply skipped an
// already-found victim, so exactly the population psi=1 exists to serve
// (a genuinely CPU-limited, throttled container) could never reach
// confirmed.
func TestEvalCPUStarvationPSIUpgradesAVictimTheTier1LoopAlreadyFound(t *testing.T) {
	in := cpuStarvationIn(testNow, true, true, 2) // tier-1 already fires for "minecraft"
	in.Tier = "psi"
	in.ContainerPSI["psi.cpu.some_pct"] = mkMatch(map[string][]store.Sample{"minecraft": seriesRange(testNow-100, testNow, 10, 27)})

	findings := evalCPUStarvation(in, librarySpecs[2].defaults)

	require.Len(t, findings, 1, "one finding per victim, upgraded in place -- never a duplicate")
	f := findings[0]
	require.Equal(t, "minecraft", f.Victim)
	require.Equal(t, ConfidenceConfirmed, f.Confidence, "a both-signals victim must reach confirmed, not stay stuck at likely")
	require.Equal(t, TierPSI, f.Tier)
	require.Equal(t, 27.0, f.Evidence.VictimStallPct)
	require.Equal(t, windowMinutes, f.Evidence.WindowMinutes)
}

// --- parity-slowdown ---------------------------------------------------

func paritySlowdownIn(now int64, withVictim, withCulprit bool) In {
	older := seriesRange(now-700, now-130, 10, 130_000_000)
	recent := seriesRange(now-120, now, 10, 60_000_000) // well under 75% of 130M
	if !withVictim {
		recent = seriesRange(now-120, now, 10, 125_000_000) // >75% of baseline
	}
	speed := append(append([]store.Sample{}, older...), recent...)

	live := map[string]map[string][]store.Sample{}
	if withCulprit {
		live["qbittorrent"] = map[string][]store.Sample{"live:io.sdc.read_bps": seriesRange(now-100, now, 10, 500)}
	}
	return In{
		Now: now, Topology: diskTopology(), Tier: "proxy",
		ParitySpeedBps:  mkMatch(map[string][]store.Sample{"array": speed}),
		ContainerLiveIO: mkPrefix(live),
	}
}

func TestEvalParitySlowdownFiresWithBothSides(t *testing.T) {
	findings := evalParitySlowdown(paritySlowdownIn(testNow, true, true), librarySpecs[3].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "array", f.VictimKind)
	require.Equal(t, "parity", f.Resource)
	require.Equal(t, []string{"qbittorrent"}, f.Culprit.Names)
}

func TestEvalParitySlowdownDoesNotFireWithoutVictimEvidence(t *testing.T) {
	require.Empty(t, evalParitySlowdown(paritySlowdownIn(testNow, false, true), librarySpecs[3].defaults))
}

func TestEvalParitySlowdownDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalParitySlowdown(paritySlowdownIn(testNow, true, false), librarySpecs[3].defaults))
}

// --- disk-spinup-churn ---------------------------------------------------

func diskSpinupChurnIn(now int64, withVictim, withCulprit bool) In {
	transitions := 1
	if withVictim {
		transitions = 5
	}
	var spunUp []store.Sample
	val := 0.0
	for i := 0; i < transitions*2; i++ {
		spunUp = append(spunUp, store.Sample{TS: now - int64(3600-i*300), Val: val})
		val = 1 - val
	}

	live := map[string]map[string][]store.Sample{}
	if withCulprit {
		var plexIO []store.Sample
		for _, s := range spunUp {
			if s.Val == 1 { // a read right at each spin-up
				plexIO = append(plexIO, store.Sample{TS: s.TS, Val: 500})
			}
		}
		live["plex"] = map[string][]store.Sample{"live:io.sdg.read_bps": plexIO}
	}
	return In{
		Now: now, Topology: diskTopology(),
		DiskSpunUp:      mkMatch(map[string][]store.Sample{"disk5": spunUp}),
		ContainerLiveIO: mkPrefix(live),
	}
}

func TestEvalDiskSpinupChurnFiresWithBothSides(t *testing.T) {
	findings := evalDiskSpinupChurn(diskSpinupChurnIn(testNow, true, true), librarySpecs[4].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "disk5", f.Resource)
	require.Equal(t, []string{"plex"}, f.Culprit.Names)
	require.Equal(t, 5, f.Evidence.SpinCount)
}

func TestEvalDiskSpinupChurnDoesNotFireWithoutVictimEvidence(t *testing.T) {
	require.Empty(t, evalDiskSpinupChurn(diskSpinupChurnIn(testNow, false, true), librarySpecs[4].defaults))
}

func TestEvalDiskSpinupChurnDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalDiskSpinupChurn(diskSpinupChurnIn(testNow, true, false), librarySpecs[4].defaults))
}

// TestEvalDiskSpinupChurnGatesOnRotationalKnownSeamInvariant1 pins the
// rule's own use of seam invariant 1: an unplaced ("disk not currently in
// the array") slot must never be treated as a spinning disk just because
// a lookup happened to default false -- it must not fire, not fire as if
// solid-state.
func TestEvalDiskSpinupChurnGatesOnRotationalKnownSeamInvariant1(t *testing.T) {
	in := diskSpinupChurnIn(testNow, true, true)
	in.Topology = NewTopology(nil, nil) // no slots known at all -- ResolveSlot("disk5") now fails

	findings := evalDiskSpinupChurn(in, librarySpecs[4].defaults)

	require.Empty(t, findings)
}

// --- gpu-engine-contention ---------------------------------------------------

func gpuEngineContentionIn(now int64, withVictim, withCulprit bool) In {
	busy := seriesRange(now-100, now, 10, 50)
	if withVictim {
		busy = seriesRange(now-100, now, 10, 94)
	}
	containerGPU := map[string]map[string][]store.Sample{}
	if withCulprit {
		containerGPU["jellyfin"] = map[string][]store.Sample{"gpu.video.busy_pct": seriesRange(now-100, now, 10, 54)}
		containerGPU["frigate"] = map[string][]store.Sample{"gpu.video.busy_pct": seriesRange(now-100, now, 10, 39)}
	}
	return In{
		Now:          now,
		GPUEngine:    mkPrefix(map[string]map[string][]store.Sample{"gpu0": {"engine.video.busy_pct": busy}}),
		ContainerGPU: mkPrefix(containerGPU),
	}
}

func TestEvalGPUEngineContentionFiresWithBothSides(t *testing.T) {
	findings := evalGPUEngineContention(gpuEngineContentionIn(testNow, true, true), librarySpecs[5].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "gpu:video", f.Resource)
	require.ElementsMatch(t, []string{"jellyfin", "frigate"}, f.Culprit.Names)
	require.True(t, f.Culprit.Shared)
}

func TestEvalGPUEngineContentionDoesNotFireWithoutVictimEvidence(t *testing.T) {
	require.Empty(t, evalGPUEngineContention(gpuEngineContentionIn(testNow, false, true), librarySpecs[5].defaults))
}

func TestEvalGPUEngineContentionDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalGPUEngineContention(gpuEngineContentionIn(testNow, true, false), librarySpecs[5].defaults))
}

// TestEvalGPUEngineContentionRequiresMinCulpritsNotJustOneBusyContainer
// pins the rule's own co-tenancy shape: ONE container alone driving a
// busy engine is just a busy container, not contention between two.
func TestEvalGPUEngineContentionRequiresMinCulpritsNotJustOneBusyContainer(t *testing.T) {
	in := gpuEngineContentionIn(testNow, true, true)
	in.ContainerGPU = mkPrefix(map[string]map[string][]store.Sample{
		"jellyfin": {"gpu.video.busy_pct": seriesRange(testNow-100, testNow, 10, 93)},
	})

	findings := evalGPUEngineContention(in, librarySpecs[5].defaults)

	require.Empty(t, findings)
}

// --- memory-squeeze ---------------------------------------------------

func memorySqueezeOOMIn(now int64, withVictim, withCulprit bool) In {
	var events []store.Event
	if withVictim {
		events = []store.Event{{Kind: "container.oom", Entity: "minecraft", TS: now, Severity: "alert"}}
	}
	memPct := map[string][]store.Sample{}
	if withCulprit {
		memPct["redis"] = seriesRange(now-100, now, 10, 42)
	}
	return In{Now: now, OOMEvents: events, ContainerMemPct: mkMatch(memPct)}
}

func TestEvalMemorySqueezeOOMEventFiresWithBothSides(t *testing.T) {
	findings := evalMemorySqueeze(memorySqueezeOOMIn(testNow, true, true), librarySpecs[6].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "minecraft", f.Victim)
	require.Equal(t, "container", f.VictimKind)
	require.Equal(t, ConfidenceConfirmed, f.Confidence, "an OOM kill is a hard event, not a correlation")
	require.Equal(t, "alert", f.Severity)
	require.Equal(t, []string{"redis"}, f.Culprit.Names)
}

func TestEvalMemorySqueezeDoesNotFireWithoutOOMEvent(t *testing.T) {
	require.Empty(t, evalMemorySqueeze(memorySqueezeOOMIn(testNow, false, true), librarySpecs[6].defaults))
}

func TestEvalMemorySqueezeOOMDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalMemorySqueeze(memorySqueezeOOMIn(testNow, true, false), librarySpecs[6].defaults))
}

func memorySqueezeHostIn(now int64, withVictim, withCulprit bool) In {
	used := seriesRange(now-100, now, 10, 60)
	if withVictim {
		used = seriesRange(now-100, now, 10, 93)
	}
	memPct := map[string][]store.Sample{}
	if withCulprit {
		memPct["redis"] = seriesRange(now-100, now, 10, 35)
	}
	return In{
		Now: now, Tier: "proxy",
		HostMemUsedPct:  mkMatch(map[string][]store.Sample{"": used}),
		ContainerMemPct: mkMatch(memPct),
		HostPSI:         map[string]MatchResult{},
	}
}

func TestEvalMemorySqueezeHostThresholdFiresWithBothSides(t *testing.T) {
	findings := evalMemorySqueeze(memorySqueezeHostIn(testNow, true, true), librarySpecs[6].defaults)

	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, "host", f.VictimKind)
	require.Equal(t, ConfidenceLikely, f.Confidence)
	require.Equal(t, []string{"redis"}, f.Culprit.Names)
}

func TestEvalMemorySqueezeHostThresholdDoesNotFireWithoutVictimEvidence(t *testing.T) {
	require.Empty(t, evalMemorySqueeze(memorySqueezeHostIn(testNow, false, true), librarySpecs[6].defaults))
}

func TestEvalMemorySqueezeHostThresholdDoesNotFireWithoutCulpritAttribution(t *testing.T) {
	require.Empty(t, evalMemorySqueeze(memorySqueezeHostIn(testNow, true, false), librarySpecs[6].defaults))
}

func TestEvalMemorySqueezeHostPSIUpgrade(t *testing.T) {
	in := memorySqueezeHostIn(testNow, true, true)
	in.Tier = "psi"
	in.HostPSI["psi.mem.some_pct"] = mkMatch(map[string][]store.Sample{"": seriesRange(testNow-100, testNow, 10, 15)})

	findings := evalMemorySqueeze(in, librarySpecs[6].defaults)

	require.Len(t, findings, 1)
	require.Equal(t, ConfidenceConfirmed, findings[0].Confidence)
}

// --- seam invariant 5: one shared window-coverage helper ---------------

// TestBothOldestTSShapesGateCoverageThroughSustainedSeamInvariant5 pins
// the foundations review's own regression class (a Phase 4 F1 critical):
// Live.MatchSince's oldestTS is a flat map[entity]int64 (MatchResult),
// while Live.MatchPrefixSince's is nested, map[entity]map[metric]int64
// (PrefixResult) -- two different shapes carrying the SAME "can the ring
// prove this window is covered" fact. Both must gate through the
// identical Sustained coverage check with no second, hand-written
// "is this covered" implementation for the nested shape. Exercised
// through two real rules, one per shape: io-driven-cpu-load reads
// HostCPUIowait (a MatchResult), gpu-engine-contention reads GPUEngine
// (a PrefixResult) -- both fixtures otherwise fire cleanly (see their
// own "fires with both sides" tests); only the coverage floor changes.
func TestBothOldestTSShapesGateCoverageThroughSustainedSeamInvariant5(t *testing.T) {
	flatIn := ioDrivenCPULoadIn(testNow, true, true)
	flatIn.HostCPUIowait.Oldest[""] = testNow - 5 // 5s of proven history, far short of sustain_secs=90
	require.Empty(t, evalIODrivenCPULoad(flatIn, librarySpecs[1].defaults),
		"a flat MatchResult oldestTS must gate an uncovered window exactly like any other Sustained call")

	nestedIn := gpuEngineContentionIn(testNow, true, true)
	nestedIn.GPUEngine.Oldest["gpu0"]["engine.video.busy_pct"] = testNow - 5
	require.Empty(t, evalGPUEngineContention(nestedIn, librarySpecs[5].defaults),
		"a nested PrefixResult oldestTS must gate an uncovered window the SAME way -- no second, hand-written coverage check for this shape")
}

// --- Rules()/DefaultRules() plumbing --------------------------------

func TestDefaultRulesReturnsAllSevenWithCompiledInDefaults(t *testing.T) {
	rules := DefaultRules()

	require.Len(t, rules, 7)
	ids := make([]string, len(rules))
	for i, r := range rules {
		ids[i] = r.ID
	}
	require.ElementsMatch(t, []string{
		RuleDiskIOContention, RuleIODrivenCPULoad, RuleCPUStarvation, RuleParitySlowdown,
		RuleDiskSpinupChurn, RuleGPUEngineContention, RuleMemorySqueeze,
	}, ids)
}

// TestSustainSecsOverrideCompressesTheFiringWindow is the mechanism Task
// 10's fake-mode demo schedule needs: sustain_secs is a per-rule
// Threshold like any other, so the SAME override plumbing that changes a
// numeric floor also lets a much shorter breach count as "sustained" --
// no separate engine-level compression path is needed.
func TestSustainSecsOverrideCompressesTheFiringWindow(t *testing.T) {
	in := diskIOContentionIn(testNow, true, true)
	// Shrink the covered history to 15s -- far short of the DEFAULT 90s
	// sustain_secs, but comfortably past a 5s override.
	in.HostDiskIO.Samples[""]["diskio.sde.util_pct"] = seriesRange(testNow-15, testNow, 5, 97)
	in.HostDiskIO.Oldest[""]["diskio.sde.util_pct"] = testNow - 15
	older := seriesRange(testNow-700, testNow-130, 10, 5)
	recent := seriesRange(testNow-15, testNow, 5, 45)
	await := append(append([]store.Sample{}, older...), recent...)
	in.HostDiskIO.Samples[""]["diskio.sde.await_ms"] = await
	in.HostDiskIO.Oldest[""]["diskio.sde.await_ms"] = testNow - 700

	require.Empty(t, DefaultRules()[0].Eval(in), "15s of history cannot satisfy the default 90s sustain_secs")

	compressed := Rules(map[string]map[string]float64{RuleDiskIOContention: {"sustain_secs": 5}})
	require.NotEmpty(t, compressed[0].Eval(in), "the same 15s of history satisfies a 5s sustain_secs override")
}

func TestDefaultRuleConfigsSeedsAllSevenEnabledWithNotifyOff(t *testing.T) {
	configs := DefaultRuleConfigs(false)

	require.Len(t, configs, 7)
	for _, c := range configs {
		require.True(t, c.Enabled, "rule %s must default enabled", c.RuleID)
		require.False(t, c.Notify, "rule %s must default notify off -- Global Constraints: no seeded rule pages by default", c.RuleID)
		require.Empty(t, c.Overrides, "a real (non-fake) boot seeds no overrides -- the true 90s/120s numbers")
	}
	ids := make([]string, len(configs))
	for i, c := range configs {
		ids[i] = c.RuleID
	}
	require.ElementsMatch(t, []string{
		RuleDiskIOContention, RuleIODrivenCPULoad, RuleCPUStarvation, RuleParitySlowdown,
		RuleDiskSpinupChurn, RuleGPUEngineContention, RuleMemorySqueeze,
	}, ids)
}

// TestDefaultRuleConfigsFastCompressesSustainSecsOnEverySustainBearingRule
// pins Task 10's own fake-mode requirement: every rule that HAS a
// sustain_secs threshold gets it compressed; disk-spinup-churn (which has
// none) is left with no override at all rather than one that would be
// silently ignored.
func TestDefaultRuleConfigsFastCompressesSustainSecsOnEverySustainBearingRule(t *testing.T) {
	configs := DefaultRuleConfigs(true)

	byID := make(map[string]store.InsightRuleConfig, len(configs))
	for _, c := range configs {
		byID[c.RuleID] = c
	}

	for _, id := range []string{RuleDiskIOContention, RuleIODrivenCPULoad, RuleCPUStarvation, RuleParitySlowdown, RuleGPUEngineContention, RuleMemorySqueeze} {
		require.NotEmpty(t, byID[id].Overrides, "rule %s must carry a compressed sustain_secs override in fast mode", id)
		require.JSONEq(t, `{"sustain_secs": 10}`, byID[id].Overrides)
	}
	require.Empty(t, byID[RuleDiskSpinupChurn].Overrides, "disk-spinup-churn has no sustain_secs threshold to compress")

	// The override must actually take effect through the normal Rules()
	// merge path -- proving this isn't just a config row nothing reads.
	rules := Rules(map[string]map[string]float64{RuleDiskIOContention: {"sustain_secs": fastSustainSecsOverride}})
	for _, r := range rules {
		if r.ID == RuleDiskIOContention {
			require.Equal(t, float64(fastSustainSecsOverride), r.Thresholds["sustain_secs"])
		}
	}
}

func TestMergeThresholdsIgnoresUnknownOverrideKeys(t *testing.T) {
	defaults := map[string]float64{"a": 1, "b": 2}
	got := mergeThresholds(defaults, map[string]float64{"b": 20, "c": 30})

	require.Equal(t, map[string]float64{"a": 1, "b": 20}, got, "an override for a threshold the rule doesn't define is silently dropped, never smuggled in as a new knob")
}

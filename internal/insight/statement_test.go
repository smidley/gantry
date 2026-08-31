package insight

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// allShapes is the closed set Verb must answer safely for every entry --
// the property test below iterates it directly rather than hand-picking
// examples, so a future Shape addition fails loudly here if Verb isn't
// updated to cover it.
var allShapes = []Shape{ShapeSlowing, ShapeLoading, ShapeKeepingAwake}

// causalWords are the words ONLY ConfidenceConfirmed may ever produce
// (Global Constraints: "A likely finding that says 'causing' is a bug
// with a test").
var causalWords = []string{"starving", "causing", "forcing"}

// TestVerbNeverReturnsCausalVerbForConfidenceLikely is the plan's own
// required property test, over every Shape.
func TestVerbNeverReturnsCausalVerbForConfidenceLikely(t *testing.T) {
	for _, shape := range allShapes {
		v := Verb(ConfidenceLikely, shape)
		for _, word := range causalWords {
			require.NotContains(t, v, word, "shape %v likely verb %q must never contain a causal word", shape, v)
		}
	}
}

// TestVerbAlwaysReturnsACausalVerbForConfidenceConfirmed is the mirror
// property: Confirmed must always earn one of the causal words, over
// every Shape -- a Confirmed finding that reads exactly like a Likely
// one would silently discard the confidence upgrade's whole point.
func TestVerbAlwaysReturnsACausalVerbForConfidenceConfirmed(t *testing.T) {
	for _, shape := range allShapes {
		v := Verb(ConfidenceConfirmed, shape)
		found := false
		for _, word := range causalWords {
			if strings.Contains(v, word) {
				found = true
			}
		}
		require.True(t, found, "shape %v confirmed verb %q must contain a causal word", shape, v)
	}
}

func TestPluralizeIsRewritesLeadingIsToAre(t *testing.T) {
	require.Equal(t, "are likely slowing", pluralizeIs("is likely slowing"))
	require.Equal(t, "are starving", pluralizeIs("is starving"))
}

// --- Statement golden strings --------------------------------------------
//
// The disk-io-contention (both confidences), io-driven-cpu-load (likely),
// and disk-spinup-churn (likely) cases below are the plan's OWN verbatim
// examples (Task 6) -- copied exactly, not paraphrased. Every other case
// is this implementation's own golden text, pinned here as a regression
// guard the same way the mandated ones are; Verb's own property tests
// above are what actually enforce the likely/confirmed word choice.

func TestStatementDiskIOContentionLikelyMatchesPlanGolden(t *testing.T) {
	f := Finding{
		RuleID: RuleDiskIOContention, Resource: "disk3",
		Culprit:    Culprits{Names: []string{"qbittorrent"}, Fraction: 0.78},
		Confidence: ConfidenceLikely, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 78, DeviceUtilPct: 98, AwaitMs: 42, OtherUsers: []string{"jellyfin", "sonarr"}},
	}

	require.Equal(t,
		"qbittorrent is likely slowing other containers on disk3 — it's driving 78% of the disk's IO while the device sits at 98% utilisation and 42ms average latency. jellyfin and sonarr are also reading from disk3.",
		Statement(f))
}

func TestStatementDiskIOContentionConfirmedMatchesPlanGolden(t *testing.T) {
	f := Finding{
		RuleID: RuleDiskIOContention, Victim: "jellyfin", Resource: "disk3",
		Culprit:    Culprits{Names: []string{"qbittorrent"}, Fraction: 0.78},
		Confidence: ConfidenceConfirmed, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 78, VictimStallPct: 38, WindowMinutes: 10},
	}

	require.Equal(t,
		"qbittorrent is starving jellyfin on disk3 — jellyfin's IO was stalled 38% of the last 10 minutes while qbittorrent drove 78% of the disk's IO.",
		Statement(f))
}

// TestStatementDiskIOContentionSharedPairWithNoBystanderRendersHonestly
// pins I7 (review): when the shared culprit set IS every co-tenant the
// device has (a two-container device where together they clear the
// floor), Evidence.OtherUsers is correctly empty -- but the likely-tier
// template's "other containers" filler would then claim an unnamed third
// party is being slowed when there is none to name. The two culprits are
// slowing each other, not some bystander that doesn't exist.
func TestStatementDiskIOContentionSharedPairWithNoBystanderRendersHonestly(t *testing.T) {
	f := Finding{
		RuleID: RuleDiskIOContention, Resource: "disk3",
		Culprit:    Culprits{Names: []string{"qbittorrent", "sabnzbd"}, Fraction: 1.0, Shared: true},
		Confidence: ConfidenceLikely, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 100, DeviceUtilPct: 97, AwaitMs: 45},
	}

	s := Statement(f)

	require.NotContains(t, s, "other containers", "no bystander exists to name -- these two ARE the whole co-tenancy")
	require.Contains(t, s, "each other")
	require.Contains(t, s, "qbittorrent and sabnzbd")
}

func TestStatementIODrivenCPULoadLikelyMatchesPlanGolden(t *testing.T) {
	f := Finding{
		RuleID: RuleIODrivenCPULoad, VictimKind: "host", Resource: "cpu",
		Culprit:    Culprits{Names: []string{"sabnzbd"}, Fraction: 0.63},
		Confidence: ConfidenceLikely, Shape: ShapeLoading,
		Evidence: Evidence{CulpritSharePct: 63, IowaitPct: 24},
	}

	require.Equal(t,
		"sabnzbd's storage IO is loading the host CPU — it's driving 63% of all disk IO while the CPU spends 24% of its time waiting on IO.",
		Statement(f))
}

func TestStatementIODrivenCPULoadConfirmed(t *testing.T) {
	f := Finding{
		RuleID: RuleIODrivenCPULoad, VictimKind: "host", Resource: "cpu",
		Culprit:    Culprits{Names: []string{"sabnzbd"}, Fraction: 0.63},
		Confidence: ConfidenceConfirmed, Shape: ShapeLoading,
		Evidence: Evidence{CulpritSharePct: 63, VictimStallPct: 32, WindowMinutes: 10},
	}

	require.Equal(t,
		"sabnzbd's storage IO is causing host CPU starvation — the host was stalled on IO 32% of the last 10 minutes while sabnzbd drove 63% of all disk IO.",
		Statement(f))
}

func TestStatementDiskSpinupChurnMatchesPlanGolden(t *testing.T) {
	f := Finding{
		RuleID: RuleDiskSpinupChurn, Resource: "disk5",
		Culprit:    Culprits{Names: []string{"plex"}, Fraction: 1},
		Confidence: ConfidenceLikely, Shape: ShapeKeepingAwake,
		Evidence: Evidence{SpinCount: 5},
	}

	require.Equal(t,
		"plex is keeping disk5 awake — it has spun up 5 times in the last hour, each within a minute of plex reading from it.",
		Statement(f))
}

func TestStatementCPUStarvationLikely(t *testing.T) {
	f := Finding{
		RuleID: RuleCPUStarvation, Victim: "minecraft",
		Culprit:    Culprits{Names: []string{"sabnzbd"}, Fraction: 0.46},
		Confidence: ConfidenceLikely, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 46, VictimStallPct: 8, HostCPUPct: 91},
	}

	require.Equal(t,
		"sabnzbd is likely slowing minecraft's CPU — minecraft was throttled 8% of the window while sabnzbd holds 46% of host CPU and the host sits at 91% overall.",
		Statement(f))
}

func TestStatementCPUStarvationConfirmed(t *testing.T) {
	f := Finding{
		RuleID: RuleCPUStarvation, Victim: "minecraft",
		Culprit:    Culprits{Names: []string{"sabnzbd"}, Fraction: 0.46},
		Confidence: ConfidenceConfirmed, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 46, VictimStallPct: 27, WindowMinutes: 10},
	}

	require.Equal(t,
		"sabnzbd is starving minecraft of CPU — minecraft's CPU was stalled 27% of the last 10 minutes while sabnzbd holds 46% of host CPU.",
		Statement(f))
}

func TestStatementParitySlowdownLikely(t *testing.T) {
	f := Finding{
		RuleID: RuleParitySlowdown, Resource: "parity",
		Culprit:    Culprits{Names: []string{"qbittorrent"}, Fraction: 0.34},
		Confidence: ConfidenceLikely, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 34, BaselinePct: 58},
	}

	require.Equal(t,
		"qbittorrent is likely slowing the parity check — parity speed has dropped to 58% of its usual baseline while qbittorrent drives 34% of the array's data IO.",
		Statement(f))
}

// TestStatementParitySlowdownConfirmedNeverProducedByEngineButStillRenders
// covers Statement's own completeness for a shape/confidence pairing the
// engine never actually reaches (parity-slowdown is tier-1 native --
// Task 6's table has no PSI-upgrade column for it) but that Statement
// must still answer safely if ever called.
func TestStatementParitySlowdownConfirmedNeverProducedByEngineButStillRenders(t *testing.T) {
	f := Finding{
		RuleID: RuleParitySlowdown, Resource: "parity",
		Culprit:    Culprits{Names: []string{"qbittorrent"}, Fraction: 0.34},
		Confidence: ConfidenceConfirmed, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 34, BaselinePct: 58},
	}

	require.Equal(t,
		"qbittorrent is stalling the parity check — parity speed has dropped to 58% of its usual baseline while qbittorrent drives 34% of the array's data IO.",
		Statement(f))
}

func TestStatementGPUEngineContentionSharedCulprits(t *testing.T) {
	f := Finding{
		RuleID: RuleGPUEngineContention, Victim: "video", Resource: "gpu:video",
		Culprit:    Culprits{Names: []string{"jellyfin", "frigate"}, Fraction: 0.93, Shared: true},
		Confidence: ConfidenceLikely, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 93, EngineBusyPct: 94},
	}

	require.Equal(t,
		"jellyfin and frigate are likely slowing gpu:video at 94% busy — jellyfin and frigate together drive 93% of its usage.",
		Statement(f))
}

func TestStatementMemorySqueezeContainerConfirmedOOM(t *testing.T) {
	f := Finding{
		RuleID: RuleMemorySqueeze, VictimKind: "container", Victim: "minecraft", Resource: "memory",
		Culprit:    Culprits{Names: []string{"redis"}, Fraction: 0.42},
		Confidence: ConfidenceConfirmed, Shape: ShapeSlowing,
		Evidence: Evidence{CulpritSharePct: 42},
	}

	require.Equal(t,
		"redis is starving minecraft of memory — minecraft was OOM-killed while redis holds 42% of host memory.",
		Statement(f))
}

func TestStatementMemorySqueezeHostLikely(t *testing.T) {
	f := Finding{
		RuleID: RuleMemorySqueeze, VictimKind: "host", Resource: "memory",
		Culprit:    Culprits{Names: []string{"redis"}, Fraction: 0.35},
		Confidence: ConfidenceLikely, Shape: ShapeLoading,
		Evidence: Evidence{CulpritSharePct: 35, VictimStallPct: 93},
	}

	require.Equal(t,
		"redis is loading host memory — the host is at 93% memory used while redis holds 35% of it.",
		Statement(f))
}

func TestStatementMemorySqueezeHostConfirmed(t *testing.T) {
	f := Finding{
		RuleID: RuleMemorySqueeze, VictimKind: "host", Resource: "memory",
		Culprit:    Culprits{Names: []string{"redis"}, Fraction: 0.35},
		Confidence: ConfidenceConfirmed, Shape: ShapeLoading,
		Evidence: Evidence{CulpritSharePct: 35, VictimStallPct: 15, WindowMinutes: 10},
	}

	require.Equal(t,
		"redis is causing host memory pressure — the host was stalled on memory 15% of the last 10 minutes while redis holds 35% of host memory.",
		Statement(f))
}

func TestPctFormatsWholeNumbersWithoutDecimal(t *testing.T) {
	require.Equal(t, "78", pct(78))
	require.Equal(t, "78.5", pct(78.5))
}

func TestJoinAndFormatsListsGrammatically(t *testing.T) {
	require.Equal(t, "", joinAnd(nil))
	require.Equal(t, "a", joinAnd([]string{"a"}))
	require.Equal(t, "a and b", joinAnd([]string{"a", "b"}))
	require.Equal(t, "a, b, and c", joinAnd([]string{"a", "b", "c"}))
}

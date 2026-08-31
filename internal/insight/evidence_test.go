package insight

import (
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// --- Share -------------------------------------------------------------

// TestShareIsMeanBasedNotLatestSample pins the plan's own required
// property: a single-tick spike must not flip the ranking against a
// window of samples. "steady" holds a constant 40 across the whole
// window (mean 40); "spiky"'s latest sample (500) is the single highest
// value in the whole map, but its other 19 samples are all 5, so its
// window MEAN (29.75) stays below steady's -- ranking by the latest
// sample alone would wrongly put spiky first.
func TestShareIsMeanBasedNotLatestSample(t *testing.T) {
	steady := make([]store.Sample, 20)
	spiky := make([]store.Sample, 20)
	for i := range steady {
		steady[i] = store.Sample{TS: 100 + int64(i)*10, Val: 40}
		v := 5.0
		if i == len(spiky)-1 {
			v = 500 // one spike, on the LATEST sample
		}
		spiky[i] = store.Sample{TS: 100 + int64(i)*10, Val: v}
	}
	parts := map[string][]store.Sample{"steady": steady, "spiky": spiky}

	ranked, _ := Share(parts)

	require.Len(t, ranked, 2)
	require.Equal(t, "steady", ranked[0].Entity, "steady's window mean (40) must beat spiky's window mean (29.75) despite spiky's higher latest sample")
}

// TestShareRanksByMeanDescending is the plain positive case: two flat
// series, higher mean ranks first.
func TestShareRanksByMeanDescending(t *testing.T) {
	parts := map[string][]store.Sample{
		"low":  sampleSeries(100, 10, 10, 10, 10),
		"high": sampleSeries(100, 10, 90, 90, 90),
	}

	ranked, total := Share(parts)

	require.Equal(t, []string{"high", "low"}, []string{ranked[0].Entity, ranked[1].Entity})
	require.InDelta(t, 100.0, total, 0.001)
	require.InDelta(t, 0.9, ranked[0].Fraction, 0.001)
	require.InDelta(t, 0.1, ranked[1].Fraction, 0.001)
}

// TestShareTiesBrokenByNameForDeterminism pins the plan's own
// determinism requirement: equal fractions must not depend on Go's
// unordered map iteration.
func TestShareTiesBrokenByNameForDeterminism(t *testing.T) {
	parts := map[string][]store.Sample{
		"zeta":  sampleSeries(100, 10, 50, 50),
		"alpha": sampleSeries(100, 10, 50, 50),
		"mike":  sampleSeries(100, 10, 50, 50),
	}

	ranked, _ := Share(parts)

	require.Equal(t, []string{"alpha", "mike", "zeta"}, []string{ranked[0].Entity, ranked[1].Entity, ranked[2].Entity})
}

// TestShareEmptyEntitySeriesExcluded pins that an entity with zero
// samples in the window contributes nothing rather than a divide-by-zero
// NaN mean poisoning the ranking.
func TestShareEmptyEntitySeriesExcluded(t *testing.T) {
	parts := map[string][]store.Sample{
		"has-data": sampleSeries(100, 10, 10, 10),
		"no-data":  {},
	}

	ranked, total := Share(parts)

	require.Len(t, ranked, 1)
	require.Equal(t, "has-data", ranked[0].Entity)
	require.InDelta(t, 10.0, total, 0.001)
}

// TestShareEmptyInputYieldsEmptyNotNilPanic covers the fully-empty case:
// no parts at all.
func TestShareEmptyInputYieldsEmptyNotNilPanic(t *testing.T) {
	ranked, total := Share(nil)

	require.Empty(t, ranked)
	require.Equal(t, 0.0, total)
}

// --- Dominant ------------------------------------------------------------

func TestDominantSingleCulpritClearsFloor(t *testing.T) {
	ranked := []EntityShare{
		{Entity: "qbittorrent", Fraction: 0.62},
		{Entity: "jellyfin", Fraction: 0.20},
		{Entity: "sonarr", Fraction: 0.18},
	}

	got, ok := Dominant(ranked, 0.60, 3)

	require.True(t, ok)
	require.Equal(t, Culprits{Names: []string{"qbittorrent"}, Fraction: 0.62, Shared: false}, got)
}

func TestDominantSharedPairClearsFloorTogether(t *testing.T) {
	ranked := []EntityShare{
		{Entity: "qbittorrent", Fraction: 0.44},
		{Entity: "sabnzbd", Fraction: 0.31},
		{Entity: "plex", Fraction: 0.25},
	}

	got, ok := Dominant(ranked, 0.60, 3)

	require.True(t, ok)
	require.Equal(t, Culprits{Names: []string{"qbittorrent", "sabnzbd"}, Fraction: 0.75, Shared: true}, got)
}

func TestDominantNothingWhenNoLeadingSetClearsFloorWithinMaxN(t *testing.T) {
	ranked := []EntityShare{
		{Entity: "a", Fraction: 0.30},
		{Entity: "b", Fraction: 0.28},
		{Entity: "c", Fraction: 0.22},
	}

	_, ok := Dominant(ranked, 0.60, 2)

	require.False(t, ok, "a (30) then a+b (58) never reaches the 60 floor within maxN=2")
}

// TestDominantCappedAtMaxNEvenIfMoreWouldClearFloor pins the cap: a
// leading set is never grown past maxN even when doing so would clear
// the floor -- three culprits at 25 each (75 total) with maxN 2 must not
// silently become a three-way shared finding.
func TestDominantCappedAtMaxNEvenIfMoreWouldClearFloor(t *testing.T) {
	ranked := []EntityShare{
		{Entity: "a", Fraction: 0.25},
		{Entity: "b", Fraction: 0.25},
		{Entity: "c", Fraction: 0.25},
	}

	_, ok := Dominant(ranked, 0.60, 2)

	require.False(t, ok)
}

func TestDominantEmptyRankedReturnsFalse(t *testing.T) {
	_, ok := Dominant(nil, 0.60, 3)

	require.False(t, ok)
}

// --- Baseline ------------------------------------------------------------

func TestBaselineUsesMedianOfHistoryWhenPresent(t *testing.T) {
	history := []float64{100, 110, 90, 105, 95}

	got, ok := Baseline(history, []float64{1, 2, 3})

	require.True(t, ok)
	require.Equal(t, 100.0, got, "history is used over opening whenever it's non-empty")
}

func TestBaselineFallsBackToOpeningSamplesWhenHistoryEmpty(t *testing.T) {
	got, ok := Baseline(nil, []float64{80, 82, 78, 84})

	require.True(t, ok)
	require.Equal(t, 81.0, got, "median of an even-length slice is the mean of the two middle values")
}

func TestBaselineFalseWhenNeitherHistoryNorOpeningAvailable(t *testing.T) {
	_, ok := Baseline(nil, nil)

	require.False(t, ok)
}

// --- Seam invariant 2: partition fold --------------------------------

// TestFoldPartitionDeviceSeamInvariant2 pins the fold every disk-related
// culprit-attribution join depends on: docker's cgroup io.stat can name a
// PARTITION's major:minor (live:io.sda1.*, nvme0n1p2.*, mdNpM.*), while
// host diskio.* (parseDiskstats' wholeDeviceRe) is whole-device only.
// Without folding first, a container writing through sda1 would never
// join against the host's sda row at all.
func TestFoldPartitionDeviceSeamInvariant2(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"sda1", "sda"},
		{"sdb12", "sdb"},
		{"nvme0n1p2", "nvme0n1"},
		{"nvme1n2p10", "nvme1n2"},
		{"md1p1", "md1"},
		{"md10p2", "md10"},
		// Whole devices must pass through unchanged -- a partition-fold
		// that also mangled a whole device would break every OTHER join.
		{"sda", "sda"},
		{"nvme0n1", "nvme0n1"},
		{"md1", "md1"},
		// Unrecognized names (a loop device, anything else fake mode or a
		// future kernel might report) pass through unchanged rather than
		// erroring -- degrade, don't drop.
		{"loop2", "loop2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, foldPartitionDevice(tc.name))
		})
	}
}

// --- Seam invariant 3: resource column = Device.Slot ------------------

// TestResourceLabelSeamInvariant3PrefersSlotOverName pins the resource
// column contract: Device.Slot ("disk3", "cache") whenever it's known,
// falling back to Device.Name ONLY when Slot is empty (RoleUnknown) --
// never Name when a Slot exists, even though Name is always populated.
func TestResourceLabelSeamInvariant3PrefersSlotOverName(t *testing.T) {
	require.Equal(t, "disk3", resourceLabel(Device{Name: "md3", Slot: "disk3", Role: RoleData}))
	require.Equal(t, "cache", resourceLabel(Device{Name: "nvme0n1", Slot: "cache", Role: RolePool}))
	require.Equal(t, "nvme2n1", resourceLabel(Device{Name: "nvme2n1", Slot: "", Role: RoleUnknown}),
		"Name is the fallback ONLY when Slot is empty")
}

// --- Seam invariant 1: Topology entry is name-keyed, degrade don't drop --

// TestDeviceOrUnknownSeamInvariant1DegradesRatherThanDrops pins the
// engine-level entry point every rule uses: series are keyed by device
// NAME (host diskio.<name>.*, docker live:io.<slug(name)>.*), never
// major:minor, so lookups always go through ResolveName -- and when
// ResolveName reports no known slot, the caller still gets a real,
// Contended, RoleUnknown Device carrying just the raw name (mirroring
// Topology.Resolve's own degrade-don't-drop contract for an unresolvable
// majMin) rather than nothing at all -- a device the engine doesn't
// understand is still real evidence, not grounds to silently skip a
// finding.
func TestDeviceOrUnknownSeamInvariant1DegradesRatherThanDrops(t *testing.T) {
	topo := NewTopology(nil, map[string]SlotMeta{"disk1": {Device: "sdc", Rotational: true}})

	known := deviceOrUnknown(topo, "sdc")
	require.Equal(t, Device{Name: "sdc", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, known)

	unknown := deviceOrUnknown(topo, "nvme9n9")
	require.Equal(t, Device{Name: "nvme9n9", Role: RoleUnknown}, unknown)
	require.True(t, topo.Contended(unknown), "an unplaced device is still real evidence, never dropped")
	require.False(t, unknown.RotationalKnown, "RotationalKnown must be false for an unplaced device -- see RotationalKnown's own doc")
}

// TestCanonicalDeviceFoldsPartitionThenResolvesThenCanonicalizes is the
// composed join every culprit-attribution rule actually calls: fold a
// partition down to its whole device (seam 2), resolve it by name (seam
// 1), then canonicalize a data member onto its md form so a raw member
// name and its md alias are recognized as the SAME resource.
func TestCanonicalDeviceFoldsPartitionThenResolvesThenCanonicalizes(t *testing.T) {
	topo := NewTopology(nil, map[string]SlotMeta{"disk1": {Device: "sdc", Rotational: true}})

	viaPartition := canonicalDevice(topo, "sdc1")
	viaWhole := canonicalDevice(topo, "sdc")
	viaMD := canonicalDevice(topo, "md1")

	require.Equal(t, "md1", viaPartition.Name, "a partition on a data member folds all the way to the canonical md device")
	require.Equal(t, viaWhole, viaPartition, "the raw whole device and its partition must canonicalize identically")
	require.Equal(t, viaMD, viaPartition, "the md device is already canonical and must match the folded form exactly")
}

// TestCanonicalDeviceLeavesPoolDeviceAloneAfterFold covers a pool member
// (no md wrapper, no partition in practice, but the fold+resolve pipeline
// must still be a no-op for it end to end).
func TestCanonicalDeviceLeavesPoolDeviceAloneAfterFold(t *testing.T) {
	topo := NewTopology(nil, map[string]SlotMeta{"cache": {Device: "nvme0n1", Rotational: false}})

	got := canonicalDevice(topo, "nvme0n1")

	require.Equal(t, Device{Name: "nvme0n1", Slot: "cache", Role: RolePool, Rotational: false, RotationalKnown: true}, got)
}

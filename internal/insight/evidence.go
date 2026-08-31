package insight

import (
	"regexp"
	"sort"

	"github.com/smidley/gantry/internal/store"
)

// EntityShare is one entity's window-mean value on a resource and its
// fraction of that resource's window-mean total -- Share's own ranked
// output element.
type EntityShare struct {
	Entity   string
	Value    float64
	Fraction float64
}

// Culprits is Dominant's positive answer: either one entity that alone
// clears the floor (Shared false), or the smallest leading set that
// clears it together (Shared true) -- Open question 2's "one finding,
// honest arithmetic" resolution. Fraction is that entity's own share when
// Shared is false, or the SET's combined share when it is true (the
// number a rendered "together drive N%" statement quotes).
type Culprits struct {
	Names    []string
	Fraction float64
	Shared   bool
}

// Share computes each entity's fraction of a resource's total across the
// window, using the MEAN of each entity's own samples rather than its
// latest value: a single 2s tick is noise, and the claim an insight makes
// is about the whole window, not one instant of it. total is the sum of
// every entity's own mean (the "window-mean total" a per-entity fraction
// is taken against) -- entities with zero samples in the window
// contribute nothing rather than a divide-by-zero NaN. ranked is sorted
// descending by fraction, ties broken by entity name so output never
// depends on Go's unordered map iteration; always non-nil.
func Share(parts map[string][]store.Sample) (ranked []EntityShare, total float64) {
	type meanEntry struct {
		entity string
		mean   float64
	}
	means := make([]meanEntry, 0, len(parts))
	for entity, samples := range parts {
		if len(samples) == 0 {
			continue
		}
		sum := 0.0
		for _, s := range samples {
			sum += s.Val
		}
		mean := sum / float64(len(samples))
		means = append(means, meanEntry{entity, mean})
		total += mean
	}

	ranked = make([]EntityShare, 0, len(means))
	for _, m := range means {
		var frac float64
		if total > 0 {
			frac = m.mean / total
		}
		ranked = append(ranked, EntityShare{Entity: m.entity, Value: m.mean, Fraction: frac})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Fraction != ranked[j].Fraction {
			return ranked[i].Fraction > ranked[j].Fraction
		}
		return ranked[i].Entity < ranked[j].Entity
	})
	return ranked, total
}

// Dominant applies the dominance rule (Open question 2): the top entity
// alone when it clears floor; otherwise the smallest leading set (in
// ranked's own descending order, capped at maxN entities) whose combined
// fraction clears floor; otherwise nothing. "Clears" is inclusive (>=) --
// floor is a minimum bar to name a culprit, not a display-band threshold
// crossing. ranked is assumed already sorted descending (Share's own
// output shape); Dominant does not re-sort it.
func Dominant(ranked []EntityShare, floor float64, maxN int) (Culprits, bool) {
	if len(ranked) == 0 {
		return Culprits{}, false
	}
	if ranked[0].Fraction >= floor {
		return Culprits{Names: []string{ranked[0].Entity}, Fraction: ranked[0].Fraction, Shared: false}, true
	}

	sum := 0.0
	names := make([]string, 0, maxN)
	for i := 0; i < len(ranked) && i < maxN; i++ {
		sum += ranked[i].Fraction
		names = append(names, ranked[i].Entity)
		if sum >= floor {
			return Culprits{Names: names, Fraction: sum, Shared: true}, true
		}
	}
	return Culprits{}, false
}

// median returns the middle value of a sorted copy of vs (the mean of
// the two middle values for an even-length slice). vs must be non-empty;
// callers (Baseline) check that first.
func median(vs []float64) float64 {
	sorted := append([]float64(nil), vs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// Baseline is the parity rule's victim evidence (spec §16): the median of
// history when there is any, falling back to the median of opening (the
// run's own first samples) for a first-ever check with no prior history
// at all. false when neither is available -- there is nothing to compare
// a current speed against yet, which must read as "no verdict", never as
// a baseline of zero (a zero baseline would make any positive speed read
// as "faster than baseline", the opposite of the parity-slowdown rule's
// intent).
func Baseline(history []float64, opening []float64) (float64, bool) {
	if len(history) > 0 {
		return median(history), true
	}
	if len(opening) > 0 {
		return median(opening), true
	}
	return 0, false
}

// --- attribution primitives: folding/resolving/canonicalizing device
// names before Share/Dominant ever runs over them ------------------------

// sdPartitionRe/nvmePartitionRe/mdPartitionRe match a partition device
// name, capturing its whole-device prefix. Boundaries mirror
// host/diskstats.go's own wholeDeviceRe family exactly (sd[a-z]+,
// nvme\d+n\d+, md\d+) so a name this package folds is always a partition
// OF a device that family already recognizes as whole.
var (
	sdPartitionRe   = regexp.MustCompile(`^(sd[a-z]+)\d+$`)
	nvmePartitionRe = regexp.MustCompile(`^(nvme\d+n\d+)p\d+$`)
	mdPartitionRe   = regexp.MustCompile(`^(md\d+)p\d+$`)
)

// foldPartitionDevice folds a partition device name to the whole device
// that owns it -- seam invariant 2. Docker's cgroup io.stat can report a
// PARTITION's major:minor (live:io.sda1.*, nvme0n1p2.*, mdNpM.*: a
// container writing to a filesystem mounted on a partition, or reading
// straight off an md member's own partition table), while host
// diskio.<dev>.* is whole-device only (parseDiskstats' wholeDeviceRe
// already excludes partitions on the host side). Without folding first,
// a container's per-partition series could never join against the host's
// whole-device row for the SAME physical resource at all. A name that's
// already whole-device, or unrecognized entirely (a loop device, a
// future kernel's naming this package doesn't know), passes through
// unchanged -- degrade, don't drop.
func foldPartitionDevice(name string) string {
	if m := sdPartitionRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	if m := nvmePartitionRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	if m := mdPartitionRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

// resourceLabel is the insight_instances.resource column's one producer
// -- seam invariant 3: Device.Slot ("disk3", "cache", "parity") whenever
// it is known, falling back to Device.Name ONLY when Slot is empty
// (RoleUnknown -- see Topology.Resolve/ResolveName's own degrade-don't-
// drop doc). Name is always populated once a device resolves at all, so
// naively preferring it would silently leak kernel device names
// ("sdc"/"md1") into user-facing resource labels for every ordinary
// array disk; this function is the one place that choice is made.
func resourceLabel(d Device) string {
	if d.Slot != "" {
		return d.Slot
	}
	return d.Name
}

// deviceOrUnknown resolves name through topo.ResolveName -- seam
// invariant 1: every series an insight rule reads is keyed by device
// NAME (host diskio.<name>.*, docker live:io.<slug(name)>.*), never by
// major:minor, so ResolveName (not Resolve, which needs a majMin) is the
// one correct entry point. When the topology has no slot for name at all,
// this degrades to a synthetic RoleUnknown Device carrying just the raw
// name -- mirroring Topology.Resolve's own contract for an unresolvable
// majMin -- rather than reporting failure: a device the engine can't
// place in the array is still a real, Contended device, and a rule must
// not silently drop a finding just because Topology doesn't recognize
// it. RotationalKnown is correctly false on this fallback (Device's own
// zero value), so a rotational-dependent rule can never misread an
// unplaced disk as an SSD.
func deviceOrUnknown(topo *Topology, name string) Device {
	if d, ok := topo.ResolveName(name); ok {
		return d
	}
	return Device{Name: name, Role: RoleUnknown}
}

// canonicalDevice is the composed join every culprit-attribution rule
// actually calls on a raw series device name: fold a partition to its
// whole device (seam 2), resolve it by name with degrade-don't-drop
// (seam 1), then canonicalize a data member onto its md form (so a raw
// member device and its md alias -- whichever one a given container's
// cgroup accounting happens to report -- are recognized as the SAME
// resource rather than splitting one container's true share across two
// resource buckets, or letting a host-side reading on "md1" fail to
// join a culprit-side reading on "sdc"). Every other role is a Canonical
// no-op, so this is safe to call uniformly regardless of what kind of
// device name is on hand.
func canonicalDevice(topo *Topology, rawName string) Device {
	d := deviceOrUnknown(topo, foldPartitionDevice(rawName))
	return topo.Canonical(d)
}

// Package insight resolves cross-container impact: correlating a
// victim's measured distress with a culprit's dominant share of a
// contended resource. This file is the device topology half of that
// machinery -- Unraid's array/pool layer sits between a raw kernel block
// device and the "disk3" or "cache" a human thinks in, and every array
// write also drives the parity disk as a CONSEQUENCE of the data write,
// not as an independent contention. Getting that translation wrong
// produces a confidently wrong insight (see Resolve/Contended/Canonical
// below). The rest of the package -- evidence, the rule library, the
// engine loop -- lands in later phase-5 tasks.
package insight

import "regexp"

// Role classifies a device's place in Unraid's array/pool layer.
type Role int

const (
	// RoleUnknown is the zero value: a maj:min resolved to a real kernel
	// device name, but that name doesn't match any currently-present
	// array or pool slot. Still a real device -- Contended is true for
	// it. Degrade to naming it by its kernel name; don't drop the
	// finding just because Topology can't place it in the array.
	RoleUnknown Role = iota
	// RoleData is an array data disk (an Unraid "diskN" slot), or its
	// canonical "mdN" form -- see Canonical.
	RoleData
	// RoleParity is the array's parity disk. Never Contended: every
	// array write drives parity as a CONSEQUENCE of the data write, so
	// naming parity as an independently contended resource would
	// restate the data write rather than report a second finding. See
	// Contended.
	RoleParity
	// RolePool is a cache/pool member -- "cache" or any custom-named
	// pool. Unraid does not restrict pool naming to "cache" (see
	// testdata/disks_real.ini's "rocket_pool").
	RolePool
	// RoleFlash is the boot flash device (Unraid's fixed "flash" slot).
	RoleFlash
)

// String renders Role for logs and test failures, the same reason
// alert.Verdict has one.
func (r Role) String() string {
	switch r {
	case RoleData:
		return "data"
	case RoleParity:
		return "parity"
	case RolePool:
		return "pool"
	case RoleFlash:
		return "flash"
	default:
		return "unknown"
	}
}

// Device is one kernel block device as Topology understands it.
type Device struct {
	Name       string // "sdb", "nvme0n1", "md1"
	Slot       string // "disk3", "cache", "parity" -- "" for a device with no known array/pool slot
	Role       Role
	Rotational bool
}

// SlotMeta is Topology's per-slot input: one present array/pool slot's
// raw device name and rotational reading. A narrow local mirror of
// unraid.DiskMeta (Device, Kind) plus the rotational value disks.go
// records as a separate live metric rather than on DiskMeta itself --
// deliberately not importing internal/collect/unraid here, matching the
// repo's standing collector-agnostic-core boundary (internal/server
// stays store/collector-shape agnostic against its own collectors; see
// api_storage.go's doc). The caller -- the engine's per-tick topology
// build -- assembles this from the unraid collector's DiskMeta() plus
// its disk.<slot>.rotational live sample.
type SlotMeta struct {
	Device     string
	Rotational bool
}

// Topology answers the three questions per-device attribution needs. It
// is a snapshot: built fresh each tick from that tick's own device-name
// and slot inputs (see NewTopology), not a long-lived subscription.
type Topology struct {
	deviceName func(majMin string) (string, bool)
	nameToSlot map[string]string // kernel device name (raw OR canonical mdN) -> Unraid slot
	slotRot    map[string]bool   // Unraid slot -> rotational
}

// NewTopology builds one tick's Topology snapshot. deviceName resolves a
// cgroup-reported "major:minor" to its kernel device name -- the exact
// closure shape host.Collector.DeviceName already is, injected into the
// docker collector the same way (docker.go's DeviceName field); pass
// that method directly rather than pre-copying its map. slots is every
// currently-present array/pool slot's metadata, keyed by Unraid slot
// name, assembled by the caller from the unraid collector's DiskMeta()
// plus its own rotational reading.
//
// A nil deviceName degrades to "nothing resolves" rather than panicking,
// matching the docker collector's own no-op default; slots may be nil.
func NewTopology(deviceName func(majMin string) (string, bool), slots map[string]SlotMeta) *Topology {
	if deviceName == nil {
		deviceName = func(string) (string, bool) { return "", false }
	}
	nameToSlot := make(map[string]string, len(slots)*2)
	slotRot := make(map[string]bool, len(slots))
	for slot, meta := range slots {
		slotRot[slot] = meta.Rotational
		if meta.Device != "" {
			nameToSlot[meta.Device] = slot
		}
		if md, ok := mdName(slot); ok {
			nameToSlot[md] = slot
		}
	}
	return &Topology{deviceName: deviceName, nameToSlot: nameToSlot, slotRot: slotRot}
}

// Resolve maps a cgroup-reported "major:minor" to the Device it names.
// ok is false only when deviceName itself can't name majMin at all;
// once a kernel name is known, Resolve always succeeds -- a name that
// matches no known slot simply comes back RoleUnknown with Slot "".
func (t *Topology) Resolve(majMin string) (Device, bool) {
	name, ok := t.deviceName(majMin)
	if !ok {
		return Device{}, false
	}
	slot := t.nameToSlot[name]
	return Device{
		Name:       name,
		Slot:       slot,
		Role:       classifyRole(slot),
		Rotational: t.slotRot[slot],
	}, true
}

// ResolveName maps a device name directly to the Device it names, for
// callers that already have a kernel device name rather than a
// cgroup-reported "major:minor" -- every live series the engine reads is
// keyed by device name, not majMin (host diskio.<name>.*, docker
// live:io.<slug(name)>.*). It is the second half of Resolve: the
// deviceName(majMin) step that produces a kernel name is skipped because
// the caller already has one.
//
// Unlike Resolve, ok is false whenever name matches no known slot.
// Resolve can fall back to RoleUnknown because its prior deviceName call
// already proved a real kernel device exists; ResolveName has no such
// proof for an arbitrary name, so an unmatched one comes back empty
// rather than a fabricated RoleUnknown device.
//
// name may be either a slot's raw device ("sdc") or a data slot's
// canonical "mdN" form ("md1") -- nameToSlot carries both to the same
// slot (see NewTopology), so ResolveName("md1") comes back already in
// Canonical's output form without ResolveName treating md specially.
func (t *Topology) ResolveName(name string) (Device, bool) {
	slot, known := t.nameToSlot[name]
	if !known {
		return Device{}, false
	}
	return Device{
		Name:       name,
		Slot:       slot,
		Role:       classifyRole(slot),
		Rotational: t.slotRot[slot],
	}, true
}

// Contended reports whether d may be named as a contended resource in
// its own right. False only for RoleParity -- see RoleParity's own doc.
func (t *Topology) Contended(d Device) bool {
	return d.Role != RoleParity
}

// Canonical maps an array data member's raw device to the mdN device
// that represents the array-level write, so a single logical write is
// attributed once (on md1) rather than counted separately on both sdc
// and md1. Every other role -- pool, parity, flash, unknown -- has no
// such wrapper and comes back unchanged.
func (t *Topology) Canonical(d Device) Device {
	if d.Role != RoleData {
		return d
	}
	if md, ok := mdName(d.Slot); ok {
		d.Name = md
	}
	return d
}

var (
	parityRe = regexp.MustCompile(`^parity\d*$`)
	dataRe   = regexp.MustCompile(`^disk(\d+)$`)
)

// classifyRole derives a device's Role from its Unraid slot name alone,
// per Unraid's own fixed, version-independent slot-naming convention:
// parity slots are always "parity"/"parity2", data slots are always
// "diskN", the boot device is always "flash", and anything else present
// is a pool -- Unraid does not restrict custom pool names to "cache"
// (see testdata/disks_real.ini's "rocket_pool"). slot=="" (no known slot
// at all) is RoleUnknown, matching Role's own zero value.
func classifyRole(slot string) Role {
	switch {
	case slot == "":
		return RoleUnknown
	case parityRe.MatchString(slot):
		return RoleParity
	case dataRe.MatchString(slot):
		return RoleData
	case slot == "flash":
		return RoleFlash
	default:
		return RolePool
	}
}

// mdName derives a data slot's canonical array device name: Unraid
// exposes array data disk "diskN" as kernel block device "mdN" (verified
// against testdata/disks_real.ini's deviceSb field: disk1 -> md1p1,
// disk6 -> md6p1). ok is false for a non-"diskN" slot, which has no md
// wrapper.
func mdName(slot string) (string, bool) {
	m := dataRe.FindStringSubmatch(slot)
	if m == nil {
		return "", false
	}
	return "md" + m[1], true
}

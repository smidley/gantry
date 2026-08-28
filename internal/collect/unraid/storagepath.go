package unraid

import (
	"regexp"
	"strings"
)

// StorageRef identifies which Unraid storage system backs a host path,
// as resolved by ResolveStoragePath. Kind is one of "share", "pool",
// "disk", "flash", or "other"; Name is the share/pool/disk-slot name --
// "" for "flash" (a single well-known device, nothing to name) and
// "other" (nothing recognized).
type StorageRef struct {
	Kind string
	Name string
}

// diskSlotPattern matches an array disk slot's name -- "disk" followed
// by one or more digits (disk1 .. disk28 and beyond) -- Unraid's fixed,
// reserved naming for array data disks; a pool can't be named this, so
// no fleet lookup is needed to tell a disk slot apart from a pool one
// (contrast poolSlot membership below, which does need the fleet: pool
// names are arbitrary).
var diskSlotPattern = regexp.MustCompile(`^disk[0-9]+$`)

// ResolveStoragePath maps a container mount's host source path to the
// Unraid storage system that backs it, per the design in
// docs/superpowers/backlog.md's "Per-container storage panel" sketch:
//
//	/mnt/user/<share>/...  -> {share, <share>}
//	/mnt/user0/<share>/... -> {share, <share>}  (array-only alias)
//	/mnt/<poolslot>/...    -> {pool, <poolslot>} when poolslot is in pools
//	/mnt/diskN/...         -> {disk, diskN}
//	/boot/...              -> {flash, ""}
//	anything else          -> {other, ""}
//
// pools is the current set of known pool slot names (Collector.Slots()'s
// first return value -- "cache" included when it exists on this fleet,
// same as any custom-named pool; there is no hardcoded literal-"cache"
// special case). A nil/empty pools (e.g. disks.ini never successfully
// read yet) simply means no path resolves as a pool until it's known,
// same degrade-gracefully convention as every other pre-first-tick
// accessor in this package.
//
// Pure and side-effect-free: this is deliberately just a path-shape
// classifier over its two inputs, not a Collector method, so it's
// exhaustively table-testable without any ini fixture or Collector at
// all.
func ResolveStoragePath(path string, pools []string) StorageRef {
	trimmed := strings.TrimPrefix(path, "/")
	top, rest := splitFirstSegment(trimmed)

	if top == "boot" {
		return StorageRef{Kind: "flash"}
	}
	if top != "mnt" {
		return StorageRef{Kind: "other"}
	}

	slot, tail := splitFirstSegment(rest)
	switch {
	case slot == "":
		return StorageRef{Kind: "other"}
	case slot == "user" || slot == "user0":
		if share, _ := splitFirstSegment(tail); share != "" {
			return StorageRef{Kind: "share", Name: share}
		}
		return StorageRef{Kind: "other"}
	case diskSlotPattern.MatchString(slot):
		return StorageRef{Kind: "disk", Name: slot}
	case isKnownPool(slot, pools):
		return StorageRef{Kind: "pool", Name: slot}
	default:
		return StorageRef{Kind: "other"}
	}
}

// splitFirstSegment splits s on its first "/", returning the part before
// it (head) and everything after (rest, "" if there was no "/" at all).
func splitFirstSegment(s string) (head, rest string) {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// isKnownPool reports whether slot is one of the caller-supplied known
// pool slot names. Linear scan: pools is a handful of entries at most
// (real Unraid fleets have a handful of cache pools), and this runs once
// per mount, not once per tick.
func isKnownPool(slot string, pools []string) bool {
	for _, p := range pools {
		if p == slot {
			return true
		}
	}
	return false
}

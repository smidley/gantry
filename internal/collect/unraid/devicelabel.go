package unraid

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DeviceLabel is a raw block device's friendlier presentation for the
// storage panel's Live IO rows -- see ResolveDeviceLabel's own doc. Kind
// is "" whenever it isn't known from DiskMeta (every loop device, and
// any device this fleet's disks.ini doesn't cover at all), never
// guessed.
type DeviceLabel struct {
	Label string
	Kind  string
	// Slot is the Unraid array/pool/flash slot this device belongs to
	// ("disk7", "rocket_pool", "flash"), or "" when it belongs to none --
	// every loop device, and any device this fleet's disks.ini doesn't
	// cover. Label already carries the slot name whenever one is known,
	// but a caller can't tell that apart from a loop device's backing-file
	// basename by looking at Label alone; Slot says so outright, which is
	// what lets the storage endpoint fold the two devices that name the
	// SAME array disk (its md device and its physical member) into one
	// row.
	Slot string
}

// ResolveDeviceLabel turns a raw block-device name (as /proc/diskstats
// spells it -- host.Collector.DeviceName's own contract, e.g. "loop0",
// "sdi", "nvme0n1") into a label a person actually recognizes, per
// Scott's own report on the storage panel's first cut ("I don't know
// what loop0 and loop2 are"):
//
//   - a loop device -- Unraid mounts docker.img (or a btrfs pool image)
//     through one, so a container's own image-layer/writable-layer IO
//     lands against "loopN", not anything naming docker -- resolves via
//     its /sys/block/<dev>/loop/backing_file to that file's own
//     basename, e.g. "docker.img";
//   - a device this fleet's disks.ini places in a known array/pool/
//     flash slot (diskMeta, keyed by slot -- Collector.DiskMeta()'s own
//     map) resolves to that SLOT's name, e.g. "rocket_pool", "flash",
//     with Kind and Slot carried along from the same entry. An ARRAY
//     data disk matches on two names, not one: disks.ini's own `device`
//     field names the physical member ("sdf"), but Unraid reaches that
//     disk through the md driver, so a container writing to /mnt/diskN
//     has its cgroup io.stat charged to the array device (major 9 --
//     "md7p1") while the member gets a token zero-byte row. Both spell
//     the same slot, so both resolve to it (see arrayDeviceForSlot);
//   - anything else -- an unrecognized device, or a loop device whose
//     backing file isn't readable -- keeps device itself as Label, Kind
//     left "" (unknown either way, never guessed).
//
// sysRoot is the same host-sysfs mount every other sysfs reader in this
// codebase uses (GANTRY_HOST_SYS, "/host/sys" in production -- see
// host.Collector's own sysRoot field, gpu.Collector.SysRoot).
func ResolveDeviceLabel(device, sysRoot string, diskMeta map[string]DiskMeta) DeviceLabel {
	if strings.HasPrefix(device, "loop") {
		if backing, ok := readLoopBackingFile(device, sysRoot); ok {
			return DeviceLabel{Label: filepath.Base(backing)}
		}
		return DeviceLabel{Label: device}
	}
	// The two comparisons below can never match different slots for the
	// same device: folded is an "mdN" only when device was one, and no
	// slot's disks.ini `device` field is ever an md name, so map
	// iteration order can't make this ambiguous.
	folded := foldArrayDevice(device)
	for slot, m := range diskMeta {
		if m.Device == device {
			return DeviceLabel{Label: slot, Kind: m.Kind, Slot: slot}
		}
		if md, ok := arrayDeviceForSlot(slot); ok && md == folded {
			return DeviceLabel{Label: slot, Kind: m.Kind, Slot: slot}
		}
	}
	return DeviceLabel{Label: device}
}

var (
	// arrayDataSlotRe matches an array DATA slot's name -- "disk" followed
	// by disk1 and up; Unraid has no disk0. Parity slots ("parity",
	// "parity2") deliberately don't match: Unraid exposes no md device for
	// parity at all (disks.ini reports no deviceSb for a parity slot), and
	// nothing mounts it, so a parity slot only ever resolves through its
	// own physical device name.
	arrayDataSlotRe = regexp.MustCompile(`^disk([1-9][0-9]*)$`)
	// arrayPartitionRe matches the partition-suffixed spelling
	// /proc/diskstats uses for an Unraid array device ("md7p1"), so
	// foldArrayDevice can strip it back to the "mdN" form
	// arrayDeviceForSlot produces.
	arrayPartitionRe = regexp.MustCompile(`^(md\d+)p\d+$`)
)

// arrayDeviceForSlot derives an array data slot's canonical kernel block
// device: Unraid reaches data disk "diskN" through the md driver as
// "mdN". Verified against a live Unraid 7.3.2 box -- disks.ini's disk7
// carries deviceSb="md7p1", `mount` shows /dev/md7p1 on /mnt/disk7, and
// /dev/md7p1 is major 9 minor 7, which is exactly what that disk's IO
// shows up as in a container's cgroup io.stat. ok is false for any slot
// with no md wrapper (parity, flash, every pool).
//
// internal/insight/topology.go's mdName is the same convention, applied
// to the same slot names, for the contention resolver's own Canonical
// collapse. The two are separate on purpose: insight documents a
// standing boundary against importing a collector package, so it keeps
// its own narrow mirror rather than depending on this one.
func arrayDeviceForSlot(slot string) (string, bool) {
	m := arrayDataSlotRe.FindStringSubmatch(slot)
	if m == nil {
		return "", false
	}
	return "md" + m[1], true
}

// foldArrayDevice folds /proc/diskstats' "md7p1" spelling of an Unraid
// array device back to the "mdN" form arrayDeviceForSlot produces, so a
// cgroup io.stat row resolved through host.Collector.DeviceName can be
// compared against a slot. Every other name -- a physical member, a pool
// device, a pool PARTITION, a loop device -- passes through unchanged:
// only md devices are folded here, because only they were observed to
// carry a container's array IO. A btrfs pool charges io.stat against its
// whole device ("259:0" -> "nvme0n1"), never its partition, so folding
// those too would be speculative rather than evidence-backed.
func foldArrayDevice(device string) string {
	if m := arrayPartitionRe.FindStringSubmatch(device); m != nil {
		return m[1]
	}
	return device
}

// readLoopBackingFile reads a loop device's backing_file sysfs
// attribute -- present only for an actual loop device. Absent (along
// with every other reason this can fail: no permission, this device
// doesn't actually exist on the host, sysRoot isn't mounted at all)
// degrades the same way, per ResolveDeviceLabel's own "unreadable ->
// keep raw name" contract: return false, never a guess. The file's
// contents carry a trailing newline the kernel always includes;
// TrimSpace strips it before the caller takes its basename.
func readLoopBackingFile(device, sysRoot string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(sysRoot, "block", device, "loop", "backing_file"))
	if err != nil {
		return "", false
	}
	backing := strings.TrimSpace(string(data))
	if backing == "" {
		return "", false
	}
	return backing, true
}

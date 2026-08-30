package unraid

import (
	"os"
	"path/filepath"
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
//     with Kind carried along from the same entry;
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
	for slot, m := range diskMeta {
		if m.Device == device {
			return DeviceLabel{Label: slot, Kind: m.Kind}
		}
	}
	return DeviceLabel{Label: device}
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

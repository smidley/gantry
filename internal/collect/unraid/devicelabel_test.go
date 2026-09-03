package unraid

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeLoopBackingFile builds a fixture sys tree's one file
// ResolveDeviceLabel's loop branch reads:
// <sysRoot>/block/<device>/loop/backing_file -- mirroring a real
// /sys/block/loopN/loop/backing_file, whose contents are the absolute
// host path of the image file the loop device is mounting.
func writeLoopBackingFile(t *testing.T, sysRoot, device, backing string) {
	t.Helper()
	dir := filepath.Join(sysRoot, "block", device, "loop")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "backing_file"), []byte(backing+"\n"), 0o644))
}

func TestResolveDeviceLabel(t *testing.T) {
	t.Run("loop device resolves to its backing file's basename", func(t *testing.T) {
		sysRoot := t.TempDir()
		writeLoopBackingFile(t, sysRoot, "loop2", "/mnt/user/system/docker/docker.img")

		got := ResolveDeviceLabel("loop2", sysRoot, nil)
		require.Equal(t, DeviceLabel{Label: "docker.img"}, got, "Kind stays unknown for a loop device -- never guessed")
	})

	t.Run("loop device with no backing_file at all keeps its raw name", func(t *testing.T) {
		sysRoot := t.TempDir() // no block/loop0 directory whatsoever

		got := ResolveDeviceLabel("loop0", sysRoot, nil)
		require.Equal(t, DeviceLabel{Label: "loop0"}, got)
	})

	t.Run("loop device whose backing_file exists but is empty keeps its raw name", func(t *testing.T) {
		sysRoot := t.TempDir()
		writeLoopBackingFile(t, sysRoot, "loop3", "")

		got := ResolveDeviceLabel("loop3", sysRoot, nil)
		require.Equal(t, DeviceLabel{Label: "loop3"}, got)
	})

	t.Run("sysRoot not mounted at all degrades the same way as a missing file", func(t *testing.T) {
		got := ResolveDeviceLabel("loop2", filepath.Join(t.TempDir(), "never-created"), nil)
		require.Equal(t, DeviceLabel{Label: "loop2"}, got)
	})

	t.Run("a device in a known array/pool/flash slot resolves to that slot's name and kind", func(t *testing.T) {
		diskMeta := map[string]DiskMeta{
			"rocket_pool": {Device: "nvme0n1", Kind: "nvme"},
			"flash":       {Device: "sdi", Kind: "usb"},
			"disk1":       {Device: "sdc", Kind: "hdd"},
		}

		require.Equal(t, DeviceLabel{Label: "rocket_pool", Kind: "nvme", Slot: "rocket_pool"}, ResolveDeviceLabel("nvme0n1", "/unused", diskMeta))
		require.Equal(t, DeviceLabel{Label: "flash", Kind: "usb", Slot: "flash"}, ResolveDeviceLabel("sdi", "/unused", diskMeta))
		require.Equal(t, DeviceLabel{Label: "disk1", Kind: "hdd", Slot: "disk1"}, ResolveDeviceLabel("sdc", "/unused", diskMeta))
	})

	t.Run("a device not in diskMeta and not loop-prefixed passes through as its raw name", func(t *testing.T) {
		diskMeta := map[string]DiskMeta{"disk1": {Device: "sdc", Kind: "hdd"}}

		got := ResolveDeviceLabel("sda", "/unused", diskMeta)
		require.Equal(t, DeviceLabel{Label: "sda"}, got, "sda isn't any known slot's device -- stays raw, Kind unknown")
	})

	t.Run("nil diskMeta (pre-first-tick, or fake mode with no fleet wired) never panics", func(t *testing.T) {
		got := ResolveDeviceLabel("sda", "/unused", nil)
		require.Equal(t, DeviceLabel{Label: "sda"}, got)
	})
}

// TestResolveDeviceLabelArrayDevices pins the array half of the slot
// join, captured from a live Unraid 7.3.2 box: a container writing to
// /mnt/diskN has its cgroup io.stat charged to the ARRAY device (major
// 9, e.g. "9:7" -> "md7p1"), never to the physical member disks.ini's
// own `device` field names ("sdf") -- that one gets a token row with
// zero bytes. Before this, only the physical name matched, so the real
// traffic came back labelled "md7p1"/Kind "" while a second row named
// after the slot sat at 0 B/s.
func TestResolveDeviceLabelArrayDevices(t *testing.T) {
	// Slots and devices as disks.ini reports them on the capture box:
	// disk7's device is "sdf", its deviceSb is "md7p1".
	diskMeta := map[string]DiskMeta{
		"disk7":       {Device: "sdf", Kind: "hdd"},
		"disk11":      {Device: "sdj", Kind: "hdd"},
		"rocket_pool": {Device: "nvme0n1", Kind: "nvme"},
		"flash":       {Device: "sdi", Kind: "usb"},
	}

	t.Run("an array device resolves to its own data slot", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "disk7", Kind: "hdd", Slot: "disk7"},
			ResolveDeviceLabel("md7p1", "/unused", diskMeta))
	})

	t.Run("the bare mdN spelling resolves the same way", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "disk7", Kind: "hdd", Slot: "disk7"},
			ResolveDeviceLabel("md7", "/unused", diskMeta))
	})

	t.Run("two-digit slots are not confused with their single-digit prefix", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "disk11", Kind: "hdd", Slot: "disk11"},
			ResolveDeviceLabel("md11p1", "/unused", diskMeta))
	})

	t.Run("the physical member still resolves to the same slot", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "disk7", Kind: "hdd", Slot: "disk7"},
			ResolveDeviceLabel("sdf", "/unused", diskMeta))
	})

	t.Run("an md device for a slot this array doesn't have stays raw", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "md3p1"}, ResolveDeviceLabel("md3p1", "/unused", diskMeta))
	})

	t.Run("pool and flash devices are untouched by the array branch", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "rocket_pool", Kind: "nvme", Slot: "rocket_pool"},
			ResolveDeviceLabel("nvme0n1", "/unused", diskMeta))
		require.Equal(t, DeviceLabel{Label: "flash", Kind: "usb", Slot: "flash"},
			ResolveDeviceLabel("sdi", "/unused", diskMeta))
	})

	t.Run("a pool partition is NOT folded -- no array device, no observed rows", func(t *testing.T) {
		require.Equal(t, DeviceLabel{Label: "nvme0n1p1"}, ResolveDeviceLabel("nvme0n1p1", "/unused", diskMeta),
			"btrfs pools charge io.stat against the whole device; folding partitions here would be speculative")
	})
}

// TestArrayDeviceForSlot pins Unraid's own diskN <-> mdN convention --
// the mapping that turns a cgroup io.stat major:minor (9:N) into an
// array slot. Confirmed against a live Unraid 7.3.2 box: disks.ini's
// disk7 carries deviceSb="md7p1", `mount` shows /dev/md7p1 on
// /mnt/disk7, and /dev/md7p1 is major 9 minor 7.
func TestArrayDeviceForSlot(t *testing.T) {
	for _, tc := range []struct {
		slot string
		want string
		ok   bool
	}{
		{"disk1", "md1", true},
		{"disk7", "md7", true},
		{"disk28", "md28", true},
		{"parity", "", false}, // parity has no md wrapper -- disks.ini reports no deviceSb for it
		{"parity2", "", false},
		{"flash", "", false},
		{"cache", "", false},
		{"rocket_pool", "", false},
		{"disk", "", false},
		{"disk0", "", false}, // Unraid has no disk0
		{"", "", false},
	} {
		got, ok := arrayDeviceForSlot(tc.slot)
		require.Equal(t, tc.ok, ok, "slot %q", tc.slot)
		require.Equal(t, tc.want, got, "slot %q", tc.slot)
	}
}

// TestFoldArrayDevice pins the one partition fold ResolveDeviceLabel
// applies: /proc/diskstats spells Unraid's array devices "mdNp1", so a
// 9:N io.stat row resolves to that name and has to fold back to "mdN"
// before it can be compared against a slot's own array device.
func TestFoldArrayDevice(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"md7p1", "md7"},
		{"md7", "md7"},
		{"md11p1", "md11"},
		{"md1p2", "md1"},
		{"sdf", "sdf"},
		{"sdf1", "sdf1"}, // only md devices fold -- see foldArrayDevice's own doc
		{"nvme0n1p1", "nvme0n1p1"},
		{"loop2", "loop2"},
		{"", ""},
	} {
		require.Equal(t, tc.want, foldArrayDevice(tc.in), "device %q", tc.in)
	}
}

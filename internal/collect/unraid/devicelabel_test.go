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

		require.Equal(t, DeviceLabel{Label: "rocket_pool", Kind: "nvme"}, ResolveDeviceLabel("nvme0n1", "/unused", diskMeta))
		require.Equal(t, DeviceLabel{Label: "flash", Kind: "usb"}, ResolveDeviceLabel("sdi", "/unused", diskMeta))
		require.Equal(t, DeviceLabel{Label: "disk1", Kind: "hdd"}, ResolveDeviceLabel("sdc", "/unused", diskMeta))
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

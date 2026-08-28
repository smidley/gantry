package gpu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writePCIVendorFile creates sysRoot/bus/pci/devices/<pdev>/vendor with
// the given content, matching the real sysfs layout readPCIVendor and
// hasPCIVendor both walk.
func writePCIVendorFile(t *testing.T, sysRoot, pdev, content string) {
	t.Helper()
	path := filepath.Join(sysRoot, "bus/pci/devices", pdev, "vendor")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestVendorNameForPdevMapsKnownVendors(t *testing.T) {
	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:00:02.0", "0x8086\n")
	writePCIVendorFile(t, sysRoot, "0000:01:00.0", "0x10de\n")
	writePCIVendorFile(t, sysRoot, "0000:02:00.0", "0x1002\n")

	require.Equal(t, "Intel", vendorNameForPdev(sysRoot, "0000:00:02.0"))
	require.Equal(t, "NVIDIA", vendorNameForPdev(sysRoot, "0000:01:00.0"))
	require.Equal(t, "AMD", vendorNameForPdev(sysRoot, "0000:02:00.0"))
}

func TestVendorNameForPdevFallsBackToGPUForUnrecognizedVendor(t *testing.T) {
	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:03:00.0", "0x1234\n")
	require.Equal(t, "GPU", vendorNameForPdev(sysRoot, "0000:03:00.0"))
}

// TestVendorNameForPdevFallsBackToGPUWhenVendorFileMissing pins the
// missing-file fallback explicitly called for in the plan: a synthetic
// entity id (collector.go's own "gpu0", used when a client's fdinfo
// carries no drm-pdev) or a box with no /host/sys mount at all both
// land here, and both must degrade to the generic label rather than
// erroring or panicking.
func TestVendorNameForPdevFallsBackToGPUWhenVendorFileMissing(t *testing.T) {
	sysRoot := t.TempDir() // no vendor file ever written for this pdev
	require.Equal(t, "GPU", vendorNameForPdev(sysRoot, "gpu0"))
	require.Equal(t, "GPU", vendorNameForPdev(filepath.Join(sysRoot, "does-not-exist"), "0000:00:02.0"))
}

func TestVendorNameForPdevTrimsWhitespace(t *testing.T) {
	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:00:02.0", "  0x8086  \n")
	require.Equal(t, "Intel", vendorNameForPdev(sysRoot, "0000:00:02.0"))
}

func TestHasPCIVendorFindsAMatchAmongMultipleDevices(t *testing.T) {
	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:00:02.0", "0x8086\n") // Intel iGPU
	writePCIVendorFile(t, sysRoot, "0000:01:00.0", "0x10de\n") // Nvidia dGPU

	require.True(t, hasPCIVendor(sysRoot, nvidiaVendorID))
	require.True(t, hasPCIVendor(sysRoot, "0x8086"))
	require.False(t, hasPCIVendor(sysRoot, "0x1002")) // no AMD device present
}

// TestHasPCIVendorFalseWhenOnlyOtherVendorsPresent is the NvidiaCollector
// Probe scenario this exists for: Intel-only hardware (Scott's own box --
// "I don't have an nvidia GPU") must read as "no Nvidia here", not error.
func TestHasPCIVendorFalseWhenOnlyOtherVendorsPresent(t *testing.T) {
	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:00:02.0", "0x8086\n")
	require.False(t, hasPCIVendor(sysRoot, nvidiaVendorID))
}

// TestHasPCIVendorFalseWhenSysRootUnreadable pins the "no /host/sys
// mount at all" fallback: ReadDir fails outright, and this must report
// false (not error/panic) -- Probe's own nvidia-smi PATH check already
// covers the "nothing works here" case regardless of which reason.
func TestHasPCIVendorFalseWhenSysRootUnreadable(t *testing.T) {
	sysRoot := filepath.Join(t.TempDir(), "does-not-exist")
	require.False(t, hasPCIVendor(sysRoot, nvidiaVendorID))
}

func TestHasPCIVendorFalseWhenNoDevicesAtAll(t *testing.T) {
	sysRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sysRoot, "bus/pci/devices"), 0o755))
	require.False(t, hasPCIVendor(sysRoot, nvidiaVendorID))
}

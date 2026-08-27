package gpu

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func parseFixture(t *testing.T, name string) (FDInfo, bool) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	return ParseFDInfo(f)
}

func TestParseI915Client(t *testing.T) {
	info, ok := parseFixture(t, "i915_client.txt")
	require.True(t, ok)
	require.Equal(t, "i915", info.Driver)
	require.Equal(t, "972", info.ClientID)
	require.Equal(t, "137463412963 ns", info.Fields["drm-engine-render"])
	require.Equal(t, "507869073 ns", info.Fields["drm-engine-video"])
}

func TestParseAmdgpuClient(t *testing.T) {
	info, ok := parseFixture(t, "amdgpu_client.txt")
	require.True(t, ok)
	require.Equal(t, "amdgpu", info.Driver)
	require.Equal(t, "524288 KiB", info.Fields["drm-memory-vram"])
}

func TestNonDRMFileRejected(t *testing.T) {
	_, ok := parseFixture(t, "not_drm.txt")
	require.False(t, ok)
}

func TestParseRealUnraidI915Capture(t *testing.T) {
	info, ok := parseFixture(t, "i915_unraid_7_3_2.txt")
	require.True(t, ok)
	require.Equal(t, "i915", info.Driver)
	require.Equal(t, "940", info.ClientID)
	require.Equal(t, "315136210141 ns", info.Fields["drm-engine-render"])
	require.Equal(t, "118615034512 ns", info.Fields["drm-engine-video"])
	require.Equal(t, "251808 KiB", info.Fields["drm-total-system0"])
}

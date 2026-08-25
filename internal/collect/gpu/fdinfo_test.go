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
	defer f.Close()
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

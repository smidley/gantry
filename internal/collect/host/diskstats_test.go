package host

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func openDiskstatsFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open("testdata/diskstats.txt")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestParseDiskstatsKeepsWholeDevicesOnly(t *testing.T) {
	counters, err := parseDiskstats(openDiskstatsFixture(t))
	require.NoError(t, err)

	require.Equal(t, diskCounters{readSectors: 4000000, writeSectors: 8000000}, counters["sda"])
	require.Equal(t, diskCounters{readSectors: 800000000, writeSectors: 900000000}, counters["nvme0n1"])
	require.Equal(t, diskCounters{readSectors: 640000000, writeSectors: 720000000}, counters["md1"])

	_, ok := counters["sda1"]
	require.False(t, ok, "partitions must be dropped")
	_, ok = counters["nvme0n1p1"]
	require.False(t, ok, "partitions must be dropped")
	require.Len(t, counters, 3)
}

func TestBuildDeviceMapIncludesPartitions(t *testing.T) {
	m, err := buildDeviceMap(openDiskstatsFixture(t))
	require.NoError(t, err)
	require.Equal(t, "sda", m["8:0"])
	require.Equal(t, "sda1", m["8:1"])
	require.Equal(t, "nvme0n1", m["259:0"])
	require.Equal(t, "nvme0n1p1", m["259:1"])
	require.Equal(t, "md1", m["9:1"])
}

func TestWholeDeviceRegexRejectsPartitionLikeNames(t *testing.T) {
	require.True(t, wholeDeviceRe.MatchString("sda"))
	require.True(t, wholeDeviceRe.MatchString("sdaa"))
	require.True(t, wholeDeviceRe.MatchString("nvme0n1"))
	require.True(t, wholeDeviceRe.MatchString("md1"))
	require.False(t, wholeDeviceRe.MatchString("sda1"))
	require.False(t, wholeDeviceRe.MatchString("nvme0n1p1"))
	require.False(t, wholeDeviceRe.MatchString("md1p1"))
}

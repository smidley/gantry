package host

import (
	"os"
	"strings"
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

	require.Equal(t, diskCounters{
		readSectors: 4000000, writeSectors: 8000000,
		extended: true, reads: 123456, msReading: 45678,
		writes: 234567, msWriting: 56789,
		inFlight: 0, ioTicks: 12345, timeInQueue: 102467,
	}, counters["sda"])
	require.Equal(t, diskCounters{
		readSectors: 800000000, writeSectors: 900000000,
		extended: true, reads: 5000000, msReading: 12000,
		writes: 6000000, msWriting: 15000,
		inFlight: 0, ioTicks: 30000, timeInQueue: 27000,
	}, counters["nvme0n1"])
	require.Equal(t, diskCounters{
		readSectors: 640000000, writeSectors: 720000000,
		extended: true, reads: 2000000, msReading: 8000,
		writes: 3000000, msWriting: 9000,
		inFlight: 0, ioTicks: 18000, timeInQueue: 17000,
	}, counters["md1"])

	_, ok := counters["sda1"]
	require.False(t, ok, "partitions must be dropped")
	_, ok = counters["nvme0n1p1"]
	require.False(t, ok, "partitions must be dropped")
	require.Len(t, counters, 3)
}

// TestParseDiskstatsShortRowSkipsExtendedFields covers a pre-4.18 kernel:
// a whole-device row with only 12 of the modern 14 fields (missing
// io_ticks and time_in_queue) still yields the sector counts host.go
// needs for read_bps/write_bps, but leaves `extended` false so host.go
// never mistakes an absent field for a genuine zero sample.
func TestParseDiskstatsShortRowSkipsExtendedFields(t *testing.T) {
	const shortRow = "   8       0 sdb 1000 5 1000 100 2000 10 2000 200 0\n"
	counters, err := parseDiskstats(strings.NewReader(shortRow))
	require.NoError(t, err)

	require.Equal(t, diskCounters{readSectors: 1000, writeSectors: 2000}, counters["sdb"],
		"a 12-field row must parse throughput and leave every extended field unset")
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

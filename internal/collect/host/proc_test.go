package host

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestParseProcStat(t *testing.T) {
	total, perCore, err := parseProcStat(openFixture(t, "proc_stat.txt"))
	require.NoError(t, err)
	require.Equal(t, cpuTimes{user: 103000, nice: 5000, system: 20000, idle: 855000, iowait: 3000, irq: 0, softirq: 1200, steal: 0}, total)
	require.Len(t, perCore, 2)
	require.Equal(t, cpuTimes{user: 51000, nice: 2500, system: 10000, idle: 427000, iowait: 1500, irq: 0, softirq: 600, steal: 0}, perCore[0])
	require.Equal(t, cpuTimes{user: 52000, nice: 2500, system: 10000, idle: 428000, iowait: 1500, irq: 0, softirq: 600, steal: 0}, perCore[1])
}

func TestParseProcStatRejectsShortLine(t *testing.T) {
	_, _, err := parseProcStat(strings.NewReader("cpu  100 0 50\n"))
	require.Error(t, err)
}

func TestParseProcStatRejectsMissingAggregate(t *testing.T) {
	_, _, err := parseProcStat(strings.NewReader("cpu0 500 0 250 4000 250 0 0 0\n"))
	require.Error(t, err)
}

func TestParseMeminfo(t *testing.T) {
	memTotal, memAvailable, swapTotal, swapFree, err := parseMeminfo(openFixture(t, "meminfo.txt"))
	require.NoError(t, err)
	require.Equal(t, uint64(16777216), memTotal)
	require.Equal(t, uint64(9437184), memAvailable)
	require.Equal(t, uint64(8388608), swapTotal)
	require.Equal(t, uint64(8388608), swapFree)
}

func TestParseLoadavg(t *testing.T) {
	load1, err := parseLoadavg(openFixture(t, "loadavg.txt"))
	require.NoError(t, err)
	require.InDelta(t, 0.52, load1, 1e-9)
}

func TestParseUptime(t *testing.T) {
	uptime, err := parseUptime(strings.NewReader("12345.67 9999.99\n"))
	require.NoError(t, err)
	require.InDelta(t, 12345.67, uptime, 1e-9)
}

func TestParseArcstats(t *testing.T) {
	size, ok := parseArcstats(openFixture(t, "arcstats.txt"))
	require.True(t, ok)
	require.Equal(t, uint64(8589934592), size)
}

func TestParseArcstatsMissingSizeLine(t *testing.T) {
	_, ok := parseArcstats(strings.NewReader("name type data\nhits 4 123\n"))
	require.False(t, ok)
}

func TestCPUBusyPct(t *testing.T) {
	prev := cpuTimes{user: 1000, system: 500, idle: 8000, iowait: 500}
	cur := cpuTimes{user: 1100, system: 550, idle: 8200, iowait: 550}
	pct, ok := cpuBusyPct(prev, cur)
	require.True(t, ok)
	require.InDelta(t, 37.5, pct, 1e-9)
}

func TestCPUBusyPctFirstSampleOrResetIsFalse(t *testing.T) {
	same := cpuTimes{user: 1000, idle: 500}
	_, ok := cpuBusyPct(same, same)
	require.False(t, ok, "zero delta must not divide by zero")

	newer := cpuTimes{user: 100, idle: 50}
	older := cpuTimes{user: 1000, idle: 500}
	_, ok = cpuBusyPct(older, newer)
	require.False(t, ok, "counter reset must report false")
}

// TestCPUIowaitPct uses the same two snapshots TestCPUBusyPct does
// (prev/cur here match its prev/cur exactly: delta-total=400,
// delta-iowait=50) so the two metrics' shared fixture story stays
// obviously consistent: cpu.total's 37.5% busy and cpu.iowait_pct's
// 12.5% iowait are two views of the same underlying delta.
func TestCPUIowaitPct(t *testing.T) {
	prev := cpuTimes{user: 1000, system: 500, idle: 8000, iowait: 500}
	cur := cpuTimes{user: 1100, system: 550, idle: 8200, iowait: 550}
	pct, ok := cpuIowaitPct(prev, cur)
	require.True(t, ok)
	require.InDelta(t, 12.5, pct, 1e-9)
}

func TestCPUIowaitPctFirstSampleOrResetIsFalse(t *testing.T) {
	same := cpuTimes{user: 1000, idle: 500, iowait: 100}
	_, ok := cpuIowaitPct(same, same)
	require.False(t, ok, "zero delta must not divide by zero")

	newer := cpuTimes{user: 100, idle: 50, iowait: 10}
	older := cpuTimes{user: 1000, idle: 500, iowait: 100}
	_, ok = cpuIowaitPct(older, newer)
	require.False(t, ok, "counter reset must report false")
}

// TestCPUIowaitPctNegativeDeltaIsFalse pins that a regressed iowait counter reports false, never negative.
func TestCPUIowaitPctNegativeDeltaIsFalse(t *testing.T) {
	prev := cpuTimes{user: 1000, idle: 500, iowait: 600}
	cur := cpuTimes{user: 1100, idle: 550, iowait: 550} // total advances, iowait itself regresses
	pct, ok := cpuIowaitPct(prev, cur)
	require.False(t, ok, "a regressed iowait counter must not surface as a negative gauge")
	require.Equal(t, 0.0, pct)
}

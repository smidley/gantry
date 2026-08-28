package selfstat

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeSink struct {
	records map[store.SeriesKey]float64
}

func newFakeSink() *fakeSink { return &fakeSink{records: make(map[store.SeriesKey]float64)} }

func (f *fakeSink) Record(key store.SeriesKey, ts int64, val float64) {
	f.records[key] = val
}

func (f *fakeSink) value(metric string) (float64, bool) {
	v, ok := f.records[store.SeriesKey{Kind: "host", Metric: metric}]
	return v, ok
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "self"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "self", name), []byte(content), 0o644))
}

// statLine builds a minimal-but-realistic /proc/<pid>/stat line: pid,
// "(comm)", then 20 space-separated fields starting at state (field 3),
// with utime/stime (fields 14/15) set to the given jiffy counts.
func statLine(comm string, utime, stime uint64) string {
	// index:     0(f3) 1(f4) 2(f5) 3(f6) 4(f7) 5(f8) 6(f9)     7(f10) 8(f11) 9(f12) 10(f13) 11(f14) 12(f15) 13(f16) 14(f17) 15(f18) 16(f19) 17(f20) 18(f21) 19(f22)
	return "1 (" + comm + ") S 0 1 1 0 -1 4194560 100 0 0 0 " +
		strconv.FormatUint(utime, 10) + " " + strconv.FormatUint(stime, 10) + " 0 0 20 0 8 0 12345\n"
}

func TestSelfstatNameIntervalAndProbe(t *testing.T) {
	c := New(newFakeSink(), t.TempDir())
	require.Equal(t, "selfstat", c.Name())
	require.Equal(t, 10*time.Second, c.Interval())
	require.True(t, c.Probe(context.Background()).Available, "selfstat is always available")
}

func TestSelfstatProbeAvailableEvenWithoutProcRoot(t *testing.T) {
	c := New(newFakeSink(), filepath.Join(t.TempDir(), "does-not-exist"))
	require.True(t, c.Probe(context.Background()).Available)
}

func TestParseSelfStatExtractsUtimeAndStime(t *testing.T) {
	utime, stime, err := parseSelfStat(statLine("gantry", 100, 100))
	require.NoError(t, err)
	require.Equal(t, uint64(100), utime)
	require.Equal(t, uint64(100), stime)
}

// The comm field is "(...)" and may itself contain a space (a renamed
// binary) — a naive strings.Fields(line) split would misalign every
// field after it. parseSelfStat must split only after the LAST ')'.
func TestParseSelfStatToleratesSpaceInCommField(t *testing.T) {
	utime, stime, err := parseSelfStat(statLine("gantry helper", 700, 450))
	require.NoError(t, err)
	require.Equal(t, uint64(700), utime)
	require.Equal(t, uint64(450), stime)
}

func TestParseSelfStatErrorsWithoutClosingParen(t *testing.T) {
	_, _, err := parseSelfStat("1 (gantry S 0 1\n")
	require.Error(t, err)
}

func TestParseSelfStatErrorsWhenTooShortAfterComm(t *testing.T) {
	_, _, err := parseSelfStat("1 (gantry) S 0 1\n")
	require.Error(t, err)
}

func TestParseSelfStatmExtractsResidentPages(t *testing.T) {
	rss, err := parseSelfStatm("50000 12345 8000 100 0 45000 0\n")
	require.NoError(t, err)
	require.Equal(t, uint64(12345), rss)
}

func TestParseSelfStatmErrorsOnShortLine(t *testing.T) {
	_, err := parseSelfStatm("50000\n")
	require.Error(t, err)
}

func TestSelfstatTickFirstTickEmitsOnlyRSSGauge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statLine("gantry", 100, 100))
	writeFile(t, dir, "statm", "10000 1000 500 100 0 9000 0\n")

	sink := newFakeSink()
	c := New(sink, dir)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	_, ok := sink.value("gantry.cpu_pct")
	require.False(t, ok, "first tick must not emit a CPU rate")

	rss, ok := sink.value("gantry.rss_bytes")
	require.True(t, ok, "first tick must still emit the RSS gauge")
	require.Equal(t, 1000.0*4096, rss)
}

func TestSelfstatTickComputesCPUPctAcrossTwoTicks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statLine("gantry", 100, 100)) // 200 jiffies = 2.0s
	writeFile(t, dir, "statm", "10000 1000 500 100 0 9000 0\n")

	sink := newFakeSink()
	c := New(sink, dir)
	c.HostCores = func() int { return 8 }
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "stat", statLine("gantry", 250, 150)) // 400 jiffies = 4.0s (+2.0s over 2s wall)
	writeFile(t, dir, "statm", "10000 1200 500 100 0 9000 0\n")
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	pct, ok := sink.value("gantry.cpu_pct")
	require.True(t, ok)
	require.InDelta(t, 12.5, pct, 1e-9) // one full core (2.0s CPU / 2s wall) is 12.5% of an 8-core host

	rss, ok := sink.value("gantry.rss_bytes")
	require.True(t, ok)
	require.Equal(t, 1200.0*4096, rss)
}

// TestSelfstatTickFallsBackToNumCPUWhenHostCoresZero mirrors the docker
// collector's own fallback (cgroupv2.go's recordContainerStats): an unset
// HostCores must not leave gantry.cpu_pct blank, since it's the one number
// the Settings page's footprint receipt always needs.
func TestSelfstatTickFallsBackToNumCPUWhenHostCoresZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statLine("gantry", 100, 100)) // 200 jiffies = 2.0s

	sink := newFakeSink()
	c := New(sink, dir) // HostCores left at its default-0 stub
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	writeFile(t, dir, "stat", statLine("gantry", 250, 150)) // +2.0s over 2s wall = one full core
	require.NoError(t, c.Tick(context.Background(), time.Unix(1002, 0)))

	pct, ok := sink.value("gantry.cpu_pct")
	require.True(t, ok, "gantry.cpu_pct must fall back to runtime.NumCPU() rather than go blank")
	require.InDelta(t, 100.0/float64(runtime.NumCPU()), pct, 1e-9)
}

func TestSelfstatTickErrorsWhenStatMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "statm", "10000 1000 500 100 0 9000 0\n")

	c := New(newFakeSink(), dir)
	require.Error(t, c.Tick(context.Background(), time.Unix(1000, 0)))
}

func TestSelfstatTickToleratesMissingStatm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stat", statLine("gantry", 100, 100)) // statm intentionally not written

	sink := newFakeSink()
	c := New(sink, dir)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	_, ok := sink.value("gantry.rss_bytes")
	require.False(t, ok)
}

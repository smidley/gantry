package pressure

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeSink captures every recorded sample, keyed by its full SeriesKey
// (last write wins per key). This collector writes both "host" and
// "container" kind series, so value() takes Kind explicitly.
type fakeSink struct {
	records map[store.SeriesKey]float64
}

func newFakeSink() *fakeSink { return &fakeSink{records: make(map[store.SeriesKey]float64)} }

func (f *fakeSink) Record(key store.SeriesKey, ts int64, val float64) {
	f.records[key] = val
}

func (f *fakeSink) value(kind, entity, metric string) (float64, bool) {
	v, ok := f.records[store.SeriesKey{Kind: kind, Entity: entity, Metric: metric}]
	return v, ok
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestParseSomeLineExtractsAvg10AndIgnoresFullLine(t *testing.T) {
	val, ok := parseSomeLine("some avg10=0.00 avg60=1.11 avg300=2.22 total=12345678")
	require.True(t, ok)
	require.InDelta(t, 0.00, val, 1e-9)

	val, ok = parseSomeLine("some avg10=1.23 avg60=4.56 avg300=7.89 total=12345678")
	require.True(t, ok)
	require.InDelta(t, 1.23, val, 1e-9)

	_, ok = parseSomeLine("full avg10=9.99 avg60=8.88 avg300=7.77 total=87654321")
	require.False(t, ok, "the full line must be ignored, not parsed as if it were some")
}

func TestReadSomeAvg10ReadsOnlyTheFirstLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "io")
	writeFile(t, path, "some avg10=0.00 avg60=1.11 avg300=2.22 total=12345678\n"+
		"full avg10=9.99 avg60=8.88 avg300=7.77 total=87654321\n")

	val, ok := readSomeAvg10(path)
	require.True(t, ok)
	require.InDelta(t, 0.00, val, 1e-9)
}

func TestReadSomeAvg10MissingFile(t *testing.T) {
	_, ok := readSomeAvg10(filepath.Join(t.TempDir(), "does-not-exist"))
	require.False(t, ok)
}

func TestPressureNameAndInterval(t *testing.T) {
	c := New(newFakeSink(), t.TempDir(), t.TempDir(), func() []docker.Meta { return nil })
	require.Equal(t, "pressure", c.Name())
	require.Equal(t, 2*time.Second, c.Interval())
}

func TestProbeAvailableIffPressureIoExists(t *testing.T) {
	procRoot := t.TempDir()
	c := New(newFakeSink(), procRoot, t.TempDir(), func() []docker.Meta { return nil })

	status := c.Probe(context.Background())
	require.False(t, status.Available)
	require.Equal(t, "PSI disabled — add psi=1 to the syslinux append line to enable (optional; used by insights)", status.Detail)

	writeFile(t, filepath.Join(procRoot, "pressure", "io"), "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	require.True(t, c.Probe(context.Background()).Available)
}

func TestTickRecordsHostAndContainerPSIAndSkipsMissingResourcesSilently(t *testing.T) {
	procRoot := t.TempDir()
	cgroupRoot := t.TempDir()

	writeFile(t, filepath.Join(procRoot, "pressure", "cpu"), "some avg10=1.50 avg60=1.00 avg300=0.50 total=100\n")
	writeFile(t, filepath.Join(procRoot, "pressure", "io"), "some avg10=2.50 avg60=2.00 avg300=1.50 total=200\n")
	writeFile(t, filepath.Join(procRoot, "pressure", "memory"), "some avg10=3.50 avg60=3.00 avg300=2.50 total=300\n")

	containerDir := filepath.Join(cgroupRoot, "docker", "abc123")
	writeFile(t, filepath.Join(containerDir, "cpu.pressure"), "some avg10=4.40 avg60=0 avg300=0 total=0\n")
	writeFile(t, filepath.Join(containerDir, "io.pressure"), "some avg10=5.50 avg60=0 avg300=0 total=0\n")
	// memory.pressure deliberately absent: partial PSI must not error the tick.

	sink := newFakeSink()
	c := New(sink, procRoot, cgroupRoot, func() []docker.Meta {
		return []docker.Meta{{ID: "abc123", Name: "jellyfin", State: "running"}}
	})

	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	cpuPct, ok := sink.value("host", "", "psi.cpu.some_pct")
	require.True(t, ok)
	require.InDelta(t, 1.50, cpuPct, 1e-9)
	ioPct, ok := sink.value("host", "", "psi.io.some_pct")
	require.True(t, ok)
	require.InDelta(t, 2.50, ioPct, 1e-9)
	memPct, ok := sink.value("host", "", "psi.mem.some_pct")
	require.True(t, ok)
	require.InDelta(t, 3.50, memPct, 1e-9)

	cCPU, ok := sink.value("container", "jellyfin", "psi.cpu.some_pct")
	require.True(t, ok)
	require.InDelta(t, 4.40, cCPU, 1e-9)
	cIO, ok := sink.value("container", "jellyfin", "psi.io.some_pct")
	require.True(t, ok)
	require.InDelta(t, 5.50, cIO, 1e-9)
	_, ok = sink.value("container", "jellyfin", "psi.mem.some_pct")
	require.False(t, ok, "a missing memory.pressure file must be skipped silently, not error the tick")
}

func TestTickSkipsContainerWithNoPressureFilesAtAll(t *testing.T) {
	procRoot := t.TempDir()
	cgroupRoot := t.TempDir() // no docker/<id> dirs at all

	sink := newFakeSink()
	c := New(sink, procRoot, cgroupRoot, func() []docker.Meta {
		return []docker.Meta{{ID: "abc123", Name: "jellyfin", State: "running"}}
	})

	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))
	require.Empty(t, sink.records)
}

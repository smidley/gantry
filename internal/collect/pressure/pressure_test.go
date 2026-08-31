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

func TestParseLineExtractsAvg10ForTheRequestedLineKindOnly(t *testing.T) {
	val, ok := parseLine("some avg10=1.23 avg60=4.56 avg300=7.89 total=12345678", "some")
	require.True(t, ok)
	require.InDelta(t, 1.23, val, 1e-9)

	_, ok = parseLine("some avg10=1.23 avg60=4.56 avg300=7.89 total=12345678", "full")
	require.False(t, ok, "a some line must not satisfy a full request")

	val, ok = parseLine("full avg10=9.99 avg60=8.88 avg300=7.77 total=87654321", "full")
	require.True(t, ok)
	require.InDelta(t, 9.99, val, 1e-9)

	_, ok = parseLine("full avg10=9.99 avg60=8.88 avg300=7.77 total=87654321", "some")
	require.False(t, ok, "a full line must not satisfy a some request")
}

func TestReadLineAvg10FindsTheRequestedLineRegardlessOfOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "io")
	writeFile(t, path, "some avg10=1.11 avg60=0 avg300=0 total=0\n"+
		"full avg10=2.22 avg60=0 avg300=0 total=0\n")

	val, ok := readLineAvg10(path, "some")
	require.True(t, ok)
	require.InDelta(t, 1.11, val, 1e-9)

	val, ok = readLineAvg10(path, "full")
	require.True(t, ok)
	require.InDelta(t, 2.22, val, 1e-9)
}

func TestReadLineAvg10ReturnsFalseWhenTheLineIsAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cpu")
	// Mirrors /proc/pressure/cpu at the host level: only "some", never "full".
	writeFile(t, path, "some avg10=1.50 avg60=0 avg300=0 total=0\n")

	val, ok := readLineAvg10(path, "some")
	require.True(t, ok)
	require.InDelta(t, 1.50, val, 1e-9)

	_, ok = readLineAvg10(path, "full")
	require.False(t, ok, "a missing full line must not be reported as a zero")
}

func TestReadLineAvg10MissingFile(t *testing.T) {
	_, ok := readLineAvg10(filepath.Join(t.TempDir(), "does-not-exist"), "some")
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

// TestDocsPsiMdQuotesTheProbeDetailStringExactly pins docs/psi.md's own
// quote of this hint against psiDisabledDetail itself, so a future edit
// to either one that lets them drift apart fails a test instead of
// quietly shipping a doc that no longer matches what the UI shows.
func TestDocsPsiMdQuotesTheProbeDetailStringExactly(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "psi.md"))
	require.NoError(t, err)
	require.Contains(t, string(content), psiDisabledDetail)
}

func TestTickRecordsHostAndContainerSomePctAndSkipsMissingResourcesSilently(t *testing.T) {
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

	// None of the fixtures above have a "full" line (a cgroup/host file
	// with only a "some" line), so no full_pct series exists anywhere --
	// one series recorded per file, not two.
	for _, metric := range []string{"psi.cpu.full_pct", "psi.io.full_pct", "psi.mem.full_pct"} {
		_, ok := sink.value("host", "", metric)
		require.False(t, ok, "no full line in the fixture must mean no %s series", metric)
		_, ok = sink.value("container", "jellyfin", metric)
		require.False(t, ok, "no full line in the fixture must mean no %s series", metric)
	}
}

func TestTickRecordsFullPctWhenTheKernelExposesItAndOmitsItWhenAbsent(t *testing.T) {
	procRoot := t.TempDir()
	cgroupRoot := t.TempDir()

	// io and memory carry both lines at the host level; cpu carries only
	// "some" -- the real /proc/pressure/cpu never emits a host-level
	// "full" line (see the Tick doc comment): the host's idle task is
	// always eligible to run, so the CPU can never be fully stalled
	// system-wide.
	writeFile(t, filepath.Join(procRoot, "pressure", "cpu"), "some avg10=1.50 avg60=0 avg300=0 total=0\n")
	writeFile(t, filepath.Join(procRoot, "pressure", "io"),
		"some avg10=2.50 avg60=0 avg300=0 total=0\nfull avg10=0.75 avg60=0 avg300=0 total=0\n")
	writeFile(t, filepath.Join(procRoot, "pressure", "memory"),
		"some avg10=3.50 avg60=0 avg300=0 total=0\nfull avg10=1.25 avg60=0 avg300=0 total=0\n")

	containerDir := filepath.Join(cgroupRoot, "docker", "abc123")
	writeFile(t, filepath.Join(containerDir, "io.pressure"),
		"some avg10=5.50 avg60=0 avg300=0 total=0\nfull avg10=0.10 avg60=0 avg300=0 total=0\n")

	sink := newFakeSink()
	c := New(sink, procRoot, cgroupRoot, func() []docker.Meta {
		return []docker.Meta{{ID: "abc123", Name: "jellyfin", State: "running"}}
	})

	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	_, ok := sink.value("host", "", "psi.cpu.full_pct")
	require.False(t, ok, "the host never has a cpu full line")

	ioFull, ok := sink.value("host", "", "psi.io.full_pct")
	require.True(t, ok)
	require.InDelta(t, 0.75, ioFull, 1e-9)

	memFull, ok := sink.value("host", "", "psi.mem.full_pct")
	require.True(t, ok)
	require.InDelta(t, 1.25, memFull, 1e-9)

	cIOFull, ok := sink.value("container", "jellyfin", "psi.io.full_pct")
	require.True(t, ok)
	require.InDelta(t, 0.10, cIOFull, 1e-9)
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

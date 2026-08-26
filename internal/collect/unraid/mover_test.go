package unraid

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func writeComm(t *testing.T, procRoot, pid, content string) {
	t.Helper()
	dir := filepath.Join(procRoot, pid)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(content), 0o644))
}

func TestMoverRunningTrueWhenSomeCommIsExactlyMover(t *testing.T) {
	procRoot := t.TempDir()
	writeComm(t, procRoot, "123", "bash\n")
	writeComm(t, procRoot, "456", "mover\n")
	require.True(t, moverRunning(procRoot))
}

func TestMoverRunningFalseWhenNoCommMatches(t *testing.T) {
	procRoot := t.TempDir()
	writeComm(t, procRoot, "123", "bash\n")
	writeComm(t, procRoot, "456", "sshd\n")
	require.False(t, moverRunning(procRoot))
}

func TestMoverRunningRejectsPartialMatch(t *testing.T) {
	procRoot := t.TempDir()
	writeComm(t, procRoot, "123", "movery\n")
	require.False(t, moverRunning(procRoot), `comm must match "mover" exactly, not as a substring`)
}

func TestMoverRunningFalseOnEmptyProcRoot(t *testing.T) {
	require.False(t, moverRunning(t.TempDir()))
}

func TestTickMoverRecordsRunningGauge(t *testing.T) {
	procRoot := t.TempDir()
	writeComm(t, procRoot, "456", "mover\n")

	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, t.TempDir(), procRoot)
	c.tickMover(time.Unix(1000, 0))

	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "mover.running"}], 1e-9)
}

func TestTickMoverRecordsNotRunningGauge(t *testing.T) {
	procRoot := t.TempDir()
	writeComm(t, procRoot, "456", "bash\n")

	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, t.TempDir(), procRoot)
	c.tickMover(time.Unix(1000, 0))

	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "mover.running"}], 1e-9)
}

func TestTickUPSEmitsChargeAndLoadWhenPresent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ups.ini"), []byte(`battery.charge="87"
ups.load="42"
`), 0o644))

	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())
	c.tickUPS(time.Unix(1000, 0))

	require.InDelta(t, 87, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "ups.charge_pct"}], 1e-9)
	require.InDelta(t, 42, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "ups.load_pct"}], 1e-9)
}

func TestTickUPSAbsentFileEmitsNothingAndNoError(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())
	c.tickUPS(time.Unix(1000, 0)) // no ups.ini at all
	require.Empty(t, sink.records)
}

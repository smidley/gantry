package unraid

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestTickDisksExactValuesAcrossSlots(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/disks.ini", filepath.Join(dir, "disks.ini"))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickDisks(time.Unix(1000, 0))

	// parity: present, fsSize is 0 so no fs metrics; temp/spun_up/errors present.
	require.InDelta(t, 32, sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "errors"}], 1e-9)
	_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "fs.used_bytes"}]
	require.False(t, ok, "parity has fsSize 0, so fs byte metrics must not be emitted")
	_, ok = sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "fs.free_bytes"}]
	require.False(t, ok)

	// disk1: normal present disk with full fs math.
	require.InDelta(t, 31, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "errors"}], 1e-9)
	require.InDelta(t, 6144000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.used_bytes"}], 1e-9)
	require.InDelta(t, 4096000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.free_bytes"}], 1e-9)

	// disk2: spun down, temp "*" -- absence is the signal, not a 0 reading.
	_, ok = sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "temp.c"}]
	require.False(t, ok, "a spun-down disk's temp is \"*\" and must be omitted, never emitted as 0")
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 2, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "errors"}], 1e-9)
	require.InDelta(t, 4096000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "fs.used_bytes"}], 1e-9)
	require.InDelta(t, 6144000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "fs.free_bytes"}], 1e-9)

	// disk3: DISK_NP (empty slot) -- must emit nothing at all.
	for _, metric := range []string{"temp.c", "spun_up", "errors", "fs.used_bytes", "fs.free_bytes"} {
		_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "disk3", Metric: metric}]
		require.False(t, ok, "DISK_NP slot disk3 must emit nothing for metric %s", metric)
	}

	// cache: normal present disk, different fs numbers.
	require.InDelta(t, 38, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 1536000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "fs.used_bytes"}], 1e-9)
	require.InDelta(t, 512000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "fs.free_bytes"}], 1e-9)
}

func TestTickDisksErrorIncreaseEmitsAlertButFirstSightAndUnchangedDoNot(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	events := &fakeEvents{}
	c := New(sink, events, dir, t.TempDir())

	writeDiskINI(t, dir, "disk1", "2")
	c.tickDisks(time.Unix(1000, 0))
	require.Empty(t, events.events, "first sight of a slot's error count must not fire an event")

	writeDiskINI(t, dir, "disk1", "2")
	c.tickDisks(time.Unix(1015, 0))
	require.Empty(t, events.events, "an unchanged error count must not fire an event")

	writeDiskINI(t, dir, "disk1", "5")
	c.tickDisks(time.Unix(1030, 0))
	require.Equal(t, []store.Event{
		{Kind: "disk.errors", Entity: "disk1", Severity: "alert", Detail: "errors 2 → 5"},
	}, events.events)
}

func writeDiskINI(t *testing.T, dir, slot, numErrors string) {
	t.Helper()
	content := `["` + slot + `"]
name="` + slot + `"
device="sdc"
status="DISK_OK"
temp="30"
numErrors="` + numErrors + `"
sizeSb="11718885324"
fsSize="0"
fsFree="0"
spundown="0"
rotational="1"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "disks.ini"), []byte(content), 0o644))
}

func TestTickDisksMissingFileDegradesSilently(t *testing.T) {
	dir := t.TempDir()
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())
	c.tickDisks(time.Unix(1000, 0)) // no disks.ini at all
	require.Empty(t, sink.records)
}

// TestCollectorTickWiresEveryFileTogether proves the full Collector.Tick
// (not just each tickX in isolation) reads var.ini, disks.ini,
// shares.ini, and the mover's /proc entry together on one pass — one
// representative metric from each source is enough to catch a dropped
// wiring call; per-file exactness is already covered by the other tests
// in this package.
func TestCollectorTickWiresEveryFileTogether(t *testing.T) {
	dir := t.TempDir()
	procRoot := t.TempDir()
	copyFixture(t, "testdata/var_started.ini", filepath.Join(dir, "var.ini"))
	copyFixture(t, "testdata/disks.ini", filepath.Join(dir, "disks.ini"))
	copyFixture(t, "testdata/shares.ini", filepath.Join(dir, "shares.ini"))
	writeComm(t, procRoot, "789", "mover\n")

	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, procRoot)
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	require.Equal(t, "7.3.2", c.Version(), "var.ini must have been read")
	require.InDelta(t, 31, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}], 1e-9, "disks.ini must have been read")
	require.InDelta(t, 1073741824, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "share.appdata.used_bytes"}], 1e-9, "shares.ini must have been read")
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "mover.running"}], 1e-9, "the mover's /proc entry must have been read")
}

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

// TestDiskKind pins the pure classification rule Scott's own report
// drove (a live box misread its boot flash device as HDD and its NVMe
// cache pools as generic SSD): slot name wins outright for the boot
// device, then device-name prefix, then rotational as the last resort.
func TestDiskKind(t *testing.T) {
	cases := []struct {
		name         string
		slot, device string
		rotational   float64
		rotationalOK bool
		want         string
	}{
		{"boot flash device always classifies usb, despite reporting rotational=1 and a plain scsi-style device name", "flash", "sdi", 1, true, "usb"},
		{"an nvme pool member classifies nvme regardless of its own slot name", "cache", "nvme1n1", 0, true, "nvme"},
		{"a second, differently-named nvme pool classifies nvme the same way", "rocket_pool", "nvme0n1", 0, true, "nvme"},
		{"a plain SATA/SAS solid-state disk (rotational=0, non-nvme device) classifies ssd", "cache", "sdh", 0, true, "ssd"},
		{"a spinning data disk classifies hdd", "disk1", "sdg", 1, true, "hdd"},
		{"an unparseable rotational reading falls back to hdd, the safe default", "disk1", "sdg", 0, false, "hdd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, DiskKind(tc.slot, tc.device, tc.rotational, tc.rotationalOK))
		})
	}
}

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
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "rotational"}], 1e-9)
	_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "fs.used_bytes"}]
	require.False(t, ok, "parity has fsSize 0, so fs byte metrics must not be emitted")
	_, ok = sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: "fs.free_bytes"}]
	require.False(t, ok)

	// disk1: normal present disk with full fs math.
	require.InDelta(t, 31, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "errors"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 6144000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.used_bytes"}], 1e-9)
	require.InDelta(t, 4096000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.free_bytes"}], 1e-9)

	// disk2: spun down, temp "*" -- absence is the signal, not a 0 reading.
	// rotational is a static hardware property, unrelated to spin state, so
	// it must still be recorded even while spun down.
	_, ok = sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "temp.c"}]
	require.False(t, ok, "a spun-down disk's temp is \"*\" and must be omitted, never emitted as 0")
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 2, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "errors"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 4096000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "fs.used_bytes"}], 1e-9)
	require.InDelta(t, 6144000000, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk2", Metric: "fs.free_bytes"}], 1e-9)

	// disk3: DISK_NP (empty slot) -- must emit nothing at all.
	for _, metric := range []string{"temp.c", "spun_up", "errors", "rotational", "fs.used_bytes", "fs.free_bytes"} {
		_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "disk3", Metric: metric}]
		require.False(t, ok, "DISK_NP slot disk3 must emit nothing for metric %s", metric)
	}

	// cache: normal present disk, different fs numbers, solid-state (rotational=0).
	require.InDelta(t, 38, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "rotational"}], 1e-9)
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

// TestTickDisksDiskNPDsblStatusTreatedAsAbsent is driven by a shape found
// on a real Unraid 7.3.2 box: a parity slot with no parity disk assigned
// reports status "DISK_NP_DSBL", not the bare "DISK_NP" an empty data
// slot uses -- both must be treated as absent.
func TestTickDisksDiskNPDsblStatusTreatedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	content := `["parity"]
idx="0"
name="parity"
device=""
id=""
spundown="0"
status="DISK_NP_DSBL"
temp="*"
numErrors="0"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "disks.ini"), []byte(content), 0o644))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickDisks(time.Unix(1000, 0))

	require.Empty(t, sink.records, "a DISK_NP_DSBL slot must emit nothing, same as DISK_NP")
}

// TestTickDisksFsUsedBytesPrefersAuthoritativeFsUsedOverDerived is driven
// by a real Unraid 7.3.2 btrfs cache pool where fsUsed is smaller than
// fsSize-fsFree (btrfs free-space accounting doesn't subtract to the same
// figure fsUsed reports) -- fsUsed must win when present.
func TestTickDisksFsUsedBytesPrefersAuthoritativeFsUsedOverDerived(t *testing.T) {
	dir := t.TempDir()
	content := `["cache"]
name="cache"
status="DISK_OK"
spundown="0"
temp="36"
numErrors="0"
fsSize="976761560"
fsFree="364180176"
fsUsed="610420272"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "disks.ini"), []byte(content), 0o644))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickDisks(time.Unix(1000, 0))

	require.InDelta(t, 625070358528.0,
		sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "fs.used_bytes"}], 1e-6,
		"fs.used_bytes must come from fsUsed (real value); fsSize-fsFree would wrongly give 627283337216 here")
	require.InDelta(t, 372920500224.0,
		sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "fs.free_bytes"}], 1e-6)
}

// TestTickDisksRealCaptureFromLiveUnraidBox replays a trimmed, anonymized
// disks.ini captured from a live Unraid 7.3.2 box (see
// docs/superpowers/fixtures.md), exercising both real-shape fixes above
// together against the actual file shape rather than a hand-minimized
// reproduction.
func TestTickDisksRealCaptureFromLiveUnraidBox(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/disks_real.ini", filepath.Join(dir, "disks.ini"))
	sink := newFakeSink()
	c := New(sink, &fakeEvents{}, dir, t.TempDir())

	c.tickDisks(time.Unix(1000, 0))

	// parity: DISK_NP_DSBL on this real box (no active parity disk) -- must emit nothing.
	for _, metric := range []string{"temp.c", "spun_up", "errors", "rotational", "fs.used_bytes", "fs.free_bytes"} {
		_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "parity", Metric: metric}]
		require.False(t, ok, "DISK_NP_DSBL slot parity must emit nothing for metric %s", metric)
	}

	// disk1: present, xfs -- fsUsed equals fsSize-fsFree in reality, both formulas agree here.
	// Spinning (rotational=1), same as every array data disk on this box.
	require.InDelta(t, 38, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "errors"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 16375065784320.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.used_bytes"}], 1e-6)
	require.InDelta(t, 1623005102080.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "fs.free_bytes"}], 1e-6)

	// disk6: present, xfs, a different vendor/model and size class.
	require.InDelta(t, 34, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk6", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk6", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk6", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 24695444688896.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk6", Metric: "fs.used_bytes"}], 1e-6)
	require.InDelta(t, 1302864769024.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "disk6", Metric: "fs.free_bytes"}], 1e-6)

	// disk9: DISK_NP empty data slot -- must emit nothing.
	for _, metric := range []string{"temp.c", "spun_up", "errors", "rotational", "fs.used_bytes", "fs.free_bytes"} {
		_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "disk9", Metric: metric}]
		require.False(t, ok, "DISK_NP slot disk9 must emit nothing for metric %s", metric)
	}

	// cache: present, btrfs pool, nvme transport -- fsUsed diverges from
	// fsSize-fsFree (fsUsed must win) and rotational reads 0 (solid-state).
	require.InDelta(t, 36, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 625070358528.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "fs.used_bytes"}], 1e-6)
	require.InDelta(t, 372920500224.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "cache", Metric: "fs.free_bytes"}], 1e-6)

	// rocket_pool: a second, differently-named btrfs pool, also nvme --
	// proves the collector doesn't special-case the literal name "cache",
	// for rotational same as every other per-disk metric.
	require.InDelta(t, 44, sink.records[store.SeriesKey{Kind: "disk", Entity: "rocket_pool", Metric: "temp.c"}], 1e-9)
	require.InDelta(t, 0, sink.records[store.SeriesKey{Kind: "disk", Entity: "rocket_pool", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 545635610624.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "rocket_pool", Metric: "fs.used_bytes"}], 1e-6)
	require.InDelta(t, 449255231488.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "rocket_pool", Metric: "fs.free_bytes"}], 1e-6)

	// flash: boot device -- temp is "*" (no sensor) despite spundown=0 (not
	// a spin-down case at all, just nothing to report); spun_up still
	// records. This real USB thumb drive reports rotational=1 despite being
	// flash media -- relayed as-is, same as every other raw disks.ini value.
	_, ok := sink.records[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "temp.c"}]
	require.False(t, ok, `flash reports temp "*" (no sensor) and must omit temp.c`)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "spun_up"}], 1e-9)
	require.InDelta(t, 1, sink.records[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "rotational"}], 1e-9)
	require.InDelta(t, 3022356480.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "fs.used_bytes"}], 1e-6)
	require.InDelta(t, 58987806720.0, sink.records[store.SeriesKey{Kind: "disk", Entity: "flash", Metric: "fs.free_bytes"}], 1e-6)

	// DiskMeta: the whole point of this fixture (Scott's own report) --
	// flash must classify usb despite its rotational=1/plain-sdX shape
	// above, and BOTH nvme-transport pools must classify nvme even though
	// their rotational=0 reading alone can't tell them apart from a plain
	// SATA/SAS SSD. Absent slots (DISK_NP/DISK_NP_DSBL) get no metrics
	// above and must get no DiskMeta entry either.
	meta := c.DiskMeta()
	require.Equal(t, DiskMeta{Device: "sdi", Kind: "usb"}, meta["flash"])
	require.Equal(t, DiskMeta{Device: "nvme1n1", Kind: "nvme"}, meta["cache"])
	require.Equal(t, DiskMeta{Device: "nvme0n1", Kind: "nvme"}, meta["rocket_pool"])
	require.Equal(t, DiskMeta{Device: "sdg", Kind: "hdd"}, meta["disk1"])
	require.Equal(t, DiskMeta{Device: "sdd", Kind: "hdd"}, meta["disk6"])
	_, hasParity := meta["parity"]
	require.False(t, hasParity, "DISK_NP_DSBL slot parity must have no DiskMeta entry either")
	_, hasDisk9 := meta["disk9"]
	require.False(t, hasDisk9, "DISK_NP slot disk9 must have no DiskMeta entry either")
}

// TestSlotsBeforeFirstTick pins the pre-tick default: mirroring
// Version(), the pool slot accessor reports nothing known before
// disks.ini has ever been read successfully, rather than panicking on a
// nil slice.
func TestSlotsBeforeFirstTick(t *testing.T) {
	c := New(newFakeSink(), &fakeEvents{}, t.TempDir(), t.TempDir())
	require.Nil(t, c.Slots())
}

// TestSlotsClassifiesPoolSlotsFromRealFixture pins the fleet knowledge
// the path->storage resolver depends on: disks.ini's per-slot "type"
// field is the source of truth ("Cache" -> pool, everything else --
// "Data", "Parity", "Flash" included -- is not), not the slot's name --
// rocket_pool proves this isn't a hardcoded literal-"cache" special
// case.
func TestSlotsClassifiesPoolSlotsFromRealFixture(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/disks_real.ini", filepath.Join(dir, "disks.ini"))
	c := New(newFakeSink(), &fakeEvents{}, dir, t.TempDir())

	c.tickDisks(time.Unix(1000, 0))

	require.Equal(t, []string{"cache", "rocket_pool"}, c.Slots())
}

// TestSlotsIgnoresSlotsWithNoTypeField pins the simplified (non-real)
// disks.ini fixture's degrade path: none of its slots carry a "type"
// key at all, so nothing should be classified as a pool -- this must
// not panic on a missing key, and must not misclassify by falling back
// to the slot's name.
func TestSlotsIgnoresSlotsWithNoTypeField(t *testing.T) {
	dir := t.TempDir()
	copyFixture(t, "testdata/disks.ini", filepath.Join(dir, "disks.ini"))
	c := New(newFakeSink(), &fakeEvents{}, dir, t.TempDir())

	c.tickDisks(time.Unix(1000, 0))

	require.Nil(t, c.Slots())
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

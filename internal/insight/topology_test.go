package insight

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/smidley/gantry/internal/collect/unraid"
	"github.com/stretchr/testify/require"
)

// loadSlotMeta parses a real disks.ini capture (internal/collect/unraid's
// own testdata) into the map NewTopology wants, applying the same
// presence gate unraid.tickOneDisk applies (a "DISK_NP"-prefixed status
// is an empty slot, absent from DiskMeta) so these tests see exactly what
// the unraid collector would actually hand Topology in production.
func loadSlotMeta(t *testing.T, path string) map[string]SlotMeta {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	kv, err := unraid.ParseINI(f)
	require.NoError(t, err)

	out := make(map[string]SlotMeta)
	for slot, fields := range kv {
		if slot == "" || strings.HasPrefix(fields["status"], "DISK_NP") {
			continue
		}
		rot, err := strconv.ParseFloat(fields["rotational"], 64)
		require.NoError(t, err, "slot %s rotational", slot)
		out[slot] = SlotMeta{Device: fields["device"], Rotational: rot != 0}
	}
	return out
}

// TestTopologyResolveMapsKnownMajMinToSlot pins the basic join: a
// cgroup-reported major:minor resolves through the injected deviceName
// closure to a kernel name, then through the slot metadata to the
// Unraid slot that owns it.
func TestTopologyResolveMapsKnownMajMinToSlot(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks.ini")
	deviceName := func(majMin string) (string, bool) {
		if majMin == "8:32" {
			return "sdc", true // disk1's device in this fixture
		}
		return "", false
	}

	topo := NewTopology(deviceName, slots)
	dev, ok := topo.Resolve("8:32")

	require.True(t, ok)
	require.Equal(t, Device{Name: "sdc", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, dev)
}

// TestTopologyContended pins the parity exclusion at the type level:
// false only for RoleParity, true for every other role including
// RoleUnknown -- a device we don't understand is still a real device.
func TestTopologyContended(t *testing.T) {
	topo := NewTopology(nil, nil)

	require.False(t, topo.Contended(Device{Role: RoleParity}))
	require.True(t, topo.Contended(Device{Role: RoleData}))
	require.True(t, topo.Contended(Device{Role: RolePool}))
	require.True(t, topo.Contended(Device{Role: RoleFlash}))
	require.True(t, topo.Contended(Device{Role: RoleUnknown}))
}

func TestTopologyCanonicalCollapsesArrayMemberToMDDevice(t *testing.T) {
	topo := NewTopology(nil, nil)

	got := topo.Canonical(Device{Name: "sdc", Slot: "disk1", Role: RoleData, Rotational: true})

	require.Equal(t, Device{Name: "md1", Slot: "disk1", Role: RoleData, Rotational: true}, got)
}

func TestTopologyCanonicalLeavesPoolDeviceAlone(t *testing.T) {
	topo := NewTopology(nil, nil)
	d := Device{Name: "nvme0n1", Slot: "cache", Role: RolePool, Rotational: false}

	require.Equal(t, d, topo.Canonical(d))
}

// TestTopologyResolveUnknownDeviceDegradesToRoleUnknown pins the
// degrade-don't-drop contract: a kernel name that resolves fine but
// matches no known array/pool slot is still a real, contended device --
// just one Topology can't name by Unraid slot. Its Rotational reading is
// a map-lookup zero value rather than a real one, so RotationalKnown
// must say so -- otherwise an unplaced disk silently reads as an SSD.
func TestTopologyResolveUnknownDeviceDegradesToRoleUnknown(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks.ini")
	deviceName := func(majMin string) (string, bool) {
		if majMin == "259:3" {
			return "nvme2n1", true // a real kernel name, but no slot claims it
		}
		return "", false
	}

	topo := NewTopology(deviceName, slots)
	dev, ok := topo.Resolve("259:3")

	require.True(t, ok, "a device we don't understand is still a real device")
	require.Equal(t, Device{Name: "nvme2n1", Slot: "", Role: RoleUnknown, Rotational: false, RotationalKnown: false}, dev)
	require.True(t, topo.Contended(dev))
}

// TestTopologyResolveNamePlacedDiskReportsRotationalKnown is the
// positive counterpart, exercised through ResolveName: any name that
// does match a known slot always carries a real rotational reading, so
// RotationalKnown must be true.
func TestTopologyResolveNamePlacedDiskReportsRotationalKnown(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks.ini")
	topo := NewTopology(nil, slots)

	dev, ok := topo.ResolveName("nvme0n1")

	require.True(t, ok)
	require.True(t, dev.RotationalKnown)
}

// TestTopologyResolveTotallyUnresolvableMajMin covers the one case that
// legitimately returns ok=false: deviceName itself has no name at all
// for this major:minor (the diskstats row has never been seen).
func TestTopologyResolveTotallyUnresolvableMajMin(t *testing.T) {
	topo := NewTopology(func(string) (string, bool) { return "", false }, nil)

	_, ok := topo.Resolve("0:0")

	require.False(t, ok)
}

// TestTopologyParityExcludedFromContentionAgainstDisksIniCapture is the
// plan's #1 open-question resolution, exercised end to end against a
// real disks.ini capture with a present parity disk: parity resolves to
// RoleParity, is never Contended, and has no md wrapper to collapse onto
// (Canonical is a no-op for it) -- the load a parity write carries is
// folded into the array-write finding's evidence instead of being named
// as an independent contended resource.
func TestTopologyParityExcludedFromContentionAgainstDisksIniCapture(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks.ini")
	deviceName := func(majMin string) (string, bool) {
		if majMin == "8:16" {
			return "sdb", true // this fixture's present parity device
		}
		return "", false
	}

	topo := NewTopology(deviceName, slots)
	parity, ok := topo.Resolve("8:16")

	require.True(t, ok)
	require.Equal(t, Device{Name: "sdb", Slot: "parity", Role: RoleParity, Rotational: true, RotationalKnown: true}, parity)
	require.False(t, topo.Contended(parity),
		"every array write drives parity as a CONSEQUENCE of the data write -- naming it separately double-counts the same write")
	require.Equal(t, parity, topo.Canonical(parity), "parity has no md wrapper of its own to collapse onto")
}

// TestTopologyAgainstRealDisksIniCapture exercises Resolve/Contended/
// Canonical together against Scott's actual box capture
// (testdata/disks_real.ini), which is richer than the synthetic fixture:
// a disabled/absent parity, a "type=Cache" slot literally named "cache",
// a SECOND pool with an arbitrary custom name ("rocket_pool" -- Unraid
// does not restrict pool naming to "cache"), and a flash boot device
// that (per disks.go's own documented quirk) reports rotational=1 like a
// spinning disk.
func TestTopologyAgainstRealDisksIniCapture(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks_real.ini")
	names := map[string]string{
		"8:96":  "sdg",     // disk1
		"9:1":   "md1",     // disk1's own canonical array device, resolved directly
		"259:1": "nvme1n1", // cache
		"259:2": "nvme0n1", // rocket_pool
		"8:128": "sdi",     // flash
	}
	deviceName := func(majMin string) (string, bool) {
		name, ok := names[majMin]
		return name, ok
	}
	topo := NewTopology(deviceName, slots)

	disk1, ok := topo.Resolve("8:96")
	require.True(t, ok)
	require.Equal(t, Device{Name: "sdg", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, disk1)
	require.True(t, topo.Contended(disk1))
	require.Equal(t, Device{Name: "md1", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, topo.Canonical(disk1))

	// md1 resolved directly (a host-level diskstats row for the array
	// device itself) must land on the same slot as sdg: one logical
	// write, attributed once, whichever raw name a given sample carries.
	disk1ViaMD, ok := topo.Resolve("9:1")
	require.True(t, ok)
	require.Equal(t, Device{Name: "md1", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, disk1ViaMD)
	require.Equal(t, disk1ViaMD, topo.Canonical(disk1ViaMD), "md1 is already canonical")

	cache, ok := topo.Resolve("259:1")
	require.True(t, ok)
	require.Equal(t, Device{Name: "nvme1n1", Slot: "cache", Role: RolePool, Rotational: false, RotationalKnown: true}, cache)
	require.Equal(t, cache, topo.Canonical(cache))

	rocketPool, ok := topo.Resolve("259:2")
	require.True(t, ok)
	require.Equal(t, Device{Name: "nvme0n1", Slot: "rocket_pool", Role: RolePool, Rotational: false, RotationalKnown: true}, rocketPool,
		`a custom-named pool is still RolePool -- Unraid doesn't limit pool naming to "cache"`)

	flash, ok := topo.Resolve("8:128")
	require.True(t, ok)
	require.Equal(t, Device{Name: "sdi", Slot: "flash", Role: RoleFlash, Rotational: true, RotationalKnown: true}, flash,
		"the boot device reports rotational=1 like a spinning disk -- Kind classification is a separate concern from Role")
	require.True(t, topo.Contended(flash))

	_, present := slots["parity"]
	require.False(t, present, "this capture's parity is DISK_NP_DSBL (disabled), matching tickOneDisk's own presence gate")
}

// TestTopologyResolveNameMapsKnownNamesToSlot pins ResolveName as the
// name-keyed twin of Resolve: every series the engine actually reads is
// keyed by device name, not major:minor (host diskio.<name>.*, docker
// live:io.<slug(name)>.*), so this never goes through the
// deviceName(majMin) step -- nil deviceName here proves that.
func TestTopologyResolveNameMapsKnownNamesToSlot(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks.ini")
	topo := NewTopology(nil, slots)

	parity, ok := topo.ResolveName("sdb")
	require.True(t, ok)
	require.Equal(t, Device{Name: "sdb", Slot: "parity", Role: RoleParity, Rotational: true, RotationalKnown: true}, parity)

	disk1, ok := topo.ResolveName("sdc")
	require.True(t, ok)
	require.Equal(t, Device{Name: "sdc", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, disk1)

	cache, ok := topo.ResolveName("nvme0n1")
	require.True(t, ok)
	require.Equal(t, Device{Name: "nvme0n1", Slot: "cache", Role: RolePool, Rotational: false, RotationalKnown: true}, cache)
}

// TestTopologyResolveNameAgainstRealDisksIniCapture covers the roles
// disks.ini's synthetic fixture doesn't carry (flash, a second
// custom-named pool) plus the md-name choice this task pinned down:
// nameToSlot carries both a data slot's raw device and its "mdN"
// canonical alias to the same slot (see NewTopology), so
// ResolveName("md1") is not a special case -- it comes back Named "md1"
// simply because that's the name it was asked to resolve, already in
// the form Canonical would have produced from the raw device.
func TestTopologyResolveNameAgainstRealDisksIniCapture(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks_real.ini")
	topo := NewTopology(nil, slots)

	disk1, ok := topo.ResolveName("sdg")
	require.True(t, ok)
	require.Equal(t, Device{Name: "sdg", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, disk1)

	disk1ViaMD, ok := topo.ResolveName("md1")
	require.True(t, ok)
	require.Equal(t, Device{Name: "md1", Slot: "disk1", Role: RoleData, Rotational: true, RotationalKnown: true}, disk1ViaMD,
		"md1 is disk1's canonical array device -- ResolveName resolves it directly, matching Canonical's model")

	rocketPool, ok := topo.ResolveName("nvme0n1")
	require.True(t, ok)
	require.Equal(t, Device{Name: "nvme0n1", Slot: "rocket_pool", Role: RolePool, Rotational: false, RotationalKnown: true}, rocketPool)

	flash, ok := topo.ResolveName("sdi")
	require.True(t, ok)
	require.Equal(t, Device{Name: "sdi", Slot: "flash", Role: RoleFlash, Rotational: true, RotationalKnown: true}, flash)
}

// TestTopologyResolveNameUnknownNameReturnsFalse pins ResolveName's
// stricter contract against Resolve's: a name matching no known slot at
// all means ResolveName has nothing to say about it. Resolve can fall
// back to RoleUnknown because its prior deviceName(majMin) call already
// proved a real kernel device exists; ResolveName has no such proof for
// an arbitrary name, so an unmatched one comes back empty rather than a
// fabricated RoleUnknown device.
func TestTopologyResolveNameUnknownNameReturnsFalse(t *testing.T) {
	slots := loadSlotMeta(t, "../collect/unraid/testdata/disks.ini")
	topo := NewTopology(nil, slots)

	_, ok := topo.ResolveName("nvme2n1")

	require.False(t, ok)
}

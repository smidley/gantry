package unraid

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// diskNotPresent is the status-value prefix disks.ini uses for an empty
// array slot (observed real forms: "DISK_NP" and "DISK_NP_DSBL"). Every
// other status (DISK_OK today; disabled/invalid states in later Unraid
// versions) counts as present — per-disk metrics gate on "not DISK_NP",
// not on an allowlist of known-good statuses.
const diskNotPresent = "DISK_NP"

// DiskMeta is one present disk slot's device name and classified type
// (see DiskKind) — strings, so unlike rotational/temp/etc. they can't
// ride the numeric MetricSink. Collector.DiskMeta() exposes the latest
// tick's own map for the snapshot builder to merge into
// server.SnapshotDTO.DiskMeta, mirroring Version()'s identical "set on
// tick under mu, read via a copying getter" shape, for the same
// cross-goroutine reason (the snapshot builder runs on a different
// goroutine than Tick).
type DiskMeta struct {
	Device string
	Kind   string
}

// DiskKind classifies a present disk slot into the frontend's four-way
// type badge: "usb" for the boot flash device, "nvme" for an NVMe pool
// member, "ssd" for any other solid-state member, "hdd" for a spinning
// one. Exported and pure (no *Collector receiver) so both a real box's
// own tickOneDisk below and the fake generator's disk metadata share one
// tested rule.
//
// Neither signal alone is enough (Scott's own report, reproduced against
// testdata/disks_real.ini): the boot flash device reports rotational=1
// AND a plain SCSI-style device name ("sdi") — indistinguishable from a
// real spinning disk by either signal on its own — and an NVMe pool
// member reports rotational=0, the same as a plain SATA/SAS SSD; only
// the device name ("nvme0n1" vs "sdX") tells THOSE two apart. Order
// matters: the boot device's SLOT NAME ("flash", Unraid's own fixed,
// version-independent name for it) is checked first and wins outright,
// before either signal below ever looks at this slot's own device name
// or rotational value. An unparseable/absent rotational reading (should
// not happen for a present disk in practice) falls back to "hdd", the
// least alarming default.
func DiskKind(slot, device string, rotational float64, rotationalOK bool) string {
	if slot == "flash" {
		return "usb"
	}
	if strings.HasPrefix(device, "nvme") {
		return "nvme"
	}
	if rotationalOK && rotational == 0 {
		return "ssd"
	}
	return "hdd"
}

// tickDisks reads disks.ini and records per-present-disk stats, emitting
// a disk.errors event whenever a slot's error count has risen since the
// last tick it was seen. Missing/unreadable disks.ini degrades silently
// (no error) — it isn't Probe's hard dependency, only var.ini is.
func (c *Collector) tickDisks(now time.Time) {
	f, err := os.Open(filepath.Join(c.dir, "disks.ini"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	kv, err := ParseINI(f)
	if err != nil {
		return
	}

	slots := make([]string, 0, len(kv))
	for slot := range kv {
		if slot == "" {
			continue // disks.ini has no headerless keys; stay tolerant if it ever did
		}
		slots = append(slots, slot)
	}
	sort.Strings(slots)

	c.recordSlots(slots, kv)

	ts := now.Unix()
	for _, slot := range slots {
		c.tickOneDisk(slot, kv[slot], ts)
	}
}

// recordSlots classifies every slot by its "type" field ("Cache" -> pool;
// anything else -- "Data", "Parity", "Flash", absent, or unrecognized --
// is not a pool) and stores the list for Slots(). slots is already
// sorted, so the output list stays sorted too. Runs regardless of a
// slot's present/DISK_NP status: an empty bay is still a known part of
// the fleet, unlike tickOneDisk's metrics (which have nothing
// meaningful to report for a slot with no filesystem).
func (c *Collector) recordSlots(slots []string, kv map[string]map[string]string) {
	var pools []string
	for _, slot := range slots {
		if kv[slot]["type"] == "Cache" {
			pools = append(pools, slot)
		}
	}
	c.mu.Lock()
	c.poolSlots = pools
	c.mu.Unlock()
}

func (c *Collector) tickOneDisk(slot string, disk map[string]string, ts int64) {
	if strings.HasPrefix(disk["status"], diskNotPresent) {
		return
	}

	if spundown, ok := parseFloatOK(disk["spundown"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "spun_up"}, ts, 1-spundown)
	}

	// rotational/rotationalOK feed both the numeric metric below (recorded
	// only when parseable, same as every other metric here) and DiskKind's
	// classification just after (which needs to know when it's missing,
	// not merely treat an unparseable reading as some default value).
	rotational, rotationalOK := parseFloatOK(disk["rotational"])
	if rotationalOK {
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "rotational"}, ts, rotational)
	}

	device := disk["device"]
	c.mu.Lock()
	c.diskMeta[slot] = DiskMeta{Device: device, Kind: DiskKind(slot, device, rotational, rotationalOK)}
	c.mu.Unlock()

	// temp is a number, or the literal "*" when the disk is spun down or
	// its temp is otherwise unknown; parseFloatOK's failure on "*" is
	// exactly the signal we want — omit the sample entirely rather than
	// ever recording a misleading 0.
	if temp, ok := parseFloatOK(disk["temp"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "temp.c"}, ts, temp)
	}

	if numErrors, ok := parseFloatOK(disk["numErrors"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "errors"}, ts, numErrors)
		c.checkErrorIncrease(slot, numErrors)
	}

	// fsSize/fsFree are KB-unit strings and are "0" for a disk with no
	// filesystem view (parity); only emit the byte metrics when there's
	// an actual filesystem size to report, and only when both parsed.
	// fsUsed is authoritative when present (real btrfs pools were observed
	// to diverge from fsSize-fsFree); fall back to the subtraction for a
	// disks.ini that doesn't carry it.
	fsSize, sizeOK := parseFloatOK(disk["fsSize"])
	fsFree, freeOK := parseFloatOK(disk["fsFree"])
	if sizeOK && freeOK && fsSize > 0 {
		fsUsed, usedOK := parseFloatOK(disk["fsUsed"])
		if !usedOK {
			fsUsed = fsSize - fsFree
		}
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "fs.used_bytes"}, ts, fsUsed*1024)
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "fs.free_bytes"}, ts, fsFree*1024)

		// fs.used_pct: byte-identical to web/src/lib/disks.ts:diskUsagePct
		// (used/(used+free)*100) -- fsUsed/fsFree are still in their raw
		// KB unit here, but the ratio is scale-invariant, so there's no
		// need to redo this against the *1024 values just recorded above.
		// Persisted (no "live:" prefix) so the alert engine's
		// disk-usage-high rule and the Storage view's display band read
		// the exact same number the frontend used to derive only for
		// itself, client-side.
		if total := fsUsed + fsFree; total > 0 {
			c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "fs.used_pct"}, ts, fsUsed/total*100)
		}
	}
}

// checkErrorIncrease emits a disk.errors alert event the first time (per
// slot) numErrors is seen to have risen since the previous tick that slot
// was observed. A slot seen for the first time only seeds
// prevDiskErrors silently: its possibly-already-nonzero count reflects
// history from before this collector started, not something that just
// happened.
func (c *Collector) checkErrorIncrease(slot string, numErrors float64) {
	prev, seen := c.prevDiskErrors[slot]
	c.prevDiskErrors[slot] = numErrors
	if !seen || numErrors <= prev {
		return
	}
	_, err := c.events.AppendEvent(store.Event{
		Kind: "disk.errors", Entity: slot, Severity: "alert",
		Detail: fmt.Sprintf("errors %g → %g", prev, numErrors),
	})
	if err != nil {
		log.Printf("events: %v", err)
	}
}

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

	ts := now.Unix()
	for _, slot := range slots {
		c.tickOneDisk(slot, kv[slot], ts)
	}
}

func (c *Collector) tickOneDisk(slot string, disk map[string]string, ts int64) {
	if strings.HasPrefix(disk["status"], diskNotPresent) {
		return
	}

	if spundown, ok := parseFloatOK(disk["spundown"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "spun_up"}, ts, 1-spundown)
	}

	if rotational, ok := parseFloatOK(disk["rotational"]); ok {
		c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "rotational"}, ts, rotational)
	}

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

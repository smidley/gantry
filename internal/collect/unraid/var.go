package unraid

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// ArrayState is the array/parity state derived from var.ini's headerless
// (section "") keys.
type ArrayState struct {
	State          string  // mdState, e.g. "STARTED" | "STOPPED"
	ParityRunning  bool    // mdResyncPos > 0
	ParityProgress float64 // mdResyncPos / mdResyncSize * 100
	ParitySpeedBps float64 // (mdResyncDb / mdResyncDt) * 1024; 0 if Dt absent or 0
	Version        string
	Name           string  // var.ini's own NAME field -- the box's configured server name
	SyncErrs       float64 // sbSyncErrs: the last (or currently running) check's error count
	SyncStart      int64   // sbSynced: unix epoch the last check started, 0 if never run
	SyncFinish     int64   // sbSynced2: unix epoch the last check finished, 0 while running or never run
	SyncExit       int     // sbSyncExit: emhttp's own exit status for the last check
}

// interpretVar derives ArrayState from a parsed var.ini. mdResyncPos and
// mdResyncSize are both 1024-byte-block counts (emhttp's mdResync* units);
// ParityRunning is pos > 0, ParityProgress is pos/size*100 guarded
// against a zero size (0, not +Inf/NaN). ParitySpeedBps is derived from
// mdResyncDb (1KB blocks transferred) over mdResyncDt (seconds) — emhttp's
// resync-rate block convention — times 1024, guarded to 0 when Dt is
// absent or 0 the same way ParityProgress guards a zero size. There is no
// mdResyncSpeed key on a real Unraid box (see docs/superpowers/
// fixtures.md discrepancy 5) — it must not be read. Any missing/malformed
// numeric key defaults to 0 rather than erroring — var.ini has no
// "malformed content" error path, only absent-or-zero.
func interpretVar(kv map[string]map[string]string) ArrayState {
	v := kv[""]

	pos, _ := parseFloatOK(v["mdResyncPos"])
	size, _ := parseFloatOK(v["mdResyncSize"])
	db, _ := parseFloatOK(v["mdResyncDb"])
	dt, _ := parseFloatOK(v["mdResyncDt"])
	syncErrs, _ := parseFloatOK(v["sbSyncErrs"])
	syncStart, _ := parseFloatOK(v["sbSynced"])
	syncFinish, _ := parseFloatOK(v["sbSynced2"])
	syncExit, _ := parseFloatOK(v["sbSyncExit"])

	var progress float64
	if size > 0 {
		progress = pos / size * 100
	}

	var speedBps float64
	if dt > 0 {
		speedBps = db / dt * 1024
	}

	return ArrayState{
		State:          v["mdState"],
		ParityRunning:  pos > 0,
		ParityProgress: progress,
		ParitySpeedBps: speedBps,
		Version:        v["version"],
		Name:           v["NAME"],
		SyncErrs:       syncErrs,
		SyncStart:      int64(syncStart),
		SyncFinish:     int64(syncFinish),
		SyncExit:       int(syncExit),
	}
}

// transitionEvents compares two consecutive ArrayState observations and
// returns the store.Events their differences warrant: an array.state
// event on any mdState change (severity "warning" unless the new state
// is "STARTED", which is the healthy/expected one), a parity.start event
// when a parity run begins, and a parity.finish event when one ends.
//
// The finish event reports prev's progress, not next's: by the tick a
// run shows as no-longer-running, emhttp has already reset mdResyncPos
// back toward 0, so next.ParityProgress is not "how far it got" — it's
// "the not-running baseline". prev.ParityProgress is the last progress
// figure observed while the run was still active, which is what "reached
// N.N%" means. next's SyncErrs/SyncStart/SyncFinish, by contrast, ARE
// already the freshly-written values by this tick -- emhttp updates
// sbSyncErrs/sbSynced/sbSynced2 at the same moment it resets
// mdResyncPos -- so the enrichment below reads next, not prev.
func transitionEvents(prev, next ArrayState) []store.Event {
	var out []store.Event

	if next.State != prev.State {
		severity := "warning"
		if next.State == "STARTED" {
			severity = "info"
		}
		out = append(out, store.Event{Kind: "array.state", Entity: "array", Severity: severity, Detail: next.State})
	}

	switch {
	case next.ParityRunning && !prev.ParityRunning:
		out = append(out, store.Event{Kind: "parity.start", Entity: "array", Severity: "info"})
	case !next.ParityRunning && prev.ParityRunning:
		out = append(out, store.Event{
			Kind: "parity.finish", Entity: "array", Severity: parityFinishSeverity(next),
			Detail: parityFinishDetail(prev, next),
		})
	}

	return out
}

// parityFinishSeverity is "alert" once the just-finished check found any
// errors (sbSyncErrs > 0), "info" otherwise -- this is what actually
// arms the seeded parity-errors rule's min_severity "warning" floor.
func parityFinishSeverity(next ArrayState) string {
	if next.SyncErrs > 0 {
		return "alert"
	}
	return "info"
}

// parityFinishDetail renders "reached N.N%[ · HhMMm] · N errors": the
// duration segment is included only when both sbSynced/sbSynced2 (next's
// SyncStart/SyncFinish) are present and ordered -- never a fabricated
// "0h00m" when the timestamps simply weren't captured. The error count
// always renders, including "0 errors" on a clean run.
func parityFinishDetail(prev, next ArrayState) string {
	detail := fmt.Sprintf("reached %.1f%%", prev.ParityProgress)
	if next.SyncStart > 0 && next.SyncFinish > next.SyncStart {
		detail += " · " + formatSyncDuration(next.SyncFinish-next.SyncStart)
	}
	return fmt.Sprintf("%s · %d errors", detail, int64(next.SyncErrs))
}

// formatSyncDuration renders a parity run's wall-clock time as "%dh%02dm"
// (e.g. "2h14m", "5h02m") -- time.Duration.String() doesn't zero-pad
// minutes and always appends seconds, neither of which matches this
// display shape.
func formatSyncDuration(secs int64) string {
	return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60)
}

// tickArray reads var.ini, records parity metrics while a run is active,
// and — once a previous observation exists — appends any transition
// events the new state warrants. Its dir/var.ini read is Tick's one hard
// dependency (mirroring Probe), so a failure here is returned rather than
// swallowed.
func (c *Collector) tickArray(now time.Time) error {
	f, err := os.Open(filepath.Join(c.dir, "var.ini"))
	if err != nil {
		return fmt.Errorf("unraid: open var.ini: %w", err)
	}
	defer func() { _ = f.Close() }()

	kv, err := ParseINI(f)
	if err != nil {
		return fmt.Errorf("unraid: parse var.ini: %w", err)
	}
	next := interpretVar(kv)

	c.mu.Lock()
	c.version = next.Version
	c.name = next.Name
	c.mu.Unlock()

	ts := now.Unix()
	// array.started is 1/0 rather than mdState's raw string -- Sample.Val
	// is float64-only (see store.MetricSink), and the UI's Overview array
	// card needs a live-frame-visible "is the array up" signal that
	// doesn't depend on ever having observed a STATE TRANSITION (unlike
	// the array.state event below, which only fires on change and so
	// never fires at all for a box that's stayed STARTED the whole time
	// this collector has been running).
	started := 0.0
	if next.State == "STARTED" {
		started = 1.0
	}
	c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "array.started"}, ts, started)
	// parity.errors mirrors array.started's own unconditional-every-tick
	// convention (see its comment just above): the current/last-known
	// check's error count needs a live-frame value even on a box that has
	// never once run a parity check, not only once one finishes.
	c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.errors"}, ts, next.SyncErrs)
	if next.ParityRunning {
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}, ts, next.ParityProgress)
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}, ts, next.ParitySpeedBps)
	} else if c.havePrevArray && c.prevArray.ParityRunning {
		// The ParityRunning true->false transition: record ONE final zero
		// sample for each parity metric so "not running" has an explicit,
		// permanent wire value. Without this, the store's live ring simply
		// keeps whatever was last recorded while the run was active (e.g.
		// 99.9%, 135 MB/s) forever -- Ring.Latest has no expiry -- so the
		// live frame reads as "still running" indefinitely after a finish.
		// "Zero when not running" is now the wire semantic the UI's
		// parityRunning derivation depends on (see parityIsRunning in
		// web/src/lib/metrics.ts). Guarded on the prev-tick's own
		// ParityRunning (checked before c.prevArray is overwritten below)
		// so this fires exactly once, on the same tick as the
		// parity.finish event, never on every idle tick after.
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}, ts, 0)
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}, ts, 0)
	}

	if c.havePrevArray {
		for _, e := range transitionEvents(c.prevArray, next) {
			if _, err := c.events.AppendEvent(e); err != nil {
				log.Printf("events: %v", err)
			}
		}
	}
	c.prevArray = next
	c.havePrevArray = true
	return nil
}

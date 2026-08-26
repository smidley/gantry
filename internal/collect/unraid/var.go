package unraid

import (
	"fmt"
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
	ParitySpeedBps float64 // mdResyncSpeed (KB/s) * 1024
	Version        string
}

// interpretVar derives ArrayState from a parsed var.ini. mdResyncPos and
// mdResyncSize are both 1024-byte-block counts (emhttp's mdResync* units);
// ParityRunning is pos > 0, ParityProgress is pos/size*100 guarded
// against a zero size (0, not +Inf/NaN). mdResyncSpeed is KB/s;
// ParitySpeedBps is that figure times 1024. Any missing/malformed numeric
// key defaults to 0 rather than erroring — var.ini has no "malformed
// content" error path, only absent-or-zero.
func interpretVar(kv map[string]map[string]string) ArrayState {
	v := kv[""]

	pos, _ := parseFloatOK(v["mdResyncPos"])
	size, _ := parseFloatOK(v["mdResyncSize"])
	speed, _ := parseFloatOK(v["mdResyncSpeed"])

	var progress float64
	if size > 0 {
		progress = pos / size * 100
	}

	return ArrayState{
		State:          v["mdState"],
		ParityRunning:  pos > 0,
		ParityProgress: progress,
		ParitySpeedBps: speed * 1024,
		Version:        v["version"],
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
// N.N%" means.
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
			Kind: "parity.finish", Entity: "array", Severity: "info",
			Detail: fmt.Sprintf("reached %.1f%%", prev.ParityProgress),
		})
	}

	return out
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
	defer f.Close()

	kv, err := ParseINI(f)
	if err != nil {
		return fmt.Errorf("unraid: parse var.ini: %w", err)
	}
	next := interpretVar(kv)

	c.mu.Lock()
	c.version = next.Version
	c.mu.Unlock()

	ts := now.Unix()
	if next.ParityRunning {
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}, ts, next.ParityProgress)
		c.sink.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.speed_bps"}, ts, next.ParitySpeedBps)
	}

	if c.havePrevArray {
		for _, e := range transitionEvents(c.prevArray, next) {
			c.events.AppendEvent(e)
		}
	}
	c.prevArray = next
	c.havePrevArray = true
	return nil
}

package alert

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// notifyProbeInterval bounds how often Health() actually touches the
// filesystem: a real caller (a future Settings channels card, Task 8)
// could poll Health() far more often than the mount state could ever
// change, and a write+remove pair on every call would be needless disk
// traffic for a check whose answer is almost always identical to the
// last one.
const notifyProbeInterval = 60 * time.Second

// notifyUnavailableHint is the verbatim line Health() returns when the
// spool isn't writable -- worded to match the CA template's own mount
// entry (template/gantry.xml, Task 15) so the hint tells the user
// exactly what to add.
const notifyUnavailableHint = "mount /tmp/notifications to /notify (rw) to deliver Unraid notifications"

// severityImportance maps store.Event's three-tier severity vocabulary
// (alert_rules.severity uses the same three values, see ValidateRule) onto
// WriteNotify's own three importance levels. A 1:1 rename: no value on
// either side has no counterpart on the other, so nothing here is lossy.
var severityImportance = map[string]string{
	"info":    "normal",
	"warning": "warning",
	"alert":   "alert",
}

// NotifyChannel is the Channel wrapping the already-verified WriteNotify
// (Phase 1, human-verified against real dynamix -- see notify.go, which
// this file calls but never modifies). LinkBase, called fresh on every
// Send, resolves the `alert.link_base` setting: empty (the default)
// omits Link entirely, since Gantry cannot know its own reachable URL
// and must not guess one.
type NotifyChannel struct {
	Dir      string
	LinkBase func() string
	Clock    func() time.Time

	mu        sync.Mutex
	lastProbe time.Time
	healthy   bool
	hint      string
}

// NewNotifyChannel constructs the channel and probes the spool
// immediately (the plan's "probe the dir at construction and every 60s"
// contract) so the very first Health() call -- before any Send has ever
// run -- already reflects real mount state rather than an optimistic
// zero value. linkBase and clock may both be nil: linkBase nil means
// "no link ever", clock nil defaults to time.Now.
func NewNotifyChannel(dir string, linkBase func() string, clock func() time.Time) *NotifyChannel {
	if clock == nil {
		clock = time.Now
	}
	c := &NotifyChannel{Dir: dir, LinkBase: linkBase, Clock: clock}
	c.healthy, c.hint = c.probe()
	c.lastProbe = c.Clock()
	return c
}

func (c *NotifyChannel) ID() string { return "notify" }

// Health reports "ok" or the verbatim enable hint, re-probing at most
// once per notifyProbeInterval -- a plain os.Stat can't distinguish "the
// mount is missing" from "the mount exists but this uid can't write to
// it", and only the latter is what an actual delivery attempt needs to
// know, so the probe writes and removes a real marker file every time it
// runs.
func (c *NotifyChannel) Health() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Clock()
	if now.Sub(c.lastProbe) >= notifyProbeInterval {
		c.healthy, c.hint = c.probe()
		c.lastProbe = now
	}
	if c.healthy {
		return "ok"
	}
	return c.hint
}

// probe writes and removes .gantry-probe inside dir/unread -- the exact
// subdirectory WriteNotify itself writes into, so a probe result can
// never disagree with what a real Send is about to find out the hard
// way.
func (c *NotifyChannel) probe() (ok bool, hint string) {
	unread := filepath.Join(c.Dir, "unread")
	if err := os.MkdirAll(unread, 0o755); err != nil {
		return false, notifyUnavailableHint
	}
	marker := filepath.Join(unread, ".gantry-probe")
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		return false, notifyUnavailableHint
	}
	_ = os.Remove(marker)
	return true, ""
}

// Send translates an AlertNotification into notify.go's dynamix
// Notification and writes it through WriteNotify unchanged. Exactly one
// attempt: a local file write either succeeds or it doesn't, and nothing
// about retrying an atomic rename onto the same filesystem a moment
// later would change the outcome.
func (c *NotifyChannel) Send(_ context.Context, n AlertNotification) SendResult {
	importance := severityImportance[n.Rule.Severity]
	if importance == "" {
		importance = "normal" // an unrecognized severity fails safe to the quietest tier, never silently dropped
	}

	subject := n.Subject
	if subject == "" {
		entity := n.Instance.Entity
		if entity == "" {
			entity = "host"
		}
		subject = n.Rule.Name + " — " + entity
	}
	if n.Phase == "resolved" {
		subject = "Resolved: " + subject
		importance = "normal"
	}

	var link string
	if c.LinkBase != nil {
		link = c.LinkBase()
	}

	_, err := WriteNotify(c.Dir, Notification{
		Event:       "Gantry",
		Subject:     subject,
		Description: n.Summary,
		Importance:  importance,
		Link:        link,
	}, c.Clock())
	return SendResult{OK: err == nil, Attempts: 1, Err: err}
}

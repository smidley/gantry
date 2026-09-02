package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// logsClient is the narrow slice of *client.Client this file's own
// Collector methods call -- Collector.logCli's declared type, mirroring
// imagesClient/containersClient's doc and purpose exactly (see
// imagesClient's own doc), just for the log-streaming surface. It's what
// lets the follow-mode re-attach loop below be pinned end to end --
// attach counts, re-resolved ids, the exact LogsOptions each attach sends
// -- without a real daemon. *client.Client already implements this; New
// sets logCli to the same value as cli.
type logsClient interface {
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
}

const (
	// reattachBackoffBase and reattachBackoffMax bound how eagerly follow
	// mode re-attaches after a stream ends. The first wait is short (a
	// restarting container is usually logging again within a fraction of
	// a second) and then doubles up to a 1s floor, which is what stops a
	// follow on a stopped-but-still-present container from hot-looping:
	// attaching to a stopped container with Follow returns whatever
	// backlog passes the Since boundary -- after the first re-attach,
	// nothing -- and EOFs immediately, so with no wait at all this loop
	// would spin against the daemon as fast as it could round-trip. Any
	// attach that actually delivered a record resets the schedule to base,
	// so a container that flaps repeatedly still resumes promptly each
	// time rather than inheriting a stale, longer wait.
	reattachBackoffBase = 250 * time.Millisecond
	reattachBackoffMax  = time.Second

	// nameGoneGrace is how long follow mode keeps retrying after the
	// followed NAME stops resolving in the registry before it gives up and
	// ends the stream. It has to clear the registry's own
	// inventoryInterval with room to spare: applyEvent only ever REMOVES
	// containers, so one re-created under the same name (an Unraid
	// container update: remove + create, same name, new id) doesn't
	// reappear in the registry until the next 10s inventory poll discovers
	// it -- a name-gap of up to inventoryInterval plus however long the
	// re-create itself took. Three polls' worth is comfortably past a
	// normal update while still bounding a follow on a container that
	// really was removed for good.
	nameGoneGrace = 3 * inventoryInterval
)

// restartMarker is the one line gantry writes into the stream itself,
// injected immediately before the first record a RE-attach delivers so
// the log viewer shows where the container went away and came back
// instead of silently splicing the two runs together. Deliberately
// carries no level word (error/warn/info/debug/...) and no glog-style
// prefix, so the frontend's classifyLogLine buckets it as "other" and it
// can never be mistaken for the container's own output at any severity.
const restartMarker = "--- gantry: container restarted, log stream reattached ---\n"

// errNameGone marks the "the followed name no longer resolves" attach
// failure specifically, so followLoop can tell a container that was
// removed for good (end the stream cleanly, exactly like a non-follow
// EOF) from a daemon that merely refused the attach (end it with the
// error).
var errNameGone = errors.New("container no longer present")

// StreamLogs returns one container's stdout+stderr as a single, already-
// demuxed reader: name resolves to its current id via the registry (the
// same identity source Lookup/Running use), then the SDK's raw
// ContainerLogs stream -- multiplexed stdout/stderr frames when the
// container has no TTY, a single unstructured stream when it does -- is
// always run through stdcopy, which correctly passes a non-multiplexed
// stream through unchanged. An unknown name is reported as an error
// rather than reaching the SDK at all; the caller (the /api/containers/
// {name}/logs handler) maps that to 404 the same way it maps a real
// daemon error, so an unknown name and a currently-unreachable daemon
// look identical to callers.
//
// follow is where the two modes part company. Without it this is a
// single shot: one attach, demuxed until the daemon has returned
// everything up to Tail, then EOF. With it, the daemon's own stream is
// NOT the unit of work -- the daemon ends a follow stream when the
// container exits, and a restart is an exit followed by a start, so a
// single attach goes permanently silent the moment the thing you're
// watching restarts. Follow mode therefore hands back one continuous
// pipe fed by a re-attach loop (followLoop), which keeps resuming onto
// whatever container currently answers to name until the caller's ctx
// ends or that name stays gone. The caller sees no seam: one
// io.ReadCloser, one Close, the same bytes the container logged, plus a
// restartMarker line at each resume.
func (c *Collector) StreamLogs(ctx context.Context, name string, follow bool, tail int) (io.ReadCloser, error) {
	m, ok := c.reg.lookupByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown container %q", name)
	}

	// Timestamps only in follow mode: they're the resume boundary (see
	// followWriter), stripped back off before anything reaches the pipe,
	// and a single-shot read has no resume to key -- so the non-follow
	// request the daemon sees, and the bytes it returns, stay exactly
	// what they were before follow grew a loop.
	raw, err := c.logCli.ContainerLogs(ctx, m.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Timestamps: follow,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	if !follow {
		go func() { _ = pw.CloseWithError(pumpLogs(ctx, raw, pw)) }()
		return pr, nil
	}
	go c.followLogs(ctx, name, raw, pw)
	return pr, nil
}

// pumpLogs demuxes raw into dst until whichever ends first: the
// underlying stream itself (EOF -- the normal non-follow outcome, once
// it's returned everything up to Tail, and the per-attach outcome in
// follow mode every time the container exits) or ctx (the normal follow
// outcome, when the HTTP client disconnects). The SDK's ReadCloser gives
// no other way to interrupt a blocked Follow read than closing it, so
// the ctx-done path does exactly that, which then unblocks the demux
// goroutine's pending read with an error. raw is closed either way.
//
// The returned error is the caller's whole classification: nil means the
// stream ended cleanly and only the stream ended -- the one outcome that
// makes re-attaching meaningful -- while anything else means this
// pump's own world is over (ctx cancelled, or a write into dst failed
// because the pipe's reader end went away) and no re-attach could
// deliver to anyone.
func pumpLogs(ctx context.Context, raw io.ReadCloser, dst io.Writer) error {
	copyDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(dst, dst, raw)
		copyDone <- err
	}()
	select {
	case <-ctx.Done():
		_ = raw.Close()
		<-copyDone
		return ctx.Err()
	case err := <-copyDone:
		_ = raw.Close()
		return err
	}
}

// followLogs owns one follow-mode pipe for its whole life: it pumps the
// attach StreamLogs already opened, then re-attaches and keeps pumping
// into the SAME pipe every time the daemon ends a stream, so a container
// restart reads as a gap in the output rather than the end of it.
func (c *Collector) followLogs(ctx context.Context, name string, raw io.ReadCloser, pw *io.PipeWriter) {
	c.followLoop(ctx, name, raw, pw, nameGoneGrace)
}

// followLoop is followLogs' body with the give-up window as an explicit
// parameter -- production always passes nameGoneGrace; the unit test for
// the give-up path passes a short one, since the real window is
// deliberately sized to outlast an inventory poll (see nameGoneGrace) and
// no test can afford to wait it out in real time.
//
// The loop's three exits, in the order they matter:
//
//   - pumpLogs returned an error: ctx is done (client disconnected, or
//     handleLogs returned for any other reason and net/http cancelled the
//     request context behind it) or the pipe's reader end was closed out
//     from under us (handleLogs' shutdown-drain watcher). Either way
//     nobody is reading any more, so the pipe ends carrying that error,
//     exactly as the single-shot path ends it.
//   - the name stopped resolving and stayed gone past grace: the
//     container was removed rather than re-created, so the pipe ends
//     CLEANLY (a plain Close, indistinguishable from a non-follow EOF) --
//     the stream is over, but nothing went wrong.
//   - the attach itself kept failing past the same grace budget: the
//     daemon is refusing, which is not a clean end, so that error rides
//     out on the pipe. Without this budget a follow held open against a
//     dead daemon would retry at reattachBackoffMax forever.
//
// The grace budget is shared by the last two on purpose: both mean "we
// could not get a stream", both are routinely transient for a second or
// few mid-re-create, and both need the same bound. It is a budget per
// RESUME, not per follow -- each attach that succeeds clears it, so a
// container that flaps for hours never accumulates its way to a give-up.
func (c *Collector) followLoop(ctx context.Context, name string, raw io.ReadCloser, pw *io.PipeWriter, grace time.Duration) {
	w := &followWriter{pw: pw, atRecordStart: true}
	backoff := reattachBackoffBase

	for {
		if err := pumpLogs(ctx, raw, w); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if w.endAttach() {
			backoff = reattachBackoffBase
		}

		// Keep retrying here until an attach actually hands back a
		// stream. A failed attach must NOT fall back to the outer loop:
		// raw is spent the moment its own pump ends (pumpLogs closes it),
		// and the SDK's ReadCloser answers a read after that with an
		// error rather than the polite EOF a plain buffer would give --
		// which the outer loop would read, correctly and fatally, as
		// "this stream is over". That is exactly the shape a re-create
		// produces (first re-attach lands in the name-gap and fails), so
		// getting it wrong breaks the very case this loop exists for.
		var unavailableSince time.Time
		for {
			if !waitOrDone(ctx, backoff) {
				_ = pw.CloseWithError(ctx.Err())
				return
			}
			backoff *= 2
			if backoff > reattachBackoffMax {
				backoff = reattachBackoffMax
			}

			next, err := c.attachLogs(ctx, name, w.since())
			if err == nil {
				raw = next
				break
			}
			if unavailableSince.IsZero() {
				unavailableSince = time.Now()
			}
			if time.Since(unavailableSince) < grace {
				continue
			}
			log.Printf("docker: logs: giving up following %s after %s: %v", name, grace, err)
			if errors.Is(err, errNameGone) {
				_ = pw.Close()
			} else {
				_ = pw.CloseWithError(err)
			}
			return
		}
	}
}

// attachLogs opens follow mode's next stream. It re-resolves name to its
// CURRENT id every time rather than reusing the one StreamLogs first
// looked up, which is the whole reason the resume is keyed on the name:
// a plain restart keeps the id, but an Unraid container UPDATE re-creates
// the container -- same name, new id -- and an id-keyed re-attach would
// faithfully re-attach to a container that no longer exists.
//
// since is the exact boundary the last attach reached (see
// followWriter.since); Tail is deliberately "all" rather than the
// caller's original tail, because Since has already narrowed the set to
// records the caller hasn't seen and re-applying a tail on top of that
// could only throw some of them away.
func (c *Collector) attachLogs(ctx context.Context, name, since string) (io.ReadCloser, error) {
	m, ok := c.reg.lookupByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown container %q: %w", name, errNameGone)
	}
	return c.logCli.ContainerLogs(ctx, m.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
		Tail:       "all",
		Since:      since,
	})
}

// waitOrDone sleeps for d, reporting false if ctx ended first -- the
// backoff wait is the one place followLoop spends real time with no read
// pending to notice a cancellation for it, so it has to watch ctx itself
// or a client disconnect mid-backoff would sit unnoticed for up to
// reattachBackoffMax.
func waitOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// followWriter is the io.Writer stdcopy demuxes a follow-mode attach
// into, sitting between the demuxer and the pipe to do the three jobs
// that make consecutive attaches read as one stream:
//
//   - strip the RFC3339Nano prefix StreamLogs asks the daemon for in
//     follow mode, so callers get byte-for-byte what the container
//     logged, exactly as they did before follow grew a loop;
//   - remember the last delivered record's timestamp as the next
//     attach's Since boundary (see since); and
//   - drop the records that boundary necessarily replays, since docker's
//     own since filter is inclusive (it keeps every record NOT before
//     since), so a resume delivers no duplicates.
//
// Timestamps, not the wall clock, are the boundary on purpose. They come
// from the daemon's own record of when each line was logged, so the
// boundary is self-consistent by construction -- immune to any skew
// between this process's clock and the log's, and correct even for an
// EOF that wasn't a container exit at all (a daemon-side reader hiccup
// mid-run), where "everything since the moment I noticed" would silently
// swallow whatever was in flight. Wall-clock-at-EOF is kept only as the
// fallback for when there is no timestamp to key on yet (see since).
//
// Everything is done per RECORD, and a record is recognised structurally
// rather than by trusting any one layer's framing: a timestamp is only
// ever peeled off at a record boundary (stream start, or just after a
// newline), so a record's continuation bytes -- and a stream that
// carries no timestamps at all -- pass through untouched, and a write
// carrying several records is split at the newlines between them.
// Nothing is buffered: docker's own log copier already holds an
// unterminated line until it completes, so byte-for-byte passthrough
// here costs no latency that the daemon hasn't already spent.
type followWriter struct {
	pw *io.PipeWriter

	// atRecordStart says the next byte begins a fresh record (so it may
	// carry a timestamp prefix); dropping says the record currently being
	// written is one of the replayed ones, and its remaining bytes go
	// nowhere either.
	atRecordStart bool
	dropping      bool
	// midLine says the last byte actually delivered wasn't a newline, so
	// an injected restartMarker has to open one of its own rather than
	// land on the tail of a half-written line.
	midLine bool

	// lastTS is the timestamp of the most recently DELIVERED record and
	// lastTSCount how many consecutive delivered records share it --
	// together the exact replay the next attach will produce, since
	// docker returns records in timestamp order and keeps every one not
	// before Since. fallback stands in for lastTS when no record has ever
	// carried a parseable timestamp.
	lastTS      time.Time
	lastTSCount int
	fallback    time.Time

	// dedup is armed by endAttach and disarmed by the first record that
	// clears the boundary; dedupSkip is how many records still to drop at
	// exactly lastTS (any beyond that many are genuinely new records that
	// merely share the boundary instant, and must be delivered).
	dedup     bool
	dedupSkip int

	// delivered tracks whether the current attach produced anything (it
	// drives the backoff reset); markerPending is set for every attach
	// after the first and spent on that attach's first delivered record.
	delivered     bool
	markerPending bool
}

// Write is stdcopy's demux destination for both stdout and stderr, the
// same single-pipe merge the single-shot path has always done. It never
// reports a short write: a dropped record is still accounted as
// consumed, because "the caller already has these bytes" is exactly what
// dropping means and reporting it as a write failure would tear the
// stream down.
func (w *followWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		if w.atRecordStart {
			rest, ts, ok := splitTimestamp(p)
			w.startRecord(ts, ok)
			w.atRecordStart = false
			p = rest
			continue
		}
		seg := p
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			seg, p = p[:i+1], p[i+1:]
			w.atRecordStart = true
		} else {
			p = nil
		}
		if w.dropping {
			continue
		}
		if err := w.deliver(seg); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// startRecord runs once per record, on its first byte, and decides that
// record's fate: dropped as a replay of something already delivered, or
// delivered and folded into the boundary state the next attach will
// resume from. A record whose timestamp won't parse while dedup is armed
// is delivered rather than dropped -- with no boundary to compare it to,
// a duplicate line is a far smaller failure than a lost one.
func (w *followWriter) startRecord(ts time.Time, ok bool) {
	if w.dedup {
		switch {
		case ok && ts.Before(w.lastTS):
			w.dropping = true
			return
		case ok && ts.Equal(w.lastTS) && w.dedupSkip > 0:
			w.dedupSkip--
			w.dropping = true
			return
		}
		w.dedup = false
	}
	w.dropping = false
	if !ok {
		return
	}
	if ts.Equal(w.lastTS) {
		w.lastTSCount++
		return
	}
	w.lastTS, w.lastTSCount = ts, 1
}

// deliver writes one record's bytes through to the pipe, spending a
// pending restartMarker first so the marker always precedes the resumed
// output rather than trailing the interruption.
func (w *followWriter) deliver(b []byte) error {
	if w.markerPending {
		w.markerPending = false
		marker := restartMarker
		if w.midLine {
			marker = "\n" + restartMarker
		}
		if _, err := w.pw.Write([]byte(marker)); err != nil {
			return err
		}
		w.midLine = false
	}
	if len(b) == 0 {
		return nil
	}
	if _, err := w.pw.Write(b); err != nil {
		return err
	}
	w.delivered = true
	w.midLine = b[len(b)-1] != '\n'
	return nil
}

// endAttach closes one attach out and arms the next, reporting whether
// this one delivered anything (which is what resets followLoop's
// backoff). Arming means: fix the Since boundary (falling back to the
// wall clock only while no timestamp has ever been seen -- an attach
// that delivered nothing at all, typically a follow opened on a
// container that's already stopped), expect the replay that boundary
// implies, and queue the restartMarker for whatever the next attach
// turns out to deliver. Every attach after the first queues a marker,
// but an attach that delivers nothing never spends it, so a container
// that stays stopped accumulates no markers -- exactly one appears, at
// the point output actually resumes.
func (w *followWriter) endAttach() (delivered bool) {
	if w.lastTS.IsZero() {
		w.fallback = time.Now()
	}
	w.dedup = !w.lastTS.IsZero()
	w.dedupSkip = w.lastTSCount
	w.atRecordStart = true
	w.dropping = false
	w.markerPending = true
	delivered, w.delivered = w.delivered, false
	return delivered
}

// since renders the boundary for the next attach's LogsOptions.Since:
// the last delivered record's own daemon timestamp when there is one,
// otherwise the wall-clock instant of the last EOF. Empty only before
// the first boundary exists at all, which no attach after the first can
// observe.
func (w *followWriter) since() string {
	t := w.lastTS
	if t.IsZero() {
		t = w.fallback
	}
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// splitTimestamp peels docker's leading "<RFC3339Nano> " off one record.
// ok is false -- and rest is p unchanged, not one byte consumed -- for
// anything that doesn't parse, which is how a record's continuation
// bytes and any stream that isn't carrying timestamps survive this path
// byte for byte. The candidate token is bounded to the first line so a
// record with no space of its own can't reach across a newline and
// swallow the next record's prefix.
func splitTimestamp(p []byte) (rest []byte, ts time.Time, ok bool) {
	end := len(p)
	if nl := bytes.IndexByte(p, '\n'); nl >= 0 {
		end = nl
	}
	i := bytes.IndexByte(p[:end], ' ')
	if i <= 0 {
		return p, time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, string(p[:i]))
	if err != nil {
		return p, time.Time{}, false
	}
	return p[i+1:], t, true
}

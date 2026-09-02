package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/require"
)

// TestStreamLogsUnknownContainerErrorsWithoutTouchingDaemon pins the
// registry-lookup-first contract: an unknown name must fail fast (an
// error naming the container) without ever reaching the SDK client --
// this is what lets the /api/containers/{name}/logs handler map "unknown
// name" and "docker unavailable" to the same 404 shape.
func TestStreamLogsUnknownContainerErrorsWithoutTouchingDaemon(t *testing.T) {
	dc := New(newFakeSink(), &fakeEventSink{}, func(string, string) {}, "/var/run/docker.sock")

	rc, err := dc.StreamLogs(context.Background(), "no-such-container", false, 500)
	require.Error(t, err)
	require.Nil(t, rc)
	require.Contains(t, err.Error(), "no-such-container")
}

// dockerTSLayout is the exact format docker's own log reader prefixes to
// every record when LogsOptions.Timestamps is set -- RFC3339 with the
// fractional second padded to a fixed nine digits rather than trimmed,
// verified against a real daemon. The fakes below emit it verbatim so
// these tests exercise the same parse the production path really faces,
// not a tidier stand-in.
const dockerTSLayout = "2006-01-02T15:04:05.000000000Z07:00"

// tsRecord renders one timestamped, newline-terminated log record the
// way the daemon does: "<timestamp> <line>\n".
func tsRecord(ts time.Time, line string) string {
	return ts.UTC().Format(dockerTSLayout) + " " + line + "\n"
}

// framed encodes records into the multiplexed stdout framing a non-TTY
// container's log stream really carries -- one stdcopy frame per record,
// which is also one Write per record on the way back out of the demuxer.
func framed(records ...string) []byte {
	var buf bytes.Buffer
	w := stdcopy.NewStdWriter(&buf, stdcopy.Stdout)
	for _, r := range records {
		if _, err := w.Write([]byte(r)); err != nil {
			panic(err)
		}
	}
	return buf.Bytes()
}

// spentStream is one attach's ReadCloser, faithful to the SDK's real one
// in the property the re-attach loop is most easily fooled by: once
// closed it FAILS on read, exactly as net/http's response body does
// ("http: read on closed response body"). An io.NopCloser around a
// bytes.Reader would instead keep politely answering EOF forever, which
// would quietly hide a loop that pumped an attach it had already spent
// -- the real daemon answers that with an error, and an error is
// terminal.
type spentStream struct {
	mu     sync.Mutex
	r      *bytes.Reader
	closed bool
}

func (s *spentStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("http: read on closed response body")
	}
	return s.r.Read(p)
}

func (s *spentStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// eofStream is one attach that hands back records and then ends, the
// shape every attach in these tests takes: the daemon ending a follow
// stream (container exited) is an ordinary EOF on the reader.
func eofStream(records ...string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return &spentStream{r: bytes.NewReader(framed(records...))}, nil
	}
}

// logsCall records one ContainerLogs invocation: the id the collector
// re-resolved the name to, and the options it asked for.
type logsCall struct {
	id   string
	opts container.LogsOptions
}

// fakeLogsClient is a hand-rolled logsClient double -- injected via
// Collector's own logCli field (see logsClient's own doc) so the
// follow-mode re-attach loop can be pinned without a daemon: how many
// times it attached, which id each attach resolved to, and the exact
// LogsOptions (Since/Tail/Follow/Timestamps) each one sent.
//
// scripts are consumed one per attach, in order; every attach past the
// script gets fallback, defaulting to an immediately-empty stream --
// which is exactly what re-attaching to a stopped container looks like
// once Since has excluded its whole backlog.
type fakeLogsClient struct {
	mu       sync.Mutex
	calls    []logsCall
	scripts  []func() (io.ReadCloser, error)
	fallback func() (io.ReadCloser, error)
}

func (f *fakeLogsClient) ContainerLogs(_ context.Context, id string, opts container.LogsOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, logsCall{id: id, opts: opts})
	next := f.fallback
	if n < len(f.scripts) {
		next = f.scripts[n]
	}
	f.mu.Unlock()

	if next != nil {
		return next()
	}
	return &spentStream{r: bytes.NewReader(nil)}, nil
}

func (f *fakeLogsClient) snapshot() []logsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]logsCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeLogsClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// collectorFollowing wires a Collector around fc whose registry resolves
// each given Meta -- the minimum StreamLogs needs, with no daemon, no
// ticker and no sinks in play.
func collectorFollowing(fc *fakeLogsClient, metas ...Meta) *Collector {
	c := &Collector{logCli: fc, reg: newRegistry()}
	c.reg.applyInventory(metas, &fakeEventSink{}, func(string, string) {})
	return c
}

// readWithin drains rc in the background and returns a func reporting
// everything received so far, so a test can poll for output that is
// still arriving without ever blocking on a stream that legitimately has
// nothing more to say yet.
func readWithin(t *testing.T, rc io.ReadCloser) (got func() string, done <-chan error) {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		p := make([]byte, 4096)
		for {
			n, err := rc.Read(p)
			if n > 0 {
				mu.Lock()
				buf.Write(p[:n])
				mu.Unlock()
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}, errCh
}

// TestStreamLogsFollowReattachesAfterTheDaemonEndsTheStream is the bug
// itself: the daemon ends a follow stream when the container exits, and
// a restart is an exit plus a start, so before the re-attach loop the
// first EOF was the end of the caller's stream too -- silent from then
// on. One reader, one Close, output from both runs.
func TestStreamLogsFollowReattachesAfterTheDaemonEndsTheStream(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	fc := &fakeLogsClient{scripts: []func() (io.ReadCloser, error){
		eofStream(tsRecord(t0, "before-restart")),
		eofStream(tsRecord(t0.Add(2*time.Second), "after-restart")),
	}}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, _ := readWithin(t, rc)
	require.Eventually(t, func() bool {
		return strings.Contains(got(), "after-restart")
	}, 5*time.Second, 20*time.Millisecond, "post-restart output never arrived on the same reader; got %q", got())

	out := got()
	require.Contains(t, out, "before-restart", "pre-restart output must still be there")
	require.Less(t, strings.Index(out, "before-restart"), strings.Index(out, "after-restart"), "output must stay in order across the re-attach")
	require.Equal(t, 1, strings.Count(out, restartMarker), "exactly one restart marker, at the resume: %q", out)
	require.Less(t, strings.Index(out, restartMarker), strings.Index(out, "after-restart"), "the marker must precede the resumed output")
	require.NotContains(t, out, t0.Format(dockerTSLayout), "the daemon timestamps follow mode asks for are an internal detail and must be stripped back off")
}

// TestStreamLogsFollowFirstAttachCarriesTheCallersTailAndNoMarker pins
// what the first attach must look like: the caller's own tail, follow on,
// timestamps on (they're the resume key), no Since (there's no boundary
// yet) -- and no marker, because nothing has been interrupted.
func TestStreamLogsFollowFirstAttachCarriesTheCallersTailAndNoMarker(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	fc := &fakeLogsClient{scripts: []func() (io.ReadCloser, error){
		eofStream(tsRecord(t0, "first")),
	}}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 42)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, _ := readWithin(t, rc)
	require.Eventually(t, func() bool { return strings.Contains(got(), "first") }, 2*time.Second, 10*time.Millisecond)

	first := fc.snapshot()[0]
	require.Equal(t, "id-1", first.id)
	require.True(t, first.opts.Follow)
	require.True(t, first.opts.Timestamps)
	require.Equal(t, "42", first.opts.Tail)
	require.Empty(t, first.opts.Since)
	require.Equal(t, "first\n", got(), "the first attach's output is the container's own bytes, nothing added")
}

// TestStreamLogsFollowResolvesTheNameAgainOnEveryAttach covers the
// secondary defect: an Unraid container UPDATE re-creates the container
// under the same name with a NEW id, so a re-attach keyed on the id
// StreamLogs first resolved would faithfully re-attach to a container
// that no longer exists. The name-gap the re-create leaves (the registry
// only learns of the replacement on its next inventory poll) must be
// waited out, not treated as the end of the stream.
func TestStreamLogsFollowResolvesTheNameAgainOnEveryAttach(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	c := collectorFollowing(nil, Meta{ID: "id-old", Name: "web", State: "running"})

	var once sync.Once
	fc := &fakeLogsClient{
		scripts: []func() (io.ReadCloser, error){
			// The first attach ends the way a removed container's stream
			// does; the name vanishes from the registry with it, and only
			// comes back -- under a new id -- a beat later, exactly as a
			// re-create plus the next inventory poll would replace it.
			func() (io.ReadCloser, error) {
				once.Do(func() {
					c.reg.applyInventory(nil, &fakeEventSink{}, func(string, string) {})
					time.AfterFunc(400*time.Millisecond, func() {
						c.reg.applyInventory([]Meta{{ID: "id-new", Name: "web", State: "running"}}, &fakeEventSink{}, func(string, string) {})
					})
				})
				return &spentStream{r: bytes.NewReader(framed(tsRecord(t0, "old-container")))}, nil
			},
		},
		fallback: eofStream(tsRecord(t0.Add(time.Second), "new-container")),
	}
	c.logCli = fc

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, _ := readWithin(t, rc)
	require.Eventually(t, func() bool {
		return strings.Contains(got(), "new-container")
	}, 5*time.Second, 20*time.Millisecond, "the re-created container's output never arrived; got %q", got())

	calls := fc.snapshot()
	require.Equal(t, "id-old", calls[0].id)
	require.Equal(t, "id-new", calls[len(calls)-1].id, "every attach must re-resolve the NAME, not reuse the first id")
	for _, call := range calls[1:] {
		require.NotEqual(t, "id-old", call.id, "no attach after the re-create may target the dead id")
	}
}

// TestStreamLogsFollowResumesFromTheLastRecordsTimestamp pins the resume
// key itself: every re-attach asks the daemon for exactly the records
// after the last one already delivered, and drops the one docker's own
// inclusive since filter necessarily replays at that boundary. Tail goes
// to "all" because Since has already narrowed the set -- re-applying the
// caller's tail on top could only throw away records they haven't seen.
func TestStreamLogsFollowResumesFromTheLastRecordsTimestamp(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(1500 * time.Millisecond)
	fc := &fakeLogsClient{scripts: []func() (io.ReadCloser, error){
		eofStream(tsRecord(t0, "one"), tsRecord(t1, "two")),
		// The daemon replays the boundary record (Since keeps everything
		// NOT before it) and then the genuinely new one.
		eofStream(tsRecord(t1, "two"), tsRecord(t1.Add(time.Second), "three")),
	}}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, _ := readWithin(t, rc)
	require.Eventually(t, func() bool { return strings.Contains(got(), "three") }, 5*time.Second, 20*time.Millisecond, "got %q", got())

	second := fc.snapshot()[1]
	require.Equal(t, t1.UTC().Format(time.RFC3339Nano), second.opts.Since, "the re-attach must resume from the last delivered record's own daemon timestamp")
	require.Equal(t, "all", second.opts.Tail)
	require.True(t, second.opts.Follow)
	require.True(t, second.opts.Timestamps)

	require.Equal(t, 1, strings.Count(got(), "two\n"), "the record at the boundary must be delivered exactly once: %q", got())
}

// TestStreamLogsFollowDropsOnlyTheRunItAlreadyDelivered guards the
// boundary's one genuinely ambiguous case: several records sharing the
// boundary timestamp. Dropping every record at that instant would lose
// any that are new; dropping none would duplicate the ones already sent.
// The rule is to drop exactly as many as were delivered at that instant
// and no more.
func TestStreamLogsFollowDropsOnlyTheRunItAlreadyDelivered(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	fc := &fakeLogsClient{scripts: []func() (io.ReadCloser, error){
		eofStream(tsRecord(t0, "old"), tsRecord(t1, "tie-a"), tsRecord(t1, "tie-b")),
		// Both tie records replay, and a third shares the same instant --
		// that one is new and must get through.
		eofStream(tsRecord(t1, "tie-a"), tsRecord(t1, "tie-b"), tsRecord(t1, "tie-c")),
	}}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, _ := readWithin(t, rc)
	require.Eventually(t, func() bool { return strings.Contains(got(), "tie-c") }, 5*time.Second, 20*time.Millisecond, "got %q", got())

	out := got()
	require.Equal(t, 1, strings.Count(out, "tie-a\n"), "got %q", out)
	require.Equal(t, 1, strings.Count(out, "tie-b\n"), "got %q", out)
	require.Equal(t, 1, strings.Count(out, "tie-c\n"), "got %q", out)
}

// TestStreamLogsFollowBacksOffOnAStoppedContainerInsteadOfHotLooping
// pins the reason the loop waits at all: attaching to a stopped-but-
// still-present container with Follow returns whatever passes Since --
// after the first resume, nothing -- and EOFs immediately, so an
// un-backed-off loop would spin against the daemon as fast as it could
// round-trip. Asserted as a rate over a real window against the real
// schedule rather than a mocked clock: the gap between "a handful" and
// "a busy-spin's thousands" is wide enough that no loaded machine can
// blur it.
func TestStreamLogsFollowBacksOffOnAStoppedContainerInsteadOfHotLooping(t *testing.T) {
	fc := &fakeLogsClient{fallback: eofStream()} // every attach: instant, empty EOF
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "exited"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	_, streamDone := readWithin(t, rc)
	time.Sleep(time.Second)
	n := fc.callCount()
	cancel()

	require.GreaterOrEqual(t, n, 2, "the loop must keep retrying a stopped container, not give up on the first EOF")
	require.LessOrEqual(t, n, 12, "%d attaches in one second is a hot loop; the schedule floors at %s", n, reattachBackoffMax)

	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream never ended after its context was cancelled")
	}
}

// TestStreamLogsFollowEndsCleanlyOnceTheNameStaysGone pins the terminal
// condition: a container that was removed rather than re-created ends
// the stream the way a non-follow read ends, a plain EOF with no error,
// once the name has stayed unresolvable past the grace window. Driven
// through followLoop directly with a short window -- the real one is
// deliberately sized to outlast an inventory poll (see nameGoneGrace),
// which no test can wait out.
func TestStreamLogsFollowEndsCleanlyOnceTheNameStaysGone(t *testing.T) {
	fc := &fakeLogsClient{}
	c := collectorFollowing(fc) // empty registry: the name never resolves again

	pr, pw := io.Pipe()
	raw := &spentStream{r: bytes.NewReader(framed(tsRecord(time.Now(), "last-gasp")))}
	go c.followLoop(context.Background(), "web", raw, pw, 150*time.Millisecond)

	out, err := io.ReadAll(pr)
	require.NoError(t, err, "a removed container ends the stream cleanly -- io.ReadAll sees EOF, not an error")
	require.Equal(t, "last-gasp\n", string(out), "everything the container did log must still be delivered")
	require.Zero(t, fc.callCount(), "the name never resolved, so no attach should ever have been attempted")
}

// TestStreamLogsFollowSurvivesAShortNameGap is the other half of the
// terminal condition: a re-create leaves the name unresolvable for a
// second or few (the registry only picks the replacement up on its next
// inventory poll), and that gap must not be mistaken for a removal.
func TestStreamLogsFollowSurvivesAShortNameGap(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	fc := &fakeLogsClient{fallback: eofStream(tsRecord(t0.Add(time.Second), "back-again"))}
	c := collectorFollowing(fc) // starts empty: the name is mid-gap right now

	time.AfterFunc(500*time.Millisecond, func() {
		c.reg.applyInventory([]Meta{{ID: "id-new", Name: "web", State: "running"}}, &fakeEventSink{}, func(string, string) {})
	})

	pr, pw := io.Pipe()
	raw := &spentStream{r: bytes.NewReader(framed(tsRecord(t0, "before-gap")))}
	go c.followLoop(context.Background(), "web", raw, pw, 5*time.Second)
	defer func() { _ = pr.Close() }()

	got, _ := readWithin(t, pr)
	require.Eventually(t, func() bool {
		return strings.Contains(got(), "back-again")
	}, 5*time.Second, 20*time.Millisecond, "a name-gap shorter than the grace window must not end the stream; got %q", got())
	require.Contains(t, got(), "before-gap")
}

// TestStreamLogsFollowEndsPromptlyWhenTheContextIsCancelledMidBackoff
// pins the ctx semantics the shutdown-drain and client-disconnect paths
// both rely on: the backoff wait is the one place the loop spends real
// time with no read pending to notice a cancellation for it, so it has
// to watch ctx itself rather than sit out the full wait.
func TestStreamLogsFollowEndsPromptlyWhenTheContextIsCancelledMidBackoff(t *testing.T) {
	fc := &fakeLogsClient{fallback: eofStream()}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "exited"})

	ctx, cancel := context.WithCancel(context.Background())
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	_, streamDone := readWithin(t, rc)
	// Land inside a backoff wait rather than inside a read: the first
	// attach EOFs immediately, so by now the loop is waiting.
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case err := <-streamDone:
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, time.Since(start), reattachBackoffMax, "cancellation must cut the backoff wait short, not wait it out")
	case <-time.After(2 * time.Second):
		t.Fatal("the stream never ended after its context was cancelled")
	}
}

// TestStreamLogsNonFollowStaysASingleShot pins that the mode that was
// never broken didn't change: one attach, no timestamps requested (and
// so no stripping in the way of the daemon's own bytes), the caller's
// tail, and an EOF that is simply the end.
func TestStreamLogsNonFollowStaysASingleShot(t *testing.T) {
	fc := &fakeLogsClient{fallback: eofStream("plain-one\n", "plain-two\n")}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", false, 500)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "plain-one\nplain-two\n", string(out))
	require.NotContains(t, string(out), restartMarker)

	calls := fc.snapshot()
	require.Len(t, calls, 1, "non-follow must never re-attach")
	require.False(t, calls[0].opts.Follow)
	require.False(t, calls[0].opts.Timestamps)
	require.Equal(t, "500", calls[0].opts.Tail)
	require.Empty(t, calls[0].opts.Since)

	// Nothing may attach after the read has ended, either.
	time.Sleep(2 * reattachBackoffBase)
	require.Equal(t, 1, fc.callCount())
}

// TestStreamLogsFollowLeavesRecordsWithoutATimestampIntact guards the
// stripper's blast radius. A timestamp is only ever peeled off at a
// record boundary, so a record split across frames keeps its
// continuation bytes verbatim, and a line whose own CONTENT opens with
// something timestamp-shaped survives -- only the daemon's prefix goes.
func TestStreamLogsFollowLeavesRecordsWithoutATimestampIntact(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	appTS := "2020-01-01T00:00:00.000000000Z"
	fc := &fakeLogsClient{fallback: eofStream(
		// One record whose own content opens with an app-emitted
		// timestamp, then a record deliberately split across two frames:
		// only the first carries the daemon's prefix.
		tsRecord(t0, appTS+" app-logged-its-own-time"),
		t0.Add(time.Second).UTC().Format(dockerTSLayout)+" split-head",
		"split-tail\n",
	)}
	c := collectorFollowing(fc, Meta{ID: "id-1", Name: "web", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := c.StreamLogs(ctx, "web", true, 5)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, _ := readWithin(t, rc)
	require.Eventually(t, func() bool { return strings.Contains(got(), "split-tail") }, 5*time.Second, 20*time.Millisecond, "got %q", got())

	out := got()
	require.Contains(t, out, appTS+" app-logged-its-own-time\n", "only the daemon's own prefix may be stripped")
	require.Contains(t, out, "split-headsplit-tail\n", "a record's continuation bytes must pass through untouched")
}

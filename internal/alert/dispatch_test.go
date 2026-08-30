package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeChannel records every Send call it receives and returns a
// canned SendResult -- optionally after blocking on a signal channel, so
// tests can hold a worker "wedged" the way the plan's non-blocking test
// requires.
type fakeChannel struct {
	id     string
	health string

	mu    sync.Mutex
	sends []AlertNotification

	entered atomic.Int64 // Send calls begun, including ones still wedged on block
	result  SendResult
	block   chan struct{} // if non-nil, Send waits for a receive on this before returning
}

func newFakeChannel(id string) *fakeChannel {
	return &fakeChannel{id: id, health: "ok", result: SendResult{OK: true, Attempts: 1}}
}

func (c *fakeChannel) ID() string     { return c.id }
func (c *fakeChannel) Health() string { return c.health }

func (c *fakeChannel) Send(ctx context.Context, n AlertNotification) SendResult {
	c.entered.Add(1)
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done(): // mirror a real channel post-shutdown: abort and report the failure
			c.mu.Lock()
			c.sends = append(c.sends, n)
			c.mu.Unlock()
			return SendResult{OK: false, Attempts: 1, Err: ctx.Err()}
		}
	}
	c.mu.Lock()
	c.sends = append(c.sends, n)
	c.mu.Unlock()
	return c.result
}

func (c *fakeChannel) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sends)
}

func (c *fakeChannel) sendsSnapshot() []AlertNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]AlertNotification, len(c.sends))
	copy(out, c.sends)
	return out
}

// fakeDeliveryStore is a minimal in-memory DeliveryStore recording every
// call, mirroring engine_test.go's fakeStore convention for the same
// reason: a real *store.Store is unnecessary weight for logic that never
// touches SQL.
type fakeDeliveryStore struct {
	mu         sync.Mutex
	deliveries []store.Delivery
	silences   []store.Silence
	events     []store.Event
	nextID     int64
}

func (f *fakeDeliveryStore) RecordDelivery(d store.Delivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries = append(f.deliveries, d)
	return nil
}

func (f *fakeDeliveryStore) AddSilence(s store.Silence) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	s.ID = f.nextID
	f.silences = append(f.silences, s)
	return s.ID, nil
}

func (f *fakeDeliveryStore) AppendEvent(e store.Event) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	e.ID = f.nextID
	f.events = append(f.events, e)
	return e.ID, nil
}

func (f *fakeDeliveryStore) deliveriesSnapshot() []store.Delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.Delivery, len(f.deliveries))
	copy(out, f.deliveries)
	return out
}

func (f *fakeDeliveryStore) eventsOfKind(kind string) []store.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Event
	for _, e := range f.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (f *fakeDeliveryStore) silencesSnapshot() []store.Silence {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.Silence, len(f.silences))
	copy(out, f.silences)
	return out
}

// testClock is a manually-advanced clock shared between a test and the
// Dispatcher under test -- every rate/window computation in dispatch.go
// reads time through Dispatcher.Clock, never time.Now, so a test can
// jump straight from "just exhausted the burst" to "an hour later"
// without a real sleep.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start int64) *testClock { return &testClock{now: time.Unix(start, 0)} }
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func fireNotification(ruleID, entity string) AlertNotification {
	return AlertNotification{
		Phase:    "fired",
		Rule:     store.AlertRule{ID: ruleID, Name: ruleID, Severity: "warning"},
		Instance: store.AlertInstance{RuleID: ruleID, Entity: entity},
		Summary:  ruleID + " fired on " + entity,
	}
}

func resolvedNotification(ruleID, entity string) AlertNotification {
	n := fireNotification(ruleID, entity)
	n.Phase = "resolved"
	n.Summary = ruleID + " resolved on " + entity
	return n
}

// --- resolved-notice toggle -------------------------------------------

func TestDispatcherResolvedNoticesToggleSuppressesResolvedPhase(t *testing.T) {
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	off := false
	d := NewDispatcher(st, []Channel{notify}, nil, func() bool { return off })

	d.Dispatch(resolvedNotification("disk-temp-high", "disk3"))
	require.Equal(t, 0, notify.sendCount(), "resolved notice must not reach any channel while the toggle is off")

	off = true
	d.Dispatch(resolvedNotification("disk-temp-high", "disk3"))
	require.Eventually(t, func() bool { return notify.sendCount() == 1 }, time.Second, 5*time.Millisecond)
}

func TestDispatcherResolvedNoticesDefaultTrueWhenNilFunc(t *testing.T) {
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, nil, nil)

	d.Dispatch(resolvedNotification("disk-temp-high", "disk3"))
	require.Eventually(t, func() bool { return notify.sendCount() == 1 }, time.Second, 5*time.Millisecond)
}

func TestDispatcherFiredPhaseNeverGatedByResolvedToggle(t *testing.T) {
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, nil, func() bool { return false })

	d.Dispatch(fireNotification("disk-temp-high", "disk3"))
	require.Eventually(t, func() bool { return notify.sendCount() == 1 }, time.Second, 5*time.Millisecond)
}

// --- notify-channel token bucket ---------------------------------------

func TestDispatcherBucketAllowsBurstOfFourThenThrottles(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	for i := 0; i < 4; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("rule-%d", i), "e"))
	}
	require.Eventually(t, func() bool { return notify.sendCount() == 4 }, time.Second, 5*time.Millisecond)

	// The 5th within the same instant exceeds the burst -- suppressed,
	// not delivered, and recorded as a ratelimited attempt.
	d.Dispatch(fireNotification("rule-5", "e"))
	time.Sleep(20 * time.Millisecond) // let any (wrongly) enqueued send land before asserting it didn't
	require.Equal(t, 4, notify.sendCount())

	found := false
	for _, del := range st.deliveriesSnapshot() {
		if del.Error == "ratelimited" {
			found = true
		}
	}
	require.True(t, found, "expected a ratelimited delivery record for the suppressed notification")
}

func TestDispatcherBucketRefillsAtTenPerHour(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	for i := 0; i < 4; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("rule-%d", i), "e"))
	}
	require.Eventually(t, func() bool { return notify.sendCount() == 4 }, time.Second, 5*time.Millisecond)

	d.Dispatch(fireNotification("rule-immediate", "e")) // burst exhausted
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 4, notify.sendCount())

	// 10/hour == one token every 360s; 361s later exactly one more send
	// should get through.
	clock.Advance(361 * time.Second)
	d.Dispatch(fireNotification("rule-after-refill", "e"))
	require.Eventually(t, func() bool { return notify.sendCount() == 5 }, time.Second, 5*time.Millisecond)
}

func TestDispatcherWebhookChannelUnthrottledByNotifyBucket(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	hook := newFakeChannel("webhook:home")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, clock.Now, nil)

	for i := 0; i < 20; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("rule-%d", i), "e"))
	}
	require.Eventually(t, func() bool { return hook.sendCount() == 20 }, time.Second, 5*time.Millisecond)
}

// TestDispatcherResolvedPhaseExemptFromNotifyBucket pins the accepted
// policy: resolves never consume or get blocked by the notify token
// bucket. Resolves are 1:1 bounded by fires that already paid a token,
// so exempting them can't be amplified -- and suppressing them is
// actively harmful: the human who saw "fired" would never learn the
// alert cleared.
func TestDispatcherResolvedPhaseExemptFromNotifyBucket(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	for i := 0; i < 4; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("rule-%d", i), "e"))
	}
	require.Eventually(t, func() bool { return notify.sendCount() == 4 }, time.Second, 5*time.Millisecond)

	// Bucket exhausted: a fire is suppressed, but a resolve still lands.
	d.Dispatch(fireNotification("rule-suppressed", "e"))
	d.Dispatch(resolvedNotification("rule-0", "e"))
	require.Eventually(t, func() bool { return notify.sendCount() == 5 }, time.Second, 5*time.Millisecond,
		"the bucket-exhausted resolve must still deliver")
	for _, n := range notify.sendsSnapshot() {
		if n.Phase == "resolved" {
			return
		}
	}
	t.Fatal("expected the resolved notification among the delivered sends")
}

func TestDispatcherThrottleCoalescesSuppressedIntoOneSummaryPerHour(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	for i := 0; i < 4; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("rule-%d", i), "e"))
	}
	require.Eventually(t, func() bool { return notify.sendCount() == 4 }, time.Second, 5*time.Millisecond)

	// Two more, both suppressed -- both must be named in the eventual
	// coalesced summary, each with the phase that was suppressed (a
	// swallowed renotify and a swallowed first fire read very
	// differently to the person catching up).
	d.Dispatch(fireNotification("suppressed-a", "entA"))
	renotify := fireNotification("suppressed-b", "entB")
	renotify.Phase = "renotify"
	d.Dispatch(renotify)
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 4, notify.sendCount())

	clock.Advance(3601 * time.Second)
	// The flush is checked lazily at the top of the next Dispatch call --
	// any notification does, including one that will itself go through
	// fine.
	d.Dispatch(fireNotification("suppressed-a", "entA")) // same key: a fresh cycle after the first resolved, legal
	require.Eventually(t, func() bool { return notify.sendCount() >= 6 }, time.Second, 5*time.Millisecond)

	var summary *AlertNotification
	for _, n := range notify.sendsSnapshot() {
		if n.Phase == "throttled" {
			cp := n
			summary = &cp
		}
	}
	require.NotNil(t, summary, "expected exactly one coalesced summary notification")
	require.Equal(t, "2 Gantry alerts suppressed", summary.Subject)
	require.Contains(t, summary.Summary, "suppressed-a/entA (fired)")
	require.Contains(t, summary.Summary, "suppressed-b/entB (renotify)")

	throttled := st.eventsOfKind("alert.delivery_throttled")
	require.Len(t, throttled, 1, "exactly one alert.delivery_throttled event per hour")
}

// --- per-(rule,entity) flap guard ---------------------------------------

func TestDispatcherFlapGuardDoesNotTripOnThirdCycle(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	for i := 0; i < 3; i++ {
		d.Dispatch(fireNotification("flappy", "disk4"))
		d.Dispatch(resolvedNotification("flappy", "disk4"))
		clock.Advance(time.Second)
	}
	require.Empty(t, st.silencesSnapshot(), "3 cycles must not trip the flap guard")
	require.Empty(t, st.eventsOfKind("alert.flapping"))
}

func TestDispatcherFlapGuardTripsOnFourthCycleWithSilenceAndOneNotification(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	for i := 0; i < 3; i++ {
		d.Dispatch(fireNotification("flappy", "disk4"))
		d.Dispatch(resolvedNotification("flappy", "disk4"))
		clock.Advance(time.Second)
	}
	// 4th fire within the same rolling hour trips it.
	d.Dispatch(fireNotification("flappy", "disk4"))

	require.Eventually(t, func() bool { return len(st.silencesSnapshot()) == 1 }, time.Second, 5*time.Millisecond)
	sil := st.silencesSnapshot()[0]
	require.Equal(t, "flappy", sil.RuleID)
	require.Equal(t, "disk4", sil.Entity)
	require.Equal(t, "flapping", sil.Reason)

	require.Len(t, st.eventsOfKind("alert.flapping"), 1)

	require.Eventually(t, func() bool {
		for _, n := range notify.sendsSnapshot() {
			if n.Phase == "flapping" {
				return true
			}
		}
		return false
	}, time.Second, 5*time.Millisecond)

	flapNotices := 0
	for _, n := range notify.sendsSnapshot() {
		if n.Phase == "flapping" {
			flapNotices++
		}
	}
	require.Equal(t, 1, flapNotices, "exactly one explanatory notification")
}

func TestDispatcherFlapGuardWindowIsRollingNotFixed(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, clock.Now, nil)

	d.Dispatch(fireNotification("flappy", "disk4"))
	d.Dispatch(resolvedNotification("flappy", "disk4"))
	clock.Advance(3601 * time.Second) // this cycle is now outside the rolling window

	for i := 0; i < 3; i++ {
		d.Dispatch(fireNotification("flappy", "disk4"))
		d.Dispatch(resolvedNotification("flappy", "disk4"))
		clock.Advance(time.Second)
	}
	require.Empty(t, st.silencesSnapshot(), "the first, now-expired cycle must not count toward the threshold")
}

// --- delivery ledger -----------------------------------------------------

func TestDispatcherRecordsSuccessfulDelivery(t *testing.T) {
	notify := newFakeChannel("notify")
	notify.result = SendResult{OK: true, Attempts: 1}
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, nil, nil)

	d.Dispatch(fireNotification("r", "e"))
	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) == 1 }, time.Second, 5*time.Millisecond)
	del := st.deliveriesSnapshot()[0]
	require.True(t, del.OK)
	require.Equal(t, "notify", del.Channel)
	require.Equal(t, "", del.Target)
	require.Equal(t, int64(1), del.Attempts)
}

func TestDispatcherRecordsFailedDeliveryAndDeliveryFailedEvent(t *testing.T) {
	hook := newFakeChannel("webhook:home")
	hook.result = SendResult{OK: false, Attempts: 3, Status: 500, Err: fmt.Errorf("server error")}
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)

	d.Dispatch(fireNotification("r", "e"))
	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) == 1 }, time.Second, 5*time.Millisecond)
	del := st.deliveriesSnapshot()[0]
	require.False(t, del.OK)
	require.Equal(t, "webhook", del.Channel)
	require.Equal(t, "home", del.Target)
	require.Equal(t, int64(3), del.Attempts)
	require.Equal(t, int64(500), del.Status)

	require.Eventually(t, func() bool { return len(st.eventsOfKind("alert.delivery_failed")) == 1 }, time.Second, 5*time.Millisecond)
	ev := st.eventsOfKind("alert.delivery_failed")[0]
	require.Equal(t, "webhook:home", ev.Entity)
}

func TestDispatcherDeliveryFailedEventRateLimitedPerChannelPerHour(t *testing.T) {
	clock := newTestClock(1_800_000_000)
	hook := newFakeChannel("webhook:home")
	hook.result = SendResult{OK: false, Attempts: 3, Status: 500, Err: fmt.Errorf("boom")}
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, clock.Now, nil)

	d.Dispatch(fireNotification("r1", "e"))
	d.Dispatch(fireNotification("r2", "e")) // different rule, same channel -- still only one event/hour
	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) == 2 }, time.Second, 5*time.Millisecond)
	require.Len(t, st.eventsOfKind("alert.delivery_failed"), 1)

	clock.Advance(3601 * time.Second)
	d.Dispatch(fireNotification("r3", "e"))
	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) == 3 }, time.Second, 5*time.Millisecond)
	require.Len(t, st.eventsOfKind("alert.delivery_failed"), 2)
}

// --- target URL never recorded ----------------------------------------------

// TestDispatcherNeverRecordsWebhookURLAnywhere drives a real
// WebhookChannel (secret-in-path URL, dead endpoint) through the full
// Dispatcher and then greps every surface a delivery failure reaches --
// ledger rows, events, and the channel's own health line. The path
// secret must appear in none of them: alert_deliveries.error and
// alert.delivery_failed's Detail are rendered verbatim in the UI, and
// the health line is the Settings card's own text.
func TestDispatcherNeverRecordsWebhookURLAnywhere(t *testing.T) {
	const secret = "PATH-SECRET-TOKEN"
	hook := NewWebhookChannel(WebhookTarget{
		ID: "dead", Name: "Dead", URL: "http://127.0.0.1:1/api/webhooks/1234/" + secret,
		Enabled: true, TimeoutS: 1,
	}, "v-test", nil)
	hook.Rand = nil
	hook.Sleep = func(time.Duration) {}

	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)
	t.Cleanup(d.Stop)

	d.Dispatch(fireNotification("r", "e"))
	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) == 1 }, 2*time.Second, 5*time.Millisecond)

	for _, del := range st.deliveriesSnapshot() {
		require.NotContains(t, del.Error, secret, "ledger row must not carry the target URL")
		require.NotEmpty(t, del.Error, "the failure reason itself must survive sanitizing")
	}
	st.mu.Lock()
	events := append([]store.Event(nil), st.events...)
	st.mu.Unlock()
	for _, ev := range events {
		require.NotContains(t, ev.Detail, secret, "event detail must not carry the target URL")
	}
	require.NotContains(t, hook.Health(), secret, "health line must not carry the target URL")
}

// --- queue overflow --------------------------------------------------------

func TestDispatcherQueueOverflowDropsOldestAndRecordsIt(t *testing.T) {
	hook := newFakeChannel("webhook:slow")
	hook.block = make(chan struct{}) // never released in this test: the one worker stays wedged on the first job
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)
	d.QueueCap = 2
	t.Cleanup(d.Stop)

	// job 0 is picked up by the single worker (started at construction)
	// and blocks forever (in this
	// test); jobs 1 and 2 fill the cap-2 queue; job 3 must drop job 1 (the
	// oldest STILL-QUEUED one, not the in-flight job 0) to make room.
	for i := 0; i < 4; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("r%d", i), "e"))
	}

	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) >= 1 }, time.Second, 5*time.Millisecond)
	dropped := false
	for _, del := range st.deliveriesSnapshot() {
		if del.Error == "queue overflow: dropped oldest" {
			dropped = true
		}
	}
	require.True(t, dropped, "expected a recorded drop once the bounded queue overflowed")
}

// --- shutdown responsiveness -------------------------------------------------

// stopAndAwaitWorkers stops d and waits for every worker to actually
// finish -- the exact wait main's shutdown sequence performs through
// Run(ctx, wg) -- failing the test if they take longer than limit.
func stopAndAwaitWorkers(t *testing.T, d *Dispatcher, limit time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	d.Stop()
	done := make(chan struct{})
	go func() { d.workersWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("workers still running %v after Stop()", limit)
	}
	return time.Since(start)
}

// TestDispatcherStopAbortsInFlightWebhookSend wedges a real webhook
// channel mid-request (endpoint accepts and never responds, 30s
// per-attempt timeout) and then stops the dispatcher: the in-flight
// Send must abort via the dispatcher's stop context rather than
// running out its full timeout-and-retry budget (~100s).
func TestDispatcherStopAbortsInFlightWebhookSend(t *testing.T) {
	inFlight := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight <- struct{}{}
		<-release
	}))
	defer srv.Close()
	defer close(release)

	hook := NewWebhookChannel(WebhookTarget{ID: "wedged", URL: srv.URL, Enabled: true, TimeoutS: 30}, "v-test", nil)
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)

	d.Dispatch(fireNotification("r", "e"))
	select {
	case <-inFlight:
	case <-time.After(2 * time.Second):
		t.Fatal("request never reached the wedged endpoint")
	}

	elapsed := stopAndAwaitWorkers(t, d, 2*time.Second)
	require.Less(t, elapsed, time.Second, "an in-flight Send must observe Stop, not its 30s timeout")
}

// TestDispatcherStopInterruptsWebhookBackoff parks a real webhook
// channel in its first REAL backoff gap (2s, endpoint answering 500)
// and stops the dispatcher: the backoff wait must observe stop rather
// than sleeping through the remaining 2s + 8s + attempts.
func TestDispatcherStopInterruptsWebhookBackoff(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	hook := NewWebhookChannel(WebhookTarget{ID: "flaky", URL: srv.URL, Enabled: true, TimeoutS: 5}, "v-test", nil)
	hook.Rand = nil // exact 2s first gap
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)

	d.Dispatch(fireNotification("r", "e"))
	require.Eventually(t, func() bool { return calls.Load() >= 1 }, 2*time.Second, 5*time.Millisecond,
		"first attempt must have failed and entered backoff")

	elapsed := stopAndAwaitWorkers(t, d, 2*time.Second)
	require.Less(t, elapsed, time.Second, "a backoff wait must observe Stop, not sleep out its full duration")
	require.EqualValues(t, 1, calls.Load(), "no further attempts after Stop")
}

// TestDispatcherStopDrainsQueuedJobsIntoLedger pins shutdown
// accounting: jobs enqueued but never started when Stop() lands must
// each get a dropped-delivery ledger row rather than vanish with the
// queue -- the ledger's whole point is that no notification disappears
// without a row saying what happened to it.
func TestDispatcherStopDrainsQueuedJobsIntoLedger(t *testing.T) {
	hook := newFakeChannel("webhook:slow")
	hook.block = make(chan struct{}) // never released: the worker wedges on job 0 until Stop aborts it
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)

	// job 0 is picked up by the worker and wedges; jobs 1-3 sit queued.
	for i := 0; i < 4; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("r%d", i), "e"))
	}
	require.Eventually(t, func() bool { return hook.entered.Load() == 1 }, time.Second, 5*time.Millisecond,
		"the worker must be wedged inside job 0's Send before Stop lands")

	stopAndAwaitWorkers(t, d, 2*time.Second)

	require.Eventually(t, func() bool { return len(st.deliveriesSnapshot()) == 4 }, time.Second, 5*time.Millisecond,
		"every dispatched notification gets a ledger row: 1 aborted in-flight + 3 shutdown drops")
	drops := 0
	for _, del := range st.deliveriesSnapshot() {
		require.False(t, del.OK)
		if del.Error == "shutdown: dropped queued" {
			drops++
		}
	}
	require.Equal(t, 3, drops, "each queued-but-unstarted job records as a shutdown drop")
}

// --- non-blocking dispatch -------------------------------------------------

func TestDispatcherDispatchDoesNotBlockOnAWedgedChannel(t *testing.T) {
	hook := newFakeChannel("webhook:slow")
	hook.block = make(chan struct{})
	defer close(hook.block)
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{hook}, nil, nil)
	t.Cleanup(d.Stop)

	start := time.Now()
	for i := 0; i < 10; i++ {
		d.Dispatch(fireNotification(fmt.Sprintf("r%d", i), "e"))
	}
	elapsed := time.Since(start)
	require.Less(t, elapsed, 200*time.Millisecond, "Dispatch must enqueue and return promptly even while the channel's worker is wedged")
}

// TestDispatcherConcurrentDispatchIsRaceFree exercises Dispatch from
// multiple goroutines at once (the shape a `go test -race` run needs to
// actually exercise the mutex-guarded bucket/flap state) while a
// background counter confirms nothing panics or deadlocks.
func TestDispatcherConcurrentDispatchIsRaceFree(t *testing.T) {
	notify := newFakeChannel("notify")
	st := &fakeDeliveryStore{}
	d := NewDispatcher(st, []Channel{notify}, nil, nil)

	var wg sync.WaitGroup
	var total atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				d.Dispatch(fireNotification(fmt.Sprintf("r-%d-%d", g, i), "e"))
				total.Add(1)
			}
		}(g)
	}
	wg.Wait()
	require.EqualValues(t, 200, total.Load())
}

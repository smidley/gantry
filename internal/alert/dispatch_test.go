package alert

import (
	"context"
	"fmt"
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

	result SendResult
	block  chan struct{} // if non-nil, Send waits for a receive on this before returning
}

func newFakeChannel(id string) *fakeChannel {
	return &fakeChannel{id: id, health: "ok", result: SendResult{OK: true, Attempts: 1}}
}

func (c *fakeChannel) ID() string     { return c.id }
func (c *fakeChannel) Health() string { return c.health }

func (c *fakeChannel) Send(_ context.Context, n AlertNotification) SendResult {
	if c.block != nil {
		<-c.block
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
	// coalesced summary.
	d.Dispatch(fireNotification("suppressed-a", "entA"))
	d.Dispatch(fireNotification("suppressed-b", "entB"))
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
	require.Contains(t, summary.Summary, "suppressed-a")
	require.Contains(t, summary.Summary, "entA")
	require.Contains(t, summary.Summary, "suppressed-b")
	require.Contains(t, summary.Summary, "entB")

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

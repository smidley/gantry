package alert

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// SendResult is what a Channel reports back from one Send call. Richer
// than a plain error because the webhook channel's own internal retry
// loop (channel_webhook.go) can carry out several HTTP attempts before
// returning, and alert_deliveries records the OUTCOME -- one row per
// notification/channel pair -- not each intermediate try, so Attempts
// and Status have to travel back out of Send somehow.
type SendResult struct {
	OK       bool
	Attempts int
	Status   int // HTTP status when applicable, 0 otherwise (e.g. the notify channel)
	Err      error
}

// Channel is one delivery target: the notify-spool (channel_notify.go)
// or one configured webhook (channel_webhook.go, ID "webhook:<target-id>").
type Channel interface {
	ID() string     // "notify" | "webhook:<target-id>"
	Health() string // "ok" or the enable hint, the collect.Status/Sources-map convention
	Send(ctx context.Context, n AlertNotification) SendResult
}

// DeliveryStore is the narrow slice of *store.Store the Dispatcher needs:
// recording each delivery attempt and, for the flap guard, inserting a
// silence and appending its own explanatory events. Deliberately a
// separate interface from engine.go's Store -- a different consumer with
// different needs, the same "narrow, package-local" convention, not the
// same type -- so this file never has to touch already-shipped engine.go
// logic, only *store.Store, which satisfies both.
type DeliveryStore interface {
	RecordDelivery(store.Delivery) error
	AddSilence(store.Silence) (int64, error)
	AppendEvent(store.Event) (int64, error)
}

const (
	// notifyBucketCapacity/notifyBucketRefillPerSecond implement the
	// plan's "10 files/hour, burst 4" over the notify channel only --
	// webhooks never consult this bucket at all (buildWebhookChannels'
	// own doc explains why: they're machine-facing and can rate-limit
	// themselves).
	notifyBucketCapacity        = 4.0
	notifyBucketRefillPerSecond = 10.0 / 3600.0

	// throttleWindowSeconds is both how long the coalesced-summary
	// aggregation window stays open once the bucket first runs dry, and
	// the delivery-failure event's own per-channel rate limit -- both are
	// "at most one of this per hour" rules, so they share the constant.
	throttleWindowSeconds = 3600

	flapWindowSeconds  = 3600 // per-(rule,entity) flap guard's rolling window
	flapThreshold      = 4    // fire/resolve cycles within the window that trips it
	flapSilenceSeconds = 3600 // auto-silence duration once tripped

	defaultChannelQueueCap = 256
)

type deliveryJob struct{ n AlertNotification }

type flapKey struct{ ruleID, entity string }

type suppressedItem struct{ ruleName, entity, severity string }

// Dispatcher is the policy layer between the engine's lifecycle machine
// and every configured Channel: it owns the notify-only rate limiter and
// coalesced-summary throttle, the per-(rule,entity) flap guard, the
// global resolved-notice toggle, the delivery ledger, and per-channel
// async delivery so a wedged or slow channel can never make the alert
// engine's own Tick block.
//
// Every per-channel worker goroutine starts lazily on the first Dispatch
// call (see ensureStarted), not in NewDispatcher itself: QueueCap is a
// plain field, not a constructor argument, and setting it before that
// first call (main.go never needs to; tests that want a smaller cap for
// a fast overflow test do) is race-free specifically because nothing
// reads it until then.
type Dispatcher struct {
	Channels        []Channel
	Store           DeliveryStore
	Clock           func() time.Time
	ResolvedNotices func() bool // nil => resolved notices enabled
	QueueCap        int         // <=0 => defaultChannelQueueCap, resolved at first use

	startOnce sync.Once
	queues    map[string]chan deliveryJob

	stop      chan struct{}
	stopOnce  sync.Once
	workersWG sync.WaitGroup

	mu sync.Mutex // guards everything below

	bucketTokens    float64
	bucketStamp     int64
	suppressedItems []suppressedItem
	windowStart     int64 // 0 => no open suppression window

	flapFires map[flapKey][]int64

	lastFailureEvent map[string]int64 // channel id -> last alert.delivery_failed ts
}

// NewDispatcher wires a Dispatcher over channels. clock nil defaults to
// time.Now; resolvedNotices nil defaults to "always enabled".
func NewDispatcher(store DeliveryStore, channels []Channel, clock func() time.Time, resolvedNotices func() bool) *Dispatcher {
	if clock == nil {
		clock = time.Now
	}
	return &Dispatcher{
		Channels:        channels,
		Store:           store,
		Clock:           clock,
		ResolvedNotices: resolvedNotices,
		queues:          make(map[string]chan deliveryJob, len(channels)),
		stop:            make(chan struct{}),
	}
}

func (d *Dispatcher) now() int64 { return d.Clock().Unix() }

func (d *Dispatcher) resolvedEnabled() bool {
	if d.ResolvedNotices == nil {
		return true
	}
	return d.ResolvedNotices()
}

// ensureStarted spawns exactly one worker goroutine per channel, exactly
// once, the first time this Dispatcher is ever asked to deliver anything.
func (d *Dispatcher) ensureStarted() {
	d.startOnce.Do(func() {
		queueCap := d.QueueCap
		if queueCap <= 0 {
			queueCap = defaultChannelQueueCap
		}
		for _, ch := range d.Channels {
			q := make(chan deliveryJob, queueCap)
			d.queues[ch.ID()] = q
			d.workersWG.Add(1)
			go d.worker(ch, q)
		}
	})
}

func (d *Dispatcher) worker(ch Channel, q chan deliveryJob) {
	defer d.workersWG.Done()
	for {
		select {
		case <-d.stop:
			return
		case job := <-q:
			d.deliver(ch, job.n)
		}
	}
}

// Stop signals every worker to exit once it finishes whatever it's
// currently delivering; safe to call more than once or never (a
// Dispatcher nobody Stop()s just leaks a handful of idle goroutines for
// the process's lifetime, same as an un-Drain()ed Broadcaster would).
func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() { close(d.stop) })
}

// Run wires this Dispatcher's shutdown to ctx, the same Run(ctx, wg)
// convention collect.Registry and alert.Engine already use. wg.Wait() in
// main's shutdown sequence blocks until every per-channel worker has
// actually finished delivering whatever it was mid-send on -- not merely
// until Stop() has been asked for -- because the watcher goroutine added
// here waits on the same workersWG the workers themselves report to.
func (d *Dispatcher) Run(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		d.Stop()
		d.workersWG.Wait()
	}()
}

// Dispatch is the engine's Dispatch seam: never blocks past enqueueing,
// regardless of how slow or wedged any channel's own worker currently is.
func (d *Dispatcher) Dispatch(n AlertNotification) {
	d.ensureStarted()
	now := d.now()
	d.flushThrottleIfDue(now)

	if n.Phase == "resolved" && !d.resolvedEnabled() {
		return
	}
	if n.Phase == "fired" {
		d.trackFlap(n, now)
	}
	for _, ch := range d.Channels {
		d.enqueue(ch, n, now)
	}
}

// enqueue applies the notify-only bucket, then hands off to send. Every
// other channel (every webhook target) is deliberately unthrottled here.
func (d *Dispatcher) enqueue(ch Channel, n AlertNotification, now int64) {
	if ch.ID() == "notify" {
		if !d.takeToken(now) {
			d.suppress(n, now)
			d.recordRateLimited(ch, n, now)
			return
		}
	}
	d.send(ch, n)
}

// send enqueues onto ch's own bounded queue without blocking. On overflow
// it drops the OLDEST still-queued job (never the in-flight one a worker
// already picked up) to make room for the newest, recording the dropped
// notification as a failed delivery through the same ledger + rate-
// limited event a real Send failure uses.
func (d *Dispatcher) send(ch Channel, n AlertNotification) {
	q := d.queues[ch.ID()] // safe unguarded read: fully populated before ensureStarted's sync.Once.Do returns, which every caller of send has already observed
	if q == nil {
		d.deliver(ch, n) // no queue (Channels didn't include ch): still worth attempting rather than silently dropping
		return
	}
	select {
	case q <- deliveryJob{n}:
		return
	default:
	}
	select {
	case dropped := <-q:
		d.recordDrop(ch, dropped.n)
	default:
	}
	select {
	case q <- deliveryJob{n}:
	default: // lost a race with another producer; the queue is still saturated with useful work either way
	}
}

func (d *Dispatcher) deliver(ch Channel, n AlertNotification) {
	res := ch.Send(context.Background(), n)
	d.recordDelivery(ch, n, res)
	if !res.OK {
		d.recordFailureEvent(ch.ID(), res)
	}
}

func (d *Dispatcher) recordDelivery(ch Channel, n AlertNotification, res SendResult) {
	if d.Store == nil {
		return
	}
	channel, target := splitChannelID(ch.ID())
	attempts := res.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	errStr := ""
	if res.Err != nil {
		errStr = res.Err.Error()
	}
	if err := d.Store.RecordDelivery(store.Delivery{
		InstanceID: n.Instance.ID, TS: d.now(), Channel: channel, Target: target,
		Phase: n.Phase, Attempts: int64(attempts), OK: res.OK, Status: int64(res.Status), Error: errStr,
	}); err != nil {
		log.Printf("alert dispatch: record delivery (%s): %v", ch.ID(), err)
	}
}

func (d *Dispatcher) recordRateLimited(ch Channel, n AlertNotification, now int64) {
	if d.Store == nil {
		return
	}
	channel, target := splitChannelID(ch.ID())
	if err := d.Store.RecordDelivery(store.Delivery{
		InstanceID: n.Instance.ID, TS: now, Channel: channel, Target: target,
		Phase: n.Phase, Attempts: 0, OK: false, Error: "ratelimited",
	}); err != nil {
		log.Printf("alert dispatch: record delivery (ratelimited): %v", err)
	}
}

var errQueueOverflow = errors.New("queue overflow: dropped oldest")

func (d *Dispatcher) recordDrop(ch Channel, n AlertNotification) {
	d.recordDelivery(ch, n, SendResult{OK: false, Err: errQueueOverflow})
	d.recordFailureEvent(ch.ID(), SendResult{OK: false, Err: errQueueOverflow})
}

// recordFailureEvent appends alert.delivery_failed, rate-limited to once
// per channel id per hour -- a dead webhook (or a full queue dropping
// jobs) retries or re-fires far more often than that, and without this
// cap it would flood the Events feed it was meant to inform, not spam.
func (d *Dispatcher) recordFailureEvent(chID string, res SendResult) {
	now := d.now()
	d.mu.Lock()
	if d.lastFailureEvent == nil {
		d.lastFailureEvent = map[string]int64{}
	}
	if last, ok := d.lastFailureEvent[chID]; ok && now-last < throttleWindowSeconds {
		d.mu.Unlock()
		return
	}
	d.lastFailureEvent[chID] = now
	d.mu.Unlock()

	if d.Store == nil {
		return
	}
	errStr := ""
	if res.Err != nil {
		errStr = res.Err.Error()
	}
	detail := errStr
	if res.Status != 0 {
		detail = fmt.Sprintf("status %d: %s", res.Status, errStr)
	}
	if _, err := d.Store.AppendEvent(store.Event{Kind: "alert.delivery_failed", Entity: chID, Severity: "warning", Detail: detail}); err != nil {
		log.Printf("alert dispatch: append alert.delivery_failed: %v", err)
	}
}

// splitChannelID turns a Channel.ID() into alert_deliveries' own
// channel/target column split ("notify"/"" or "webhook"/"<target-id>").
func splitChannelID(id string) (channel, target string) {
	if rest, ok := strings.CutPrefix(id, "webhook:"); ok {
		return "webhook", rest
	}
	return id, ""
}

// --- notify-only token bucket + coalesced throttle summary ---------------

// takeToken applies the classic token-bucket refill math against
// Dispatcher.Clock: capacity notifyBucketCapacity, refilling at
// notifyBucketRefillPerSecond (10/hour). now==0 on the very first call
// (bucketStamp's zero value) is indistinguishable from a genuine unix
// timestamp of zero, but no real clock ever reports that, so it's a safe
// "uninitialized" sentinel here exactly like Engine's own cursorSet
// pattern.
func (d *Dispatcher) takeToken(now int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.bucketStamp == 0:
		d.bucketTokens = notifyBucketCapacity
		d.bucketStamp = now
	case now > d.bucketStamp:
		d.bucketTokens += float64(now-d.bucketStamp) * notifyBucketRefillPerSecond
		if d.bucketTokens > notifyBucketCapacity {
			d.bucketTokens = notifyBucketCapacity
		}
		d.bucketStamp = now
	}
	if d.bucketTokens < 1 {
		return false
	}
	d.bucketTokens--
	return true
}

func (d *Dispatcher) suppress(n AlertNotification, now int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.windowStart == 0 {
		d.windowStart = now
	}
	d.suppressedItems = append(d.suppressedItems, suppressedItem{
		ruleName: n.Rule.Name, entity: n.Instance.Entity, severity: n.Rule.Severity,
	})
}

// flushThrottleIfDue is checked at the top of every Dispatch call: once
// an open suppression window has run a full hour, it sends the coalesced
// summary (bypassing the bucket entirely -- this summary IS the bucket's
// own release valve) and appends alert.delivery_throttled exactly once
// for that window. This is a lazy, next-call-driven flush rather than a
// background timer: the Dispatcher already only hears about the world
// through Dispatch calls, and a rule flapping badly enough to exhaust a
// 4-burst bucket is, in practice, going to keep generating them. A fully
// quiet hour immediately after the last suppressed alert would leave
// that hour's summary undelivered until the next alert of any kind --
// a documented, acceptable simplification for a single-user appliance,
// not a background scheduler problem worth its own goroutine.
func (d *Dispatcher) flushThrottleIfDue(now int64) {
	d.mu.Lock()
	if d.windowStart == 0 || now-d.windowStart < throttleWindowSeconds {
		d.mu.Unlock()
		return
	}
	items := d.suppressedItems
	d.suppressedItems = nil
	d.windowStart = 0
	d.mu.Unlock()

	if len(items) == 0 {
		return
	}

	maxSeverity := "info"
	lines := make([]string, 0, len(items))
	for _, it := range items {
		if rankOf(it.severity) > rankOf(maxSeverity) {
			maxSeverity = it.severity
		}
		lines = append(lines, it.ruleName+"/"+it.entity)
	}
	subject := fmt.Sprintf("%d Gantry alerts suppressed", len(items))
	summary := strings.Join(lines, ", ")

	for _, ch := range d.Channels {
		if ch.ID() != "notify" {
			continue
		}
		d.send(ch, AlertNotification{
			Phase: "throttled", Subject: subject, Summary: summary,
			Rule: store.AlertRule{Severity: maxSeverity},
		})
	}
	if d.Store != nil {
		if _, err := d.Store.AppendEvent(store.Event{
			Kind: "alert.delivery_throttled", Severity: "info",
			Detail: fmt.Sprintf("%d notifications suppressed: %s", len(items), summary),
		}); err != nil {
			log.Printf("alert dispatch: append alert.delivery_throttled: %v", err)
		}
	}
}

// --- per-(rule,entity) flap guard -----------------------------------------

// trackFlap counts "fired" phase notifications for (rule, entity) within
// a rolling flapWindowSeconds. Counting fires alone (never resolves) is
// enough to count fire/resolve CYCLES: idx_alert_active guarantees the
// engine can never dispatch a fresh "fired" for a pair whose previous
// instance hasn't already resolved, so a new fire arriving here always
// means the prior cycle is over.
func (d *Dispatcher) trackFlap(n AlertNotification, now int64) {
	key := flapKey{n.Rule.ID, n.Instance.Entity}

	d.mu.Lock()
	if d.flapFires == nil {
		d.flapFires = map[flapKey][]int64{}
	}
	cutoff := now - flapWindowSeconds
	kept := d.flapFires[key][:0]
	for _, ts := range d.flapFires[key] {
		if ts > cutoff {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	trip := len(kept) >= flapThreshold
	if trip {
		delete(d.flapFires, key) // the silence itself is the cooldown; no need to keep counting while it's active
	} else {
		d.flapFires[key] = kept
	}
	d.mu.Unlock()

	if trip {
		d.silenceFlapping(n, now)
	}
}

// silenceFlapping records the auto-silence + alert.flapping event and
// sends exactly one explanatory notification to every configured channel
// (webhooks included: the silence suppresses their deliveries for this
// pair too, so a machine consumer benefits from knowing why just as much
// as a human does). Sent directly through send(), bypassing Dispatch's
// own policy layer entirely -- re-entering Dispatch would let phase
// "flapping" trip trackFlap a second time, and letting it consume the
// notify bucket would risk this exact explanation being the thing that
// gets coalesced away.
func (d *Dispatcher) silenceFlapping(n AlertNotification, now int64) {
	if d.Store != nil {
		if _, err := d.Store.AddSilence(store.Silence{
			RuleID: n.Rule.ID, Entity: n.Instance.Entity, Until: now + flapSilenceSeconds,
			Reason: "flapping", CreatedAt: now,
		}); err != nil {
			log.Printf("alert dispatch: add flap-guard silence (%s/%s): %v", n.Rule.ID, n.Instance.Entity, err)
		}
		if _, err := d.Store.AppendEvent(store.Event{
			Kind: "alert.flapping", Entity: n.Instance.Entity, Severity: "warning",
			Detail: fmt.Sprintf("%s fired %d times in the last hour; silenced for 1h", n.Rule.Name, flapThreshold),
		}); err != nil {
			log.Printf("alert dispatch: append alert.flapping: %v", err)
		}
	}

	entity := n.Instance.Entity
	if entity == "" {
		entity = "host"
	}
	explain := AlertNotification{
		Phase: "flapping", Rule: n.Rule, Instance: n.Instance,
		Summary: fmt.Sprintf("%s on %s fired %d times in the last hour and has been silenced for 1h", n.Rule.Name, entity, flapThreshold),
	}
	for _, ch := range d.Channels {
		d.send(ch, explain)
	}
}

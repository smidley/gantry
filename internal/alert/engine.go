package alert

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// Store is the narrow slice of *store.Store the engine needs -- package-
// local, mirroring the docker/unraid/fake packages' own EventSink
// convention, so this package never imports anything beyond store's
// plain data types.
type Store interface {
	AlertRules(context.Context) ([]store.AlertRule, error)
	ActiveAlertInstances(context.Context) ([]store.AlertInstance, error)
	UpsertAlertInstance(store.AlertInstance) (int64, error)
	ResolveAlertInstance(id, at int64, reason string) error
	Silences(context.Context, int64) ([]store.Silence, error)
	QueryEventsSince(context.Context, int64, int) ([]store.Event, error)
	MaxEventID(context.Context) (int64, error)
	AppendEvent(store.Event) (int64, error)
}

// FleetMember is the engine's view of one container for boot-seeding
// purposes -- name, state, and health only, the exact inputs the
// container-unhealthy gate needs. main.go builds this from dc.All() (real
// mode) plus the fake generator's synthetic fleet.
type FleetMember struct {
	Name   string
	State  string
	Health string
}

// AlertNotification is what the engine hands to Dispatch when an alert
// fires, resolves, or re-notifies while firing. Named AlertNotification,
// not Notification -- notify.go already claims that name for the
// lower-level dynamix-file shape a future notify channel would translate
// this into.
type AlertNotification struct {
	Phase    string // "fired" | "resolved" | "renotify"
	Instance store.AlertInstance
	Rule     store.AlertRule
	Summary  string
}

// Engine evaluates every enabled alert rule on its own cadence, driving
// each (rule, entity) pair through the pending/firing/resolved lifecycle
// and writing the result through Store. A nil Dispatch means "evaluate
// but never deliver": every state transition, event, and notify-
// bookkeeping field still updates exactly as it would with a real
// channel wired -- there is simply nowhere to send the notification yet.
type Engine struct {
	Store    Store
	Match    func(kind, metric string, since int64) (samples map[string][]store.Sample, oldestTS map[string]int64)
	ClassOf  func(kind, entity string) string
	Fleet    func() []FleetMember
	Dispatch func(AlertNotification)
	Clock    func() time.Time

	// cursorSet latches once, on the engine's true first Tick, independent
	// of booted below: the event cursor must clamp to MaxEventID right
	// away regardless of whether the fleet is ready yet, or a still-empty
	// first tick would replay the whole events table as fresh alerts.
	cursorSet   bool
	eventCursor int64

	// booted latches once bootSeed has actually run against a non-empty
	// Fleet() (or Fleet is nil, meaning one is never coming) -- see
	// bootSeed's own doc for why an empty read must not latch this.
	booted bool

	// missingSince tracks, per firing threshold instance id, the tick a
	// series was first observed absent from Match. There is no schema
	// column for this -- alert_instances has nothing like "last seen" --
	// and it only needs to survive one process's lifetime: a restart
	// already re-derives every active instance straight from the store,
	// and simply restarting the absence clock at zero is the safe
	// default (see handleAbsent).
	missingSince map[int64]int64
}

// New wires an Engine. clock defaults to time.Now when nil, the same
// nil-default convention store.Open's own clock parameter uses.
func New(st Store, match func(kind, metric string, since int64) (map[string][]store.Sample, map[string]int64), classOf func(kind, entity string) string, fleet func() []FleetMember, dispatch func(AlertNotification), clock func() time.Time) *Engine {
	if clock == nil {
		clock = time.Now
	}
	return &Engine{
		Store: st, Match: match, ClassOf: classOf, Fleet: fleet, Dispatch: dispatch, Clock: clock,
		missingSince: make(map[int64]int64),
	}
}

// Run ticks the engine every `every` until ctx is cancelled. A failing
// Tick (a store read the whole pass depends on, e.g. AlertRules itself
// erroring) is logged, not fatal -- the next tick tries again.
func (e *Engine) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Tick(ctx); err != nil {
				log.Printf("alert engine: tick: %v", err)
			}
		}
	}
}

// instanceKey is the same (rule_id, entity) pair idx_alert_active
// enforces, used here as the in-memory index Tick keeps in sync with the
// store across every transition it makes in one pass.
type instanceKey struct{ RuleID, Entity string }

func keyOf(i store.AlertInstance) instanceKey { return instanceKey{i.RuleID, i.Entity} }

// Tick runs one full evaluation pass: threshold rules grouped by
// (kind, metric) against the live ring, then event rules off the id
// cursor, with silences and rule enablement applied throughout. Safe to
// call from a test with an injected clock; each call re-reads rules,
// active instances, and silences from Store, so Tick has no hidden state
// of its own beyond the boot-once cursor/seed and the no-data timers.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.Clock().Unix()

	rules, err := e.Store.AlertRules(ctx)
	if err != nil {
		return fmt.Errorf("alert engine: alert rules: %w", err)
	}
	active, err := e.Store.ActiveAlertInstances(ctx)
	if err != nil {
		return fmt.Errorf("alert engine: active alert instances: %w", err)
	}
	silences, err := e.Store.Silences(ctx, now)
	if err != nil {
		return fmt.Errorf("alert engine: silences: %w", err)
	}

	ruleByID := make(map[string]store.AlertRule, len(rules))
	for _, r := range rules {
		ruleByID[r.ID] = r
	}

	// An instance whose rule vanished (deleted) or was disabled has
	// nothing left to evaluate against; resolve it before anything else
	// this tick and keep it out of activeIdx entirely.
	activeIdx := make(map[instanceKey]store.AlertInstance, len(active))
	for _, inst := range active {
		if r, ok := ruleByID[inst.RuleID]; ok && r.Enabled {
			activeIdx[keyOf(inst)] = inst
			continue
		}
		e.resolveDisabled(inst, now)
	}

	if !e.cursorSet {
		cursor, err := e.Store.MaxEventID(ctx)
		if err != nil {
			return fmt.Errorf("alert engine: max event id: %w", err)
		}
		e.eventCursor = cursor
		e.cursorSet = true
	}
	if !e.booted && e.bootSeed(ruleByID, activeIdx, silences, now) {
		e.booted = true
	}

	e.tickThresholds(rules, activeIdx, silences, now)
	e.tickEvents(ctx, ruleByID, activeIdx, silences, now)
	return nil
}

// resolveDisabled resolves any-state -> resolved(rule-disabled): for a
// FIRING instance an event is appended (the alert's own history should
// show why it closed) but nothing is dispatched -- a rule someone just
// disabled should not also page them. A pending instance never fired, so
// it gets neither, matching resolveSilent's doctrine everywhere else in
// this file (F8). A resolve error is logged and the tick continues; see
// resolveNotify's own doc for why this can never abort the pass. This
// bypasses the no-data timeout entirely, so it cleans up missingSince
// itself too (F9) -- see resolveSilent/resolveNotify for the same sweep
// on every other resolve path.
func (e *Engine) resolveDisabled(inst store.AlertInstance, now int64) {
	delete(e.missingSince, inst.ID)
	if err := e.Store.ResolveAlertInstance(inst.ID, now, "rule-disabled"); err != nil {
		log.Printf("alert engine: resolve instance %d (%s/%s) for disabled/deleted rule: %v", inst.ID, inst.RuleID, inst.Entity, err)
	}
	if inst.State == "pending" {
		return
	}
	if _, err := e.Store.AppendEvent(store.Event{Kind: "alert.resolved", Entity: inst.Entity, Severity: "info", Detail: inst.RuleID + " rule disabled"}); err != nil {
		log.Printf("alert engine: append alert.resolved event: %v", err)
	}
}

// bootSeed runs on every tick until it can report done=true: an event
// rule can only ever see events appended after this process started, so
// a container that was already unhealthy before boot would otherwise
// never alert. This walks the current fleet and feeds a synthetic
// container.health event through the exact same per-rule matching
// processEvent uses for a real one -- it is never appended to the events
// table (nothing "happened" just now) and never advances the cursor, but
// the resulting alert.fired IS real: the condition is real and ongoing,
// Gantry just noticed it late.
//
// done is false when Fleet() itself reports nothing yet: the docker
// collector's first inventory poll can easily lag the engine's own first
// tick (t+10s), and latching "seeded" on that empty read would silently
// skip the one case boot seeding exists for -- Tick retries every
// subsequent tick until Fleet() actually reports something (F3). A nil
// Fleet is different: there is no fleet-following capability coming,
// ever, so that's done immediately.
func (e *Engine) bootSeed(ruleByID map[string]store.AlertRule, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) (done bool) {
	if e.Fleet == nil {
		return true
	}
	members := e.Fleet()
	if len(members) == 0 {
		return false
	}
	eventRules := enabledEventRules(ruleByID)
	for _, m := range members {
		if m.State != "running" || m.Health != "unhealthy" {
			continue
		}
		ev := store.Event{Kind: "container.health", Entity: m.Name, Severity: "warning", Detail: "unhealthy at boot"}
		e.processEvent(ev, eventRules, activeIdx, silences, now)
	}
	return true
}

func enabledEventRules(ruleByID map[string]store.AlertRule) []store.AlertRule {
	out := make([]store.AlertRule, 0, len(ruleByID))
	for _, r := range ruleByID {
		if r.Enabled && r.Type == "event" {
			out = append(out, r)
		}
	}
	return out
}

// --- threshold rules -------------------------------------------------------

type ruleGroup struct{ kind, metric string }

// tickThresholds groups enabled threshold rules by (kind, metric) and
// calls Match exactly once per group -- the "one Live lock pass per
// distinct (kind, metric) across enabled rules" cadence the design
// commits to, regardless of how many rules share that pair.
func (e *Engine) tickThresholds(rules []store.AlertRule, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) {
	groups := map[ruleGroup][]store.AlertRule{}
	for _, r := range rules {
		if r.Enabled && r.Type == "threshold" {
			g := ruleGroup{r.Kind, r.Metric}
			groups[g] = append(groups[g], r)
		}
	}

	for g, grules := range groups {
		var maxWindow int64
		for _, r := range grules {
			if r.ForSeconds > maxWindow {
				maxWindow = r.ForSeconds
			}
			if r.ClearSeconds > maxWindow {
				maxWindow = r.ClearSeconds
			}
		}
		var byEntity map[string][]store.Sample
		var oldestByEntity map[string]int64
		if e.Match != nil {
			byEntity, oldestByEntity = e.Match(g.kind, g.metric, now-maxWindow)
		}
		for _, r := range grules {
			e.evalThresholdRule(r, byEntity, oldestByEntity, activeIdx, silences, now)
		}
	}
}

// evalThresholdRule evaluates one rule against every in-scope entity
// (currently reporting the metric AND matching entity_glob/entity_class),
// then separately resolves any active instance whose entity no longer
// matches -- a rule edit that narrows entity_glob/entity_class must not
// leave a stray instance firing forever just because its own metric
// keeps reporting a breaching value on its own terms (see
// resolveOutOfScope; F6).
//
// Wrapped in its own recover: a single malformed rule (bad data flowing
// into a glob or a ClassOf callback) must never take the rest of the
// tick down with it, mirroring collect.safeTick's own per-collector
// isolation.
func (e *Engine) evalThresholdRule(r store.AlertRule, byEntity map[string][]store.Sample, oldestByEntity map[string]int64, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("alert engine: rule %q panicked: %v", r.ID, p)
		}
	}()

	matchesScope := func(entity string) bool {
		class := ""
		if e.ClassOf != nil {
			class = e.ClassOf(r.Kind, entity)
		}
		return MatchEntity(r.EntityGlob, entity) && MatchClass(r.EntityClass, class)
	}

	// Scope is a property of the entity, checked independently of whether
	// it has data THIS tick -- an active instance with no current window
	// data (byEntity lacks the key) is "absent", handled below by
	// evalThresholdEntity's own grace period, not "out of scope" just for
	// having nothing to report right now. Deriving scope from byEntity's
	// keys instead of checking the entity directly would conflate the
	// two and resolve an absent-but-still-in-scope instance immediately,
	// skipping no-data's timer entirely.
	candidates := map[string]struct{}{}
	for entity := range byEntity {
		if matchesScope(entity) {
			candidates[entity] = struct{}{}
		}
	}
	for k, inst := range activeIdx {
		if k.RuleID != r.ID {
			continue
		}
		if !matchesScope(k.Entity) {
			e.resolveOutOfScope(inst, r, now, silences, activeIdx)
			continue
		}
		candidates[k.Entity] = struct{}{}
	}

	for entity := range candidates {
		e.evalThresholdEntity(r, entity, byEntity[entity], oldestByEntity[entity], activeIdx, silences, now)
	}
}

// resolveOutOfScope resolves any-state -> resolved(out-of-scope): a rule
// edit narrowed entity_glob/entity_class and this instance's entity no
// longer matches. Neither the ordinary crossing check nor no-data ever
// trigger for an entity still reporting its own value on its own terms,
// so without this an out-of-scope stray that's still breaching would
// fire forever -- nothing else ever revisits it. Pending resolves
// silently (it never fired, matching resolveSilent's doctrine); firing
// resolves with the usual event and dispatched notice, since unlike
// rule-disabled the RULE itself is still very much active for everyone
// else -- this one instance's own history should say why IT closed.
func (e *Engine) resolveOutOfScope(inst store.AlertInstance, r store.AlertRule, now int64, silences []store.Silence, activeIdx map[instanceKey]store.AlertInstance) {
	if inst.State == "pending" {
		e.resolveSilent(inst, now, "out-of-scope", activeIdx)
		return
	}
	e.resolveNotify(inst, r, now, "out-of-scope", silences, activeIdx)
}

func (e *Engine) evalThresholdEntity(r store.AlertRule, entity string, samples []store.Sample, oldest int64, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) {
	verdict, value := EvaluateThreshold(r, samples, oldest, now)
	currentlyCrossing := len(samples) > 0 && crosses(r.Op, samples[len(samples)-1].Val, r.Threshold)
	absent := samples == nil

	inst, exists := activeIdx[instanceKey{r.ID, entity}]
	switch {
	case !exists:
		switch {
		case verdict == VerdictBreaching:
			e.fire(r, entity, value, now, store.AlertInstance{}, true, activeIdx, silences)
		case currentlyCrossing:
			e.startPending(r, entity, value, now, activeIdx)
		}

	case inst.State == "pending":
		switch {
		case verdict == VerdictBreaching:
			e.fire(r, entity, value, now, inst, false, activeIdx, silences)
		case absent:
			// Checked before !currentlyCrossing (F10): that check alone
			// can't tell "the value dropped" from "the series vanished
			// entirely" -- len(samples) > 0 is false for both an empty
			// and a nil samples slice -- and a vanished series is not a
			// real recovery.
			e.resolveSilent(inst, now, "no-data", activeIdx)
		case !currentlyCrossing:
			e.resolveSilent(inst, now, "cleared", activeIdx)
		default:
			inst.Value = value
			e.upsert(&inst, activeIdx)
		}

	case inst.State == "firing":
		if absent {
			e.handleAbsentThreshold(r, inst, now, silences, activeIdx)
			return
		}
		delete(e.missingSince, inst.ID)
		if verdict == VerdictClearing {
			e.resolveNotify(inst, r, now, "cleared", silences, activeIdx)
			return
		}
		inst.Value = value
		if inst.NotifyCount == 0 {
			e.catchUpSilencedFire(&inst, r, now, silences)
		} else {
			e.maybeRenotify(&inst, r, now, silences)
		}
		e.upsert(&inst, activeIdx)
	}
}

// handleAbsentThreshold implements "firing, series absent from Match for
// clear_seconds -> resolved(no-data)": the timer starts on the FIRST
// tick the entity is missing (never resolving on that same tick) and
// only fires once clear_seconds have elapsed continuously -- a
// reappearance anywhere in between deletes the timer (see
// evalThresholdEntity's delete(e.missingSince, ...) on the non-absent
// path), so it never carries over a partial count from an earlier gap.
func (e *Engine) handleAbsentThreshold(r store.AlertRule, inst store.AlertInstance, now int64, silences []store.Silence, activeIdx map[instanceKey]store.AlertInstance) {
	first, tracked := e.missingSince[inst.ID]
	if !tracked {
		e.missingSince[inst.ID] = now
		return
	}
	if now-first >= r.ClearSeconds {
		e.resolveNotify(inst, r, now, "no-data", silences, activeIdx) // clears missingSince itself
	}
}

// maybeRenotify bumps last_notified_at/notify_count and dispatches a
// "renotify" notification when renotify_hours has elapsed since the
// last one -- but only when the pair isn't silenced. Silenced ticks
// leave the bookkeeping untouched: if they didn't, lifting the silence
// later would still have to wait out a full fresh interval before saying
// anything, for a notification that was never actually suppressed-and-
// queued, just skipped.
func (e *Engine) maybeRenotify(inst *store.AlertInstance, r store.AlertRule, now int64, silences []store.Silence) {
	if r.RenotifyHours <= 0 || now-inst.LastNotifiedAt < r.RenotifyHours*3600 {
		return
	}
	if silenced(silences, r.ID, inst.Entity) {
		return
	}
	inst.LastNotifiedAt = now
	inst.NotifyCount++
	if e.Dispatch != nil {
		e.Dispatch(AlertNotification{Phase: "renotify", Instance: *inst, Rule: r, Summary: inst.Summary})
	}
}

// catchUpSilencedFire dispatches the "fired" notification a silenced fire
// never sent: fire() leaves NotifyCount at 0 for an instance that started
// firing while silenced, and maybeRenotify only ever re-notifies an
// instance that already notified once -- at renotify_hours<=0 (6 of the
// 12 builtins) it never will. Without this, an alert born during a
// silence stays firing forever with nobody ever told. The first tick the
// silence no longer covers it, this treats it exactly like a fresh fire:
// stamp bookkeeping and dispatch phase "fired". A tick that's STILL
// silenced no-ops, same as fire() itself.
func (e *Engine) catchUpSilencedFire(inst *store.AlertInstance, r store.AlertRule, now int64, silences []store.Silence) {
	if silenced(silences, r.ID, inst.Entity) {
		return
	}
	inst.LastNotifiedAt = now
	inst.NotifyCount++
	if e.Dispatch != nil {
		e.Dispatch(AlertNotification{Phase: "fired", Instance: *inst, Rule: r, Summary: inst.Summary})
	}
}

// fire promotes (none)->firing or pending->firing: seed is the existing
// pending row when isNew is false (its StartedAt/ID carry forward
// unchanged; only a freshly-created instance sets StartedAt here). Always
// writes alert.fired; dispatches "fired" and bumps notify bookkeeping
// only when the pair isn't silenced.
func (e *Engine) fire(r store.AlertRule, entity string, value float64, now int64, seed store.AlertInstance, isNew bool, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence) {
	inst := seed
	if isNew {
		inst = store.AlertInstance{RuleID: r.ID, Kind: r.Kind, Entity: entity, Metric: r.Metric, StartedAt: now}
	}
	inst.State = "firing"
	inst.Severity = r.Severity
	inst.Value = value
	inst.Threshold = r.Threshold
	inst.FiredAt = now
	inst.Summary = summarizeThreshold(r, entity, value)

	if !silenced(silences, r.ID, entity) {
		inst.LastNotifiedAt = now
		inst.NotifyCount++
	}

	e.upsert(&inst, activeIdx)

	if _, err := e.Store.AppendEvent(store.Event{Kind: "alert.fired", Entity: entity, Severity: r.Severity, Detail: inst.Summary}); err != nil {
		log.Printf("alert engine: append alert.fired event: %v", err)
	}
	if inst.LastNotifiedAt == now && e.Dispatch != nil {
		e.Dispatch(AlertNotification{Phase: "fired", Instance: inst, Rule: r, Summary: inst.Summary})
	}
}

// startPending inserts a brand-new pending row: no event, no dispatch --
// see the lifecycle table in the package doc. StartedAt marks the first
// tick a breach was observed; the sustained-for arithmetic itself lives
// entirely in EvaluateThreshold reading the ring, not in anything this
// timestamp drives.
func (e *Engine) startPending(r store.AlertRule, entity string, value float64, now int64, activeIdx map[instanceKey]store.AlertInstance) {
	inst := store.AlertInstance{
		RuleID: r.ID, Kind: r.Kind, Entity: entity, Metric: r.Metric,
		State: "pending", Severity: r.Severity, Value: value, Threshold: r.Threshold,
		Summary: summarizeThreshold(r, entity, value), StartedAt: now,
	}
	e.upsert(&inst, activeIdx)
}

// resolveSilent resolves pending->resolved with no event and no dispatch:
// a pending alert never fired, so there is nothing to announce recovery
// from. Also clears missingSince (F9): harmless here today (nothing ever
// sets it for a pending instance), but every resolve path sweeps it so
// none of them has to reason about whether IT specifically needs to.
func (e *Engine) resolveSilent(inst store.AlertInstance, now int64, reason string, activeIdx map[instanceKey]store.AlertInstance) {
	delete(e.missingSince, inst.ID)
	if err := e.Store.ResolveAlertInstance(inst.ID, now, reason); err != nil {
		log.Printf("alert engine: resolve pending instance %d (%s/%s): %v", inst.ID, inst.RuleID, inst.Entity, err)
	}
	delete(activeIdx, keyOf(inst))
}

// resolveNotify resolves firing->resolved with an alert.resolved event
// and, unless silenced, a dispatched resolved notification. A
// ResolveAlertInstance error (the carry-forward fix: it now errors on an
// unknown id, e.g. a row Maintain already pruned out from under a stale
// engine handle) is logged, not returned -- one instance's stale id must
// never abort the rest of this tick's evaluation. Also clears
// missingSince (F9): the "cleared" and "timeout" reasons never had one
// to begin with, but "no-data" (handleAbsentThreshold) and "out-of-scope"
// (resolveOutOfScope, F6) both resolve a possibly-still-tracked instance
// through here, and this is the one place that's true regardless of
// which of those called it.
func (e *Engine) resolveNotify(inst store.AlertInstance, r store.AlertRule, now int64, reason string, silences []store.Silence, activeIdx map[instanceKey]store.AlertInstance) {
	delete(e.missingSince, inst.ID)
	if err := e.Store.ResolveAlertInstance(inst.ID, now, reason); err != nil {
		log.Printf("alert engine: resolve instance %d (%s/%s): %v", inst.ID, inst.RuleID, inst.Entity, err)
	}
	delete(activeIdx, keyOf(inst))
	inst.State, inst.ResolvedAt, inst.ResolveReason = "resolved", now, reason

	summary := describeResolve(inst, reason)
	if _, err := e.Store.AppendEvent(store.Event{Kind: "alert.resolved", Entity: inst.Entity, Severity: "info", Detail: summary}); err != nil {
		log.Printf("alert engine: append alert.resolved event: %v", err)
	}
	if !silenced(silences, r.ID, inst.Entity) && e.Dispatch != nil {
		e.Dispatch(AlertNotification{Phase: "resolved", Instance: inst, Rule: r, Summary: summary})
	}
}

// upsert inserts (ID==0) or updates an instance and keeps activeIdx in
// sync. A store error is logged and swallowed, the same log-and-continue
// posture as every other write in this file -- a single instance write
// failing (e.g. a race against idx_alert_active) must not abort the rest
// of the tick.
func (e *Engine) upsert(inst *store.AlertInstance, activeIdx map[instanceKey]store.AlertInstance) {
	id, err := e.Store.UpsertAlertInstance(*inst)
	if err != nil {
		log.Printf("alert engine: upsert instance (%s/%s): %v", inst.RuleID, inst.Entity, err)
		return
	}
	inst.ID = id
	activeIdx[keyOf(*inst)] = *inst
}

// silenced reports whether any silence in the slice covers (ruleID,
// entity): "" on either field means "any". Silences never change a state
// transition, only whether Dispatch is called for it -- see
// resolveNotify/fire/maybeRenotify, the only three call sites.
func silenced(silences []store.Silence, ruleID, entity string) bool {
	for _, s := range silences {
		if (s.RuleID == "" || s.RuleID == ruleID) && (s.Entity == "" || s.Entity == entity) {
			return true
		}
	}
	return false
}

// --- event rules -------------------------------------------------------

// sustainedEventRules maps an event rule id to a predicate asking "is the
// live condition that fired this instance still true right now, per
// Fleet()". Most event rules are true point-in-time occurrences -- a
// container died, OOM'd, a parity check finished with errors -- where
// clear_seconds counted from the one event that started them is already
// the complete recovery signal (see the rule split documented at
// DefaultAlertRules). container-unhealthy is not: Health stays
// "unhealthy" for as long as the underlying condition holds, and while
// the collector's own container.health "healthy" event is the fast path
// out (see matchesClear), a missed clear -- container removed mid-
// unhealthy, a restart losing the transition -- would otherwise leave
// clear_seconds counting from the ORIGINAL unhealthy event, silently
// resolving a container Fleet() still shows as broken. tickEvents'
// sweep refreshes FiredAt every tick a listed predicate still matches,
// re-anchoring the fallback timeout to "since last confirmed live"
// instead; StartedAt is untouched, so the instance's true age is still
// recoverable. The other four builtin event rules (oom/exit/disk-
// errors/parity-errors) deliberately have no entry: they're point
// events with nothing analogous to refresh against.
var sustainedEventRules = map[string]func(FleetMember) bool{
	"container-unhealthy": func(m FleetMember) bool { return m.State == "running" && m.Health == "unhealthy" },
}

// tickEvents reads events since the cursor, matches each against every
// enabled event rule, then separately sweeps active event-rule instances:
// refreshing any sustained condition's clear anchor, catching up a
// silenced fire or renotifying exactly like the threshold sweep at
// evalThresholdEntity:406-410, and finally the clear-event-independent
// timeout -- an instance with no matching clear_event_kinds configured
// (or one that just never saw a matching event) still has to auto-resolve
// eventually.
func (e *Engine) tickEvents(ctx context.Context, ruleByID map[string]store.AlertRule, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) {
	events, err := e.Store.QueryEventsSince(ctx, e.eventCursor, 0)
	if err != nil {
		log.Printf("alert engine: query events since %d: %v", e.eventCursor, err)
		events = nil
	}

	eventRules := enabledEventRules(ruleByID)
	for _, ev := range events {
		e.processEvent(ev, eventRules, activeIdx, silences, now)
		if ev.ID > e.eventCursor {
			e.eventCursor = ev.ID
		}
	}

	var fleetByName map[string]FleetMember
	if e.Fleet != nil {
		members := e.Fleet()
		fleetByName = make(map[string]FleetMember, len(members))
		for _, m := range members {
			fleetByName[m.Name] = m
		}
	}

	for k, inst := range activeIdx {
		r, ok := ruleByID[k.RuleID]
		if !ok || r.Type != "event" {
			continue
		}
		if sustained, tracked := sustainedEventRules[r.ID]; tracked {
			if m, live := fleetByName[inst.Entity]; live && sustained(m) {
				inst.FiredAt = now
			}
		}
		if inst.NotifyCount == 0 {
			e.catchUpSilencedFire(&inst, r, now, silences)
		} else {
			e.maybeRenotify(&inst, r, now, silences)
		}
		e.upsert(&inst, activeIdx)

		if r.ClearSeconds <= 0 {
			continue
		}
		if now-inst.FiredAt >= r.ClearSeconds {
			e.resolveNotify(inst, r, now, "timeout", silences, activeIdx)
		}
	}
}

// processEvent runs one event (real, off the cursor, or a boot-seeded
// synthetic one) through every enabled event rule.
func (e *Engine) processEvent(ev store.Event, eventRules []store.AlertRule, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) {
	for _, r := range eventRules {
		e.processEventForRule(r, ev, activeIdx, silences, now)
	}
}

// processEventForRule is wrapped in its own recover for the same reason
// evalThresholdRule is: one rule's bad data (or a ClassOf panic) must not
// take the rest of this event, or any other rule, down with it.
//
// Deliberately does not gate on r.Kind (F11): unlike a threshold rule,
// where Kind picks which metric ring gets read at all, an event rule's
// eligibility is fully decided by matchesFire (EventKinds + MinSeverity)
// below, then EntityGlob/EntityClass. A dot-namespaced event kind like
// "container.health" or "disk.errors" already names its own entity
// domain unambiguously, so gating on Kind too would be redundant for
// every well-formed rule -- and actively wrong for parity-errors
// specifically (Kind "unraid", EventKinds "parity.finish": "parity" is
// Unraid's own event vocabulary, not literally the string "unraid").
// Kind still matters here for two things: it rides onto the created
// instance, and it's the kind ClassOf(r.Kind, ev.Entity) is called with
// for entity_class matching just below.
func (e *Engine) processEventForRule(r store.AlertRule, ev store.Event, activeIdx map[instanceKey]store.AlertInstance, silences []store.Silence, now int64) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("alert engine: event rule %q panicked: %v", r.ID, p)
		}
	}()

	key := instanceKey{r.ID, ev.Entity}
	if inst, exists := activeIdx[key]; exists {
		// A fresh fire-matching event on an already-active pair is
		// folded silently into the existing row (idx_alert_active
		// permits nothing else); only a clear-matching event changes
		// anything here.
		if matchesClear(r, ev) {
			e.resolveNotify(inst, r, now, "cleared", silences, activeIdx)
		}
		return
	}

	if !matchesFire(r, ev) {
		return
	}
	class := ""
	if e.ClassOf != nil {
		class = e.ClassOf(r.Kind, ev.Entity)
	}
	if !MatchEntity(r.EntityGlob, ev.Entity) || !MatchClass(r.EntityClass, class) {
		return
	}

	inst := store.AlertInstance{
		RuleID: r.ID, Kind: r.Kind, Entity: ev.Entity, State: "firing", Severity: r.Severity,
		Summary: summarizeEvent(ev), StartedAt: now, FiredAt: now,
	}
	if !silenced(silences, r.ID, ev.Entity) {
		inst.LastNotifiedAt = now
		inst.NotifyCount = 1
	}
	e.upsert(&inst, activeIdx)

	if _, err := e.Store.AppendEvent(store.Event{Kind: "alert.fired", Entity: ev.Entity, Severity: r.Severity, Detail: inst.Summary}); err != nil {
		log.Printf("alert engine: append alert.fired event: %v", err)
	}
	if inst.LastNotifiedAt == now && e.Dispatch != nil {
		e.Dispatch(AlertNotification{Phase: "fired", Instance: inst, Rule: r, Summary: inst.Summary})
	}
}

func matchesFire(r store.AlertRule, ev store.Event) bool {
	return containsKind(r.EventKinds, ev.Kind) && severityAtLeast(ev.Severity, r.MinSeverity)
}

// matchesClear: "" clear_event_kinds means timeout-only auto-resolve, so
// no event can ever clear it here regardless of kind/severity.
func matchesClear(r store.AlertRule, ev store.Event) bool {
	if r.ClearEventKinds == "" {
		return false
	}
	return containsKind(r.ClearEventKinds, ev.Kind) && severityAtMost(ev.Severity, r.ClearMaxSeverity)
}

func containsKind(csv, kind string) bool {
	for _, part := range strings.Split(csv, ",") {
		if strings.TrimSpace(part) == kind {
			return true
		}
	}
	return false
}

// severityRank orders store.Event's three-tier vocabulary for the
// min_severity/clear_max_severity floor/ceiling checks. An unrecognized
// string ranks below "info" (-1), so a typo'd rule field fails safe
// (never matches a floor) rather than silently matching everything.
var severityRank = map[string]int{"info": 0, "warning": 1, "alert": 2}

func rankOf(sev string) int {
	if r, ok := severityRank[sev]; ok {
		return r
	}
	return -1
}

func severityAtLeast(sev, floor string) bool {
	if floor == "" {
		return true
	}
	return rankOf(sev) >= rankOf(floor)
}

func severityAtMost(sev, ceiling string) bool {
	if ceiling == "" {
		return true
	}
	return rankOf(sev) <= rankOf(ceiling)
}

// --- human-readable summaries --------------------------------------------

// summarizeThreshold produces the instance's stored Summary and doubles
// as a Notification's Summary once dispatched -- one sentence, computed
// once, so the alert_instances row, the Events feed detail, and a future
// delivery channel's message body can never drift from each other.
func summarizeThreshold(r store.AlertRule, entity string, value float64) string {
	verb := "over"
	if r.Op == "<" {
		verb = "under"
	}
	name := entity
	if name == "" {
		name = "host"
	}
	return fmt.Sprintf("%s is at %s (%s %s for %s)", name, formatNum(value), verb, formatNum(r.Threshold), formatDuration(r.ForSeconds))
}

func summarizeEvent(ev store.Event) string {
	if ev.Detail != "" {
		return fmt.Sprintf("%s: %s (%s)", ev.Entity, ev.Kind, ev.Detail)
	}
	return fmt.Sprintf("%s: %s", ev.Entity, ev.Kind)
}

func describeResolve(inst store.AlertInstance, reason string) string {
	name := inst.Entity
	if name == "" {
		name = "host"
	}
	switch reason {
	case "cleared":
		return name + " recovered"
	case "no-data":
		return name + " stopped reporting"
	case "timeout":
		return name + " auto-resolved (no clear signal within the window)"
	default:
		return name + " resolved (" + reason + ")"
	}
}

func formatNum(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func formatDuration(seconds int64) string { return (time.Duration(seconds) * time.Second).String() }

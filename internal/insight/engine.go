package insight

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// Store is the narrow slice of *store.Store the engine needs -- the same
// package-local, minimal-interface convention alert.Engine's own Store
// uses, so this package never imports anything beyond store's plain data
// types.
type Store interface {
	ActiveInsights(context.Context) ([]store.InsightInstance, error)
	UpsertInsight(store.InsightInstance) (int64, error)
	ResolveInsight(id, at int64, reason string) error
	InsightRuleConfigs(context.Context) ([]store.InsightRuleConfig, error)
	InsightDismissals(context.Context, int64) ([]store.InsightDismissal, error)
	AppendEvent(store.Event) (int64, error)
	QueryEvents(context.Context, store.EventFilter) ([]store.Event, error)
}

// Notification is what the engine hands to Dispatch when a Confirmed,
// alert-severity finding clears every Notifiable gate. Deliberately its
// own small type, not alert.AlertNotification: an insight has no
// store.AlertRule/AlertInstance to fill that shape's fields with, and
// forcing one would mean fabricating data the delivery ledger would then
// render as if it were a real alert rule.
type Notification struct {
	Finding  Finding
	Instance store.InsightInstance
}

// Anti-noise defaults (Task 7's four layers): Sustain lives in each
// rule's own Eval (SustainForSecs, rules.go) -- see Engine's own package
// doc below for why that collapses this state machine's "pending" phase.
// ClearFor/Cooldown/MaxActive are engine-level because they genuinely
// need multi-tick memory Eval's single-call, ring-history-only contract
// can't carry on its own.
const (
	DefaultClearForSecs = 180
	DefaultCooldownSecs = 30 * 60
	DefaultMaxActive    = 10
)

// tupleKey is the engine's own dedup identity: (rule, victim, resource),
// deliberately WITHOUT culprit. idx_insight_active's own uniqueness key
// DOES include culprit, which means the schema alone permits a
// single-culprit row (culprit="qbittorrent") and a shared-culprit row
// (culprit="", culprits="qbittorrent,sabnzbd") to be simultaneously
// active for what is really the SAME contended resource -- Dominant's
// leading-set/single-culprit choice can genuinely flip tick to tick as
// shares shift near the floor (the store review's own seam invariant 7).
// upsertFinding is what actually enforces "never both": it looks up any
// active row(s) for this tupleKey regardless of their own culprit column
// and resolves every one that doesn't match the NEW finding's shape as
// "superseded" before writing the new one.
type tupleKey struct{ RuleID, Victim, Resource string }

// Engine evaluates every enabled insight rule on its own cadence,
// gathering one In per tick (Rules.go's own "no rule does I/O" contract)
// and driving each (rule, victim, resource) tuple through the lifecycle
// documented on Tick.
//
// A nil Dispatch means "evaluate and record, but never deliver" --
// exactly alert.Engine's own convention for the same field.
type Engine struct {
	Store            Store
	MatchSince       func(kind, metric string, since int64) (samples map[string][]store.Sample, oldestTS map[string]int64)
	MatchPrefixSince func(kind, prefix string, since int64) (samples map[string]map[string][]store.Sample, oldestTS map[string]map[string]int64)
	// DeviceName/Slots build this tick's Topology snapshot -- the exact
	// injected-closure shape NewTopology itself documents: DeviceName is
	// host.Collector.DeviceName; Slots is assembled by the caller from
	// the unraid collector's DiskMeta() plus each slot's own rotational
	// live sample (SlotMeta's own doc).
	DeviceName func(majMin string) (string, bool)
	Slots      func() map[string]SlotMeta
	// PressureTier is pressure.Collector.Tier() -- "psi" or "proxy". Nil
	// degrades to "proxy" (stock Unraid's default), never an error.
	PressureTier func() string
	Dispatch     func(Notification)
	Clock        func() time.Time

	// ClearForSecs/CooldownSecs/MaxActive default to the constants above
	// in New; exported so fake-mode (and tests) can compress them the
	// way store.DefaultAlertRules(fast) compresses alert rules' own
	// for_seconds/clear_seconds -- there is no per-rule DB column for
	// these the way alert_rules has, so compression is a constructor
	// concern here rather than a seeded-table one.
	ClearForSecs int64
	CooldownSecs int64
	MaxActive    int

	// lastSeen tracks, per tuple, the most recent tick's unix-seconds
	// `now` at which some rule returned a Finding for it -- the clear-for
	// timer's own anchor. There is no schema column for this (mirroring
	// alert.Engine's own missingSince doc): it only needs to survive one
	// process's lifetime, and a restart's cold map simply grants every
	// still-active row a fresh clear-for grace period rather than
	// resolving it on the very first post-restart tick (see Tick's own
	// handling of an untracked active tuple).
	lastSeen map[tupleKey]int64
	// cooldownUntil blocks a resolved tuple from re-firing until this
	// unix-seconds timestamp -- the flap guard (anti-noise layer 3). A
	// "superseded" resolve (the SAME contention changing shape, not
	// actually clearing) never sets this; see resolve's own doc.
	cooldownUntil map[tupleKey]int64
	// dropped counts findings the global cap (MaxActive) discarded --
	// Task 7's own "record the drop in a debug counter surfaced in
	// Settings" requirement. Dropped reports it.
	dropped int
}

// New wires an Engine with the anti-noise defaults; Clock defaults to
// time.Now when nil, store.Open's own convention.
func New(st Store) *Engine {
	return &Engine{
		Store: st, Clock: time.Now,
		ClearForSecs: DefaultClearForSecs, CooldownSecs: DefaultCooldownSecs, MaxActive: DefaultMaxActive,
		lastSeen: map[tupleKey]int64{}, cooldownUntil: map[tupleKey]int64{},
	}
}

// Dropped reports how many findings the global cap has discarded across
// this Engine's lifetime -- Settings' own debug counter (Task 7).
func (e *Engine) Dropped() int { return e.dropped }

// Run ticks the engine every `every` until ctx is cancelled -- alert.
// Engine.Run's own shape.
func (e *Engine) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Tick(ctx); err != nil {
				log.Printf("insight engine: tick: %v", err)
			}
		}
	}
}

// Tick runs one full evaluation pass.
//
// Lifecycle: this package's rules already gate their OWN victim evidence
// on Sustained (rules.go) -- unlike alert.EvaluateThreshold, which the
// engine calls fresh each tick against a raw crossing check, Sustained is
// evaluated ENTIRELY inside one Eval call against the ring's own
// retrospective history. That collapses the "pending: both sides present
// but sustain not yet met" phase the plan's own lifecycle table
// describes into a single-tick formality from the engine's point of
// view: Eval simply returns nothing at all until the underlying window
// is already fully sustained, then starts returning a Finding the very
// tick it first is. So this state machine has two states where
// alert.Engine's has three: absent -> active (fired; insight.detected
// appended, both StartedAt and FiredAt stamped to the tick the engine
// first notices, since there is no earlier "pending" moment this engine
// itself ever observed) -> resolved (insight.resolved appended, except
// for "superseded" -- see resolve's own doc). This is a deliberate,
// documented deviation from the plan's literal three-state description,
// not an oversight: there is no multi-tick accumulation for a "pending"
// state to model here, because Sustained already did that accumulation
// inside the single Eval call that produced the Finding.
//
// Clearing, by contrast, genuinely needs cross-tick memory: a NEW Eval
// call for an already-resolved tuple can't retroactively say "yes, this
// cleared 3 minutes ago" the way it can positively confirm a sustained
// breach from ring history alone. So clear-for is tracked here, at
// tick granularity (lastSeen), not by re-deriving each rule's own
// clear-side Sustained check a second time in the engine -- an active
// tuple resolves once ClearForSecs have passed since the last tick ANY
// rule still returned a Finding for it. This implementation does not
// separately distinguish "no-data" (the series stopped) from "cleared"
// (the value dropped below threshold) as the plan's own resolve-reason
// list names them: both look identical from Eval's own return value (no
// Finding), and telling them apart would need per-rule series-presence
// introspection this engine deliberately does not carry. Every
// resolution this path reaches uses reason "cleared".
func (e *Engine) Tick(ctx context.Context) error {
	now := e.Clock().Unix()

	cfgs, err := e.Store.InsightRuleConfigs(ctx)
	if err != nil {
		return fmt.Errorf("insight engine: rule configs: %w", err)
	}
	cfgByID := make(map[string]store.InsightRuleConfig, len(cfgs))
	overrides := make(map[string]map[string]float64, len(cfgs))
	for _, c := range cfgs {
		cfgByID[c.RuleID] = c
		if c.Overrides == "" {
			continue
		}
		var ov map[string]float64
		if err := json.Unmarshal([]byte(c.Overrides), &ov); err != nil {
			log.Printf("insight engine: rule %q: parse overrides: %v", c.RuleID, err)
			continue
		}
		overrides[c.RuleID] = ov
	}

	active, err := e.Store.ActiveInsights(ctx)
	if err != nil {
		return fmt.Errorf("insight engine: active insights: %w", err)
	}
	activeByTuple := make(map[tupleKey][]store.InsightInstance, len(active))
	for _, inst := range active {
		k := tupleKey{inst.RuleID, inst.Victim, inst.Resource}
		activeByTuple[k] = append(activeByTuple[k], inst)
	}

	dismissals, err := e.Store.InsightDismissals(ctx, now)
	if err != nil {
		return fmt.Errorf("insight engine: dismissals: %w", err)
	}

	in := e.gather(ctx, now)

	seen := make(map[tupleKey]bool, len(active))
	for _, rule := range Rules(overrides) {
		cfg, hasCfg := cfgByID[rule.ID]
		if hasCfg && !cfg.Enabled {
			continue
		}
		for _, f := range safeEval(rule, in) {
			k := tupleKey{f.RuleID, f.Victim, f.Resource}
			seen[k] = true
			e.lastSeen[k] = now
			if e.cooldownUntil[k] > now {
				continue // anti-noise layer 3: flap guard after a real resolve
			}
			culprit, culprits := culpritColumns(f.Culprit)
			if dismissedTuple(dismissals, f.RuleID, f.Victim, culprit, f.Resource) {
				continue // anti-noise layer 4
			}
			e.upsertFinding(f, culprit, culprits, activeByTuple[k], now, cfg)
		}
	}

	for k, insts := range activeByTuple {
		if cfg, ok := cfgByID[k.RuleID]; ok && !cfg.Enabled {
			for _, inst := range insts {
				e.resolve(inst, now, "rule-disabled")
			}
			delete(e.lastSeen, k)
			continue
		}
		if seen[k] {
			continue
		}
		last, tracked := e.lastSeen[k]
		if !tracked {
			e.lastSeen[k] = now // restart: grant a fresh clear-for window rather than resolving immediately
			continue
		}
		if now-last >= e.ClearForSecs {
			for _, inst := range insts {
				e.resolve(inst, now, "cleared")
			}
			delete(e.lastSeen, k)
		}
	}

	e.enforceCap(ctx, now)
	return nil
}

// safeEval isolates one rule's panic from the rest of the tick --
// alert.evalThresholdRule's own defensive posture, for the same reason:
// bad data flowing into one rule's arithmetic must never take every
// other rule down with it.
func safeEval(rule Rule, in In) (findings []Finding) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("insight engine: rule %q panicked: %v", rule.ID, p)
		}
	}()
	return rule.Eval(in)
}

// culpritColumns renders a Culprits value into insight_instances' own
// two-column shape (004_insights.sql): a single name in `culprit` and an
// empty `culprits`, or an empty `culprit` and a comma-joined `culprits`
// for a Shared set -- never both populated, matching the schema's own
// documented convention.
func culpritColumns(c Culprits) (culprit, culprits string) {
	if !c.Shared {
		if len(c.Names) > 0 {
			return c.Names[0], ""
		}
		return "", ""
	}
	return "", strings.Join(c.Names, ",")
}

// dismissedTuple reports whether any dismissal in ds suppresses this
// candidate -- see dismissalMatches' own doc for the per-axis "empty
// means any" contract and the all-empty fail-safe (seam invariant 6).
func dismissedTuple(ds []store.InsightDismissal, ruleID, victim, culprit, resource string) bool {
	for _, d := range ds {
		if dismissalMatches(d, ruleID, victim, culprit, resource) {
			return true
		}
	}
	return false
}

// dismissalMatches is seam invariant 6's own implementation. Each of
// RuleID/Victim/Culprit/Resource is matched independently: empty on the
// dismissal means "any" for that axis alone. The one deliberate
// exception: a dismissal with ALL FOUR axes empty would otherwise
// silence every insight from every rule forever, which no dismiss
// gesture should be able to produce by accident. 004_insights.sql's
// insight_dismissals table carries no "scope:all" marker column to tell
// an intentional mute-everything apart from an accidental empty row, so
// until a future migration adds one (and an API adds the explicit
// opt-in gesture that sets it), an all-empty row matches NOTHING here --
// fail safe, not fail broad. culprit is matched against the CANDIDATE's
// own single-name culprit column (culpritColumns' first return, "" for a
// Shared set) -- the same granularity insight_instances itself stores,
// not against individual members of a shared set.
func dismissalMatches(d store.InsightDismissal, ruleID, victim, culprit, resource string) bool {
	if d.RuleID == "" && d.Victim == "" && d.Culprit == "" && d.Resource == "" {
		return false
	}
	return (d.RuleID == "" || d.RuleID == ruleID) &&
		(d.Victim == "" || d.Victim == victim) &&
		(d.Culprit == "" || d.Culprit == culprit) &&
		(d.Resource == "" || d.Resource == resource)
}

// upsertFinding writes one tick's Finding for a tuple, enforcing seam
// invariant 7 first: existing is every currently-active row for this
// EXACT tupleKey (rule, victim, resource) regardless of ITS OWN culprit
// column, since the DB's unique index keys on culprit too and would
// otherwise happily let a single-culprit and a shared-culprit row for
// the same real contention coexist. Any existing row whose culprit
// columns don't match the new finding's shape is resolved "superseded"
// before the new (or continued) row is written.
func (e *Engine) upsertFinding(f Finding, culprit, culprits string, existing []store.InsightInstance, now int64, cfg store.InsightRuleConfig) {
	var keep *store.InsightInstance
	for i := range existing {
		if existing[i].Culprit == culprit && existing[i].Culprits == culprits {
			inst := existing[i]
			keep = &inst
			continue
		}
		e.resolve(existing[i], now, "superseded")
	}

	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		log.Printf("insight engine: marshal evidence (%s/%s/%s): %v", f.RuleID, f.Victim, f.Resource, err)
		evidence = []byte("{}")
	}

	inst := store.InsightInstance{
		RuleID: f.RuleID, VictimKind: f.VictimKind, Victim: f.Victim,
		Culprit: culprit, Culprits: culprits, Resource: f.Resource,
		State: "active", Severity: f.Severity, Confidence: f.Confidence.String(), Tier: f.Tier.String(),
		Statement: Statement(f), Evidence: string(evidence), StartedAt: now, FiredAt: now,
	}
	// isNew is false whenever this tuple already had an active row of
	// ANY shape (keep, or one just resolved "superseded" above) -- I3
	// (review): a culprit-shape flip (single <-> shared crossing the
	// floor tick to tick) is the SAME contention, not a fresh
	// detection, so it must neither append a second insight.detected
	// nor reset the tuple's own age.
	isNew := keep == nil && len(existing) == 0
	switch {
	case keep != nil:
		inst.ID, inst.StartedAt, inst.FiredAt, inst.NotifiedAt = keep.ID, keep.StartedAt, keep.FiredAt, keep.NotifiedAt
	case len(existing) > 0:
		// Shape flip: every row in existing was just superseded above
		// (none matched the new culprit/culprits shape). Carry the
		// EARLIEST StartedAt/FiredAt forward rather than restarting at
		// now -- idx_insight_active's own doc allows more than one
		// superseded row to coexist only transiently, so this also
		// covers that rare case honestly rather than just existing[0].
		inst.StartedAt, inst.FiredAt = existing[0].StartedAt, existing[0].FiredAt
		for _, ex := range existing[1:] {
			if ex.StartedAt < inst.StartedAt {
				inst.StartedAt = ex.StartedAt
			}
			if ex.FiredAt < inst.FiredAt {
				inst.FiredAt = ex.FiredAt
			}
		}
	}

	id, err := e.Store.UpsertInsight(inst)
	if err != nil {
		log.Printf("insight engine: upsert instance (%s/%s/%s): %v", f.RuleID, f.Victim, f.Resource, err)
		return
	}
	inst.ID = id

	if isNew {
		if _, err := e.Store.AppendEvent(store.Event{Kind: "insight.detected", Entity: f.Victim, Severity: "info", Detail: inst.Statement}); err != nil {
			log.Printf("insight engine: append insight.detected event: %v", err)
		}
	}
	if inst.NotifiedAt == 0 && Notifiable(f, cfg) {
		inst.NotifiedAt = now
		if _, err := e.Store.UpsertInsight(inst); err != nil {
			log.Printf("insight engine: stamp notified_at (%d): %v", inst.ID, err)
		}
		if e.Dispatch != nil {
			e.Dispatch(Notification{Finding: f, Instance: inst})
		}
	}
}

// resolve closes out inst with reason, appending insight.resolved and
// arming the per-tuple cooldown -- UNLESS reason is "superseded": that
// transition is the SAME real-world contention changing shape (a
// single culprit crossing into a shared pair or back), not an actual
// resolution the user ever saw clear, so announcing it in the events
// feed or blocking the tuple's immediate continuation with a 30-minute
// cooldown would both be wrong. The replacement row upsertFinding writes
// in the same tick carries its own insight.detected instead.
func (e *Engine) resolve(inst store.InsightInstance, now int64, reason string) {
	if err := e.Store.ResolveInsight(inst.ID, now, reason); err != nil {
		log.Printf("insight engine: resolve instance %d (%s/%s/%s): %v", inst.ID, inst.RuleID, inst.Victim, inst.Resource, err)
		return
	}
	if reason == "superseded" {
		return
	}
	detail := describeResolve(inst, reason)
	if _, err := e.Store.AppendEvent(store.Event{Kind: "insight.resolved", Entity: inst.Victim, Severity: "info", Detail: detail}); err != nil {
		log.Printf("insight engine: append insight.resolved event: %v", err)
	}
	if reason == "capped" || reason == "rule-disabled" {
		// I2 (review): neither reason is evidence that the contention
		// itself resolved -- "capped" is a display decision (Task 7's
		// own cap, nothing about the tuple's own signal changed) and
		// "rule-disabled" can be undone by the same human who did it.
		// Arming the flap-guard here locked a tuple out for 30 minutes
		// even after room freed or the rule was re-enabled, which is
		// the cooldown protecting against the wrong kind of flap.
		return
	}
	e.cooldownUntil[tupleKey{inst.RuleID, inst.Victim, inst.Resource}] = now + e.CooldownSecs
}

func describeResolve(inst store.InsightInstance, reason string) string {
	name := inst.Victim
	if name == "" {
		name = inst.Resource
	}
	switch reason {
	case "cleared":
		return name + " is no longer contended"
	case "restart":
		return name + " insight cleared at restart"
	case "rule-disabled":
		return inst.RuleID + " rule disabled"
	case "dismissed":
		return name + " insight dismissed"
	case "capped":
		return name + " insight dropped: too many active insights"
	default:
		return name + " resolved (" + reason + ")"
	}
}

// enforceCap keeps at most MaxActive active insights (Task 7's global
// cap): when there are more, the higher (severity, confidence,
// started_at-ascending) set is kept and every excess row is resolved
// "capped" -- "a screen with 30 findings is a screen nobody reads."
// Re-reads ActiveInsights rather than reusing this tick's own
// activeByTuple so a row upsertFinding just created or superseded this
// same tick is included in the count.
func (e *Engine) enforceCap(ctx context.Context, now int64) {
	active, err := e.Store.ActiveInsights(ctx)
	if err != nil {
		log.Printf("insight engine: enforce cap: active insights: %v", err)
		return
	}
	if len(active) <= e.MaxActive {
		return
	}
	sort.SliceStable(active, func(i, j int) bool {
		if si, sj := severityRank(active[i].Severity), severityRank(active[j].Severity); si != sj {
			return si > sj
		}
		if ci, cj := confidenceRank(active[i].Confidence), confidenceRank(active[j].Confidence); ci != cj {
			return ci > cj
		}
		return active[i].StartedAt < active[j].StartedAt
	})
	for _, inst := range active[e.MaxActive:] {
		e.resolve(inst, now, "capped")
		e.dropped++
	}
}

func severityRank(s string) int {
	switch s {
	case "alert":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

func confidenceRank(c string) int {
	if c == "confirmed" {
		return 1
	}
	return 0
}

// Notifiable reports whether f may be dispatched through the Phase 4
// alert channels (Global Constraints; Task 8). THREE gates, all
// required: the rule's own config has notify on (default off), the
// finding is Confirmed (a Likely correlational claim must never wake a
// phone), and severity is alert. There is no override, and this is the
// engine's ONLY call site that can lead to e.Dispatch -- see
// upsertFinding.
func Notifiable(f Finding, cfg store.InsightRuleConfig) bool {
	return cfg.Notify && f.Confidence == ConfidenceConfirmed && f.Severity == "alert"
}

// gather performs every Live read this tick needs exactly once,
// regardless of how many rules are enabled -- one MatchPrefixSince call
// per distinct (kind, prefix) family, one MatchSince call per distinct
// (kind, metric) pair, one Topology build, one events read. Lookback
// windows vary by what the family needs: EvidenceWindowSecs (120s) for
// everything Sustained checks directly, BaselineLookbackSecs (600s) for
// the two series a rolling Baseline needs real history for
// (await_ms, parity speed), SpinupLookbackSecs (3600s) for spun_up.
func (e *Engine) gather(ctx context.Context, now int64) In {
	in := In{Now: now, Tier: "proxy"}
	if e.PressureTier != nil {
		in.Tier = e.PressureTier()
	}

	var slots map[string]SlotMeta
	if e.Slots != nil {
		slots = e.Slots()
	}
	in.Topology = NewTopology(e.DeviceName, slots)

	prefix := func(kind, p string, since int64) PrefixResult {
		if e.MatchPrefixSince == nil {
			return PrefixResult{}
		}
		s, o := e.MatchPrefixSince(kind, p, since)
		return PrefixResult{Samples: s, Oldest: o}
	}
	in.HostDiskIO = prefix("host", "diskio.", now-BaselineLookbackSecs)
	in.ContainerLiveIO = prefix("container", "live:io.", now-EvidenceWindowSecs)
	in.GPUEngine = prefix("gpu", "engine.", now-EvidenceWindowSecs)
	in.ContainerGPU = prefix("container", "gpu.", now-EvidenceWindowSecs)

	match := func(kind, metric string, since int64) MatchResult {
		if e.MatchSince == nil {
			return MatchResult{}
		}
		s, o := e.MatchSince(kind, metric, since)
		return MatchResult{Samples: s, Oldest: o}
	}
	in.HostCPUIowait = match("host", "cpu.iowait_pct", now-EvidenceWindowSecs)
	in.HostCPUTotal = match("host", "cpu.total", now-EvidenceWindowSecs)
	in.HostMemUsedPct = match("host", "mem.used_pct", now-EvidenceWindowSecs)
	in.ContainerCPUThrottled = match("container", "cpu.throttled_pct", now-EvidenceWindowSecs)
	in.ContainerCPUAllocCores = match("container", "cpu.alloc_cores", now-EvidenceWindowSecs)
	in.ContainerCPUPct = match("container", "cpu.pct", now-EvidenceWindowSecs)
	in.ContainerMemPct = match("container", "mem.pct", now-EvidenceWindowSecs)
	in.ParitySpeedBps = match("unraid", "parity.speed_bps", now-BaselineLookbackSecs)
	in.ParityProgressPct = match("unraid", "parity.progress_pct", now-EvidenceWindowSecs)
	in.DiskSpunUp = match("disk", "spun_up", now-SpinupLookbackSecs)
	in.DiskRotational = match("disk", "rotational", now-EvidenceWindowSecs)

	in.HostPSI = make(map[string]MatchResult, 6)
	in.ContainerPSI = make(map[string]MatchResult, 6)
	for _, res := range [...]string{"cpu", "io", "mem"} {
		for _, kind := range [...]string{"some", "full"} {
			metric := "psi." + res + "." + kind + "_pct"
			in.HostPSI[metric] = match("host", metric, now-EvidenceWindowSecs)
			in.ContainerPSI[metric] = match("container", metric, now-EvidenceWindowSecs)
		}
	}

	if events, err := e.Store.QueryEvents(ctx, store.EventFilter{Kinds: []string{"container.oom"}, From: now - EvidenceWindowSecs}); err == nil {
		in.OOMEvents = events
	} else {
		log.Printf("insight engine: query container.oom events: %v", err)
	}
	return in
}

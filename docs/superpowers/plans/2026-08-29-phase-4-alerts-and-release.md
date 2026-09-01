# Gantry Phase 4: Alerts + Community Apps Release — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gantry stops being a dashboard you have to be looking at. A hysteresis alert engine evaluates user-editable rules against the live ring and the events table, delivers through Unraid's own notification spool and generic webhooks, surfaces everything in a ninth view — and the whole thing ships: multi-tag GHCR images on a `v*` tag, a Community Applications template, a public-facing README, and a validated on-box soak.

**Architecture:** A new `alert.Engine` runs on its own 10s ticker beside the existing collector registry, reading through two narrow injected closures (a live-ring matcher and an events cursor) exactly the way `server.Options` already takes `Query`/`Top`/`Events`. Rules and alert instances live in SQLite (migration `003_alerts.sql`) next to samples/events/settings. Firing and resolving both append `alert.fired` / `alert.resolved` rows to the **existing** events table, so the Events feed, chart markers, and history come free. Delivery is a `Dispatcher` with two pluggable channels (notify spool, webhooks) behind one policy layer that owns dedup, re-notify, resolved notices, throttling, and the flap guard. The frame gains an `alerts` block so the UI is live without polling. The six `thresholds.ts` display-band families become **derived from the same seven threshold rules** — one config, one set of numbers, colors and alerts can never disagree.

**Tech Stack:** Backend: stdlib only (no new Go deps — `net/http` for webhooks, `os` for the spool). Frontend: no new deps. CI: `docker/build-push-action`, `docker/login-action`, `softprops/action-gh-release` (release job only).

**Spec:** `docs/superpowers/specs/2026-08-25-gantry-design.md` §8 (alerting) and §12 (packaging/CI/CA). Carry-ins: `docs/superpowers/phase-3-carry-ins.md` ("Known-unvalidated (Phase 4 pre-release checklist)"), `docs/superpowers/fixtures.md` (discrepancy 3, 5), `docs/superpowers/phase-3-validation.md` ("Phase 4 polish").

## Phase context

Phase 1 shipped `alert.WriteNotify` (dynamix spool writer) and **human-verified it on the real box** (spikes.md S2: Scott saw the notification in the Unraid UI). Nothing else in `internal/alert/` exists — `WriteNotify` has exactly one caller today, `cmd/spikeprobe`. Phase 2 shipped every metric the default rules need. Phase 3 shipped SSE, the history/top/events/settings/groups APIs, and 8 of 9 spec views; `web/src/lib/router.ts` already carries the comment *"Alerts, the original spec's 7th view, arrives with Phase 4's alerting engine"* — this phase makes that true. Phase 5 is the §16 insights engine, which will reuse this phase's instance/dispatch machinery for `insight.*` findings.

## Deviations from the spec (code wins, recorded)

| Spec §8 says | Code/reality | Ruling |
|---|---|---|
| "per-entity cooldown" | Not built; the shape that actually matters on a home box is **re-notify while firing** plus a **flap guard**, not a post-resolve mute | Implement re-notify + flap guard (Task 7). No separate cooldown column. |
| "disk/pool ≥ 90% full" | No `fs.used_pct` metric exists — `disks.go` records `fs.used_bytes`/`fs.free_bytes` only, and `web/src/lib/disks.ts:diskUsagePct` derives the percent client-side | Add a persisted `fs.used_pct` metric (Task 2) with the identical formula, so one number drives the chart, the band, and the rule. |
| "parity finished with errors" | `parity.finish` carries only `Detail: "reached N.N%"`; `sbSyncErrs`/`sbSynced`/`sbSynced2`/`sbSyncExit` exist in `var.ini` and are **not parsed** | Parse them and enrich the event (Task 2). Retires the carry-in *"parity.finish event gains duration with Phase 4's parity-result work."* |
| "disk ≥ 55°C" (one number) | `thresholds.ts` already splits `disk.temp` (45/55) from `disk.temp.nvme` (60/70) — an NVMe at 58°C is normal | Two rules, scoped by disk class (Task 3). The single-number spec default would fire constantly on Scott's own box. |
| "N targets; optional body template" | — | Targets: yes. **Body template: deferred to v2** (see Open question 4). Fixed JSON envelope in v1. |
| §12 "CI ... Tag `v*` — full test, build, push" | `ci.yml` has no release job at all; the Dockerfile's **stage 1 already builds webdist inside the image** (`node:22-alpine` → `npm run build` → `COPY --from=web-build`) | **Verified: the release job needs no `setup-node` and no `make web`.** `docker buildx build` alone produces the full-UI image (Task 14). |
| §7 "Alerts — rule list, firing history" | — | Plus silence/snooze, which the spec omits and a home box needs the first time a disk sits at 91% for a week. |

## Global Constraints

- **No new Go dependencies.** Webhooks use `net/http` with an explicit `http.Client{Timeout}`; the notify channel uses the already-shipped `alert.WriteNotify` unchanged (its format is human-verified against dynamix — do not touch it).
- **New package layout:** everything engine-side lives in `internal/alert/` (spec §14 already reserves it: *"rules engine, notify writer, webhooks"*). The `server` package stays store-shape-agnostic — it talks to alerts through narrow interfaces in `Options`, adapters wired in `cmd/gantry/main.go`, exactly as `SettingsIface`/`GroupsIface` do today.
- **Series identity unchanged.** No metric renames. Two new metrics only: `disk/<slot>/fs.used_pct` and `unraid/array/parity.errors`. Neither carries a `live:` prefix, so both persist through every tier like `cpu.cores` does.
- **Events are the alert history substrate.** `alert.fired` / `alert.resolved` go into the existing `events` table via `store.AppendEvent`. `alert_instances` holds the state machine; the events table holds the human-readable trail. Do not build a parallel history feed.
- **Engine cadence 10s**, one `Live` lock pass per distinct `(kind, metric)` across enabled rules (≈7 passes/10s at the defaults). Event rules read the events table by id cursor, not by timestamp.
- **Read-only posture:** `GANTRY_READ_ONLY=1` gates **webhook-target writes only** (they configure an outbound side-effect capability). Rule and silence writes follow the `/api/settings` + `/api/groups` precedent: no `X-Gantry-Confirm`, not READ_ONLY-gated (config-shaped data, not a destructive mutation). See Open question 3 — this asymmetry is deliberate and flagged.
- **Fake mode demos everything.** `GANTRY_FAKE_DATA=1` must produce firing alerts, a resolve, a silence-able instance, a delivery failure, and a populated history within ~3 minutes of boot. No feature ships without a fake-mode path (Task 9).
- **Degradation, never errors.** Notify spool unmounted ⇒ the channel reports its enable hint (the exact template line), same convention as collector sources; it is never an error banner. No `/unraid` mount ⇒ `array.started` is never recorded ⇒ `array-stopped` simply never evaluates. That is correct behavior, and it is tested.
- TDD for every Go change. `go test ./... -race` + `make lint` pristine per commit. `npm test` + Playwright green per frontend commit. No AI attribution in commits. ⛔-marked tasks are gated on Scott.

## Task summary

| # | Task | Track | Lane | Depends on |
|---|---|---|---|---|
| 1 | Alert schema + store CRUD | full | A engine | — |
| 2 | Evaluation inputs: `Live.MatchSince`, `fs.used_pct`, parity result | full | A engine | — |
| 3 | Rule model + pure evaluator | full | A engine | 2 |
| 4 | Lifecycle state machine + engine loop | full | A engine | 1, 3 |
| 5 | Default rule seeding | full | A engine | 1, 3 |
| 6 | Dispatcher + notify-spool channel + policy | full | B delivery | 4 |
| 7 | Webhook channel + delivery ledger | full | B delivery | 6 |
| 8 | `/api/alerts/*` + frame `alerts` block | full | C api | 1, 4 |
| 9 | Fake-mode alert demo | full | D fake | 5, 6, 7 |
| 10 | Alerts view (active / history / silence) | fast | E ui | 8 |
| 11 | Rule editor + validation | fast | E ui | 10 |
| 12 | Band unification + Overview anomaly merge | fast | E ui | 8, 11 |
| 13 | Playwright + responsive/a11y pass | fast | E ui | 10–12 |
| 14 | Release workflow + versioning + CHANGELOG | full | F release | — |
| 15 | CA template XML + icon | full | F release | 14 |
| 16 | Container hardening decision (non-root analysis) | full | F release | 15 |
| 17 | README/docs/screenshots for public consumption | fast | F release | 13, 15 |
| 18 | ⛔ On-box validation, soak, pre-release checklist | ⛔ | F release | all |

---

### Task 1: Alert schema + store CRUD

**Track:** full (adversarial review). **Files:** Create `internal/store/migrations/003_alerts.sql`, `internal/store/alerts.go`, `internal/store/alerts_test.go`; Modify `internal/store/maintain.go` (+test) for alert retention.

**Schema** (`003_alerts.sql`):

```sql
CREATE TABLE alert_rules (
    id                TEXT PRIMARY KEY,          -- stable slug, e.g. "disk-temp-high"
    name              TEXT NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    builtin           INTEGER NOT NULL DEFAULT 0, -- seeded default: disable-only, never deletable
    type              TEXT NOT NULL,             -- 'threshold' | 'event'
    kind              TEXT NOT NULL DEFAULT '',  -- host|container|disk|gpu|unraid
    entity_glob       TEXT NOT NULL DEFAULT '*',
    entity_class      TEXT NOT NULL DEFAULT '',  -- '' any; 'nvme'; '!nvme' negates
    metric            TEXT NOT NULL DEFAULT '',  -- threshold rules
    op                TEXT NOT NULL DEFAULT '>', -- '>' | '<'
    threshold         REAL NOT NULL DEFAULT 0,   -- FIRE boundary (== the band's "serious")
    clear_threshold   REAL NOT NULL DEFAULT 0,   -- hysteresis EXIT boundary (engine-only)
    warn_threshold    REAL NOT NULL DEFAULT 0,   -- display band only, never fires
    critical_threshold REAL NOT NULL DEFAULT 0,  -- display band only; 0 = family has no 4th tier
    band_family       TEXT NOT NULL DEFAULT '',  -- thresholds.ts MetricFamily this rule drives; '' = none
    for_seconds       INTEGER NOT NULL DEFAULT 0,
    clear_seconds     INTEGER NOT NULL DEFAULT 0,
    event_kinds       TEXT NOT NULL DEFAULT '',  -- comma-separated; event rules
    min_severity      TEXT NOT NULL DEFAULT '',  -- info|warning|alert floor on the source event
    clear_event_kinds TEXT NOT NULL DEFAULT '',  -- '' = timeout-only auto-resolve
    clear_max_severity TEXT NOT NULL DEFAULT '',
    severity          TEXT NOT NULL DEFAULT 'warning',
    channels          TEXT NOT NULL DEFAULT '',  -- '' = every enabled channel; else comma-separated ids
    renotify_hours    INTEGER NOT NULL DEFAULT 0,
    updated_at        INTEGER NOT NULL
);

CREATE TABLE alert_instances (
    id               INTEGER PRIMARY KEY,
    rule_id          TEXT NOT NULL,
    kind             TEXT NOT NULL,
    entity           TEXT NOT NULL,
    metric           TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL,             -- pending|firing|resolved
    severity         TEXT NOT NULL,
    value            REAL NOT NULL DEFAULT 0,
    threshold        REAL NOT NULL DEFAULT 0,   -- snapshot at fire time (a later rule edit must not rewrite history)
    summary          TEXT NOT NULL DEFAULT '',
    started_at       INTEGER NOT NULL,          -- first breach
    fired_at         INTEGER NOT NULL DEFAULT 0,
    resolved_at      INTEGER NOT NULL DEFAULT 0,
    resolve_reason   TEXT NOT NULL DEFAULT '',  -- cleared|timeout|no-data|rule-disabled
    last_notified_at INTEGER NOT NULL DEFAULT 0,
    notify_count     INTEGER NOT NULL DEFAULT 0
);
-- One ACTIVE instance per (rule, entity), enforced by the DB, not by engine bookkeeping.
CREATE UNIQUE INDEX idx_alert_active ON alert_instances (rule_id, entity) WHERE resolved_at = 0;
CREATE INDEX idx_alert_instances_started ON alert_instances (started_at);

CREATE TABLE alert_silences (
    id         INTEGER PRIMARY KEY,
    rule_id    TEXT NOT NULL DEFAULT '',   -- '' = any rule
    entity     TEXT NOT NULL DEFAULT '',   -- '' = any entity
    until      INTEGER NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE alert_deliveries (
    id          INTEGER PRIMARY KEY,
    instance_id INTEGER NOT NULL,
    ts          INTEGER NOT NULL,
    channel     TEXT NOT NULL,             -- 'notify' | 'webhook'
    target      TEXT NOT NULL DEFAULT '',  -- webhook target id; '' for notify
    phase       TEXT NOT NULL,             -- 'fired' | 'resolved' | 'renotify'
    attempts    INTEGER NOT NULL DEFAULT 1,
    ok          INTEGER NOT NULL,
    status      INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_alert_deliveries_ts ON alert_deliveries (ts);
```

**Interfaces** (`internal/store/alerts.go` — mirrors `events.go`'s split: writes on `s.db`, reads on `s.readDB` with ctx):

```go
type AlertRule struct{ /* one field per column above */ }
type AlertInstance struct{ /* ditto */ }
type Silence struct{ ID int64; RuleID, Entity, Reason string; Until, CreatedAt int64 }
type Delivery struct{ /* ditto */ }

func (s *Store) AlertRules(ctx context.Context) ([]AlertRule, error)          // ordered by builtin DESC, id ASC
func (s *Store) ReplaceAlertRules(rules []AlertRule) error                    // one tx, whole-document replace (see Task 8)
func (s *Store) UpsertAlertRule(r AlertRule) error
func (s *Store) ActiveAlertInstances(ctx context.Context) ([]AlertInstance, error) // resolved_at = 0
func (s *Store) UpsertAlertInstance(i AlertInstance) (int64, error)
func (s *Store) ResolveAlertInstance(id int64, at int64, reason string) error
func (s *Store) AlertHistory(ctx context.Context, from, to int64, limit int) ([]AlertInstance, error)
func (s *Store) Silences(ctx context.Context, now int64) ([]Silence, error)   // expired rows excluded from the read, pruned in Maintain
func (s *Store) AddSilence(sil Silence) (int64, error)
func (s *Store) DeleteSilence(id int64) error
func (s *Store) RecordDelivery(d Delivery) error
func (s *Store) LastDeliveries(ctx context.Context, limit int) ([]Delivery, error)
// QueryEventsSince reads forward by id (NOT ts) so the event-rule cursor
// can never miss a row inserted with an equal or earlier timestamp than
// one it already saw -- the clock is not monotonic across an NTP step,
// but the rowid is.
func (s *Store) QueryEventsSince(ctx context.Context, afterID int64, limit int) ([]Event, error)
func (s *Store) MaxEventID(ctx context.Context) (int64, error)
```

`Maintain` gains: delete `alert_instances` with `resolved_at > 0 AND resolved_at < now - R2` (30d default, same knob), delete `alert_deliveries` older than 7d, delete `alert_silences` with `until < now`.

**Tests:** migration applies to version 3 and the partial unique index exists; a second active instance for the same `(rule_id, entity)` is rejected while the first is unresolved and accepted once resolved; `QueryEventsSince` returns rows strictly after the cursor id, ordered ascending, respecting `limit` (insert three events sharing one ts — all three come back exactly once across two cursor calls); `Silences` excludes an expired row; `Maintain` prunes resolved instances/old deliveries/expired silences and leaves active ones alone.

---

### Task 2: Evaluation inputs — `Live.MatchSince`, `fs.used_pct`, parity result

**Track:** full. **Files:** Modify `internal/store/live.go` (+test), `internal/collect/unraid/disks.go` (+test), `internal/collect/unraid/var.go` (+test), `internal/collect/unraid/testdata/var_parity_running.ini` if a finished-run fixture is needed.

**Interfaces:**

```go
// MatchSince returns, for every series of this kind+metric, the samples at
// or after `since`, keyed by entity -- ONE read-lock pass, the same
// reasoning SnapshotLatest already documents (the alert engine asks per
// (kind, metric), not per series, so the N+1 shape would multiply by rule
// count every tick). oldestTS reports the oldest sample the ring still
// holds for each entity, so a caller can tell "clean for the whole window"
// from "the series is younger than the window" without a second call.
func (l *Live) MatchSince(kind, metric string, since int64) map[string][]Sample
```

`fs.used_pct` (disks.go, recorded on the same tick, right after the existing `fs.used_bytes`/`fs.free_bytes` pair, and only when both are present):

```go
if total := fsUsed + fsFree; total > 0 {
    c.sink.Record(store.SeriesKey{Kind: "disk", Entity: slot, Metric: "fs.used_pct"}, ts, fsUsed/total*100)
}
```

Formula is byte-identical to `web/src/lib/disks.ts:diskUsagePct` — assert that in the test with the same numbers `disks.test.ts` uses. `disks.ts` keeps deriving client-side (older history has no `fs.used_pct` series); the new metric exists so the RULE and the BAND read one number.

Parity result (`var.go`): extend `ArrayState` with `SyncErrs float64`, `SyncStart, SyncFinish int64`, `SyncExit int` from `sbSyncErrs`/`sbSynced`/`sbSynced2`/`sbSyncExit` (all present in `testdata/var_real.ini`, all currently unread). Record `unraid/array/parity.errors`. `transitionEvents`' finish event becomes:

```
Detail: "reached 99.9% · 2h14m · 0 errors"   Severity: "info"
Detail: "reached 100.0% · 5h02m · 3 errors"  Severity: "alert"   // errs > 0
```

Duration from `sbSynced2 - sbSynced` when both are non-zero, omitted from the string otherwise (never a fabricated "0s"). Retires two carry-ins at once.

**Tests:** `MatchSince` returns only matching kind+metric, respects `since`, is empty (non-nil) for an unknown pair, and takes exactly one lock (assert via a concurrent writer that would deadlock under nested locking); `fs.used_pct` is absent for parity (no fs keys) and matches `diskUsagePct`'s value for `disks_real.ini`'s cache pool; the parity finish event carries duration + error count and flips to severity `alert` on `sbSyncErrs="3"`; a run with `sbSynced2="0"` omits the duration segment.

---

### Task 3: Rule model + pure evaluator

**Track:** full. **Files:** Create `internal/alert/rule.go`, `internal/alert/rule_test.go`, `internal/alert/eval.go`, `internal/alert/eval_test.go`.

**Interfaces:**

```go
type Verdict int
const (
    VerdictInsufficient Verdict = iota // the series is younger than the window -- NOT a breach, NOT a clear
    VerdictBreaching                   // every sample in [now-ForSeconds, now] crosses Threshold
    VerdictClearing                    // every sample in [now-ClearSeconds, now] is past ClearThreshold
    VerdictHolding                     // neither -- an existing instance keeps its state
)

// EvaluateThreshold is pure: no clock, no store, no I/O.
// oldest is the ring's oldest retained TS for this series (0 = unknown).
func EvaluateThreshold(r store.AlertRule, samples []store.Sample, oldest, now int64) (Verdict, float64)

// MatchEntity glob-matches an entity name: "*" any, a trailing "*" is a
// prefix match, otherwise exact. Deliberately NOT full glob/regex --
// docker names and Unraid slot names have no charset that needs it, and a
// regex in a user-editable rule is a support burden.
func MatchEntity(glob, entity string) bool
// MatchClass: "" matches anything; "nvme" requires class=="nvme";
// "!nvme" requires class!="nvme". Leading "!" is the only operator.
func MatchClass(spec, class string) bool
func ValidateRule(r store.AlertRule) error
```

**Sustained-for semantics (the correctness core — get this right):**

1. `VerdictInsufficient` when the ring cannot cover the window: `oldest > now-r.ForSeconds`. A rule with a 10-minute window on a container that started 90s ago is *not* evaluated. This is what stops every rule firing at once on boot, and it must be a distinct verdict, not a silent `false`.
2. `VerdictBreaching` requires **every** sample in the window to cross — one sample below `Threshold` resets it. Not an average, not a majority: the spec says *sustained*.
3. `VerdictClearing` uses `ClearThreshold` and `ClearSeconds`, evaluated with the opposite comparison — for `op ">"`, clear means `val < ClearThreshold` for the whole clear window. `ClearThreshold` may equal `Threshold` (no hysteresis) but the validator warns.
4. An empty window (series exists, ring covers it, zero samples inside) is `VerdictInsufficient`, never `VerdictClearing` — silence is not evidence of recovery. A *missing* series entirely is handled at the engine layer (Task 4) as `no-data`.
5. Comparison is strict (`>` / `<`), matching `thresholds.ts`'s documented "a value sitting exactly ON a threshold reads as the band below it."

**Tests (the fire/clear/edge matrix spec §11 names explicitly):** all-breaching full window fires; one dip mid-window does not; a window not yet covered returns Insufficient regardless of values; exactly-on-threshold does not breach; `op "<"` (the `array-stopped` shape) fires on 0 and clears on 1; clear window shorter than the fire window behaves independently; empty window is Insufficient; glob matrix (`*`, `jelly*`, exact, non-match); class matrix (`""`, `nvme`, `!nvme` against `nvme`/`hdd`/`""`); `ValidateRule` rejects: unknown type/op/severity, `for_seconds` > 3600, `threshold == clear_threshold` on a `>` rule with `clear_seconds > 0`, a threshold rule with no metric, an event rule with no `event_kinds`, `renotify_hours` outside 0–168, a `band_family` not in `thresholds.ts`'s six.

---

### Task 4: Lifecycle state machine + engine loop

**Track:** full. **Files:** Create `internal/alert/engine.go`, `internal/alert/engine_test.go`; Modify `cmd/gantry/main.go` (wiring + one goroutine).

**Interfaces:**

```go
type Store interface { // narrow, package-local, mirrors fake.EventSink's convention
    AlertRules(context.Context) ([]store.AlertRule, error)
    ActiveAlertInstances(context.Context) ([]store.AlertInstance, error)
    UpsertAlertInstance(store.AlertInstance) (int64, error)
    ResolveAlertInstance(id, at int64, reason string) error
    Silences(context.Context, int64) ([]store.Silence, error)
    QueryEventsSince(context.Context, int64, int) ([]store.Event, error)
    MaxEventID(context.Context) (int64, error)
    AppendEvent(store.Event) (int64, error)
}

type Engine struct {
    Store    Store
    Match    func(kind, metric string, since int64) map[string][]store.Sample // st.Live().MatchSince
    ClassOf  func(kind, entity string) string  // main: ur.DiskMeta()[entity].Kind for kind=="disk", "" otherwise
    Fleet    func() []FleetMember              // main: dc.All() (+ fakeMetas) -> {Name, State, Health}
    Dispatch func(Notification)                // Task 6; nil = evaluate but never deliver
    Clock    func() time.Time
}
func New(...) *Engine
func (e *Engine) Tick(ctx context.Context) error   // one full evaluation pass; safe to call from a test with an injected clock
func (e *Engine) Run(ctx context.Context, every time.Duration)
```

**State machine** — for each `(rule, entity)`:

| current | verdict | next | side effect |
|---|---|---|---|
| (none) | Breaching | `firing` | insert instance, `alert.fired` event, dispatch |
| (none) | Holding, value crosses but window not yet covered | `pending` | insert instance, **no event, no dispatch** |
| `pending` | Breaching | `firing` | update, `alert.fired` event, dispatch |
| `pending` | Clearing / Holding-below | resolved (`cleared`) | resolve silently — a pending alert that never fired produces **no** resolved notice and **no** event |
| `firing` | Clearing | `resolved` | resolve, `alert.resolved` event, dispatch resolved notice |
| `firing` | Holding/Breaching | `firing` | update `value`; re-notify if `renotify_hours` elapsed |
| `firing` | series absent from `Match` for `clear_seconds` | `resolved` (`no-data`) | resolve, event, resolved notice |
| any active | rule disabled or deleted | `resolved` (`rule-disabled`) | resolve, event, **no** dispatch |
| any active | silence covers it | state unchanged | dispatch suppressed; instance flagged `silenced` in the API |

**Event rules** run in the same `Tick`, off the id cursor: read `QueryEventsSince(lastID)`, for each event match `event_kinds` + `min_severity` floor + entity glob → fire an instance immediately (event rules have no sustained-for). Resolve when a `clear_event_kinds` event at or below `clear_max_severity` arrives for the same entity, or after `clear_seconds` elapses (timeout), whichever first.

**Boot seeding (do not skip — this is the failure the design invites):** an event rule can only ever see events that arrive *after* Gantry starts. A container that was already unhealthy before boot emits nothing. On the first `Tick`, the engine walks `Fleet()` and synthesizes the state that `container-unhealthy` would have fired on (`state=="running" && health=="unhealthy"`) — the exact gate `unhealthyContainerNames` in `web/src/lib/containerStatus.ts` already applies, so the banner and the alert can never disagree about a stale post-stop `unhealthy`. The cursor itself starts at `MaxEventID()` so a restart does not replay the entire events table as fresh alerts.

**Wiring (`main.go`):** one `wg.Add(1)` goroutine running `eng.Run(runCtx, 10*time.Second)`, constructed after `registry.Run` and before the server, alongside the existing publish loop. The engine tick recovers from panic per pass and logs (collector-isolation posture, spec §9) — one malformed rule must never take the process down.

**Tests:** full transition matrix above driven with an injected clock and a stub `Match` (no store, no docker); a `pending`→resolved path writes zero events; `no-data` fires only after `clear_seconds`, not on the first missing tick; the partial unique index is respected across two consecutive ticks (no duplicate active instance); a silence suppresses dispatch but not the state transition; boot seeding fires for a pre-existing unhealthy container and does **not** fire for a stopped one carrying a stale `unhealthy`; the cursor starts at `MaxEventID` (insert 50 events, start engine, assert zero alerts); a rule whose evaluation panics is contained and the other rules still evaluate.

---

### Task 5: Default rule seeding

**Track:** full. **Files:** Create `internal/alert/defaults.go`, `internal/alert/defaults_test.go`; Modify `cmd/gantry/main.go` (seed call at boot).

**Contract:** on boot, for every rule in `DefaultRules()`, insert it if `id` is absent from `alert_rules`; **never** overwrite an existing row (a user's edited threshold survives every upgrade) and never re-insert one the user deleted-by-disabling. `builtin=1` rules cannot be deleted through the API, only disabled — so "absent" unambiguously means "new default introduced by an upgrade", which is exactly when seeding should run. The seed is idempotent and runs before the engine's first tick.

**The default rule set** (12 rules; every threshold rule's `threshold` is its `thresholds.ts` family's existing **serious** boundary, and `warn_threshold`/`critical_threshold` are that family's existing **warn**/**critical** — no number in this table is new):

| id | type | scope | metric / events | fire | for | clear | clear-for | severity | band family (warn/serious/crit) | re-notify |
|---|---|---|---|---|---|---|---|---|---|---|
| `host-cpu-high` | threshold | host | `cpu.total` | `> 85` | 10m | `< 70` | 5m | warning | `host.cpu` 70/85/95 | — |
| `host-mem-high` | threshold | host | `mem.used_pct` | `> 85` | 10m | `< 70` | 5m | warning | `host.mem` 70/85/95 | — |
| `disk-usage-high` | threshold | disk `*` | `fs.used_pct` | `> 90` | 15m | `< 85` | 15m | warning | `disk.capacity` 70/90/95 | 24h |
| `disk-temp-high` | threshold | disk class `!nvme` | `temp.c` | `> 55` | 10m | `< 50` | 10m | warning | `disk.temp` 45/55/— | 12h |
| `disk-temp-nvme-high` | threshold | disk class `nvme` | `temp.c` | `> 70` | 10m | `< 65` | 10m | warning | `disk.temp.nvme` 60/70/— | 12h |
| `container-mem-limit-high` | threshold | container `*` | `mem.limit_pct` | `> 85` | 10m | `< 75` | 10m | warning | `container.mem_limit_pct` 75/85/95 | — |
| `array-stopped` | threshold | unraid `array` | `array.started` | `< 1` | 5m | `> 0` | 1m | alert | — | 24h |
| `container-unhealthy` | event | container `*` | `container.health` ≥ warning | on event | — | `container.health` ≤ info | timeout 6h | alert | — | 24h |
| `container-oom` | event | container `*` | `container.oom` ≥ alert | on event | — | timeout | 60m | alert | — | — |
| `container-exit-nonzero` | event | container `*` | `container.die` ≥ warning | on event | — | timeout | 60m | warning | — | — |
| `disk-errors` | event | disk `*` | `disk.errors` ≥ alert | on event | — | timeout | 24h | alert | — | 24h |
| `parity-errors` | event | unraid `array` | `parity.finish` ≥ warning | on event | — | timeout | 24h | alert | — | — |

Notes that must land as code comments, because each one is a trap:

- **The `min_severity` floor does the predicate work.** `docker/registry.go` already assigns `container.die` severity `warning` only for a nonzero exit code, and `container.health` severity `warning` only for `unhealthy`. So `container-exit-nonzero` and `container-unhealthy` need no detail-string parsing at all — the collector already made that call, in one place. **But:** `diffEvents` (the 10s poll path) emits `container.die` with severity `info` and `Detail: "state: exited"`, carrying no exit code, while `translateEvent` (the event-stream path) carries it. A container that dies while the event stream has a gap therefore produces no exit-code alert. That is honest degradation, not a bug — document it in the rule's own description string, visible in the UI.
- **`array-stopped` uses `op "<"` on a 1/0 metric** rather than an `array.state` event rule, deliberately: `array.state` only fires on a *transition* (its own code comment says so), so a box that booted with the array already stopped would never emit one. `array.started` is recorded every tick.
- **`container-mem-limit-high` self-scopes.** `mem.limit_pct` is emitted only for containers that actually have a memory limit (carry-in 2026-08-28: absence means unlimited). No series ⇒ no evaluation ⇒ no alert. Nothing to filter.
- **No rule on `update_status`.** See Open question 2.

**Tests:** every default passes `ValidateRule`; ids are unique; each threshold rule's `(warn, threshold, critical)` triple equals its `thresholds.ts` family's `(warn, serious, critical)` — a table-driven test that reads the numbers from a shared fixture so a future edit to one side fails the other; seeding twice inserts once; seeding does not resurrect a disabled builtin nor overwrite an edited threshold; a new default added to the list is inserted on the next boot of an existing DB.

---

### Task 6: Dispatcher + notify-spool channel + delivery policy

**Track:** full. **Files:** Create `internal/alert/dispatch.go`, `internal/alert/channel_notify.go`, and tests; Modify `cmd/gantry/main.go`.

**Interfaces:**

```go
type Notification struct {
    Phase     string  // "fired" | "resolved" | "renotify"
    Instance  store.AlertInstance
    Rule      store.AlertRule
    Summary   string  // plain sentence, e.g. "disk3 is at 57.0 C (over 55.0 C for 10 minutes)"
}
type Channel interface {
    ID() string                                    // "notify" | "webhook:<target-id>"
    Health() string                                // "ok" or the enable hint, sources-map convention
    Send(ctx context.Context, n Notification) error
}
type Dispatcher struct{ Channels []Channel; Store Store; Clock func() time.Time }
func (d *Dispatcher) Dispatch(n Notification)      // non-blocking: enqueues, one worker per channel
```

**Notify channel** (`channel_notify.go`) wraps the already-verified `WriteNotify` — do not modify `notify.go`:

- Severity → dynamix importance: `info`→`normal`, `warning`→`warning`, `alert`→`alert`. These are the only three `WriteNotify` accepts, and the mapping is 1:1 with `store.Event.Severity`'s own vocabulary, so no lossy translation exists anywhere.
- `Event: "Gantry"` (dynamix groups by this), `Subject: "<rule name> — <entity>"`, `Description: Summary`, `Link:` from setting `alert.link_base` (default `""` ⇒ omitted; Gantry cannot know its own reachable URL and must not guess one).
- Resolved notices: importance `normal` regardless of the rule's severity, subject prefixed `Resolved: `.
- `Health()`: probe the dir at construction and every 60s by writing and removing `.gantry-probe`; on failure return the verbatim enable hint `"mount /tmp/notifications to /notify (rw) to deliver Unraid notifications"`.

**Policy layer** (`dispatch.go`), all four pieces tested independently:

1. **Dedup** — the `(rule_id, entity)` partial unique index is the dedup key; the dispatcher never sees a duplicate fire because the engine cannot create one.
2. **Re-notify** — while `firing`, if `renotify_hours > 0` and `now - last_notified_at >= renotify_hours`, dispatch `phase="renotify"` and bump `notify_count`. Defaults: 24h on the four `alert`-severity rules and the two slow-burn disk rules, off elsewhere (see the table). Rationale: an OOM or a nonzero exit is a *point* event whose repeat is a fresh alert; a hot disk is a *condition* worth re-raising once a day.
3. **Resolved notices** — global setting `alert.notify_resolved`, default `true`. A `pending` instance that never fired never produces one.
4. **Spool throttle + flap guard** (Open question 5's implementation):
   - Global token bucket over the notify channel only: **10 files/hour, burst 4**. On exhaustion, deliveries are coalesced into one summary file per hour (`Subject: "N Gantry alerts suppressed"`, importance = the max suppressed severity, description listing the rule/entity pairs) and an `alert.delivery_throttled` event is appended once per hour.
   - Per-`(rule, entity)` flap guard: **4 fire→resolve cycles within 1 hour** auto-inserts a 1-hour silence for that pair, dispatches exactly one notification saying so, and appends `alert.flapping`. The silence appears in the Alerts view like any other and can be lifted there.
   - Webhooks are deliberately **not** throttled by the bucket — a webhook target is machine-facing and its own consumer can rate-limit; the bucket exists to protect Unraid's notification UI, which is human-facing and has no dismissal-at-scale affordance.

**Tests:** severity→importance mapping table; a `Link` is written only when `alert.link_base` is set; the probe writes and cleans up its file and reports the verbatim hint on a read-only dir (`t.TempDir()` chmod 0500); the bucket allows 4 immediately then throttles, and emits exactly one summary and one event per hour (injected clock); the flap guard trips on the 4th cycle and not the 3rd; a silenced instance produces zero `Send` calls; resolved notices honor the global toggle; `Dispatch` returns without blocking when a channel's worker is wedged (send 10, assert prompt return — same shape as `Broadcaster`'s slow-client drop test).

---

### Task 7: Webhook channel + delivery ledger

**Track:** full. **Files:** Create `internal/alert/channel_webhook.go`, `internal/alert/webhook_test.go`; Modify `internal/alert/dispatch.go` (ledger writes), `cmd/gantry/main.go` (targets adapter over the settings table, `webhookTargetsSettingsKey = "alert.webhook_targets"`, JSON blob — the exact `groupsAdapter` precedent).

**Target config:**

```go
type WebhookTarget struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    URL         string `json:"url"`
    Enabled     bool   `json:"enabled"`
    HeaderName  string `json:"header_name,omitempty"`  // e.g. "Authorization" or "X-Api-Key"
    HeaderValue string `json:"header_value,omitempty"` // never returned by GET -- see below
    TimeoutS    int    `json:"timeout_s"`              // 1-30, default 10
}
```

- `GANTRY_WEBHOOK_URL` (spec §5's documented env var) seeds a single target with id `env` on first boot; while the env var is set that target is **read-only in the API** and marked `env_overridden`, mirroring `/api/settings`' own per-field env-lock contract exactly (409 on a differing write, silent no-op on an identical one).
- Validation: scheme must be `http` or `https`; a URL carrying userinfo (`https://user:pass@…`) is rejected outright (it would land in the DB and in logs); max 8 targets; `HeaderName` must be a valid token, `HeaderValue` ≤ 1KB.
- **`HeaderValue` is never returned by GET** — the response carries `"header_set": true|false`. A PUT that omits `header_value` on an existing target keeps the stored one; a PUT that sends `""` clears it. This is the only way to have an editable secret without echoing it to every browser tab on the LAN. See Open question 1.

**Body** — one fixed JSON envelope, versioned so a template feature can arrive later without breaking consumers:

```json
{ "version": 1, "event": "alert.fired",
  "alert": { "rule_id": "disk-temp-high", "rule_name": "Disk temperature high", "severity": "warning",
             "state": "firing", "kind": "disk", "entity": "disk3", "metric": "temp.c",
             "value": 57.0, "threshold": 55.0, "op": ">",
             "started_at": 1756400000, "fired_at": 1756400600, "resolved_at": 0 },
  "summary": "disk3 is at 57.0 C (over 55.0 C for 10 minutes)",
  "source": "gantry", "gantry_version": "v0.1.0" }
```

Headers: `Content-Type: application/json`, `User-Agent: gantry/<version>`, plus the target's own header pair when set.

**Retry/backoff:** 3 attempts, backoff 2s → 8s → 32s with ±20% jitter, per-attempt timeout `TimeoutS`. Retry on 5xx, 408, 429, and transport errors; **do not** retry any other 4xx (a 404 or 401 is a configuration mistake and retrying it three times per alert just amplifies it). Every attempt's outcome lands in `alert_deliveries`. Queue: buffered channel cap 256 per target, one worker per target so a dead target cannot stall a live one; on overflow drop the oldest and count it.

**Failure surfacing:** on a delivery that exhausts its attempts, append `alert.delivery_failed` (severity `warning`, entity = target id, detail = last status/error) — **rate-limited to one per target per hour** so a dead webhook cannot itself become the noise source it was meant to prevent. `Health()` returns `"ok"` or `"last delivery failed: <status> <error> (<relative time>)"`, which the Settings Channels card renders verbatim.

**Tests:** `httptest.Server` returning 200 → one delivery row, `ok=1`, one attempt; 500 → three attempts then one `ok=0` row and one `alert.delivery_failed`; 404 → exactly one attempt; a hanging handler trips the per-attempt timeout; the secret header is present on the wire and absent from the GET response; a userinfo URL and a `file://` URL are both rejected by validation; `GANTRY_WEBHOOK_URL` produces an env-locked target and a differing PUT 409s; queue overflow drops rather than blocks; the `alert.delivery_failed` rate limit emits once per hour under a clock.

---

### Task 8: `/api/alerts/*` + frame `alerts` block

**Track:** full. **Files:** Create `internal/server/api_alerts.go`, `internal/server/api_alerts_test.go`; Modify `internal/server/server.go` (routes + `Options`), `internal/server/api_snapshot.go` (DTO), `cmd/gantry/main.go` (adapters).

**Routes** (all gzipped; `AlertsIface`/`WebhooksIface` in `Options`, nil-tolerant with the established "meaningful empty for GET, 404 for PUT" convention):

| Route | Shape |
|---|---|
| `GET /api/alerts` | `{"active":[AlertInstanceDTO],"silences":[SilenceDTO],"channels":{"notify":"ok","webhook:home":"…"}}` |
| `GET /api/alerts/rules` | `{"rules":[AlertRuleDTO]}` |
| `PUT /api/alerts/rules` | same envelope in, whole-document replace, `DisallowUnknownFields` — the exact `/api/groups` contract, for the exact same reason (the UI always submits its own already-edited full list) |
| `GET /api/alerts/history?from=&to=&limit=` | `[AlertInstanceDTO]`, resolved first, `limit` default 100 cap 500 |
| `POST /api/alerts/silences` | `{"rule_id":"","entity":"","hours":8,"reason":""}` → the created silence; `hours` 1–720 |
| `DELETE /api/alerts/silences/{id}` | 204 |
| `GET /api/alerts/webhooks` | `{"targets":[…]}` with `header_set` in place of `header_value`, plus `env_overridden` |
| `PUT /api/alerts/webhooks` | whole-document replace; **403 under `GANTRY_READ_ONLY=1`**; 409 on an env-locked target mismatch |

**Frame block** — `SnapshotDTO` gains:

```go
Alerts AlertsBlockDTO `json:"alerts"`
// {"firing":[{rule_id,rule_name,severity,kind,entity,metric,value,threshold,fired_at,silenced}],
//  "firing_count":N,"truncated":M,"channels":{...}}
```

Capped at **20** entries with a `truncated` count (a healthy box carries zero; the cap exists so a pathological rule cannot bloat every 2s frame for every connected client). `pending` instances are **not** in the frame — a pending alert is engine bookkeeping, not a user-facing state.

**Validation:** `PUT /api/alerts/rules` runs `alert.ValidateRule` on each; a request removing a `builtin=1` rule is rejected 400 naming it (builtins are disable-only); duplicate ids 400; >100 rules 400. Rule writes are **not** confirm-header- or READ_ONLY-gated (config, like `/api/settings` and `/api/groups`); webhook writes **are** READ_ONLY-gated — the asymmetry is documented at the route and raised as Open question 3.

**Tests:** httptest coverage of each route incl. the nil-Options degradations; whole-document replace round-trip; builtin-deletion rejection; silence create/delete/expiry; READ_ONLY 403 on webhooks and 200 on rules in the same test to pin the asymmetry deliberately; the frame cap and `truncated` count; `header_value` never appears in any GET response body (assert on the raw bytes).

---

### Task 9: Fake-mode alert demo

**Track:** full. **Files:** Modify `internal/fake/fake.go` (+tests), `cmd/gantry/main.go` (fake wiring for notify dir + a loopback webhook target).

**Contract** — every Phase 4 feature must be exercisable with no Unraid box, per the standing convention:

- The generator drives `disk4`'s `temp.c` from 48 → 58 °C over ~90s and holds it, so `disk-temp-high` goes pending → firing on a compressed schedule. To make that observable inside a demo session, fake mode sets the seeded builtins' `for_seconds`/`clear_seconds` to **60s/60s** at seed time (a single `DefaultRules(fast bool)` parameter — not a separate rule list, so the demo exercises the real rules).
- `disk4` then cools back below 50 °C at ~T+6min, producing a real resolve, a resolved notice, and a history row.
- A synthetic `container.oom` on `sonarr` at ~T+3min fires `container-oom` (an event rule) and auto-resolves at T+63min.
- `share2`/`Share2` (the real case-collision pair from `fixtures.md`) stay in the fixture so Task 18's collision fix has a regression target.
- Notify dir: fake mode defaults `GANTRY_NOTIFY_DIR` to a temp dir and the Alerts view shows the channel as ok; files land there and are inspectable.
- Webhook: fake mode seeds a target pointing at Gantry's own `/api/healthz` (always 200) plus a second pointing at `http://127.0.0.1:1/dead` so the **failure** path, the delivery ledger, and the Settings failure text all render without any external service.

**Tests:** after N injected ticks, assert an instance reaches `firing` then `resolved`; assert a delivery row of each outcome exists; assert the Alerts frame block is non-empty at the expected tick.

---

### Task 10: Alerts view — active, history, silence

**Track:** fast (batched review pre-merge). **Files:** Create `web/src/views/Alerts.svelte`, `web/src/lib/alerts.ts` (+test); Modify `web/src/lib/router.ts` (route + nav entry + retire the "arrives with Phase 4" comment), `web/src/App.svelte`, `web/src/lib/api.ts` (typed helpers), `web/src/views/Events.svelte` (`KNOWN_KINDS` += `alert.fired`, `alert.resolved`), `web/src/lib/eventHref.ts` (`alert.*` → `#/alerts`), `web/src/lib/eventMarkers.ts` (`alert.fired` marker, severity `critical`, label `Alert`).

**Contract:** Route `#/alerts`, nav item between Events and Settings, icon = a stroke bell-with-slash-free glyph distinct from Events' bell (Events keeps the bell; Alerts gets a triangle-with-exclamation) — same `strokeIcon` helper, no new deps.

- **Active section:** one row per firing alert, live off the frame's `alerts.firing` (no polling). Row: severity dot + label (never color alone — the established `HealthDot` rule), rule name, entity linked through `eventHref`'s existing mapping, current value vs threshold, "firing for <relative>", and a silence control. Empty state: "Nothing is alerting" with the same calm register as Overview's "Everything is running".
- **Silence control:** a menu of 1h / 8h / 24h / 7d, plus "lift" on an already-silenced row. Silenced rows stay visible, dimmed, with the remaining time — a hidden silenced alert is how you forget a silenced alert.
- **History section:** `/api/alerts/history`, newest first, with duration (`resolved_at - fired_at`) and resolve reason rendered in plain words (`cleared` → "recovered", `no-data` → "stopped reporting", `timeout` → "auto-closed", `rule-disabled` → "rule turned off"). "Load more" via a before-cursor on `started_at`, same shape as the Events view's existing pagination.
- **Channels strip:** the frame's `alerts.channels` verbatim, using `SourcesBanner`'s existing hint treatment — an unmounted spool renders the exact template line to add, never an error.
- `web/src/lib/alerts.ts` is the pure half (vitest-tested, no DOM): `describeResolveReason`, `firingDuration`, `silenceLabel`, `sortActiveAlerts` (severity desc, then `fired_at` asc — oldest problem at the top within a severity, matching how `overviewStatus` orders its anomalies).

---

### Task 11: Rule editor + validation

**Track:** fast. **Files:** Modify `web/src/views/Alerts.svelte`, `web/src/lib/alerts.ts` (+test); Create `web/src/components/RuleEditor.svelte`.

**Contract:** A "Rules" section listing all 12 builtins plus any user rules. Per row: enable toggle (immediate PUT of the whole document, the `/api/groups` pattern), name, a one-line plain-English restatement of the rule (`"Warn when any disk goes over 55 °C for 10 minutes"` — generated by a pure `describeRule()` in `alerts.ts`, vitest-tested across all 12 defaults), and an edit affordance.

- Editing a builtin edits its numbers, not its identity: `id`, `type`, `kind`, `metric`, and `band_family` are read-only; `threshold`/`warn`/`critical`/`clear`/`for`/`clear-for`/`severity`/`renotify`/`channels`/`enabled` are editable. Deleting a builtin is not offered at all (the API rejects it; the UI must not present a control that 400s).
- Client-side validation mirrors `ValidateRule` exactly, with the server as the final word — the same `putSettings` error-surfacing shape already used for retention (field-level messages from the 400 body).
- A "Reset to default" per-builtin control PUTs the seeded values back (the values come from `GET /api/alerts/rules?defaults=1`, so there is one source of truth and the UI never hardcodes the table above).
- Creating a user rule: threshold rules only in v1 (event rules are the fixed builtin set — a user-authored event rule needs an event-kind vocabulary picker that would be stale the moment a collector adds a kind).

---

### Task 12: Band unification + Overview anomaly merge

**Track:** fast. **Files:** Modify `web/src/lib/thresholds.ts` (+test), `web/src/lib/overviewStatus.ts` (+test), `web/src/views/Overview.svelte`, `web/src/App.svelte` (one boot fetch).

**Band unification** — this is the point of the whole `band_family` column:

```ts
// setBands installs the runtime band table derived from /api/alerts/rules:
// a rule's warn_threshold/threshold/critical_threshold become its family's
// warn/serious/critical. The compiled-in THRESHOLDS map stays as the
// fallback for a family with no matching rule (an older DB, a rule a user
// deleted) so band() is never undefined and never throws.
export function setBands(rules: AlertRuleDTO[]): void
```

- A **disabled** rule still supplies its bands. Turning off delivery must not change what a number's color means — those are different questions ("should this page tell me" vs "should this page tell my phone"), and silently re-coloring the whole app when someone mutes a notification would be a genuinely confusing side effect. State this in the code comment.
- `App.svelte` fetches `/api/alerts/rules` once at boot and on every successful rules PUT, calling `setBands`. No per-component fetch.
- Regression test: with no rules loaded, `band()` returns exactly today's values for all six families (the compiled-in table is the fallback and its behavior is unchanged); with the seeded defaults loaded, `band()` returns **identical** results — the defaults were chosen to be the current numbers, so this test proves the unification is a no-op on day one and only diverges when a user edits something.

**Anomaly merge — the decision (alerts and anomalies coexist; alerts are a fifth source, not a replacement):**

`deriveOverviewStatus` gains an `alerts: FiringAlertDTO[]` input and a new `{kind:'alert', ruleId, entity, severity, summary}` anomaly variant. Rationale, to be written into the file's own doc comment:

- The frame-derived anomalies are **instantaneous and zero-config**. They are what makes "Everything is running" honest the moment the page loads. Replacing them with the engine would leave the headline silent for the first 10 minutes after every boot (sustained-for), and would couple a *display* concern to user-editable delivery config — someone who mutes the disk-usage notification should not thereby lose the "disk3 is nearest to full" callout.
- Alerts contribute what anomalies structurally cannot: sustained-for (so a 3-second CPU spike is never a row), hysteresis, history, silence, and delivery.
- **Dedup table** — a firing alert whose concern a frame-derived anomaly already covers suppresses the *alert* row and upgrades the existing row's severity to the alert's (the frame-derived row is more specific and already links correctly):

  | firing rule | suppressed by existing anomaly |
  |---|---|
  | `disk-usage-high` | `disk-usage` (same slot) |
  | `disk-errors` | `disk-errors` (same slot) |
  | `array-stopped` | `array-stopped` |
  | `container-unhealthy` | `unhealthy` (same name) |

  Everything else — host CPU/memory, both disk-temp rules, container memory-limit, OOM, nonzero exit, parity errors — is signal only the engine can produce, and each gets its own attention row linking to `#/alerts`.
- The headline count stays `anomalies.length`, so "N things need you" and the number of rows can still never disagree — the invariant the file already documents.

---

### Task 13: Playwright + responsive/a11y pass

**Track:** fast. **Files:** Create `web/tests/alerts.spec.ts`; Modify `web/tests/smoke.spec.ts` (the ninth route), `web/src/views/Alerts.svelte` (fixes found).

**Contract:** Against the real fake-mode binary: `#/alerts` renders its heading and the empty state on a cold boot; after the fake schedule trips (poll up to 3 min for `alerts.firing`), an active row appears with the rule name and entity; silencing a row dims it and shows remaining time, and lifting restores it; the history section populates after the fake resolve; the rule editor opens, rejects an out-of-range threshold with a field message, and a saved edit round-trips after reload; the channels strip renders both the healthy and the failing webhook target. Plus the standing invariants at 375/768/1280: no horizontal page scroll on `#/alerts`, TabBar carries the ninth item without wrapping (a `mobileLabel` if needed), every icon-only control (silence menu, edit) has an `aria-label`, severity is never color-alone, and `prefers-reduced-motion` is honored. Also fix the Phase 3 polish note: the GPU engine chart's cold-ring "no engine activity" copy becomes a "collecting…" state.

---

### Task 14: Release workflow + versioning + CHANGELOG

**Track:** full. **Files:** Create `.github/workflows/release.yml`, `CHANGELOG.md`; Modify `README.md` (install line), `Makefile` (`VERSION` from git describe when unset).

**Verified prerequisite:** the Dockerfile's stage 1 (`node:22-alpine` → `npm ci` → `npm run build`, output copied into stage 2 by absolute path) already builds `webdist` **inside** the image. The release job therefore needs no `setup-node`, no `make web`, and no `-tags webdist` step of its own — `docker buildx build` alone produces the full-UI binary. This was the open verification item in the brief; it checks out.

**Workflow** (`on: push: tags: ['v*']`):

1. `permissions: {contents: write, packages: write, id-token: write}`.
2. Re-run the gate before publishing anything: `make lint`, `go test ./... -race`, `cd web && npm ci && npm test`. A tag must never publish an image the test suite has not just passed — CI on `main` is not a substitute, because a tag can point anywhere.
3. `docker/setup-buildx-action` + `docker/login-action` (ghcr.io, `${{ github.actor }}` / `GITHUB_TOKEN`).
4. `docker/metadata-action` producing tags `ghcr.io/smidley/gantry:{{version}}`, `{{major}}.{{minor}}`, `{{major}}`, and `latest` — `latest` **only** on a non-prerelease tag (`v0.1.0-rc1` must not move `latest`; the metadata action's `latest=auto` plus a prerelease guard).
5. `docker/build-push-action` with `platforms: linux/amd64` (spec §2 non-goal: no arm64), `build-args: VERSION=${{ github.ref_name }}`, provenance + SBOM on, and GHA build cache.
6. `softprops/action-gh-release` with the extracted `CHANGELOG.md` section for this version, `prerelease` set from the tag shape.

**Versioning:** semver from `v0.1.0`; `main.version` already takes `-ldflags -X main.version` and the Dockerfile already threads `ARG VERSION` — nothing to build, only to drive. `CHANGELOG.md` in Keep-a-Changelog shape, seeded with a `0.1.0` section covering Phases 1–4.

**Tests/verification:** `act` or a throwaway `v0.0.1-test` tag on a fork to prove the workflow end-to-end; then `docker run --rm ghcr.io/smidley/gantry:v0.0.1-test /gantry -healthcheck`-shaped smoke plus `docker inspect` confirming the image is `scratch`-based and under 20MB (Phase 3 measured 13.1MB); confirm `/api/version` reports the tag, not `dev`.

---

### Task 15: CA template XML + icon

**Track:** full. **Files:** Create `templates/gantry.xml`, `template/gantry-icon.png` (256×256), `docs/install.md`.

**Contract** — every mount/flag pre-filled so a stock install boots to a live dashboard with zero edits (spec §2 success criterion):

| Config | Host | Container | Mode | Why |
|---|---|---|---|---|
| Port | 8380 | 8380 | TCP | web UI |
| Docker socket | `/var/run/docker.sock` | `/var/run/docker.sock` | **ro** | inventory, stats fallback, health, logs, events |
| Host sysfs | `/sys` | `/host/sys` | ro (rbind) | hwmon, DRM, cgroup v2 fast path |
| Unraid state | `/var/local/emhttp` | `/unraid` | ro | array, parity, mover, disks, pools, shares |
| Notifications | `/tmp/notifications` | `/notify` | **rw** | the only rw host mount — Unraid-native alert delivery |
| Update status | `/var/lib/docker/unraid-update-status.json` | `/updates/unraid-update-status.json` | ro | container update-available flags (`GANTRY_UPDATE_STATUS_PATH`'s default) |
| Config | `/mnt/user/appdata/gantry` | `/config` | rw | SQLite DB + settings snapshots |
| Extra params | `--pid=host --cap-add=SYS_PTRACE` | | | host PID table (per-container GPU, host net, mover) + foreign-process fdinfo reads |
| Network | bridge | | | host-side counters come via the PID table, so no host port namespace is burned |

Plus: `<Overview>` copy, `<Support>` (forum thread) and `<Project>` (repo) URLs, `<Icon>` (raw GitHub URL), `<Category>Tools: Network:Management</Category>`, and three documented-optional variables surfaced in the template UI:

- **PSI** — a description-only note (it is a *boot* change, not a container one): add `psi=1` to the syslinux append line to unlock full pressure signals; link `docs/psi.md`, which already exists.
- **Nvidia** — optional `--runtime=nvidia` + `NVIDIA_VISIBLE_DEVICES=all`; absent ⇒ the GPU panel shows its enable hint, never an error.
- **`GANTRY_READ_ONLY`** — off by default, described as the write-path kill switch.

`docs/install.md` carries the same table in prose plus the exact `docker run` equivalent, so the template can be reproduced by hand and reviewed line by line before anyone trusts it with a socket.

**Verification:** validate the XML against a known-good CA template's element set; install it from a local template path on the box in Task 18 and confirm zero edits are needed.

---

### Task 16: Container hardening decision — the honest non-root analysis

**Track:** full (analysis + decision; the code change is conditional on the findings). **Files:** Modify `Dockerfile` and `templates/gantry.xml` **only if** the verification below passes; always create `docs/security.md`.

The carry-in asks for a non-root `USER`. Analysed honestly, **it is not free for this application**, and shipping it blind would silently break the product's headline feature. Three concrete blockers, each with a one-command on-box check (Task 18 runs them):

1. **Docker socket ownership.** On Unraid `/var/run/docker.sock` is `root:root` mode `0660`. A `USER 99:100` (nobody:users) container cannot open it at all — Gantry's core source dies. `USER 99:0` (uid 99, **primary gid 0**) satisfies the group bits and would work. Check: `stat -c '%U %G %a' /var/run/docker.sock`.
2. **`CAP_SYS_PTRACE` does not survive the uid change.** Docker clears the permitted/effective capability sets when a container's process runs as a non-root uid, and Docker exposes no `--cap-ambient`. The only carry-over path is file capabilities on the binary (`setcap cap_sys_ptrace,cap_dac_read_search=ep /gantry`), and `COPY --from` preserves xattrs only on some builders — silently, when it doesn't. The failure mode is not a crash: foreign-process `/proc/<pid>/fdinfo` reads simply stop resolving and **per-container GPU attribution — the differentiator spike S1 was run to prove — quietly degrades to host-only**. A silent loss of the headline feature is strictly worse than running as uid 0 on a single-user LAN appliance.
3. **The notify spool must be writable.** Phase 4 *is* the alert phase; if `/tmp/notifications/unread` is `root:root 0755`, a non-root uid cannot deliver a single notification. Check: `stat -c '%U %G %a' /tmp/notifications /tmp/notifications/unread`.

**Recommendation (the plan's default):** do **not** make non-root the shipped default in v0.1.0. Instead:

- **(a)** Take the larger real reduction the uid change was a proxy for: add `--cap-drop=ALL --cap-add=SYS_PTRACE` to the template's extra params. Today the container gets Docker's full default capability set; dropping it to exactly one is a bigger, verifiable, zero-risk win, and it is compatible with everything above.
- **(b)** Add a boot self-check that logs, and reports through the sources map, which of the four privileged paths actually resolved (socket, `/host/sys`, foreign fdinfo, notify spool) — so any future hardening change that breaks one is loud instead of silent. This is the piece that makes (c) safe to try later.
- **(c)** Record `USER 99:0` as an opt-in documented variant, gated on Task 18's `stat` results plus a **10-minute side-by-side run** diffing the sources map and the GPU attribution table against a root run. Flip the default only if that diff is empty.

`docs/security.md` states the posture plainly for the README's front page (spec §10): read-only socket, no `--privileged`, exactly one capability, one rw mount, no telemetry, no CDN, no auth by default with the reverse-proxy/basic-auth options spelled out, and the honest reason Gantry runs as uid 0.

---

### Task 17: README, docs, and screenshots for public consumption

**Track:** fast. **Files:** Modify `README.md`; Create `docs/alerts.md`; refresh `docs/screenshots/`.

**README** — currently 11 lines and pre-release. First screen becomes: one-sentence positioning, the hero screenshot, the Beszel comparison table from spec §1, install (CA + the `docker run` equivalent), the privileges explanation (spec §10, linking `docs/security.md`), then features, then docs links, then status/licence. No AI attribution anywhere.

**`docs/alerts.md`:** what fires out of the box (the twelve-rule table verbatim), how sustained-for and hysteresis actually behave, how to silence, how to point a webhook at ntfy / Discord / Home Assistant with the exact envelope, and the honest note that Unraid-native delivery requires the `/notify` mount.

**Screenshots:** regenerate all from fake mode at a fixed 1440×900 (and 375×812 for the mobile shot), light **and** dark, including the new Alerts view mid-fire, so the CA listing and README show the alerting feature actually alerting. Keep the existing filenames so no links rot.

---

### Task 18: ⛔ CHECKPOINT — on-box validation, soak, and the pre-release checklist

**Track:** ⛔ gated on Scott. **Files:** Create `docs/superpowers/phase-4-validation.md`.

- [ ] ⛔ Scott's go-ahead, same posture as Phase 2/3: build `gantry:phase4` on the box from the branch, run with the exact template posture, replace the Phase 3 container.
- [ ] **Alert round-trip (spec §11's named pre-release item):** trip a real rule (warm a disk, or temporarily lower `disk-temp-high` to just under the current reading), confirm the notification reaches Unraid's UI *and* whatever agent Scott has configured, then confirm the resolve notice on recovery. This is the one thing no test can prove.
- [ ] **Webhook round-trip** against a real LAN target (ntfy or Home Assistant), including one deliberate failure (stop the target mid-alert) to confirm retry, the ledger row, and the Settings failure text.
- [ ] **Carry-in: parity speed factor.** During the next real parity check, confirm `(mdResyncDb / mdResyncDt) × 1024` matches the speed Unraid's own UI reports (`fixtures.md` discrepancy 5 — derived, never observed nonzero). Record both numbers. Also confirm the new `parity.errors` metric and the enriched `parity.finish` detail against the same run.
- [ ] **Carry-in: nvidia end-to-end.** Still no hardware. Record explicitly as unvalidated in the validation doc and in `docs/alerts.md` — do not let it quietly read as tested.
- [ ] **Carry-in: share case-collision.** `share2` and `Share2` both slug to `share2` through `collect.SlugSegment`, so **two real shares on Scott's own box currently collapse into one `share.share2.used_bytes` series** (`fixtures.md` anonymization note). `hwmon.go` already solved exactly this with a deterministic `_2`/`_3` suffix; `shares.go` never got it. Fix with the same mechanism (suffix by sorted section order so the assignment is stable across restarts), TDD against `shares_real.ini`, and note in the validation doc that pre-fix history for the losing share is unrecoverable.
- [ ] **Carry-in: long soak.** ≥ 24 hours unattended with the alert engine running and a browser tab connected (Phase 3 managed 10h without alerts). Record: footprint (budget ≤ 2% of a core, ≤ 100MB RSS), SSE stability, log-line count, engine tick duration, `alert_instances`/`alert_deliveries` row growth, DB size delta. Watch specifically for the known API-fallback degradation (N sequential stats calls under the 10s deadline can report "failing" on a slow daemon — correct signal, but note if it now also trips an alert).
- [ ] **Hardening checks (Task 16):** run the three `stat` commands, record the actual modes, then the 10-minute `--user 99:0` side-by-side diff of sources + GPU attribution. Decide and record; flip the default only on an empty diff.
- [ ] **CA template dry run:** install `templates/gantry.xml` from a local path, confirm zero edits needed, confirm the update-status mount resolves (`ls -l /var/lib/docker/unraid-update-status.json` first — the path is dockerMan's convention and must be confirmed, not assumed), confirm the notify mount is rw.
- [ ] **Release dry run:** tag `v0.1.0-rc1`, confirm the workflow publishes the rc tags and does **not** move `latest`, pull the published image on the box and run it.
- [ ] Record verdicts + numbers; fix-loop anything found (TDD); commit.

---

## Risks and open questions for Scott

Each has a recommendation; none blocks starting Lane A.

1. **Webhook secret storage.** The header value is stored in plaintext in `settings` inside `/config/gantry.db`, on the user's own array. There is no keystore in a zero-config single-container appliance, and encrypting it with a key stored beside it is theatre. **Recommendation:** store plaintext, but never return it from the API (`header_set: true`), never log it, and say so plainly in `docs/alerts.md`. Also recommend `GANTRY_WEBHOOK_URL`-style env config as the documented path for anyone who would rather the secret never touch the DB at all.

2. **Should "update available" become an alert, or stay informational?** `update_status` is already on every container in the frame and the Maintenance view already filters on it. **Recommendation: keep it informational, no rule.** An update-available flag is true for weeks at a time across a dozen containers on a normal box; as an alert it is a permanently-firing, permanently-silenced row that trains the user to ignore the Alerts view — the exact failure the anti-noise design exists to prevent. If Scott wants it, the right shape is a *weekly digest* notification ("6 containers have updates"), not a per-container alert — a small Phase 5 item, not a Phase 4 rule.

3. **Should `GANTRY_READ_ONLY` freeze config writes too?** Today it is scoped to docker mutations (images/containers). This plan gates webhook-target writes with it (they configure an outbound side-effect capability) but not rule/silence writes (config, following `/api/settings` and `/api/groups`). That is a deliberate but genuine inconsistency. **Recommendation:** ship the asymmetry documented at the route, and treat "READ_ONLY freezes *all* mutation, including settings/groups/rules" as a separate, deliberate breaking change with its own ticket — not something to slip in under an alerting phase.

4. **Webhook body templating (spec §8 asks for it; this plan defers it).** A user-supplied body template means a template language, a sandbox, an escaping story, and a whole class of "my Discord webhook 400s" support load. **Recommendation:** ship the versioned fixed envelope in v0.1.0. If a real consumer needs a different shape, the cheap next step is a small set of *named* presets (ntfy, Discord, Home Assistant) rather than a general template — the same "fixed library of explainable rules" instinct spec §16 already applies to insights.

5. **Notify-spool rate limiting — how hard?** A flapping rule writing files into `/tmp/notifications/unread` fans out to every agent the user has configured (Discord, Pushover, email) and each file must be dismissed in Unraid's UI individually. **Recommendation: yes, and both layers** — a global bucket (10/hour, burst 4, coalescing into one hourly summary) *and* a per-`(rule, entity)` flap guard (4 cycles in an hour → auto 1-hour silence + one explanatory notification). Webhooks stay unthrottled: machine-facing, and their consumers can rate-limit themselves.

6. **Re-notify defaults.** The table sets 24h on `alert`-severity and slow-burn disk rules, off elsewhere. **Recommendation:** ship as tabled and revisit after the 24h soak — if a real firing alert goes a day without a nudge and that felt wrong, raise it; if the soak produces a repeat that felt like nagging, lower it. This is a number that only real use can settle.

## Execution order across lanes

Four worktrees, per the standing parallel-agent split (disjoint files, isolated branches):

**Round 1 — three lanes in parallel, no shared files:**
- **A (engine):** Task 1 → 2 → 3 → 4 → 5, strictly sequential (each builds on the last). This is the long pole; start it first.
- **F (release):** Task 14 → 15 → 16. Touches only `.github/`, `template/`, `Dockerfile`, `docs/` — zero overlap with A, and Task 14 is independently valuable the moment it lands (a tag becomes publishable while the engine is still being built).
- **D-prep:** nothing yet — Task 9 needs 5, 6, 7.

**Round 2 — after Task 4 merges:**
- **B (delivery):** Task 6 → 7. Sequential (7 registers into 6's `Dispatcher`).
- **C (api):** Task 8. Parallel with B — it touches `internal/server/` + `main.go`'s adapter block, while B touches `internal/alert/` + `main.go`'s wiring block. Land C **before** B to keep `main.go` conflicts to one direction; if both are in flight, C rebases onto B.

**Round 3 — after Task 8 merges:**
- **D:** Task 9 (needs 5, 6, 7 for the demo to have anything to demo).
- **E (ui) — strictly sequential on one worktree**, per the fast-track rule: Task 10 → 11 → 12 → 13. These four touch the same handful of files (`Alerts.svelte`, `alerts.ts`, `router.ts`, `api.ts`) and parallelising them would spend more time on conflicts than on UI. One implementer, no per-change review, batch the asks, one adversarial review of the whole lane before merge.

**Round 4 — convergence:**
- Task 17 (README/docs/screenshots) after 13 and 15 — it needs both the finished UI to screenshot and the finished template to document.
- Task 18 last, gated on Scott, on a merge-candidate branch with everything in.

## Phase 4 exit criteria

- Twelve default rules seed on a fresh DB and evaluate without firing on a healthy box; every one is editable and disable-able through the UI, and its numbers drive the display bands.
- A real alert round-tripped through Unraid's own notification system **and** a real webhook on Scott's box, with a real resolve notice — the spec §11 pre-release item, human-verified the way S2 was.
- Alerts view live at `#/alerts`: active, history, silence, rule editor; light + dark; clean at 375px; Playwright green.
- `alert.fired`/`alert.resolved` visible in the Events feed and on container charts, with no parallel history feed.
- Every Phase 3 carry-in in the "Phase 4 pre-release checklist" section closed or explicitly recorded as still-unvalidated with a reason (nvidia will be the latter).
- A `v*` tag publishes multi-tag GHCR images through a workflow that re-ran the full gate first; `latest` moves only on a non-prerelease tag.
- `templates/gantry.xml` installs with zero edits on the real box, notify spool rw, update-status mount resolving.
- ≥ 24h soak inside budget (≤ 2% core, ≤ 100MB RSS) with the engine running and a client connected.
- README, `docs/security.md`, `docs/alerts.md`, `docs/install.md`, and refreshed screenshots ready for a CA listing and a forum support thread.

**Next:** Phase 5 — the §16 cross-container insights engine, reusing this phase's instance/dispatch machinery for `insight.*` findings, plus the backlog's container interaction map.

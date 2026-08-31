# Gantry Phase 5: Cross-Container Impact Insights — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gantry stops showing you numbers and starts telling you *who is hurting whom*. A rule-based, explainable engine correlates a victim's measured distress with a culprit's dominant share of the same contended resource, and says so in one sentence with the numbers attached: "qbittorrent is driving 78% of disk3's IO while the device sits at 98% utilisation — jellyfin and sonarr are reading from the same disk." Findings surface on a dedicated Insights view, as an impact panel on Container detail, as a callout on Overview, and — the flagship — as a **container interaction map**: a live picture of who is leaning on what.

This is Scott's founding ask, recorded verbatim in the design sessions: *"I would be cool to have something that can tell you if one container is impacting the performance of another container in a specific way, or impacting the array"* and *"Surface information about how a container is impacting the overall system. eg. Container x has high storage io which is causing cpu load."* The second sentence is literally rule `io-driven-cpu-load` below.

**Architecture:** A new `insight.Engine` runs on its own 60s ticker beside `alert.Engine`, reading the live ring through the same narrow injected closures the alert engine already uses (`Live.MatchSince`, plus one new `Live.MatchPrefixSince` for the per-device series). Rules are a **fixed, compiled-in library** — not user-authored — because an insight is a causal claim and a user-editable causal claim is a footgun; only thresholds and enable/disable are exposed. Insight instances live in SQLite (`004_insights.sql`) with a denormalised evidence bundle, and every transition also appends `insight.detected` / `insight.resolved` to the **existing** events table, so the Events feed and chart markers come free — the same split Phase 4 chose for alerts. The frame gains an `insights` block so every surface is live without polling — the whole `SnapshotDTO` is re-marshalled and broadcast every 2s, so a new block rides the existing stream with no SSE change at all.

**Insights are not alerts.** They never touch the notify spool or webhooks by default (see Global Constraints). Alerts tell you something is broken; insights tell you why it's slow.

**Tech Stack:** Backend: stdlib only (no new Go deps). Frontend: **no new deps** — the interaction map is hand-rolled SVG in the existing token system, the same way `BaySchematic.svelte` already draws the disk bays. CI unchanged.

**Spec:** `docs/superpowers/specs/2026-08-25-gantry-design.md` §16 (cross-container impact insights), with §4.1/§4.2 for the collectors and §8 for the machinery being reused. Backlog: `docs/superpowers/backlog.md` — "Container interaction map" (the flagship view) and "Container→system impact surfacing". Spike record: `docs/superpowers/spikes.md` (S3 + the PSI two-tier decision). User-facing PSI doc: `docs/psi.md`.

## Phase context

Phase 2 shipped every collector §16 names as an enabler, and this plan's task specs were written against the code, not the spec — the exact series that exist today are inventoried in Task 0's table below. Phase 4 shipped the machinery this phase reuses wholesale: `alert_instances` and its partial unique index, `Live.MatchSince`, the sustained-for/clear-for verdict model, the flap guard, `store.AppendEvent`, the frame-block pattern, `eventMarkers`/`eventHref`, and the `deriveOverviewStatus` anomaly system that a new source plugs into.

Two of the three backlog items are already partly discharged: the **per-container storage panel** shipped (`internal/server/api_storage.go` resolves `Meta.Mounts` + `unraid.ResolveStoragePath` + the live `live:io.*` rates; `web/src/lib/containerStorage.ts` shapes it). That means the ContainerDetail impact panel has a home and a data path already built. The **interaction map** and the **insight engine** are what remain, and they are the same feature seen from two angles.

The deployed reality that shapes every decision here: **Scott has not enabled `psi=1`**. His box is stock Unraid 7.3.2 with `append initrd=/bzroot` (spikes.md). So tier 1 has to be genuinely useful on its own — not a degraded teaser — and this plan spends its single largest backend task (Task 1) making that true.

## Deviations from the spec (code wins, recorded)

| Spec §16 / backlog says | Code/reality | Ruling |
|---|---|---|
| Tier-1 victim signals include "io.stat latency/queue proxies" | cgroup `io.stat` has **no latency and no queue data** — it is byte/IO counters only. `internal/collect/docker/cgroupv2.go` emits `live:io.<dev>.read_bps`/`.write_bps` and nothing else. The real per-device latency and saturation numbers live in `/proc/diskstats` fields 6/10/11/12/13, and `internal/collect/host/diskstats.go` parses **only** fields 5 and 9 (sectors read/written) | Add device saturation + latency from diskstats (Task 1). Tier 1's victim evidence becomes "the *device* is saturated and slow", measured, not inferred. This is the single change that makes tier 1 stand alone. |
| Tier-1 CPU victim signal is "`nr_throttled` rising" | `readCPUStat` (`cgroupv2.go:161`) parses `nr_throttled` into `cgStats.NrThrottled` but **never emits it** — only `throttled_usec` reaches a series, as the derived `container/<name>/cpu.throttled_pct`. And throttling **only ever happens when `cpu.max` is set**; on a stock Unraid box most containers have no CPU limit, so the signal is structurally zero either way | `cpu.throttled_pct` is the better signal of the two (a rate, not a raw counter) and already exists — use it, and do **not** add `nr_throttled` just to match the spec's wording. Gate the rule on `cpu.alloc_cores > 0` and label it honestly in the UI. This limitation is the headline argument in the `psi=1` upgrade copy (Task 16): PSI's `cpu.pressure` measures starvation for *unlimited* containers too. |
| "Findings are stored as events (`insight.*` kinds)" | Phase 4 established the split: a table holds the state machine, the events table holds the human-readable trail, and there is no parallel history feed | Both, mirroring alerts exactly: `insight_instances` + `insight.detected`/`insight.resolved` events. The spec's sentence is satisfied; the state machine needs a row the events table can't give it. |
| "High-severity insights can optionally notify through the §8 alert channels" | — | Opt-in per rule, **default off for all seven**, and structurally forbidden for `likely`-confidence findings (Task 8). Paging on a correlational claim is how you teach someone to ignore the pager. |
| Backlog: map as "a force/graph layout … time-scrubbable" | — | **Deterministic three-column layered SVG**, not force-directed (Task 14 states the four reasons). Force layouts jitter on every 2s tick and destroy the positional memory that makes a live map readable. |
| §16 rules are "a fixed library" but §8's rules are user-editable | — | Fixed library, thresholds tunable, no user-authored insight rules in v1. A user-authored causal claim has no evidence contract. |
| Backlog: "per-container storage panel" as Phase 5 work | Already shipped — `api_storage.go`, `containerStorage.ts`, `StorageDeviceRow.svelte` | Retired from this phase. The impact panel (Task 12) sits beside it and reuses its `StorageRefDTO` plumbing. |

## Global Constraints

- **No new dependencies, Go or npm.** The interaction map is hand-rolled SVG using `web/src/lib/tokens.css` custom properties, following `BaySchematic.svelte`'s precedent. A layout library would be a new dep for a graph that is never more than ~40 nodes and is inherently tripartite.
- **Insights never page by default.** No notify-spool write, no webhook, for any seeded rule. They write `insight.*` events at severity `info`, which puts them in the Events feed and on chart markers but nowhere near Unraid's notification agents. The one sanctioned bridge is the reverse direction: an active insight **annotates** a firing alert (Task 13), so the alert says what broke and the insight says why.
- **Both sides or nothing.** No rule may fire on victim evidence alone or culprit share alone. `EvaluateRule` returns a `Finding` only when it can populate both `Victim` and `Culprit` evidence structs. This is enforced by the type — `Finding` has no zero-valued path to construction — not by discipline.
- **Two confidence tiers, two vocabularies.** `ConfidenceLikely` (tier-1 proxy evidence) renders "is likely slowing / is loading"; `ConfidenceConfirmed` (a measured PSI stall on the victim, or a hard event like an OOM kill) renders "is starving / is causing". The verb is a pure function of confidence, computed in one place (`insight.Statement`), never hand-written per rule. A `likely` finding that says "causing" is a bug with a test.
- **Series identity unchanged; no renames.** New metrics only, and all on existing entities: `host/diskio.<dev>.util_pct`, `.await_ms`, `.queue_avg`, `.inflight`, and `host|container/psi.<res>.full_pct`. Note the existing PSI metric segment for memory is **`mem`, not `memory`** (`pressure.go:38-42` maps the `memory` file to segment `mem`) — the live keys are `psi.cpu.some_pct`, `psi.io.some_pct`, `psi.mem.some_pct`. Do not invent `psi.memory.*`. Per-container per-device IO stays `live:`-prefixed and live-ring-only (see Open question 4).
- **Engine cadence 60s** (spec §16), evidence window 120s, sustained-for 90s within it. One `Live` lock pass per distinct `(kind, metric)` and one prefix pass for `live:io.*`, shared across all enabled rules — not a pass per rule.
- **Mind the ring.** `DefaultRingCap = 450` samples (`store/store.go:11`) is shared by *every* series regardless of its collector's tick, so 450 samples is 15 min at the docker/host 2s cadence but ~112 min at the unraid collector's 15s cadence. Every window in this plan fits (the longest, `disk-spinup-churn`'s 60 min, is 240 unraid samples), but any new window must be checked against its collector's cadence the way `alert.maxProvableWindowSeconds` already does.
- **Test conventions are the repo's, not new ones.** `github.com/stretchr/testify/require` (never `assert`), one descriptive `Test*` per scenario over table tests where the scenario has a name worth reading, `_test.go` beside its source in the same package, raw fixture text under `testdata/`. Frontend vitest runs in the **`node`** environment — there is no jsdom, so only pure `lib/*.ts` modules are unit-testable and all component behaviour is asserted through Playwright.
- **Quiet by default is a requirement, not an aspiration.** Target: **≤ 3 active insights on a healthy box on a normal day.** Task 17's soak measures this, and a rule that exceeds it gets its thresholds raised before release, not after.
- **Fake mode demos everything.** `GANTRY_FAKE_DATA=1` must produce at least one firing insight per rule family, one PSI-confirmed and one tier-1-likely variant of the same rule, a resolve, and a populated map within ~4 minutes of boot (Task 10). No feature ships without a fake-mode path.
- **Degradation, never errors.** No `/proc/pressure` ⇒ PSI-tier rules simply never evaluate and the UI says what enabling it would add — never an error, never a broken panel. No GPU ⇒ `gpu-engine-contention` never evaluates. No `/unraid` ⇒ the array rules never evaluate. Each is correct behaviour and each is tested.
- TDD for every Go change. `go test ./... -race` + `make lint` pristine per commit. `npm test` + Playwright green per frontend commit. **No AI attribution in commits.** ⛔-marked tasks are gated on Scott.

## Task 0: The signal inventory (reference, not a task)

Verified against the code on 2026-08-30. **Everything in the "exists" column needs no work** — this table is what Tasks 5 and 6 evaluate against, and task specs must use these exact keys.

**Exists — victim-side evidence**

| Series | Source | Use |
|---|---|---|
| `container/<name>/psi.{cpu,io,mem}.some_pct` | `collect/pressure/pressure.go:96` | **PSI tier.** The measured stall. Ground truth for every victim claim. Segment is `mem`, not `memory`. |
| `host/psi.{cpu,io,mem}.some_pct` | `collect/pressure/pressure.go:87` | **PSI tier.** Host-wide stall. |
| `host/cpu.iowait_pct` | `collect/host/host.go:116` | Tier 1. The "IO is loading the CPU" victim signal. |
| `container/<name>/cpu.throttled_pct` | `collect/docker/cgroupv2.go` | Tier 1, **only for containers with a CPU limit**. |
| `host/cpu.total`, `host/mem.used_pct` | `collect/host/host.go:113,153` | Tier 1 host distress. |
| `unraid/array/parity.speed_bps`, `.progress_pct` | `collect/unraid/var.go` | Tier 1, array victim (speed vs baseline). |
| `disk/<slot>/spun_up`, `rotational` | `collect/unraid/disks.go` | Tier 1, array victim (spin-up churn). |
| `gpu/<entity>/engine.<eng>.busy_pct` | `collect/gpu/` | Tier 1, GPU engine saturation. |
| `container.oom`, `container.health` events | docker collector | Tier 1, hard events. Upgrade confidence to `confirmed`. |

**Exists — culprit-side attribution**

| Series | Source | Use |
|---|---|---|
| `container/<name>/live:io.<dev>.read_bps`, `.write_bps` | `cgroupv2.go:641,648` | **The core attribution signal.** Per-container per-device. Live-ring only. |
| `container/<name>/cpu.pct` | `cgroupv2.go` | Share of total host cores. |
| `container/<name>/mem.bytes`, `mem.pct` | `cgroupv2.go` | Memory footprint. |
| `container/<name>/gpu.<eng>.busy_pct` | `collect/gpu/` | Per-container GPU engine share. |
| `unraid/array/mover.running` | `collect/unraid/mover.go:18` | Context enricher (see Open question 2). |
| `host/diskio.<dev>.read_bps`, `.write_bps` | `collect/host/host.go:233,236` | Device totals — the denominator for IO share. |

**Gaps — each is a task**

| Gap | Why it matters | Task |
|---|---|---|
| Device **saturation** (`util_pct`), **latency** (`await_ms`), **queue depth** (`queue_avg`), **in-flight** | `/proc/diskstats` fields 6/10/11/12/13 are unparsed — `parseDiskstats` reads only fields 5 and 9 and its `len(fields) < 10` guard never even looks further. Without these, tier 1 has **no measured victim evidence for disk contention at all**, only "someone is doing a lot of IO", which is not a finding. There is also no per-disk busy% anywhere else to borrow: `disks.ini` carries no such field | **1** |
| PSI `full` variant | `parseSomeLine` (`pressure.go:126-143`) returns early unless `fields[0] == "some"` and reads only `avg10`, so `full`, `avg60`, `avg300` and `total` are all unavailable. `full` (every task stalled) is the strictly stronger "the box was stuck" claim | **2** |
| `Live.MatchPrefixSince` | The engine needs every container's `live:io.<dev>.*` **over a window** in one lock pass. `MatchSince` matches an exact metric; `LatestByMetricPrefix` (used by `main.go:1030`'s storage-panel path) matches a prefix but returns only the latest sample for one entity. Neither shape fits | **3** |
| Device → array-slot topology (`md` → members + parity) | `buildDeviceMap` (`diskstats.go:51-68`) already resolves `maj:min → name` and is exposed as `host.Collector.DeviceName` (`host.go:69`), wired into the docker collector as a plain func field (`docker.go:59`) — copy that wiring. What is missing is `name → unraid slot` (join through `DiskMeta.Device`) plus **which devices are md members and which is parity** — without it the engine will name the parity disk as a contended resource in its own right, which is wrong (Open question 1) | **3** |

## Task summary

| # | Task | Track | Lane | Depends on |
|---|---|---|---|---|
| 1 | Device saturation + latency series from `/proc/diskstats` | full | A signals | — |
| 2 | PSI `full` + pressure tier reporting | full | A signals | — |
| 3 | `Live.MatchPrefixSince` + device→slot topology resolver | full | A signals | — |
| 4 | Insight schema + store CRUD (`004_insights.sql`) | full | B engine | — |
| 5 | Evidence + attribution primitives (pure) | full | B engine | 1, 3 |
| 6 | The seven-rule library (pure evaluators) | full | B engine | 5 |
| 7 | Lifecycle state machine + engine loop + anti-noise | full | B engine | 4, 6 |
| 8 | Events integration + opt-in notify | full | B engine | 7 |
| 9 | `/api/insights/*` + frame `insights` block | full | C api | 4, 7 |
| 10 | Fake-mode insight scenarios | full | D fake | 6, 7 |
| 11 | Insights view — active, history, evidence drawer | fast | E ui | 9 |
| 12 | ContainerDetail impact panel | fast | E ui | 9 |
| 13 | Overview callout + alert annotation | fast | E ui | 11 |
| 14 | **Container interaction map** (flagship, a mode inside Insights) | fast | E ui | 11 |
| 15 | Playwright + responsive/a11y pass | fast | E ui | 11–14 |
| 16 | PSI upgrade copy + `docs/insights.md` | fast | F docs | 2, 11 |
| 17 | ⛔ On-box validation + quiet-by-default soak | ⛔ | F docs | all |

---

### Task 1: Device saturation + latency series from `/proc/diskstats`

**Track:** full (adversarial review). **Files:** Modify `internal/collect/host/diskstats.go`, `internal/collect/host/diskstats_test.go`, `internal/collect/host/host.go` (+test), `internal/collect/host/testdata/` (add a two-sample diskstats fixture pair).

This is the highest-value task in the phase: it is what makes tier 1 a real engine rather than a placeholder waiting for a reboot.

**Today** `parseDiskstats` returns `diskCounters{readSectors, writeSectors}` from fields 5 and 9, guarded by `len(fields) < 10`. The kernel gives fourteen. Extend `diskCounters` with the five that matter (kernel 1-indexed → Go 0-indexed):

| Kernel field | Go index | Meaning |
|---|---|---|
| 4 | 3 | reads completed |
| 7 | 6 | ms spent reading |
| 8 | 7 | writes completed |
| 11 | 10 | ms spent writing |
| 12 | 11 | I/Os currently in progress (instantaneous gauge) |
| 13 | 12 | ms spent doing I/O (`io_ticks`) |
| 14 | 13 | weighted ms spent doing I/O (`time_in_queue`) |

**New series** (`host` kind, alongside the existing `diskio.<dev>.read_bps`/`.write_bps` in `host.go:233`):

```go
// util_pct: the share of wall time the device had at least one request in
// flight -- iostat's %util. Delta of io_ticks over the tick interval.
// Clamped to 100: io_ticks is millisecond-granular and a short tick can
// round above the interval.
"diskio." + dev + ".util_pct"
// await_ms: mean time a request spent in the queue plus service, i.e.
// iostat's await. (dms_read + dms_write) / (dreads + dwrites), and NOT
// recorded at all when the completed-IO delta is zero -- a device that
// served no IO has no latency, and emitting 0 would read as "instant".
"diskio." + dev + ".await_ms"
// queue_avg: mean queue depth over the interval -- iostat's aqu-sz.
// Delta of time_in_queue over the interval.
"diskio." + dev + ".queue_avg"
// inflight: instantaneous requests in flight. A gauge, not a delta.
"diskio." + dev + ".inflight"
```

**Correctness notes for the implementer:**

- All four are **derived from deltas against the previous tick**, following exactly the `RateTracker` pattern `host.go` already uses for `read_bps`/`write_bps`. First tick emits nothing.
- Counters are 32-bit-wrappable on some kernels. Reuse whatever wrap handling `RateTracker` already does; if it has none, a negative delta means "skip this sample", never a negative rate.
- `await_ms` is **omitted, not zeroed**, when `dreads+dwrites == 0`. A missing sample is honest; a zero is a lie the rules would read as "fast".
- `wholeDeviceRe` (`^(sd[a-z]+|nvme\d+n\d+|md\d+)$`) is unchanged and already includes `md*`, so the array's md devices get util/await too — which is what the parity rule needs.
- Raise the `len(fields) < 10` guard to `< 14`, but **keep parsing the row's throughput if it has ≥10 and fewer than 14 fields** (ancient kernels) rather than dropping the device entirely — degradation, never errors.

**Tests:** golden two-sample fixture yields exact expected util/await/queue for a hand-computed case; `util_pct` clamps at 100 when `io_ticks` delta exceeds the interval; `await_ms` is absent (not zero) for a device with no completed IOs across the pair; a counter that goes backwards produces no sample rather than a negative one; a 12-field row still yields `read_bps`/`write_bps` and no latency series; `md1` gets the full set.

---

### Task 2: PSI `full` + pressure tier reporting

**Track:** full. **Files:** Modify `internal/collect/pressure/pressure.go` (+test), `internal/collect/pressure/testdata/`.

`pressure.go:87,96` records `psi.<res>.some_pct` for host and container (segments `cpu`, `io`, **`mem`** — the resources table at `pressure.go:38-42` maps the `memory` file to metric segment `mem`; keep that mapping, do not "fix" it into `memory` and break every existing series). `/proc/pressure/*` and each cgroup's `*.pressure` also carry a `full` line (every non-idle task stalled). Record it: `host|container/psi.<res>.full_pct`, same `avg10` source, same shape.

This means generalising `parseSomeLine` (`pressure.go:126-143`), which today hard-returns unless `fields[0] == "some"`. Rename it and take the wanted line as a parameter rather than adding a second near-copy. `avg60`/`avg300` stay unread — the engine samples `avg10` every tick across a 120s window, which is a better-resolved version of the same information.

`full` is what separates "something was waiting" from "nothing could run" and it is the difference between a `warning` and a `critical` insight severity in Task 6. Note the kernel quirk to encode in a comment: `/proc/pressure/cpu` has **no `full` line** at the host level (by definition — if any task is runnable the CPU isn't fully stalled). Absent line ⇒ no series, not a zero.

Also surface which tier is live, for Task 16's copy and Task 9's API: the pressure collector's source status already flips ok/hint. Add an exported `func (c *Collector) Tier() string` returning `"psi"` or `"proxy"` so the server reports it without string-matching a banner.

**Tests:** `full` parsed for io/memory and absent for host cpu; a cgroup `.pressure` file with only a `some` line records one series not two; `Tier()` returns `"proxy"` when `/proc/pressure/io` is missing and `"psi"` when the fixture is present.

---

### Task 3: `Live.MatchPrefixSince` + device→slot topology resolver

**Track:** full. **Files:** Modify `internal/store/live.go` (+test); Create `internal/insight/topology.go`, `internal/insight/topology_test.go`.

**`Live.MatchPrefixSince`** — mirrors Phase 4's `MatchSince` exactly, including its one-lock-pass reasoning, but matches a metric *prefix* and returns the matched metric alongside the entity, because the engine needs "every container's IO on every device" in one pass. Its two existing siblings in `live.go` are the reference: `MatchSince` (exact metric, all entities, windowed) and `LatestByMetricPrefix` (prefix, one entity, latest only). This is the missing third combination:

```go
// MatchPrefixSince returns, for every series of this kind whose metric
// begins with `prefix`, the samples at or after `since`, keyed by entity
// then by full metric name. ONE read-lock pass, same reasoning
// MatchSince documents: the insight engine asks for "all of live:io.*"
// once per tick, not once per (container, device) pair -- the N+1 shape
// would multiply by container count times device count every tick.
func (l *Live) MatchPrefixSince(kind, prefix string, since int64) map[string]map[string][]Sample
```

**The topology resolver** is the correctness heart of per-device attribution on Unraid, and getting it wrong produces confidently wrong insights. `buildDeviceMap` (`collect/host/diskstats.go:51-68`) already gives `maj:min → name`, exposed as `host.Collector.DeviceName` and injected into the docker collector as a func field (`docker.go:59`) — the topology resolver takes the same injected-closure shape rather than importing a collector. The `name → slot` half joins through the Unraid collector's already-parsed `DiskMeta.Device`:

```go
type Device struct {
    Name    string // "sdb", "nvme0n1", "md1"
    Slot    string // "disk3", "cache", "parity" -- "" for non-array devices
    Role    Role   // RoleData | RoleParity | RolePool | RoleFlash | RoleUnknown
    Rotational bool
}

// Topology answers the three questions attribution needs.
type Topology struct{ /* built per tick from the unraid + host collectors */ }

func (t *Topology) Resolve(majMin string) (Device, bool)
// Contended reports whether this device may be named as a contended
// resource in its own right. FALSE for parity devices: on Unraid every
// array write drives the parity disk as a CONSEQUENCE of the data write,
// so "container X is driving 100% of parity IO" is a restatement of the
// data write, not an independent finding. Parity load is folded into the
// array-write insight instead. See Open question 1.
func (t *Topology) Contended(d Device) bool
// Canonical maps an array member's raw device to the md device that
// represents the array-level write, so a single logical write is
// attributed once rather than counted on both sdb and md1.
func (t *Topology) Canonical(d Device) Device
```

**Tests:** `Resolve` maps a known maj:min through to its slot; `Contended` is false for parity and true for data/pool/flash; `Canonical` collapses an array member to its md device and leaves a pool device alone; an unknown maj:min resolves to `RoleUnknown` with `Contended` true (a device we don't understand is still a real device — degrade to naming it by its kernel name, don't drop the finding); `MatchPrefixSince` returns only prefix matches, respects `since`, is non-nil-empty for an unknown prefix, and takes exactly one lock (assert with the concurrent-writer deadlock probe `MatchSince`'s test already uses).

---

### Task 4: Insight schema + store CRUD

**Track:** full. **Files:** Create `internal/store/migrations/004_insights.sql`, `internal/store/insights.go`, `internal/store/insights_test.go`; Modify `internal/store/maintain.go` (+test).

Migration number is **004** — `migrations/` currently holds `001_core.sql`, `002_ts_indexes.sql`, `003_alerts.sql`. `migrate.go:98-145` applies files in numeric-prefix order from a `//go:embed migrations/*.sql`, each in one transaction, tracked in `schema_migrations`; adding the file is the whole wiring.

```sql
CREATE TABLE insight_instances (
    id             INTEGER PRIMARY KEY,
    rule_id        TEXT NOT NULL,             -- 'disk-io-contention' etc, from the compiled library
    -- The dedup identity: one active finding per (rule, victim, culprit, resource).
    victim_kind    TEXT NOT NULL,             -- container|host|array|disk|gpu
    victim         TEXT NOT NULL,             -- container name, slot, engine, '' for host
    culprit        TEXT NOT NULL,             -- container name; '' when the culprit is a set
    culprits       TEXT NOT NULL DEFAULT '',  -- comma-separated when >1 (the shared-culprit shape)
    resource       TEXT NOT NULL,             -- 'disk3' | 'cpu' | 'memory' | 'gpu:video'
    state          TEXT NOT NULL,             -- pending|active|resolved
    severity       TEXT NOT NULL,             -- info|warning|alert  (matches the alert vocabulary)
    confidence     TEXT NOT NULL,             -- likely|confirmed
    tier           TEXT NOT NULL,             -- proxy|psi  (which evidence actually fired it)
    statement      TEXT NOT NULL,             -- the rendered one-sentence finding, frozen at fire time
    evidence       TEXT NOT NULL,             -- JSON bundle: series ids, window, the actual numbers
    started_at     INTEGER NOT NULL,
    fired_at       INTEGER NOT NULL DEFAULT 0,
    resolved_at    INTEGER NOT NULL DEFAULT 0,
    resolve_reason TEXT NOT NULL DEFAULT '',  -- cleared|no-data|restart|rule-disabled|dismissed
    notified_at    INTEGER NOT NULL DEFAULT 0
);
-- One ACTIVE finding per identity tuple, enforced by the DB (the Phase 4
-- alert_instances precedent), not by engine bookkeeping.
CREATE UNIQUE INDEX idx_insight_active ON insight_instances
    (rule_id, victim, culprit, resource) WHERE resolved_at = 0;
CREATE INDEX idx_insight_started ON insight_instances (started_at);

-- Per-rule tuning + enablement. Thresholds only; rule SHAPE is compiled in.
CREATE TABLE insight_rule_config (
    rule_id     TEXT PRIMARY KEY,
    enabled     INTEGER NOT NULL DEFAULT 1,
    notify      INTEGER NOT NULL DEFAULT 0,   -- opt-in; see Task 8
    overrides   TEXT NOT NULL DEFAULT '',     -- JSON: threshold name -> value
    updated_at  INTEGER NOT NULL
);

-- "This wasn't useful" -- feedback without ML (Open question 3).
CREATE TABLE insight_dismissals (
    id         INTEGER PRIMARY KEY,
    rule_id    TEXT NOT NULL,
    victim     TEXT NOT NULL DEFAULT '',
    culprit    TEXT NOT NULL DEFAULT '',
    resource   TEXT NOT NULL DEFAULT '',
    until      INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
```

**Interfaces** (`internal/store/insights.go` — same write-on-`s.db`/read-on-`s.readDB` split as `events.go` and `alerts.go`):

```go
type InsightInstance struct{ /* one field per column; Evidence stays a string at this layer */ }
type InsightRuleConfig struct{ RuleID string; Enabled, Notify bool; Overrides string; UpdatedAt int64 }
type InsightDismissal struct{ /* ditto */ }

func (s *Store) ActiveInsights(ctx context.Context) ([]InsightInstance, error)
func (s *Store) UpsertInsight(i InsightInstance) (int64, error)
func (s *Store) ResolveInsight(id, at int64, reason string) error
func (s *Store) InsightHistory(ctx context.Context, from, to int64, limit int) ([]InsightInstance, error)
func (s *Store) InsightRuleConfigs(ctx context.Context) ([]InsightRuleConfig, error)
func (s *Store) UpsertInsightRuleConfig(c InsightRuleConfig) error
// SeedInsightRuleConfigs is INSERT OR IGNORE, never UPDATE -- the exact
// SeedAlertRules contract (store/alert_defaults.go:202-222). This is how
// a rule added in a later version gets a config row on upgrade without
// ever stomping a threshold the user has tuned.
func (s *Store) SeedInsightRuleConfigs(defaults []InsightRuleConfig) error
func (s *Store) InsightDismissals(ctx context.Context, now int64) ([]InsightDismissal, error)
func (s *Store) AddInsightDismissal(d InsightDismissal) (int64, error)
// StaleActiveInsights marks every still-active row resolved with reason
// 'restart' at boot. The live ring is empty after a restart, so no rule
// can be evaluated for the first window and a carried-over "active"
// finding would be asserting something we cannot currently see. See
// Open question 5.
func (s *Store) StaleActiveInsights(at int64) error
```

`Maintain` gains: delete resolved instances older than R2 (30d, the same knob alerts use), delete expired dismissals.

**Tests:** migration applies and the partial unique index exists; a second active row for the same `(rule, victim, culprit, resource)` is rejected while the first is unresolved and accepted once resolved; a *different* resource for the same victim/culprit pair is accepted concurrently (two devices can genuinely both be contended); `StaleActiveInsights` resolves actives with reason `restart` and leaves already-resolved rows untouched; `Maintain` prunes on the documented boundaries; `InsightHistory` orders newest-first and respects the limit.

---

### Task 5: Evidence + attribution primitives (pure)

**Track:** full. **Files:** Create `internal/insight/evidence.go`, `internal/insight/evidence_test.go`, `internal/insight/window.go`, `internal/insight/window_test.go`.

Pure functions: no clock, no store, no I/O. Everything below takes samples and returns numbers.

```go
// Window is the evidence window every rule shares: 120s of samples, of
// which the signal must hold for SustainFor (90s) to count as sustained.
type Window struct{ From, To int64; Samples []store.Sample }

// Sustained reports whether EVERY sample in the trailing `for` seconds
// crosses the threshold: sustained means sustained, not "on average".
// Returns Insufficient when the ring cannot cover the window, which is
// NOT a breach and NOT a clear.
//
// This re-implements alert.EvaluateThreshold's semantics rather than
// calling it, because that function's signature is bound to a
// store.AlertRule and an insight rule is not one. The verdict vocabulary
// is deliberately identical (see internal/alert/eval.go:5-36) and a
// shared-edge-matrix test asserts the two agree case for case -- one
// lifecycle model in the codebase, two entry points into it.
func Sustained(w Window, op Op, threshold float64, forSecs int64, oldest int64) (Verdict, float64)

// Share computes a culprit's fraction of a total across the window, using
// MEAN over the window rather than the latest sample: a single 2s tick is
// noise, and the claim being made is about the window.
//   parts:  entity -> that entity's samples on this resource
//   Returns shares sorted descending, and the window-mean total.
func Share(parts map[string][]store.Sample) (ranked []EntityShare, total float64)

// Dominant applies the dominance rule: the top entity if it clears
// `floor`, else the smallest leading SET that together clears `floor`
// (capped at maxN), else nothing. This is what stops the engine either
// firing N single-culprit findings or staying silent when two containers
// are jointly hammering a disk. See Open question 2.
func Dominant(ranked []EntityShare, floor float64, maxN int) (Culprits, bool)

// Baseline is the parity rule's victim evidence: the median of the
// historical values, or -- for a first-ever check with no history -- the
// median of the run's own opening samples, per spec §16.
func Baseline(history []float64, opening []float64) (float64, bool)
```

`EntityShare{Entity string; Value, Fraction float64}`; `Culprits{Names []string; Fraction float64; Shared bool}`.

**Tests:** `Share` is mean-based (a one-tick spike does not flip the ranking); ties broken by name so output is deterministic; `Dominant` returns a single culprit at 62% with floor 60; returns a shared pair at 44%+31% with floor 60 and `Shared=true`; returns nothing for 30/28/22 with maxN 2; `Sustained` inherits the full Phase 4 edge matrix **and is asserted case-for-case against `alert.EvaluateThreshold` on the shared cases** (one dip resets; exactly-on-threshold does not breach; uncovered window is Insufficient; empty window is Insufficient, never a clear); `Baseline` falls back to opening samples when history is empty and reports `false` when it has neither.

---

### Task 6: The seven-rule library

**Track:** full. **Files:** Create `internal/insight/rules.go`, `internal/insight/rules_test.go`, `internal/insight/statement.go`, `internal/insight/statement_test.go`.

A rule is data plus one pure evaluator. The library is compiled in and closed.

```go
type Rule struct {
    ID          string
    Title       string   // "Disk IO contention"
    Tier        Tier     // TierProxy = works on stock; TierPSI = needs psi=1
    PSIUpgrade  bool     // true if a PSI signal upgrades this rule's confidence
    Thresholds  map[string]float64 // named, overridable via insight_rule_config
    Eval        func(In) []Finding
}
```

`In` carries the tick's already-gathered inputs (the `MatchSince`/`MatchPrefixSince` results, the topology, recent events, the enabled tier) so no evaluator does I/O.

**The seven rules.** Victim evidence and culprit attribution are both required; the tier-1 row is what fires on Scott's box **today**, the PSI row is the upgrade.

| id | Victim (tier 1) | Victim (PSI upgrade) | Culprit attribution | Default severity |
|---|---|---|---|---|
| `disk-io-contention` | `host/diskio.<dev>.util_pct` ≥ 90 sustained **and** `await_ms` ≥ 2× the device's own rolling median, **and** ≥1 container other than the culprit has IO on the device | victim container's `psi.io.some_pct` ≥ 20 → names the actual victim and its stall % | culprit ≥ 60% of the device's `live:io.<dev>` bytes | warning |
| `io-driven-cpu-load` | `host/cpu.iowait_pct` ≥ 15 sustained | `host/psi.io.some_pct` ≥ 20 (`full_pct` ≥ 10 → severity `alert`) | culprit ≥ 50% of total host disk IO | warning |
| `cpu-starvation` | victim's `cpu.throttled_pct` ≥ 5 sustained **and** `cpu.alloc_cores > 0` | victim's `psi.cpu.some_pct` ≥ 20 — **no CPU limit required**, which is the whole argument for `psi=1` | culprit `cpu.pct` ≥ 40 **and** `host/cpu.total` ≥ 85 | warning |
| `parity-slowdown` | `parity.speed_bps` ≤ 75% of `Baseline` sustained 120s | — (tier-1 native) | culprit ≥ 25% of IO to array data devices in the window | warning |
| `disk-spinup-churn` | `disk/<slot>/spun_up` 0→1 transitions ≥ 3 within 60m on a `rotational=1` disk | — (tier-1 native) | culprit is the dominant `live:io.<dev>` source within ±60s of each transition | info |
| `gpu-engine-contention` | `gpu/<ent>/engine.<eng>.busy_pct` ≥ 90 sustained | — (tier-1 native) | ≥2 containers each ≥ 10% on `gpu.<eng>.busy_pct` | info |
| `memory-squeeze` | `host/mem.used_pct` ≥ 92 sustained **or** a `container.oom` event in the window (OOM ⇒ `confirmed` + severity `alert`) | victim's `psi.mem.some_pct` ≥ 10 | culprit `mem.pct` ≥ 30 | warning |

Every number above is a **named entry in `Rule.Thresholds`**, not a literal in the evaluator, so Task 9 can expose them and Task 17's soak can retune without a code change.

**`statement.go`** owns the single mapping from confidence to verb, so no rule hand-writes causal language:

```go
// Verb is the ONLY place causal language is chosen. ConfidenceLikely
// never yields a causal verb; ConfidenceConfirmed always does. A rule
// that wants to say "causing" must earn it by supplying a measured
// victim stall or a hard event -- there is no third path.
func Verb(c Confidence, shape Shape) string
func Statement(f Finding) string
```

Rendered examples the tests pin verbatim:

- likely / `disk-io-contention`: *"qbittorrent is likely slowing other containers on disk3 — it's driving 78% of the disk's IO while the device sits at 98% utilisation and 42ms average latency. jellyfin and sonarr are also reading from disk3."*
- confirmed / `disk-io-contention`: *"qbittorrent is starving jellyfin on disk3 — jellyfin's IO was stalled 38% of the last 10 minutes while qbittorrent drove 78% of the disk's IO."*
- likely / `io-driven-cpu-load` (**Scott's founding example, verbatim in shape**): *"sabnzbd's storage IO is loading the host CPU — it's driving 63% of all disk IO while the CPU spends 24% of its time waiting on IO."*
- likely / `disk-spinup-churn`: *"plex is keeping disk5 awake — it has spun up 5 times in the last hour, each within a minute of plex reading from it."*

**Tests:** each rule fires on a hand-built `In` with both sides and does **not** fire with either side removed (14 cases — this is the core contract); `cpu-starvation` does not fire for a container with `cpu.alloc_cores == 0` even with throttling present; PSI evidence flips confidence and the verb; `Statement` output for all seven × both confidences matches golden strings; `Verb` never returns a causal verb for `ConfidenceLikely` (property test over every shape); a rule whose device is parity-role never fires (topology `Contended` gate); threshold overrides change the firing point.

---

### Task 7: Lifecycle state machine + engine loop + anti-noise

**Track:** full. **Files:** Create `internal/insight/engine.go`, `internal/insight/engine_test.go`; Modify `cmd/gantry/main.go` (wiring + one goroutine).

60s ticker. Per tick: gather inputs once (one `MatchSince` pass per distinct `(kind, metric)` across enabled rules, one `MatchPrefixSince` for `live:io.`, one topology build, one events-since-cursor read), then run every enabled rule against the shared `In`.

**State machine**, mirroring `alert.Engine`'s so there is one lifecycle model in the codebase: `pending` (both sides present but sustain not yet met) → `active` (fired; `insight.detected` appended) → `resolved` (`insight.resolved` appended). Resolve reasons: `cleared` (victim evidence gone for the clear window), `no-data` (series stopped), `restart`, `rule-disabled`, `dismissed`.

**Anti-noise — four layers, all required:**

1. **Sustain** — 90s within the 120s window, `Sustained`'s all-samples semantics.
2. **Clear-for** — 180s of the victim signal below threshold before resolving. Deliberately longer than the fire window: a finding that flickers off and back on is worse than one that lingers.
3. **Per-tuple cooldown** — after a resolve, the same `(rule, victim, culprit, resource)` cannot re-fire for 30 minutes. This is the flap guard, and unlike Phase 4's it is a plain cooldown rather than an auto-silence, because insights don't page.
4. **Dismissal suppression** — an `insight_dismissals` row suppresses its tuple until `until`.

Plus the **global cap**: at most 10 active insights. When a new finding would exceed it, keep the higher-severity/higher-confidence set and record the drop in a debug counter surfaced in Settings. A screen with 30 findings is a screen nobody reads.

**Tests:** simulated-clock table driving pending→active→resolved and the cooldown blocking an immediate re-fire; clear-for shorter than the observed gap does not resolve; a dismissal suppresses a would-be fire and expires correctly; the global cap keeps the top 10 by (severity, confidence, started_at) and is stable; a rule disabled mid-flight resolves its actives with `rule-disabled`; one tick performs exactly one `MatchPrefixSince` call regardless of rule count (assert with a counting fake).

---

### Task 8: Events integration + opt-in notify

**Track:** full. **Files:** Modify `internal/insight/engine.go`, `internal/alert/dispatch.go` (+tests), `internal/store/events.go` if a kind constant list exists.

Every transition appends to the **existing** events table via `store.AppendEvent`: kind `insight.detected` / `insight.resolved`, entity = the victim (so it lands on the victim's container chart, which is where you'd be looking), severity `info` by default, detail = the rendered statement.

**Notify is opt-in and constrained**, per Global Constraints and the §16 "can optionally notify" sentence:

```go
// Notifiable reports whether this finding may be dispatched through the
// Phase 4 alert channels. THREE gates, all required:
//   1. the rule's insight_rule_config.notify is on (default: off), and
//   2. confidence is Confirmed -- a "likely" finding is a correlational
//      claim and must never wake a phone, and
//   3. severity is alert.
// There is no override. If a user wants to be paged on a symptom, the
// alert engine is the correct tool and already exists.
func Notifiable(f Finding, cfg store.InsightRuleConfig) bool
```

**Tests:** a detected finding appends exactly one event with the victim as entity and the statement as detail; resolve appends exactly one more; `Notifiable` is false for every seeded rule out of the box; it stays false for a `likely` finding even with `notify` on and severity `alert`; it is true only for the full three-gate case; no notify path is reachable from the engine except through `Notifiable` (assert by construction in the dispatcher test).

---

### Task 9: `/api/insights/*` + frame `insights` block

**Track:** full. **Files:** Create `internal/server/api_insights.go`, `internal/server/api_insights_test.go`; Modify `internal/server/frame.go` (+test), `internal/server/server.go` (`Options` gains a narrow `InsightsIface`), `cmd/gantry/main.go` (adapter).

Follow the Phase 4 alerts precedent exactly — `server` stays store-shape-agnostic, adapters wired in `main.go`.

- `GET /api/insights` — active findings (with evidence bundles) + the tier (`proxy`|`psi`) from Task 2.
- `GET /api/insights/history?from&to&limit` — resolved, newest first, cursor-paginated like the Events view.
- `GET /api/insights/rules` — the seven rules with titles, tiers, thresholds (effective + default), enabled/notify state.
- `PUT /api/insights/rules` — whole-document replace of the config rows (the `/api/groups` pattern). Thresholds and enable/notify only; rule shape is not writable and an attempt is a 400.
- `POST /api/insights/{id}/dismiss` — body `{days:int}`, creates a dismissal and resolves the instance with reason `dismissed`.
- `GET /api/insights/graph` — the interaction map's payload (Task 14): nodes + edges, derived server-side so the layout code has one shape to lay out and the Playwright test has one thing to assert.

**Frame block** `insights: {active: [...], tier: "proxy"|"psi", suppressed: int}` — the compact shape, statements included, evidence excluded (fetched on demand when a drawer opens). Keeps the 2s frame small.

**Tests:** each route's happy path and its 400s; `PUT` rejects an attempt to change a rule's metric or shape; the frame block is present and compact (assert evidence is not inlined); the dismiss route both creates the row and resolves the instance; routes degrade correctly with an empty store.

---

### Task 10: Fake-mode insight scenarios

**Track:** full. **Files:** Modify `internal/fake/fake.go` (+tests), `cmd/gantry/main.go` (fake wiring).

Per the standing convention, every feature must be exercisable with no Unraid box. The generator already emits `live:io.sda|nvme0n1|loop2.{read,write}_bps` per container (`fake.go:576-581`) — extend it to drive a **scripted contention story** on a compressed schedule.

Follow the established `alertDemo*` pattern exactly (`fake.go:332-364`), which is the house shape for a scripted scenario: named `insightDemo*` constants near the existing block; a one-shot `bool` field on `Generator` for each edge-triggered event; a **pure** `func(elapsed time.Duration, rng *rand.Rand) float64` per smooth ramp (unit-testable with no `Generator`, mirroring `alertDemoDiskTempC`); and the trigger wired into whichever `emit*` method owns that entity, keyed off `elapsed := now.Sub(g.boot)`. Insight windows need the same fake-mode compression `store.DefaultAlertRules(fast bool)` applies to alert rules (`alert_defaults.go:179-186`): a `DefaultInsightRules(fast bool)` that shortens sustain/clear/cooldown so the whole story completes inside a demo session, exercising the **real** rules rather than a parallel demo-only set.

- **T+60s** — `qbittorrent` ramps to 80% of `sda`'s IO while `sda`'s new `util_pct` climbs to 97 and `await_ms` to 45; `jellyfin` and `sonarr` keep reading `sda`. Fires `disk-io-contention` at **likely**.
- **T+150s** — fake mode's PSI generator (new) starts emitting `container/jellyfin/psi.io.some_pct` = 38. The **same instance upgrades to `confirmed`** and its statement changes verb. This is the single most important fake-mode assertion in the phase: it demonstrates the tier upgrade without a reboot.
- **T+90s** — `host/cpu.iowait_pct` rises to 24 alongside qbittorrent's IO share, firing `io-driven-cpu-load` (Scott's founding example) as a second, distinct finding.
- **T+210s** — `disk5` spin-up churn: three `spun_up` 0→1 transitions with `plex` as the dominant IO source, firing `disk-spinup-churn`.
- **T+240s** — the GPU engine story: `jellyfin` and `frigate` both ≥ 10% on the video engine with the engine at 94%, firing `gpu-engine-contention`.
- **T+300s** — qbittorrent backs off; `disk-io-contention` resolves with reason `cleared`, producing a history row.

A `GANTRY_FAKE_PSI=0` variant of the same script must run the whole story at tier 1 only, so both tiers are demonstrable and Task 15 can screenshot both.

**Tests:** after N injected ticks the expected instance reaches `active`; the confidence upgrade at T+150 changes `confidence`, `tier`, and the rendered `statement` on the *same* instance id (not a new one); the resolve produces a history row; with `GANTRY_FAKE_PSI=0` every finding stays `likely` and no `psi.*` series exists; the graph endpoint is non-empty at T+180.

---

### Task 11: Insights view — active, history, evidence drawer

**Track:** fast (batched review pre-merge). **Files:** Create `web/src/views/Insights.svelte`, `web/src/lib/insights.ts` (+test); Modify `web/src/lib/router.ts` (+`router.test.ts`: `RouteName`, `routeDefs`, `routes[]` nav entry, a new `strokeIcon` const), `web/src/App.svelte` (one `{:else if}` branch), `web/src/components/TabBar.svelte` (recompute the per-item width budget), `web/src/lib/api.ts` (`InsightDTO`/`InsightsBlockDTO` beside `AlertsBlockDTO`, added to `SnapshotDTO`, plus typed fetch helpers modelled on `fetchAlerts`/`fetchAlertHistory`), `web/src/views/Events.svelte` (`KNOWN_KINDS` += `insight.detected`, `insight.resolved`), `web/src/lib/eventHref.ts` (`insight.*` → `#/insights`), `web/src/lib/eventMarkers.ts` (an `insight.detected` marker, severity `info`, label `Insight`).

Route `#/insights`, nav item **above** Alerts (an explanation precedes an escalation), icon distinct from Alerts' triangle and Events' bell — a two-node link/graph glyph via the existing `strokeIcon` helper, no new deps. Add the `RouteName`, the `routeDefs` entry, the matching `router.test.ts` case, the `NavItem` in `routes[]`, and one `{:else if}` branch in `App.svelte` — that is the whole mechanism.

**This phase adds exactly ONE nav item, not two.** `TabBar.svelte`'s own header comment budgets 46px per item to fit 375px, and `routes[]` is already at 9 (9 × 46 = 414px), past that budget and relying on the `overflow-x:auto` its comment calls "a safety net, not the expected path." A tenth is the last one this nav can absorb; the interaction map therefore lives *inside* this view (Task 14), not beside it. Recompute the `min-width` while you are here and record the new fit in that comment rather than letting it silently go staler.

**Contract:**

- **Active section** — one card per finding, live off the frame's `insights.active`, no polling. Each card: the statement as the headline in normal sentence case (it *is* the content — not a title with the content beneath), a confidence chip (`Likely` / `Confirmed`) that is **never colour-alone**, the victim and culprit as links through `eventHref`, "active for <relative>", and a dismiss control (1d / 7d / 30d).
- **Evidence drawer** — clicking a card fetches `/api/insights/{id}` and shows the bundle: the window, the actual numbers, the series ids, and two aligned `TimeChart`s (victim signal above, culprit share below, sharing an x-domain via the existing `xDomain` prop from the d2 chart work). This is the "show your working" surface that makes a wrong insight visibly wrong rather than mysteriously wrong.
- **Empty state** — calm, and *specific about the tier*: at tier 1, "Nothing is contending right now." plus the PSI upgrade card from Task 16. At PSI tier, just the first sentence.
- **Rules section** — the seven rules with a plain-English restatement generated by a pure `describeRule()` in `insights.ts` (vitest-tested across all seven), an enable toggle, a threshold editor, and the notify toggle rendered with its constraint stated inline ("only for confirmed findings").
- `web/src/lib/insights.ts` is the pure half: `describeRule`, `confidenceLabel`, `sortActiveInsights` (severity desc, then confidence desc, then `fired_at` asc — matching `sortActiveAlerts`' ordering instinct), `formatEvidenceNumber`.

---

### Task 12: ContainerDetail impact panel

**Track:** fast. **Files:** Modify `web/src/views/ContainerDetail.svelte`, `web/src/lib/insights.ts` (+test); Create `web/src/components/ImpactPanel.svelte`.

The backlog item "Container→system impact surfacing", now with the engine behind it. Slots beside the existing Storage section, reusing its `StorageRefDTO` plumbing.

**Two directions, always both, always labelled:**

- **"Being slowed by"** — findings where this container is the victim. *"jellyfin's IO is being starved by qbittorrent on disk3."*
- **"Slowing"** — findings where it is the culprit. *"qbittorrent is likely slowing jellyfin and sonarr on disk3."*

Below them, an always-present **share strip** (no engine required, honest at every moment): this container's current share of host CPU, of each device it touches, of host memory, and of each GPU engine — the lean correlational view the backlog asked for, rendered as `TopBarRow`-style bars. When a share crosses a rule's attribution floor but no finding has fired, the bar is annotated "high share, no contention detected" — which is genuinely informative and is *not* a causal claim.

Empty state: "Not affecting or affected by other containers right now." Never a blank panel.

---

### Task 13: Overview callout + alert annotation

**Track:** fast. **Files:** Modify `web/src/lib/overviewStatus.ts` (+test), `web/src/views/Overview.svelte`, `web/src/views/Alerts.svelte`, `web/src/lib/alerts.ts` (+test).

**Overview:** `deriveOverviewStatus` gains an `insights: InsightDTO[]` input and an eighth `OverviewAnomaly` kind, `{kind:'insight', ...}` — joining `unhealthy | stopped | disk-usage | disk-errors | array-stopped | source-critical | alert` (`overviewStatus.ts:46-66`), following exactly the pattern Phase 4 used to add `alert` as a source, including its `DEDUP_RULE_TO_ANOMALY` table (`overviewStatus.ts:122`) and `severityOverride` upgrade mechanism. Rules for the merge, to be written into the file's doc comment:

- Only `confirmed` findings, or `likely` findings at severity `warning`+, become attention rows. An `info` insight belongs on the Insights view, not in the headline count.
- An insight **never** duplicates an alert row for the same entity; when both exist the alert row wins and gains a "why" suffix linking to the insight — the alert is the actionable one.
- The headline count stays `anomalies.length`, preserving the invariant the file already documents.

**Alert annotation** (the sanctioned insight→alert bridge): an active insight whose victim matches a firing alert's entity renders as one extra line on that alert's row — "Likely cause: qbittorrent is driving 78% of disk3's IO" — linking to the insight. The alert says what broke; the insight says why. Pure function `annotateAlerts(alerts, insights)` in `alerts.ts`, vitest-tested, no component logic.

---

### Task 14: Container interaction map (flagship)

**Track:** fast. **Files:** Create `web/src/components/InteractionMap.svelte`, `web/src/lib/mapLayout.ts` (+test); Modify `web/src/views/Insights.svelte`, `web/src/lib/router.ts` (+test), `web/src/lib/api.ts`.

The backlog's flagship: *"a nice visualization of those relationships and live influence."*

**Placement:** a **List / Map toggle inside the Insights view**, built from the existing shared `.segmented` control (`app.css:144-173`) — the app's one control shape for mode pickers. Deep-linkable as `#/insights/map` (a `routeDefs` pattern of `['insights', 'map']`, mode read from the route so a link opens the map directly), but **no second nav item** — see Task 11 on the TabBar budget. Map is the default mode whenever at least one insight is active, list otherwise: the picture is the better first read when there is something to look at, and the worse one when there isn't.

**Layout decision — deterministic three-column layered SVG. Not force-directed.** Four reasons, to be recorded in `mapLayout.ts`'s doc comment:

1. **A force sim never settles on a live dashboard.** Nodes re-anneal on every 2s frame as edge weights tick; the picture would be permanently in motion. That is the opposite of the calm the d2 work established.
2. **Position is identity.** A user learns "jellyfin sits there". Force layouts relocate everything whenever the edge set changes, which is exactly when the user is trying to read it.
3. **The graph is inherently tripartite** — culprit → contended resource → victim. A layered layout *expresses the causal direction*; a force layout expresses nothing in particular. The columns carry meaning that a physics simulation would discard.
4. **Zero deps, fully testable.** A deterministic layout is a pure function that Playwright can snapshot and vitest can assert node-for-node. `d3-force` would be a new dependency producing non-deterministic output.

```ts
// layoutMap is pure and deterministic: same input, same coordinates,
// every time. Columns are culprits | resources | victims; within a
// column, nodes are stable-sorted by name so a node never moves because
// its edge weight changed -- only because the node set did.
export function layoutMap(graph: GraphDTO, viewport: {w: number; h: number}): Layout
```

**Rendering (D2-calm).** "D2" is Direction D2, *"Compass Rose, plain-reading anchor"* — `.superpowers/design-exploration/direction-d2.md`. Its two load-bearing rules for this view: **plain language beats a shape that needs literacy to read**, and (from `Overview.svelte`'s corrective-pass note) *"a line either separates two real regions or encodes real data, or it doesn't exist."* On a graph view that second rule is unusually sharp: every stroke on this canvas must be an insight or a node boundary. No decorative connectors, no grid, no frame.

- **Nodes**: containers as rounded rects with `ContainerIcon`, sized by their current share of the contended resource (not by absolute CPU — the map is about contention). Resource nodes (disk slot, `cpu`, `memory`, GPU engine) as a distinct, quieter shape. Colour from `containerColor`'s stable per-name hash for containers; `--ink-2` for resource nodes. Because this is SVG, not canvas, it consumes `var(--series-N)` directly — `theme.svelte.ts`'s `resolveToken()`/`withAlpha()` are only needed for uPlot's canvas and are not required here.
- **Edges**: one per active insight, drawn as a cubic curve, stroke width from the attribution share, and — the honest bit — **`confirmed` edges solid, `likely` edges dashed**. Confidence is legible at a glance without reading a word, and it is never colour-alone (`HealthDot.svelte:4`'s standing floor). Severity uses `--status-*` tokens, which are reserved for exactly this kind of meaning and must never be borrowed as a node-type palette.
- **Interaction**: follow `BaySchematic`/`FleetStrip`'s established hover convention rather than inventing one — a **fixed-height label row that is always present in layout and opacity-toggled**, never conditionally rendered, so nothing shifts on hover; and the full text duplicated into an `aria-label` for anyone who cannot hover. Hovering an edge highlights its two endpoints and dims the rest; clicking opens the same evidence drawer as Task 11 (shared component). Hovering a node highlights its incident edges.
- **Empty state** — and this matters, because on a healthy box it is the *normal* state: a calm fleet layout with no edges and the line "No container is currently contending with another." Plus the tier note at tier 1. The map must be worth looking at when nothing is wrong, not a blank canvas.
- **Time**: v1 is "now" plus a range picker that replays *resolved* insights over the window from `/api/insights/history`, rendering their edges ghosted. Full scrubbing is deferred — the existing `scrubbus` gives a natural hook when it's wanted.
- Respect `prefers-reduced-motion` (the existing `motion.svelte.ts`): no edge-draw animation when set.

**Tests (vitest, on `layoutMap`):** identical input yields identical coordinates across 100 runs; adding an edge does not move any existing node; nodes are stable-sorted within a column; a node appearing in both culprit and victim roles (A slows B while C slows A — real, and the layout must not duplicate or crash) is placed once, in the resource-adjacent column, with both edges attached; an empty graph yields a valid empty layout; the viewport scales without overlap at 375px width.

---

### Task 15: Playwright + responsive/a11y pass

**Track:** fast. **Files:** Create `web/tests/insights.spec.ts`, `web/tests/map.spec.ts`; Modify `web/tests/smoke.spec.ts` (the new route, incl. its `375px viewport: no route scrolls horizontally` case at line ~1133).

Playwright runs against the **real compiled binary** (`playwright.config.ts` runs `make release` then boots `GANTRY_FAKE_DATA=1 ./gantry`), so these assert the Go engine and the SPA together.

`#/insights` renders its heading and the tier-1 empty state on a cold boot; after the fake schedule trips (poll up to 4 min), an active card appears with the expected statement text; the confidence chip flips `Likely` → `Confirmed` at the scripted upgrade and the statement's verb changes with it; the evidence drawer opens with two aligned charts; dismissing removes the card and the history section gains a row; the List/Map toggle switches modes and `#/insights/map` deep-links straight into the map; the map renders nodes and at least one edge, hovering an edge dims the others, and the empty state renders on a cold boot.

Plus the standing invariants — note the repo's actual viewport conventions are **375px** (narrow mobile, heavily asserted) and **1440px** (wide desktop); 768px/48rem is the CSS sidebar↔TabBar breakpoint, exercised indirectly rather than set directly, and 1280 is not a convention here. No horizontal page scroll on the new route at 375px, TabBar carries the tenth item without wrapping (`mobileLabel` if needed), every icon-only control has an `aria-label` (there are already 90 such assertions across `web/tests/` — match that bar), confidence and severity are never colour-alone (assert the dashed/solid edge distinction survives a greyscale filter), `prefers-reduced-motion` honoured via Playwright's own `test.use({ reducedMotion: 'reduce' })` and the app's `motion.reduced` flag rather than a hand-rolled media query, and the map is keyboard-navigable — edges reachable by tab with the drawer openable by Enter. A graph you can only use with a mouse is a graph half the point of which is lost.

---

### Task 16: PSI upgrade copy + `docs/insights.md`

**Track:** fast. **Files:** Modify `docs/psi.md`, `web/src/components/SourcesBanner.svelte`, `web/src/views/Settings.svelte`, `web/src/views/Insights.svelte`; Create `docs/insights.md`.

`docs/psi.md`'s "Later:" paragraph becomes "Today:" — the engine has shipped, and the doc should say what he gets now and what he'd gain.

**The upgrade card** (Insights view empty state + Settings, dismissible, and it must be honest rather than nagging):

> **PSI is off — insights still work, but they can't name the victim.**
>
> Right now Gantry can see when a disk is saturated, who is driving its IO, and when the host CPU is stuck waiting on storage. That's enough to say "qbittorrent is *likely* slowing things on disk3", with the numbers to back it.
>
> What it can't see is which container was actually *stalled*, and for how long. PSI is a kernel-measured stall time per container, so findings become specific: "jellyfin's IO was stalled 38% of the last 10 minutes." It also makes CPU starvation detectable at all for containers with no CPU limit — which is most of them, so today that rule almost never fires on your box.
>
> One line in the syslinux append line, one reboot, well under 1% overhead. [How to enable →]

That last paragraph is the load-bearing one: it names the *specific* capability he does not currently have, rather than asking him to reboot for a vague improvement.

`docs/insights.md` documents all seven rules in user language: what each looks for, what it needs (tier), what it will say, and how to tune or turn it off. Plus one honest section — "What insights are not": they are rule-based correlation with an attribution requirement, not causal proof; the evidence drawer exists so you can check the engine's working.

---

### Task 17: ⛔ CHECKPOINT — on-box validation + quiet-by-default soak

**Track:** ⛔ gated on Scott. **Files:** `docs/superpowers/phase-5-validation.md`.

- Deploy to the real box at tier 1 (no reboot). Run a genuine contention scenario — a large qBittorrent write to the array during a Plex transcode — and confirm `disk-io-contention` and/or `io-driven-cpu-load` fire with numbers Scott agrees with. **A finding he disputes is a bug, not a tuning issue**, and it blocks release.
- Run a real parity check with array writes in flight; confirm `parity-slowdown` fires and its baseline is sane on a box with check history.
- **Quiet-by-default soak: ≥ 72h.** Count active insights. Target ≤ 3/day. Any rule exceeding it gets its thresholds raised before release. Record the counts per rule in the validation doc — this is the number that decides whether the feature is trustworthy.
- Footprint: engine inside the existing budget (≤ 2% core, ≤ 100MB RSS) with the 60s tick running.
- **Then, and only then, the `psi=1` decision.** With tier 1 proven, Scott reboots with `psi=1` and re-runs the same scenario, so the upgrade is measured against a known baseline rather than taken on faith. Record both statements side by side in the validation doc — that comparison is the honest answer to "was the reboot worth it", and it belongs in `docs/psi.md`.

## Risks and open questions for Scott

Each has a recommendation; none blocks starting Lane A.

1. **Unraid's md layer makes naive per-device attribution wrong.** Every array write goes through `mdX` *and* lands on both a data disk and the **parity** disk. A naive engine will report "container X is driving 100% of the parity disk's IO" — which is true, useless, and will destroy trust in the feature on day one, because parity IO is a *consequence* of the data write, not an independent contention. **Recommendation:** build the topology resolver in Task 3 and make `Contended()` return false for parity-role devices, folding parity load into the array-write finding's evidence rather than naming it. Attribute at the md/slot level via `Canonical()` so one logical write is counted once. This is the single most likely source of an embarrassing wrong insight and it is worth the extra task.

2. **Multiple culprits sharing one device.** Two containers at 44% and 31% of a saturated disk: firing two single-culprit insights is noise, firing none is a miss. **Recommendation:** the `Dominant(floor, maxN)` shape in Task 5 — a single culprit if one clears the floor, else the smallest leading set that jointly clears it, capped at 3, rendered as *"qbittorrent (44%) and sabnzbd (31%) together drive 75% of disk3's IO"* with `Shared=true`. One finding, honest arithmetic. The mover gets the same treatment as a *context enricher* rather than a culprit — `mover.running` appends "the mover is also active" to the evidence, because the mover has no measurable victim signal of its own and would otherwise fire a rule it can't support.

3. **False positives on correlational claims — the existential risk.** The whole feature dies if Scott reads three wrong findings in a row. **Recommendation: five layers, all shipped in v1** — (a) both-sides-or-nothing enforced by the type, (b) a confidence floor on *both* attribution share and victim magnitude, (c) the co-tenancy requirement (never claim disk contention when nobody else is using the disk — a container alone on a device it saturates is just a busy container, and `disk-io-contention` must not fire), (d) the ≤3/day quiet target measured in Task 17's soak with thresholds raised until it holds, and (e) the evidence drawer, so a wrong finding is *visibly* wrong and Scott can dismiss it — `insight_dismissals` gives feedback without any ML anywhere near this engine.

4. **Should per-container per-device IO be persisted beyond the live ring?** It's `live:`-only today. Persisting it would give insights history across restarts and richer evidence charts — but 40 containers × 8 devices × 2 suffixes is ~640 new series at every tier, against a 512MB DB cap. **Recommendation: keep it live-only.** The 120s evidence window fits in the 15-minute ring many times over, so evaluation never needs the history; and the evidence bundle is **denormalised into the instance row at fire time**, so a finding's numbers survive forever even though the series behind them do not. Revisit only if the evidence charts prove too thin in practice.

5. **Do insights survive a restart?** **Recommendation: the rows persist, the active state does not.** `StaleActiveInsights` resolves everything active at boot with reason `restart`. The live ring is empty after a restart, so a carried-over "active" finding would be asserting something the engine cannot currently see — and if the contention is still happening, the engine re-fires within two ticks anyway. History is preserved; false continuity is not.

6. **Does the map get its own nav item?** It is the flagship and the most demo-able thing in the product, which argues for top-level. Two facts argue against: it is empty on a healthy box, and the mobile TabBar is **already over its own budget** — `TabBar.svelte`'s comment sizes 46px/item to fit 8 within 375px, `routes[]` now holds 9 (414px), and its `overflow-x:auto` is documented as "a safety net, not the expected path." Two new items would make it 11 (506px) and turn a documented fallback into the normal experience on Scott's phone. **Recommendation: one nav item (`#/insights`), map as a `.segmented` mode inside it, deep-linkable at `#/insights/map`.** Nothing is lost — the map is one click from the same data, it can be the *default* mode whenever an insight is active, and a link still opens straight to it — while the nav stays inside its own stated budget. If the soak shows Scott lives in the map, promoting it later is a three-line router change; un-crowding a tab bar after release is not.

## Execution order across lanes

Per the standing parallel-agent split (disjoint files, isolated branches, worktrees per lane).

**Round 1 — three lanes in parallel, zero shared files:**
- **A (signals):** Tasks 1, 2, 3 are **fully independent of each other** — `collect/host/diskstats.go`, `collect/pressure/pressure.go`, and `store/live.go`+`insight/topology.go` don't touch. Dispatch all three at once. Task 1 is the long pole and the most valuable; start it first if capacity is limited.
- **B (engine):** Task 4 (schema) has **no dependencies** and can start immediately in parallel with A — it's pure SQLite + store CRUD.
- **F (docs):** Task 16's `docs/insights.md` skeleton can be drafted from this plan's rule table, though its copy isn't final until Task 11 exists.

**Round 2 — after A and Task 4 land:**
- **B (engine):** Task 5 → 6 → 7 → 8, strictly sequential (each builds directly on the last). This is the critical path for the whole phase.

**Round 3 — after Task 7 merges:**
- **C (api):** Task 9.
- **D (fake):** Task 10, parallel with C — it touches `internal/fake/` while C touches `internal/server/`. Both touch `main.go`; land C first to keep conflicts one-directional, exactly as Phase 4 sequenced its C and B.

**Round 4 — after Task 9 merges, UI lane strictly sequential on one worktree** (the fast-track rule: these five share `router.ts`, `api.ts`, `insights.ts`): Task 11 → 12 → 13 → 14 → 15. One implementer, no per-change review, batch the asks, one adversarial review of the whole lane before merge. **Task 14 is the flagship — do not let it become the task that gets rushed because it's last.** If the lane is running long, ship 11+12+14 and defer 13's alert annotation.

**Round 5:** Task 16 (final copy, needs the real UI), then Task 17 gated on Scott with everything merged.

### Suggested first dispatch

Four agents, right now, no coordination needed between them:

1. **Task 1** — device saturation + latency from diskstats. *The highest-value task in the phase: it is what makes tier 1 useful without a reboot.*
2. **Task 4** — insight schema + store CRUD. *Unblocks the entire engine lane and touches nothing else.*
3. **Task 3** — `MatchPrefixSince` + topology resolver. *Contains the md/parity correctness decision (Open question 1); worth landing early so the engine is built on top of a correct device model rather than retrofitted onto a wrong one.*
4. **Task 2** — PSI `full` + tier reporting. *Small, self-contained, and unblocks Task 16's copy.*

## Phase 5 exit criteria

- Seven rules evaluate on a healthy box without firing; the ≤3-insights/day quiet target is met over a ≥72h soak, with per-rule counts recorded.
- **At tier 1, with no reboot**, a real contention scenario on Scott's box produces a finding he agrees with, with numbers he can check in the evidence drawer.
- The same scenario re-run with `psi=1` produces the upgraded, victim-naming statement, and both are recorded side by side so the value of the reboot is documented rather than asserted.
- No insight has ever reached the notify spool or a webhook by default.
- `insight.detected`/`insight.resolved` visible in the Events feed and on container charts, with no parallel history feed.
- Insights view, ContainerDetail impact panel, Overview callout, and the interaction map all live; light + dark; clean at 375px; Playwright green; the map keyboard-navigable and readable in greyscale.
- `docs/insights.md` documents all seven rules and states plainly what insights are not; `docs/psi.md` reflects a shipped engine.

**Next:** the backlog's remaining correlation-only edges (shared docker networks, correlated `net.rx`/`net.tx` pairs, shared volume mounts) would extend the map from "contention" to "interaction" proper — a natural Phase 6 opener now that the graph surface exists.

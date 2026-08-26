# Gantry Phase 3: SSE + Full UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The product becomes visible: SSE live stream, history/top/events/logs/settings APIs, and the full Svelte UI (8 of 9 spec views — Alerts arrives with Phase 4's engine) — beautiful in light and dark, mobile-responsive, validated on the real box.

**Architecture:** The Phase 2 snapshot DTO becomes the SSE frame (full frame every 2s — gzip-friendly at this scale; "delta" in spec §6 is treated as transport framing, not diffing). History reads come off the Phase 2 read pool with automatic tier picking. The frontend is a static Svelte 5 SPA (hash router, no SvelteKit) embedded via a build-tag flip: plain `go build` serves an inline placeholder (no node needed for backend dev/tests); `-tags webdist` embeds the vite build (Dockerfile/CI/release path).

**Tech Stack:** Backend: stdlib only (no new Go deps). Frontend: Svelte 5 (runes), Vite, Tailwind CSS v4, uPlot; Playwright for smoke tests. Node 22+ via brew locally, actions/setup-node in CI.

**Spec:** `docs/superpowers/specs/2026-08-25-gantry-design.md` (§6 API, §7 UI). Carry-ins: `docs/superpowers/phase-3-carry-ins.md` (Tasks 1–5 retire them).

## Global Constraints

- No new Go dependencies. Frontend deps: svelte, vite, @sveltejs/vite-plugin-svelte, tailwindcss (v4), uplot, playwright (+@playwright/test) — nothing else without a ledger ruling.
- Port stays 8380. All new endpoints under `/api/`. Responses JSON unless stated; errors as `{"error":"..."}` with proper status codes.
- SSE: full `SnapshotDTO` frame per 2s tick, `event: frame`; on connect an immediate frame; `: ping` comment every 15s; client cap **32** (503 beyond); no gzip on the SSE handler (flush per event).
- History tier pick by requested range: ≤15m → live ring; ≤48h → samples_1m; ≤30d → samples_10m; else samples_1h. All reads on `Store.ReadDB()` with request ctx.
- Series identity in APIs: `kind`, `entity`, `metric` exactly as stored. `live:`-prefixed metrics are never served.
- Design tokens (validated with the dataviz palette validator, both modes PASS):
  - Categorical series (fixed order, never cycled; >8 series → "Other"): light `#2a78d6 #eb6834 #1baf7a #eda100 #e87ba4 #008300 #4a3aa7 #e34948`, dark `#3987e5 #d95926 #199e70 #c98500 #d55181 #008300 #9085e9 #e66767`.
  - Status (reserved for health/severity ONLY, always icon+label, never series): good `#0ca30c`, warning `#fab219`, serious `#ec835a`, critical `#d03b3b`.
  - Sequential (magnitude, e.g. disk-usage heat): blue ramp `#cde2fb→#0d366b`.
  - Surfaces: light chart `#fcfcfb` / page `#f9f9f7` / ink `#0b0b0b` / secondary `#52514e`; dark chart `#1a1a19` / page `#0d0d0d` / ink `#ffffff` / secondary `#c3c2b7`.
  - Chart rules: one axis only (never dual-axis); thin marks, 2px lines; legend for ≥2 series; text wears ink tokens, never series colors; color follows the entity, not rank.
- Theme: CSS custom properties on `:root`; light/dark/system with manual toggle persisted in localStorage; charts re-read tokens on theme change.
- Frontend formatting: bytes → binary units (KiB/MiB/GiB, 1 decimal), rates → bits or bytes/s as labeled (`net.*` shown as Mbps? NO — show bytes/s units MB/s to match docker convention), percents 1 decimal, timestamps browser-local.
- TDD for every Go change; frontend gets vitest unit tests for stores/formatting + Playwright smoke. `go test ./... -race` + `make lint` pristine per commit. Playwright/vitest green per frontend commit (`npm test` in web/).
- No AI attribution in commits. ⛔ Task 23 (on-box deploy) is gated on Scott.

---

### Task 1: Hygiene — slugSegment + hwmon instance uniqueness

**Files:** Create `internal/collect/slug.go`, `internal/collect/slug_test.go`; Modify `internal/collect/host/hwmon.go` (+test), `internal/collect/unraid/shares.go` (+test), `internal/collect/docker/cgroupv2.go` (device names), `internal/collect/gpu/collector.go` (engine names).

**Interfaces:** `collect.SlugSegment(s string) string` — lowercase; every char outside `[a-z0-9_-]` → `_`; runs collapse; empty → `"unknown"`. Applied to every dynamic metric-name segment (hwmon label, share name, device name, engine name). Hwmon uniqueness: when two sensors produce the same slug, suffix `_2`, `_3`… by hwmon dir order (deterministic). Tests: share name with spaces/dots (`"My Movies.4K"` → `my_movies_4k`), duplicate NVMe "Composite" sensors get distinct series, existing fixture-derived names unchanged (regression: `coretemp_package_id_0` stays).

**Note:** metric names for EXISTING clean values must not change (all current fixtures produce already-clean slugs — assert that in tests so history continuity holds).

---

### Task 2: Lifetime hygiene — key eviction

**Files:** Modify `internal/collect/rates.go` (+test), `internal/collect/gpu/collector.go` (+test), `internal/collect/docker/docker.go`/`registry.go` (+test).

**Interfaces:** `RateTracker.EvictPrefix(prefix string)` (deletes all keys with prefix). GPU: when a client leaves `c.clients` (dead read or scan replacement removes it), evict `clientID+"."` prefix. Docker: on container removal (the existing evict path), also evict that container's rate keys — give docker's tracker keys a `name+"|"` prefix convention if not already, and call EvictPrefix alongside the store evict; prune `loggedFallback` entry too. Tests: churn N clients/containers, assert tracker map size returns to baseline (expose `Len()` for tests).

---

### Task 3: Perf — single-lock snapshot, ring alloc, GPU scan prefilter

**Files:** Modify `internal/store/live.go` (+test), `internal/store/ring.go` (+test), `cmd/gantry/main.go` (snapshot closure), `internal/collect/gpu/walker.go` (+test), `internal/collect/gpu/collector.go`.

**Interfaces:**
- `Live.SnapshotLatest() map[SeriesKey]Sample` — one read lock, latest per series. The main.go snapshot closure switches to it (drops N+1 locking).
- `Ring.AppendSince(ts int64, dst []Sample) []Sample` — append-into-dst variant; `FlushMinutes` reuses one buffer across series per window (test: no behavior change; benchmark optional).
- GPU walker full scan: before opening `fdinfo/*`, `os.Readlink(procRoot/<pid>/fd/<n>)` and skip unless the target contains `/dev/dri/` (fall back to reading fdinfo when readlink errors — permission variance). Skip `drm-engine-capacity-*` keys before the ns-parse once-log. Tests: fake proc tree with fd symlinks (one dri, one socket) — only dri client discovered; capacity key ignored silently.

---

### Task 4: API polish — SnapshotDTO v2

**Files:** Modify `internal/server/api_snapshot.go` (+tests), `cmd/gantry/main.go` (buildSnapshot), `internal/collect/gpu/collector.go` (clamp).

**Interfaces (breaking DTO change, lands BEFORE the UI consumes it):**
```go
type SnapshotDTO struct {
	TS            int64                                    `json:"ts"`
	UnraidVersion string                                   `json:"unraid_version"`
	Host          map[string]float64                       `json:"host"`
	Containers    map[string]ContainerDTO                  `json:"containers"`
	Disks         map[string]map[string]float64            `json:"disks"`
	Unraid        map[string]map[string]float64            `json:"unraid"`  // entity → metric → value (was flat)
	GPU           map[string]map[string]float64            `json:"gpu"`
	Sources       map[string]string                        `json:"sources"` // moved into the frame so SSE clients see degradation live
}
```
- Containers: only entities present in `dc.Running()` OR with live samples younger than 60s AND a known Meta; stopped-and-gone containers drop out of the frame immediately (rings still age out at 15m internally — the FRAME filters).
- Recreation guard: registry's vanished-id evict only fires when no live container currently holds that name.
- Busy-% clamp: engine/gpu busy values clamped to [0,100] at emission (gpu collector).
- `/api/containers` returns `[]{name,state,health,image}` directly from Running() (no DTO detour).
- Tests updated for the new shape; a dedicated test pins the stopped-container filtering and the unraid entity dimension (`array` vs `docker` provenance preserved).

---

### Task 5: Lint gate — golangci-lint + errcheck fixes

**Files:** Create `.golangci.yml`; Modify `.github/workflows/ci.yml`, `Makefile` (lint target runs golangci-lint if installed, else vet+gofmt), event-append call sites (`internal/collect/docker/registry.go`, `internal/collect/unraid/{var,disks}.go`), `internal/store` R3 test line, migration sort (`internal/store/migrate.go` — sort by parsed version).

**Interfaces:** `.golangci.yml`: enable errcheck, govet, staticcheck, gofmt (default severities). Every `AppendEvent` error now logged (`log.Printf("events: %v", err)`). `TestRetentionFromConfig` asserts R3 override + default. Migration ordering test: add `9_` vs `10_` synthetic-name unit test on the sort (pure function extract `sortMigrations([]string) error` if needed). CI: golangci-lint-action step replacing nothing (added alongside make lint). Fix or explicitly `//nolint:<linter> // reason` any findings — zero suppressions without a reason string.

---

### Task 6: Store history queries

**Files:** Create `internal/store/query.go`, `internal/store/query_test.go`.

**Interfaces:**
```go
type SeriesPoint struct{ TS int64; Avg, Max float64 }
type SeriesResult struct{ Metric string; Points []SeriesPoint }
// QuerySeries picks the tier from [from,to): ≤15m→live ring (Avg==Max==Val),
// ≤48h→samples_1m, ≤30d→samples_10m, else→samples_1h. Uses ReadDB with ctx.
func (s *Store) QuerySeries(ctx context.Context, kind, entity string, metrics []string, from, to int64) ([]SeriesResult, error)
// TopEntities aggregates one metric across entities of a kind over [from,to):
// agg "avg" or "peak"; window "now" short-circuits to Live latest.
func (s *Store) TopEntities(ctx context.Context, kind, metric string, from, to int64, agg string, limit int) ([]struct{ Entity string; Value float64 }, error)
```
Tier boundaries tested exactly (15m/48h/30d edges); `live:` prefix rejected; unknown series → empty result not error; SQL uses the ts indexes (`WHERE series_id=? AND ts>=? AND ts<?`). TopEntities SQL: join series filtered by kind+metric, `AVG(avg)` or `MAX(max)` grouped by entity, ordered desc, limit.

---

### Task 7: History + events + top endpoints

**Files:** Create `internal/server/api_history.go` (+tests); Modify `internal/server/server.go` (routes), `cmd/gantry/main.go` (Options gain `Query`/`Top`/`Events` closures — keep the server store-agnostic as established).

**Interfaces:**
- `GET /api/series?kind=&entity=&metrics=a,b,c&from=&to=` → `[{metric, points:[[ts,avg,max],…]}]` (arrays not objects — payload size). Missing from/to → last 1h. 400 on bad numbers.
- `GET /api/top?resource=cpu|mem|net|io|gpu&window=now|1h|24h|7d&agg=avg|peak&limit=10` → `[{entity, value}]`. Resource→metric mapping: cpu→`cpu.pct`, mem→`mem.bytes`, net→`net.rx_bps`+`net.tx_bps` (two TopEntities calls summed by entity), io→`io.read_bps`+`io.write_bps`, gpu→sum over entities of `gpu.*.busy_pct` — implement gpu as QuerySeries-free special: TopEntities per engine metric set {gpu.render.busy_pct, gpu.video.busy_pct, gpu.video-enhance.busy_pct, gpu.copy.busy_pct, gpu.nvidia.mem_mib excluded} summed by entity. Kind fixed "container".
- `GET /api/events?kinds=a,b&entity=&from=&to=&limit=` → store.QueryEvents passthrough (JSON array of Event).
- httptest coverage: tier delegation (fake closures), param validation, resource mapping (net sums correctly).

---

### Task 8: SSE endpoint

**Files:** Create `internal/server/sse.go`, `internal/server/sse_test.go`; Modify `internal/server/server.go`, `cmd/gantry/main.go` (wire a broadcast loop).

**Interfaces:**
- `GET /api/live`: headers `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`. On connect: immediate `event: frame\ndata: <snapshot JSON>\n\n`, then a frame every tick, `: ping\n\n` every 15s. 33rd concurrent client → 503.
- Server side: `Broadcaster` — `Register() (ch <-chan []byte, cancel func(), ok bool)`, `Publish(frame []byte)`; per-client buffered chan (cap 4); a slow client whose buffer is full gets the frame DROPPED (never blocks the publisher); handler writes until client ctx done.
- main.go: a goroutine (WaitGroup-tracked, runCtx) marshals the snapshot every 2s and Publishes; server Options gain `Live *server.Broadcaster`.
- Tests: two httptest clients receive the connect frame + a published frame; cap test (33 clients, last gets 503); slow-client drop test (unread client doesn't stall Publish — publish 10 frames, returns promptly); ping frame appears with a shortened ping interval injected for test.

---

### Task 9: Container logs endpoint

**Files:** Create `internal/collect/docker/logs.go` (+dockertest-tagged test), `internal/server` route wiring via a `Logs func(ctx, name, follow bool, tail int) (io.ReadCloser, error)` Option.

**Interfaces:**
- Docker package: `(c *Collector) StreamLogs(ctx context.Context, name string, follow bool, tail int) (io.ReadCloser, error)` — resolve name→id via registry; SDK ContainerLogs (ShowStdout+ShowStderr, Timestamps false, Tail strconv(tail), Follow); demux via stdcopy into a pipe (goroutine ctx-bound, closes pipe on ctx done or stream end).
- `GET /api/containers/{name}/logs?follow=1&tail=500`: `Content-Type: text/plain; charset=utf-8`, chunked, flush per write; caps: tail ≤ 5000 (default 500); non-follow returns and closes; follow ends when client disconnects (request ctx). 404 unknown name.
- dockertest test: run a container that echoes, read two lines through the full endpoint via httptest against real daemon.

---

### Task 10: Settings API

**Files:** Create `internal/server/api_settings.go` (+tests); Modify main wiring.

**Interfaces:**
- `GET /api/settings` → `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":390,"size_cap_mb":512},"env_overridden":["port", ...]}` — values via config resolution; `env_overridden` lists keys whose env var is set (UI renders those read-only).
- `PUT /api/settings` body `{"retention":{...}}` — whitelist exactly those four keys; validate ranges (r1 1–168h, r2 1–90d, r3 30–1095d, cap 64–4096MB); write via `SettingSet`; effective next maintenance tick (no restart). 400 with field errors otherwise.
- Options: `Settings server.SettingsIface` (Get/Set minimal interface implemented by a small adapter in main over store+config).
- **Make PUT actually take effect:** Phase 2's `run()` resolves `ret` ONCE before the maintenance goroutine — a PUT would never apply. Modify `cmd/gantry/main.go`: resolve `ret := store.RetentionFromConfig(cfg.Int)` INSIDE the deep-tick case, each tick (10-min cadence, cost nil). Test: extend the store-level path only (main's loop is wiring); the settings handler test asserts SettingSet was called — plus one focused main-level comment noting the per-tick resolve is what makes PUT live.
- Tests: roundtrip, whitelist rejection (`port` in body → 400), range validation, env-overridden marking (fake getenv).

---

### Task 11: Fake-data extension (full-fidelity dev mode)

**Files:** Modify `internal/fake/fake.go` (+tests).

**Interfaces:** With `GANTRY_FAKE_DATA=1`, additionally synthesize: 6 disks (parity+4 data+cache; temps 32–45°C, disk3 spun down emitting no temp, slowly-drifting fs usage; one disk with a rare `disk.errors` event), unraid entity `array` (mover toggling every ~7min; a parity check that starts 2min after boot, progresses ~0.4%/s with speed ~130MB/s, finishes, emits parity.start/finish events), gpu entity `gpu0` + per-container `gpu.video.busy_pct` on two containers (jellyfin bursts, frigate steady ~20%), periodic container events (a restart every ~3min, an OOM every ~10min on distinct containers), and `gantry.cpu_pct`/`gantry.rss_bytes`. Deterministic from seed. The generator ALSO feeds an EventSink (add the param; main passes st). Snapshot closure needs no change (kinds already grouped). Tests: after simulated N ticks (inject clock), assert disks/unraid/gpu kinds present, spun-down disk has no temp key, parity progress monotonic while running, events recorded.

---

### Task 12: Frontend scaffold + build-tag embed flip

**Files:** Create `web/` (package.json, vite.config.ts, tailwind config via CSS `@theme`, src/main.ts, src/App.svelte placeholder, index.html), `internal/server/webfs_placeholder.go` (`//go:build !webdist`), `internal/server/webfs_dist.go` (`//go:build webdist`); Modify `internal/server/server.go` (use `webHandler()`), `Makefile`, `Dockerfile`, `.github/workflows/ci.yml`, `.gitignore`; Delete `internal/server/webdist/index.html` (tracked placeholder retires).

**Interfaces:**
- `webfs_placeholder.go`: `func webHandler() http.Handler` serving an inline `const placeholderHTML` (current placeholder content) at `/`.
- `webfs_dist.go`: `//go:embed all:webdist` + FileServerFS with an SPA fallback wrapper: unknown non-`/api` paths serve `index.html` (hash router makes this mostly moot; still correct).
- `web/vite.config.ts`: svelte plugin; `build.outDir: '../internal/server/webdist'`, `emptyOutDir: true`.
- `.gitignore`: `internal/server/webdist/`, `web/node_modules/`, `web/test-results/`.
- Makefile: `web: cd web && npm ci && npm run build` ; `release: web` + `CGO_ENABLED=0 go build -tags webdist ...` ; `build` unchanged (no node needed).
- Dockerfile 3 stages: `node:22-alpine` (COPY web/, npm ci, build — outputs to a path COPYed into stage 2), `golang:1.25-alpine` (`-tags webdist`), `scratch` unchanged otherwise.
- CI: setup-node@v4 (node 22, cache npm, cache-dependency-path web/package-lock.json), `make web`, then existing test steps PLUS a `-tags webdist` build; docker build already exercises the full path.
- Local toolchain step: `node --version` ≥22 else `brew install node`.
- **Gzip (spec §6 "all responses gzip"):** a stdlib `compress/gzip` middleware in `internal/server` wrapping every route EXCEPT `/api/live` (SSE must flush uncompressed) and `/api/containers/{name}/logs` (streaming): honors `Accept-Encoding: gzip`, sets `Content-Encoding` + `Vary`, pools writers (`sync.Pool`). Test: JSON endpoint returns gzip when accepted, identity otherwise; SSE response is never gzipped. (Vite's hashed assets satisfy the immutable-asset half of §6.)
- Verification: `make build` (no node artifacts needed) serves placeholder; `make release` binary serves the vite app shell; `make docker` green.

---

### Task 13: UI foundation

**Files:** Create under `web/src/`: `lib/router.ts`, `lib/sse.svelte.ts`, `lib/api.ts`, `lib/format.ts` (+vitest), `lib/theme.svelte.ts`, `lib/tokens.css`, `components/{Layout,Sidebar,TabBar,ThemeToggle,StatTile,Sparkline,TimeChart,HealthDot,EventFeedItem}.svelte`, `App.svelte` (route table), plus vitest config.

**Interfaces (binding contracts):**
- `router.ts`: hash router — `route` rune state `{name, params}`; routes: `#/` overview, `#/containers`, `#/containers/:name`, `#/top`, `#/storage`, `#/gpu`, `#/events`, `#/settings`. Sidebar (desktop ≥768px) / bottom TabBar (mobile) render from one nav table (icon+label, inline SVG icons).
- `sse.svelte.ts`: `live` rune store — connects `/api/live` via EventSource, exposes `frame` (SnapshotDTO), `connected`, auto-reconnect (EventSource native) + stale flag when no frame for >6s. History buffers: per chart, components fetch `/api/series` and append live deltas from frames.
- `format.ts` (vitest-tested): `fmtBytes(n)`, `fmtRate(bps)` (B/s → KB/s → MB/s decimal for rates), `fmtPct(n)` (1dp, clamp display 0–100), `fmtDuration(s)`, `fmtRelTime(ts)`.
- `theme.svelte.ts` + `tokens.css`: the Global Constraints palette as CSS vars (`--surface`, `--page`, `--ink`, `--ink-2`, `--series-1..8`, `--status-good/warning/serious/critical`, `--seq-*`); `data-theme` attr on `<html>` = light|dark from (localStorage ?? system), toggle cycles system→light→dark.
- `Sparkline.svelte`: uPlot, 1 series, no axes, 2px line, area fill 12% alpha, fixed height 28px, props `{points: [ts,val][], color}`.
- `TimeChart.svelte`: uPlot wrapper — props `{series: {label, points, colorVar}[], unit, height?, markers?: {ts, label, severity}[]}`; one y-axis; synced crosshair group (uPlot sync key per page); tooltip (crosshair values, ink tokens); event markers as vertical lines + hover label; legend when ≥2 series (series color follows entity via stable hash→slot? NO — callers pass explicit slot order; color follows entity across filters by assigning slots at the PAGE level, documented).
- `StatTile.svelte`: label, big value, unit, optional sparkline, optional status dot.
- Vitest: format functions exact cases; router parse cases. `npm test` runs vitest; Playwright arrives Task 22.

---

### Task 14: Overview view

**Files:** `web/src/views/Overview.svelte` (+ small components as needed).

**Contract:** Top row: 4 StatTiles (CPU %, Memory %, Net ↓↑ (two rates in one tile), Disk IO) each with 15m sparkline (live-fed). Array card: state badge (status colors + label), parity progress bar + speed + ETA when running, mover chip, hottest-disk temp, per-pool usage bars (sequential ramp by fill %). Fleet summary: running/unhealthy/stopped counts (HealthDot colors) — unhealthy list expands. Top Consumers module: top-5 CPU bar list (live), link to `#/top`. Events feed: latest 8 from `/api/events`, live-appended on frame source change… events aren't in frames — poll every 30s + refetch on window focus. GPU strip when gpu present: per-engine mini-bars. Sources degradation: any non-ok source renders a dismissible hint banner with its Detail text (docker non-ok = prominent, per spec §3).

---

### Task 15: Containers view

**Files:** `web/src/views/Containers.svelte`.

**Contract:** Dense table: health dot, name (→ detail), CPU% (with 15m sparkline cell), Mem (bytes + %), Net ↓/↑, IO r/w, GPU% (video+render sum, blank when zero), PIDs, uptime (from StartedAt), image (truncated, title attr). Sort by any column (default CPU desc), STABLE while values tick (sort key recomputed only on header click or entity add/remove — document in code comment); filter box (substring on name+image); stopped/exited section collapsed below running. Mobile: cards instead of table rows (name, health, CPU, mem, net). All numbers via format.ts.

---

### Task 16: Container detail view

**Files:** `web/src/views/ContainerDetail.svelte`, `web/src/components/LogViewer.svelte`.

**Contract:** Header: name, health/state badge, image, uptime, restart count. Range picker: Live-15m / 1h / 24h / 7d / 30d — Live mode feeds from SSE frames appended to a ring in the component; others fetch `/api/series` (kind=container, entity=name) and DON'T live-append. Charts (TimeChart, synced crosshairs): CPU % (+throttled % as series 2 when nonzero), Memory bytes (+limit line if we had it — we don't; single series), Net rx/tx, IO r/w, GPU per-engine (only engines with data), PSI when present. Event markers on all charts: `/api/events?entity=<name>` mapped to markers (restart=warning color, oom=critical, health=serious). Metadata card: image, state, started, restarts. LogViewer: fetch streaming reader of `/api/containers/{name}/logs?follow=1&tail=500` into a capped 2000-line buffer, follow toggle (auto-scroll), pause, client-side case-insensitive filter box, monospace, ANSI codes stripped. Unknown container → friendly empty state.

---

### Task 17: Top Consumers view

**Files:** `web/src/views/TopConsumers.svelte`.

**Contract:** Resource tabs: CPU / Memory / Network / Disk IO / GPU. Window segmented control: Now / 1h / 24h / 7d. Agg toggle: Average / Peak (hidden for Now). Data: Now → derive from current frame client-side; else `/api/top`. Render: horizontal bar list (top 10) — bar length relative to max, value labeled at bar end (ink token), entity name → container detail link. Bars are all ONE categorical hue (`--series-1`); identity is carried by the row label, never by per-rank colors (dataviz: color follows entity, not rank — and a leaderboard's entities churn per window). Empty window → explanatory empty state (e.g. 7d on a fresh install).

---

### Task 18: Storage / Array view

**Files:** `web/src/views/Storage.svelte`.

**Contract:** Array section: disk grid — one card per disk entity (parity/disk*/cache/pools/flash): name, temp (or "spun down" chip when no temp metric present), spun_up state icon, usage bar (sequential ramp: fill % → ramp step; >90% shows warning status chip), errors count (nonzero → serious status chip), fs bytes text. Parity card: last/active check state from unraid metrics + events (parity.start/finish from `/api/events?kinds=parity.start,parity.finish&limit=5` history list), progress+speed+ETA while running. Mover chip. Shares table: name → used bytes (sorted desc) with the pool-total caveat surfaced as an info tooltip (from fixtures.md finding — "value is the backing array/pool total"). Docker storage card: images/containers/volumes bytes (unraid:docker entity).

---

### Task 19: GPU view

**Files:** `web/src/views/GPU.svelte`.

**Contract:** Per GPU entity: engine utilization TimeChart (stacked? NO — dataviz one-axis multi-series lines, legend, categorical slots per engine fixed: render=1, video=2, video-enhance=3, copy=4), live-fed 15m + range picker sharing ContainerDetail's control. Attribution table: containers with any gpu.* metric — per-engine busy % columns + total, live. Nvidia section when nvidia source ok (mem MiB per container). When gpu source not ok or no engines seen: empty state with the source Detail hint. PSI absent → one-line tier hint (pressure source Detail verbatim).

---

### Task 20: Events + Settings views

**Files:** `web/src/views/Events.svelte`, `web/src/views/Settings.svelte`.

**Contract:** Events: filter row (kind multi-select from a fixed known-kinds list, entity text, time range presets 1h/24h/7d/all), list via `/api/events` (limit 200, "load more" via before-cursor = min ts), severity-colored icons (status palette + icon + label per dataviz status rule), relative + absolute time. Settings: Sources card (every source, ok/detail, the psi/nvidia hints verbatim); Retention editor (four numeric fields, ranges from Task 10, save via PUT, env-overridden fields disabled with a lock note); Gantry footprint receipt (gantry.cpu_pct + gantry.rss_bytes live StatTiles + spec budget captions); Theme control (system/light/dark); About (version from `/api/version`, unraid version from frame, link to repo).

---

### Task 21: Responsive + a11y pass

**Files:** Touch-ups across views/components; `web/src/lib/tokens.css`.

**Contract:** Verify at 375px, 768px, 1280px: no horizontal page scroll (tables get their own overflow-x container), TabBar on mobile, cards replace tables where specified, charts resize (uPlot resize observer in TimeChart), touch targets ≥40px on nav/toggles. A11y: every icon-only control gets aria-label; health/status never color-alone (dot + text/icon everywhere); focus-visible rings (ink token); `prefers-reduced-motion` disables sparkline transitions. Playwright (Task 22) asserts the no-horizontal-scroll invariant at 375px on every route.

---

### Task 22: Playwright smoke + CI

**Files:** `web/playwright.config.ts`, `web/tests/smoke.spec.ts`; Modify `.github/workflows/ci.yml`, `web/package.json`.

**Contract:** Playwright starts the REAL binary: build with `-tags webdist` (config `webServer.command`: `sh -c "cd .. && make release >/dev/null && GANTRY_FAKE_DATA=1 GANTRY_DB_PATH=$(mktemp -d)/g.db GANTRY_PORT=8391 ./gantry"`, url `http://127.0.0.1:8391/api/healthz`). Tests: each route renders its h1/landmark; overview shows ≥10 container rows worth of fleet count and a ticking CPU tile (two samples differ within 6s); containers table sorts on click; container detail renders charts + log viewer element (logs endpoint returns 404 on fake container — viewer shows its empty state, asserted); top consumers switches windows without error; theme toggle flips `data-theme` and persists across reload; 375px viewport: no horizontal scroll on any route (`document.documentElement.scrollWidth <= innerWidth`). CI: new `ui` job (needs test job? parallel fine): setup-go + setup-node, npm ci, npx playwright install --with-deps chromium, `npx playwright test`.

---

### Task 23: ⛔ CHECKPOINT — on-box deploy + real-data UI validation

**Files:** Create `docs/superpowers/phase-3-validation.md`.

- [ ] ⛔ Scott's go-ahead (same posture as Phase 2's deploy — rebuild gantry:phase3 on the box from main after merge-candidate is ready, replace gantry-p2).
- [ ] Deploy; validate against REAL data: all 8 views against the 38-container box; GPU view during a transcode; SSE stability (leave a tab connected 10+ min, frames keep ticking, reconnect works after container restart); mobile check (Scott's phone or 375px responsive proof + screenshots); settings PUT roundtrip on the box; footprint re-measure with SSE active (budget: ≤2% core, ≤100MB RSS w/ one client connected).
- [ ] Screenshots (light + dark, overview + container detail + top consumers) saved to `docs/screenshots/` — the README/CA-listing assets Phase 4 needs.
- [ ] Record verdicts + numbers; fix-loop anything found (TDD); commit.

---

## Phase 3 exit criteria

- All 8 views live on real data on the box; SSE stable ≥10 min; mobile clean at 375px; light+dark validated (palette PASSes recorded); Playwright + vitest + full Go suite + golangci green in CI; screenshots captured; footprint within budget with a live client.

**Next:** Phase 4 plan (alert engine + delivery + Alerts view + CA packaging/release).

# Gantry — Design Spec

**Date:** 2026-08-25
**Status:** Approved design, pre-implementation
**Repo:** `github.com/smidley/gantry` (module path; GitHub repo created at implementation start)
**License:** MIT

## 1. Summary & positioning

Gantry is a Docker and server monitor built specifically for Unraid, shipped as a **single container** on Community Applications. It is the anti-Beszel in deployment shape: no hub, no agents, no accounts, no setup. Install from CA, open the web UI, everything is already live.

Positioning against Beszel (the current community favorite):

| | Beszel | Gantry |
|---|---|---|
| Deployment | Hub container + agent per host | One container |
| Setup | Pair agents, create account | Zero-config (template pre-fills everything) |
| Scope | Multi-server | Single server (the Unraid box it runs on) |
| Unraid awareness | None (generic Linux) | Array, parity, mover, disk temps/spin, pools, shares, Unraid notifications |
| Per-container GPU | No (host-level only) | Yes — Intel & AMD per-container via DRM fdinfo; Nvidia via NVML |
| Container drill-down | Metrics | Metrics + event markers + live log tail |

## 2. Goals, success criteria, non-goals

### Goals (v1)

1. Beautiful realtime web UI, light + dark mode, mobile-responsive.
2. Lightweight: `scratch`-based image ≤ ~25MB, idle RSS ≤ ~80MB, idle CPU ≤ ~1.5% of one core on a 30-container box.
3. Per-container CPU, memory, network, disk IO, GPU; status/health; live log tail.
4. Host stats at Beszel parity: CPU, load, memory (incl. ZFS ARC), swap, per-interface network, per-device disk IO, hwmon temps/fans, uptime.
5. Unraid-specific: array state, parity check (progress/speed/ETA/last result), mover, per-disk temp + spin state + usage + error counters, pools, shares, UPS if detected.
6. History with tiered retention + a live view.
7. Configurable alerting with sensible defaults enabled out of the box; delivery via Unraid's own notification system + webhooks.
8. **Top Consumers** view: leaderboards (CPU / memory / network / disk IO / GPU) over Now / 1h / 24h / 7d, average vs peak toggle.
9. Zero-configuration: stock CA template boots to a live dashboard in seconds with no edits, no wizard, no account.

### Success criteria

- Fresh install from template → live dashboard with **zero user edits**.
- All six requirement pillars (UI, lightweight, container stats + drill-down, alerting, host stats, Unraid detail) demoable on Scott's server.
- Footprint targets above verified and displayed in-app (Settings shows Gantry's own CPU/RAM).
- CA-ready: template XML, icon, screenshots, support-thread draft, privileges documented.

### Non-goals (v1 — explicit)

- Multi-server monitoring (single-server is the product identity; no remote-host seam is built).
- Container management actions (start/stop/restart). Read-only socket, period.
- Unraid GraphQL API integration (needs API key → breaks zero-config; emhttp state files cover v1 needs).
- Authentication beyond optional basic auth (LAN posture; reverse-proxy guidance in README).
- VM (libvirt) metrics — v2 candidate.
- Deep SMART attribute parsing (emhttp's computed summary only), Nvidia MIG, multi-GPU-vendor per-container parity for Nvidia beyond NVML process stats.
- arm64 image (Unraid is x86_64-only).

## 3. Architecture

One container, one Go process, one embedded SPA.

```
┌─────────────────────────── gantry (Go) ────────────────────────────┐
│ collectors (goroutines, per-source tickers)                        │
│   docker   2s   inventory, status/health, stats, events, logs      │
│   host     2s   /proc /sys: cpu mem net diskio temps fans          │
│   gpu      2s   DRM fdinfo walk + optional NVML                    │
│   unraid   15s  /var/local/emhttp/*.ini parse + mover pid check    │
│        │                                                           │
│        ▼                                                           │
│ live ring buffer (RAM, 2s res, 15 min)                             │
│        │ 1-min rollup                                              │
│        ▼                                                           │
│ SQLite (/config/gantry.db): samples_1m/_10m/_1h, events,           │
│   alert rules+history, settings   [downsampler + pruner]           │
│        │                                                           │
│        ▼                                                           │
│ alert engine (hysteresis state machine)                            │
│   → unraid notify spool   → webhooks   → UI                        │
│                                                                    │
│ HTTP :8380  — embedded Svelte SPA (go:embed)                       │
│             — REST /api/* (history, entities, rules, settings)     │
│             — SSE /api/live (2s delta frames)                      │
│             — chunked /api/containers/{id}/logs (follow)           │
└────────────────────────────────────────────────────────────────────┘
```

### Container posture (the CA template)

No `--privileged`. The template ships exactly:

| Mount / flag | Mode | Enables |
|---|---|---|
| `/var/run/docker.sock` | **ro** | container inventory, stats fallback, health, logs, events |
| `/sys` → `/host/sys` | ro (rbind) | hwmon temps/fans, DRM/GPU sysfs, cgroup v2 fast path |
| `/var/local/emhttp` → `/unraid` | ro | array, parity, mover, disks, pools, shares |
| `/tmp/notifications` → `/notify` | **rw** (the only rw host mount) | Unraid-native alert delivery |
| named volume → `/config` | rw | SQLite DB, settings snapshots |
| `--pid=host` | — | host PID table: per-process fdinfo (GPU), `/proc/1/net/dev` (host net), per-container netns counters, mover detection |
| `--cap-add=SYS_PTRACE` | — | read other processes' `/proc/<pid>/fdinfo` |
| Port `8380` → host | — | web UI |

Bridge networking (not host) — host-side counters come via the host PID table, so we don't burn a host port namespace.

### Feature detection & degradation

Every source is probed at boot and re-probed every 60s. Missing source ⇒ the corresponding UI panel renders an "enable this" hint (the exact template edit), never an error. Docker socket missing ⇒ prominent banner (it's the core). This also makes Gantry runnable on generic Docker (dev on macOS/Linux) with host/GPU/Unraid panels dark.

## 4. Collectors

### 4.1 Docker

- Client: official Docker Go SDK over the unix socket, API version-negotiated.
- **Inventory/metadata** (10s + on docker events): name, image, state, health, restart count, exit code, started-at, ports, mounts, labels. The events stream also feeds the events table (start/stop/die/oom/health_status) in realtime.
- **Hot-loop stats** (2s): fast path reads cgroup v2 directly under `/host/sys/fs/cgroup/docker/<id>/` — `cpu.stat`, `memory.current`, `memory.stat`, `io.stat`, `pids.current`. Fallback path (cgroup v1 boxes or unreadable cgroupfs): docker stats API one-shot. Fast path is what keeps 2s × N-containers cheap.
- **Insight enablers** (2s, cgroup v2 fast path only — collected from day one so the §16 engine has history to analyze): per-container PSI (`cpu.pressure`, `io.pressure`, `memory.pressure` — "% of time stalled waiting"), CPU throttling counters from `cpu.stat` (`nr_throttled`, `throttled_usec`), and **per-device** IO from `io.stat` (rows are per major:minor → mapped to sdX/nvme/md device identity, which the Unraid collector ties to array disk/pool). Host PSI from `/proc/pressure/*`. All are flat-file reads; readability is verified by spike S3.
- **Per-container network** (2s): read `/proc/<container-init-pid>/net/dev` (pid from inspect; we share the host PID namespace). Containers in `network_mode=host` are labeled "host network" and excluded from per-container net attribution honestly.
- **Memory metric definition:** report `memory.current − inactive_file` (what `docker stats` calls "used"), plus the raw value in drill-down.
- **Logs:** `docker logs --follow --tail=500` equivalent via SDK, streamed to the UI over a chunked endpoint; bounded buffers; never persisted to SQLite.
- **Docker disk usage** (5m): images/containers/volumes bytes via the API DiskUsage endpoint, surfaced on Overview — a filling docker.img is the classic Unraid failure and deserves visibility even without fill-% of the loopback itself.

### 4.2 Host

- CPU total + per-core from `/proc/stat`; load from `/proc/loadavg`; memory from `/proc/meminfo` (+ ZFS ARC from `/proc/spl/kstat/zfs/arcstats` when present); swap; uptime.
- Host network: `/proc/1/net/dev` (host init netns), per-interface, veth/virbr filtered by default.
- Disk IO: `/proc/diskstats` per block device (sd*, nvme*, md*), bps + IOPS deltas.
- Temps/fans: `/host/sys/class/hwmon` enumeration with sensible labeling (CPU package, NVMe, MB).
- Filesystem usage is intentionally NOT collected host-side: disk/pool/share usage comes from the Unraid collector (§4.3), Docker storage from the DiskUsage poll (§4.1). We never mount the array.

### 4.3 Unraid

- Parse `/unraid/var.ini` (array state `mdState`, parity sync position/size/speed → progress + ETA, last check result, Unraid version), `/unraid/disks.ini` (per-disk: temp, spin state, status e.g. DISK_OK, device, fs, size/used/free, error counters, role — parity/data/cache/pool), `/unraid/shares.ini` (share usage). INI dialect quirks (quoted values, `[section]` per disk) handled by a dedicated tolerant parser with fixtures.
- Defensive: unknown keys ignored, missing file ⇒ feature off, malformed line ⇒ skip + debug log. Compatibility validated against captured fixtures from Unraid 6.12 and 7.x. **Version floor: 6.12.**
- Mover: process check for `mover` in the host PID table (cheap, reliable).
- UPS: if `/unraid` exposes UPS state (apcupsd-fed), surface basic charge/load/runtime; feature-detected, best-effort.

### 4.4 GPU

- **DRM fdinfo path (Intel i915 + xe, AMD amdgpu) — per-container:** every 2s, walk host PIDs with open DRM FDs; read `/proc/<pid>/fdinfo/<fd>`; accumulate per-client `drm-engine-*` busy-ns and `drm-memory-*`; delta between ticks → per-engine busy %; map PID → container via `/proc/<pid>/cgroup` (docker cgroup path carries the container ID; works on cgroup v1 and v2). Sum per container per engine (render / video / video-enhance / blitter / copy / compute). Host totals = sum of clients; clocks via `/host/sys/class/drm`, power via hwmon/RAPL where exposed.
- Efficiency: full PID scan every 30s discovers new DRM clients; the 2s tick only re-reads known clients (+ any PID the docker collector reports as new). Dedupe by `drm-client-id` so multi-FD clients aren't double-counted.
- Gantry itself needs **no `/dev/dri` access** — it reads accounting, not the GPU. Users' existing Intel-GPU-TOP plugin / `/dev/dri` passthrough setups are untouched.
- **NVML path (Nvidia) — optional:** template variable flips on `--runtime=nvidia` + `NVIDIA_VISIBLE_DEVICES=all`; Gantry dlopens `libnvidia-ml.so` if present; host utilization + per-process (SM %, memory) → same PID→container mapping. Runtime absent ⇒ Nvidia panel shows the enable hint.

## 5. Storage & retention

- **Engine:** SQLite via `modernc.org/sqlite` (pure Go → CGO-free static binary → `scratch` image). WAL, `synchronous=NORMAL`, single writer goroutine.
- **Series model:** `series(id, kind, entity, metric)` — kind ∈ {host, container, disk, gpu, unraid}; entity is the stable identity (container name, disk id); metric is the series name. Samples tables per tier: `samples_1m/_10m/_1h (series_id, ts, avg, max)`. Avg + max both survive rollups (powers the Top Consumers avg/peak toggle).

| Tier | Resolution | Retention (default, configurable) | Where |
|---|---|---|---|
| Live | 2s | 15 min | RAM ring buffer |
| R1 | 1 min | 48 h | SQLite |
| R2 | 10 min | 30 d | SQLite |
| R3 | 1 h | 13 months | SQLite |

- Downsampler cascades R1→R2→R3 on schedule; pruner enforces retention + a hard DB size cap (default 512MB, prunes oldest first). Expected steady-state for a 30-container box: tens of MB.
- **Events table:** `events(ts, kind, entity, severity, detail)` — container start/stop/die/oom/health flips, array state changes, parity start/finish/result, alert fired/resolved. Powers the Events feed, chart markers, and alert history.
- **Settings:** SQLite table, edited via UI; env vars override (documented set: `GANTRY_PORT`, `GANTRY_BASIC_AUTH_USER/PASS`, `GANTRY_RETENTION_*`, `GANTRY_WEBHOOK_URL`, `GANTRY_FAKE_DATA`). Nightly JSON snapshot of settings + alert rules to `/config/backup/` (survives DB reset).
- Container identity across recreations keys on **container name** (Unraid names are stable; IDs churn on update). History survives image updates.

## 6. Realtime & API surface

- `GET /api/live` — SSE; one compact JSON delta frame per 2s tick (host, containers, gpu, unraid snapshot). Client re-syncs full state on (re)connect. SSE over WebSockets: free auto-reconnect, proxy-friendly, one-way is all we need.
- `GET /api/series?ids=…&from=…&to=…` — history; server picks the tier for the range.
- `GET /api/top?resource=cpu|mem|net|io|gpu&window=now|1h|24h|7d&agg=avg|peak` — Top Consumers.
- `GET /api/containers`, `/api/containers/{name}`, `GET /api/containers/{name}/logs?follow=1` (chunked), `/api/unraid`, `/api/gpu`, `/api/events?filter…`, CRUD `/api/alerts/rules`, `/api/alerts/history`, `/api/settings`, `GET /api/healthz`.
- All responses gzip; SPA served from `go:embed` with immutable asset hashing.

## 7. Web UI

Svelte 5 + Vite + Tailwind CSS v4 + uPlot (~50KB, canvas, built for high-frequency time series). No CDN calls — fully LAN/offline-clean. Light/dark follows system with a manual toggle (persisted). Collapsible sidebar; bottom tab bar on mobile.

Views:

1. **Overview** — host tiles with live sparklines (CPU, mem, net, IO, GPU), array card (state, parity progress, mover, hottest disk, pool bars), fleet summary (running/unhealthy/stopped), compact Top Consumers, recent events.
2. **Containers** — dense sortable/filterable table: status/health dot, CPU %, mem, net ↓↑, IO, GPU %, uptime; per-row sparkline; stable sort while values tick.
3. **Container detail** — chart set (CPU/mem/net/IO/GPU) with time-range picker (Live 15m / 1h / 24h / 7d / 30d / custom), **event markers on charts**, live log tail (follow, pause, search), metadata (image, ports, mounts, restart policy, env count — values redacted), health-check history.
4. **Top Consumers** — leaderboards per resource (CPU, memory, network, disk IO, GPU); window Now / 1h / 24h / 7d; avg vs peak toggle; horizontal bars; click-through to container detail.
5. **Storage / Array** — visual disk map (parity/data/cache/pools), per-disk temp + spin state + usage + errors, parity card with progress/ETA/history, share list.
6. **GPU** — stacked per-engine utilization (video engine = transcode visibility), per-container attribution table, VRAM, clocks/power where exposed.
7. **Alerts** — rule list (enable/edit/create), firing history with resolution times.
8. **Events** — filterable feed (kind, entity, severity, time).
9. **Settings** — retention, intervals, webhooks, theme, basic auth, Gantry's own CPU/RAM footprint ("the receipt").

SSE deltas patch Svelte stores; uPlot appends in place (no re-render jank). Charts render in browser-local time.

## 8. Alerting

- **Threshold rules:** scope (host | all containers | containers by name/glob | disk | pool | GPU) + condition (metric, op, threshold, **sustained-for**) + severity (info/warning/alert) + per-entity cooldown. Auto-resolve after **clear-for**; optional resolution notice.
- **Event rules (toggles):** container exited non-zero, went unhealthy, OOM-killed, array not started, parity finished with errors, disk error counter incremented.
- **Defaults enabled out of the box:** unhealthy container; OOM kill; unexpected exit; disk ≥ 55°C sustained 10m; disk/pool ≥ 90% full; array not started; parity errors > 0. All tunable/disableable.
- **Anti-flap:** fire only after sustained-for; dedup key (rule, entity); cooldown per entity; hysteresis via clear-for.
- **Delivery:**
  1. **Unraid-native (default):** write a notify file into `/notify` (the mounted `/tmp/notifications` spool) in the dynamix format — Unraid's configured agents (Discord/Pushover/email/…) dispatch it. Severity → normal/warning/alert. *Exact format pinned by Spike S2.*
  2. **Webhooks:** N targets; POST JSON (event, entity, metric, value, threshold, severity, timestamps); optional custom headers + body template (covers ntfy, Discord webhooks, Home Assistant).
  3. **In-UI:** badge + history.

## 9. Failure modes

- Collector isolation: per-collector panic recovery, error backoff (exponential to 5m), health surfaced in Settings; one bad source never kills the process.
- SQLite: integrity check at boot; on corruption, move DB aside (`gantry.db.corrupt-<ts>`), start fresh, restore settings/rules from the nightly JSON snapshot; banner informs the user.
- Bounded everything: SSE client cap (32), log-tail buffer caps, collector concurrency caps, ring buffers fixed-size. Memory is O(containers), not O(uptime).
- Clock jumps (NTP): rollups tolerate monotonic/wall divergence — deltas computed on monotonic clock.

## 10. Security posture (README-front-and-center)

- Read-only docker socket; no container mutation endpoints exist in the binary.
- No `--privileged`; capabilities limited to `SYS_PTRACE`; all host mounts ro except the notification spool.
- No auth by default (LAN posture, consistent with Unraid dashboards); `GANTRY_BASIC_AUTH_USER/PASS` enables basic auth; README documents reverse-proxy + SSO patterns.
- No telemetry, no phone-home, no CDN. Update check (GHCR latest tag) is opt-in and clearly labeled.
- Env values in container metadata are redacted in the UI (names/count only).

## 11. Testing

TDD throughout (superpowers dev workflow).

- **Unit:** emhttp ini parsers (fixtures captured from real 6.12 + 7.x boxes — including mid-parity-check and spun-down states), fdinfo parser + engine-delta math, cgroup→container mapping (v1 + v2 layouts), docker stats math, downsampler/pruner (simulated clock), **alert hysteresis state machine** (fire/clear/cooldown edge matrix), notify-file writer, webhook templating.
- **Integration:** against a real Docker daemon in CI (spin test containers, assert stats/events/logs); SQLite retention over simulated weeks.
- **Frontend:** vitest component tests for stores/formatting; Playwright smoke — all views render with `GANTRY_FAKE_DATA=1`, SSE ticks update DOM, dark mode toggles, mobile viewport.
- **Fixture-replay dev mode:** `GANTRY_FAKE_DATA=1` synthesizes a plausible server (host + ~20 containers + array + GPU) so UI dev runs anywhere; also generates demo screenshots.
- **Pre-release:** validation checklist on Scott's Unraid box (real parity check, real transcode for GPU attribution, alert round-trip through Unraid notifications).

## 12. Packaging, CI/CD, CA release

- **Dockerfile:** stage 1 node → build SPA; stage 2 Go (`CGO_ENABLED=0`, `go:embed` dist) → static binary; stage 3 `scratch` + binary + tzdata + CA certs. linux/amd64 only. `HEALTHCHECK` in exec form invoking the binary's own `-healthcheck` mode (which curls `/api/healthz` internally) — no shell needed in scratch.
- **CI (GitHub Actions):** PR — golangci-lint, go test, frontend lint+test, image build. Tag `v*` — full test, build, push `ghcr.io/smidley/gantry` (`latest` + semver), GitHub release with changelog.
- **CA distribution:** separate template repo (`smidley/unraid-templates`) with gantry.xml — all mounts/caps/port pre-filled, Nvidia toggle documented as optional; icon; screenshots; Unraid forum support thread; CA registration once the thread is live. README's first screen: screenshots + the privileges explanation.
- Versioning: semver from `v0.1.0`; CHANGELOG kept from the first release.

## 13. Implementation spikes (day one, before the main build)

- **S1 — fdinfo access:** verify foreign-process `/proc/<pid>/fdinfo` DRM reads work with `pid=host` + `SYS_PTRACE` (no privileged) in a container on Unraid. Fallback if blocked: document `--privileged` as an *optional* template variant for per-container GPU (undesirable; decision point if hit).
- **S2 — notify format:** write a file into `/tmp/notifications/unread` on the real box, confirm dynamix dispatches it to a configured agent, pin the exact field format. Fallback: shell-out is impossible from scratch image, so if the spool format proves unstable, fall back to webhook-only + document Unraid-native as 7.x-API-key optional (undesirable; decision point if hit).
- **S3 — cgroup v2 via /host/sys:** confirm `/host/sys/fs/cgroup/docker/<id>/` is readable rbind on Unraid. Fallback (non-fatal): docker stats API path already specified.

Each spike is throwaway code; results recorded in `docs/superpowers/spikes.md`.

## 14. Repo layout

```
cmd/gantry/            main
internal/collect/      docker/ host/ unraid/ gpu/   (one package per source)
internal/store/        sqlite, ring buffer, downsampler
internal/alert/        rules engine, notify writer, webhooks
internal/server/       http, sse, api handlers
web/                   Svelte app (built into internal/server via go:embed)
template/              CA XML + icon (until split to smidley/unraid-templates)
docs/superpowers/      specs/ plans/ spikes.md
testdata/              emhttp fixtures, fdinfo fixtures, cgroup layouts
```

## 15. Deferred to v2 (recorded, not designed)

VM (libvirt) metrics · Unraid GraphQL API enrichment (needs key) · browser push notifications · full multi-channel alert integrations (Shoutrrr) · SMART attribute drill-down · container update-available indicators · multi-user/auth providers.

(Cross-container impact insights are NOT on this list — they are designed in §16; only the engine's release phase is a scheduling decision.)

## 16. Cross-container impact insights

**Goal:** Gantry doesn't just show metrics — it tells you *who is hurting whom*. Plain-English, evidence-backed findings such as "qbittorrent is saturating disk3 — jellyfin's IO was stalled 38% of the last 10 minutes" or "parity check is running 55% below its usual speed while sabnzbd writes to the array". No other Unraid monitor does this; it is a headline differentiator.

**Two ingredients per finding:**

1. **Victim signal — measured stall, not inference:** cgroup v2 PSI per container (`cpu.pressure`, `io.pressure`, `memory.pressure`) reports the share of time a container's tasks were stalled waiting on each resource. This is kernel ground truth for "X is being impacted." Complemented by CPU throttling counters, parity-speed-vs-baseline, and OOM/health events.
   *Two tiers (spike S3 finding, 2026-08-25):* stock Unraid ships PSI compiled but default-disabled (`CONFIG_PSI_DEFAULT_DISABLED`; no `/proc/pressure`). **Tier 1 (stock):** victim signals = throttling counters, io.stat proxies, OOM/health events, parity-speed baseline. **Tier 2 (opt-in):** the user adds `psi=1` to the syslinux append line — documented in the README and surfaced as the pressure source's enable hint — unlocking full PSI victim signals. Insight rules declare which tier they need; GPU and array categories work identically in both.
2. **Culprit attribution — who holds the contended resource:** per-device IO share (io.stat major:minor → array disk/pool identity via the Unraid collector), host CPU share, memory footprint, GPU engine busy share, mover/parity state.

**Detection:** a fixed library of explainable rules evaluated every 60s over rolling windows (live ring + 1m rollups). A rule fires only when BOTH sides are present — a victim's pressure signal AND a culprit's dominant share of the same contended resource in the same window. Correlation alone never fires. Rule categories:

- **Disk-IO contention (per device):** container X `io.pressure` sustained high while container Y drives ≥60% of that device's IO bps → "Y is starving X on disk3."
- **Array-operation impact:** parity check speed below its rolling baseline (median of past checks; first-ever check baselines on its own opening rate) while containers drive IO to array devices → "parity ~N% slower while Y writes to disk3." Also: container IO repeatedly waking / keeping array disks spun up.
- **CPU contention:** X `cpu.pressure` high (or `nr_throttled` rising) while Y holds a dominant host-CPU share.
- **Memory pressure:** host+container `memory.pressure` high with Y's footprint dominant; recent OOM events strengthen severity.
- **GPU engine contention:** an engine (e.g. video) near-saturated with 2+ containers active on it → "jellyfin and frigate are contending for the iGPU video engine."

**Each finding carries:** a one-sentence statement, severity, the evidence bundle (series ids, window, the actual numbers), and links to the relevant charts. Findings are stored as events (`insight.*` kinds) — history and chart markers come free.

**Anti-noise:** sustained-for windows, per-(victim, culprit, resource) cooldown, auto-resolve when pressure clears, and a confidence floor (attribution share + pressure magnitude thresholds). Insights must be rare enough to be read.

**Surfacing:** an Insights card on Overview + a dedicated Insights view (active + resolved history). High-severity insights can optionally notify through the §8 alert channels.

**Phasing:** the data enablers (PSI, per-device IO, throttle counters — see §4.1) land in **Phase 2** collection so every install accumulates the raw material from day one; the engine + UI are a distinct phase whose release slot (pre- vs post-CA-launch) is a scheduling decision, not a design one.

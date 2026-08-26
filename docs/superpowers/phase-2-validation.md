# Phase 2 on-box validation — 2026-08-25, Unraid 7.3.2, 38 running containers

Deployment: image built ON the box from the public branch (`docker build https://github.com/smidley/gantry.git#phase-2-collectors`), run with the exact CA-template posture — `--pid=host --cap-add=SYS_PTRACE`, docker.sock ro, `/sys→/host/sys` ro, `/var/local/emhttp→/unraid` ro, notify spool rw, `/config` volume, port 8380. No `--privileged`, default (private) cgroup namespace.

## Verdicts

| Check | Result |
|---|---|
| healthz sources | 6/8 "ok" (docker, docker-disk, gpu, host, selfstat, unraid); nvidia + pressure correctly unavailable with their designed enable-hint strings verbatim |
| Containers | 38 live, cpu/mem/net/io flowing; top-consumer ordering plausible against known workloads |
| Disks | 12 entities incl. pools + flash; a spun-down disk reported `spun_up=0` and — by design — **no** `temp.c` sample |
| Unraid | version 7.3.2 surfaced; mover + parity gauges present; share usage per share (values are array/pool-level totals per fixtures.md discrepancy 3) |
| **GPU per-container attribution** | **PASS** — during a live ffmpeg transcode: the owning container showed `gpu.render.busy_pct 99.7` / `gpu.video.busy_pct 37.5`, matching gpu-entity totals exactly (single active client) |
| HEALTHCHECK | `healthy` (exec-form probe on scratch, on-box) |
| Logs | pristine — a single startup line |
| Footprint (spec §2, pro-rated to 38 containers) | `gantry.cpu_pct` ≈ 0.5–0.6 sustained (budget ≤ ~2), `gantry.rss_bytes` ≈ 30MB (budget ≤ 100MB); docker-side snapshot 2.16% momentary / 47MiB incl. cache |

## Finding fixed during validation (the reason this task exists)

Per-container GPU attribution was initially EMPTY despite a busy GPU. Root cause, proven live: Gantry runs in docker's default **private cgroup namespace**, so `/proc/<pid>/cgroup` of a foreign container's process reads relativized — `0::/../<64hex>` — with the `docker/` component stripped; the extractor required a `docker[/-]` prefix and mis-bucketed every containerized client as host. Fixed zero-config in `cgroup.ContainerID` (bare 64-lowercase-hex path-segment fallback, `62f23da`) rather than adding a `--cgroupns=host` template flag. This also retroactively explains spike S1's "host-side DRM client" (see the correction note in spikes.md).

## Residuals

- `engine.render.busy_pct` occasionally reads ~100.001 (float overshoot on delta boundaries) — cosmetic; clamp with the Phase 3 formatting work.
- Soak beyond ~15 minutes not yet observed; the test container stays up for a longer unattended window before the CA release (Phase 4 pre-release checklist).
- nvidia collector remains hardware-unvalidated (no Nvidia device available); parser-level tests only.

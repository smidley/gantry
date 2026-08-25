# Spike results — 2026-08-25, Unraid 7.3.2 (kernel 6.18.38-Unraid), 40 running containers

Probe: `cmd/spikeprobe` run in an `alpine:3` container with the exact Gantry template flags — `--pid=host --cap-add=SYS_PTRACE`, `/sys:/host/sys:ro`, `/var/local/emhttp:/unraid:ro`, `/tmp/notifications:/notify`, docker.sock ro. **No `--privileged` at any point.**

## S1 — foreign-process DRM fdinfo readability: PASS

- One foreign i915 DRM client was readable (driver `i915`, client-id 940, pdev `0000:00:02.0`), zero unreadable PIDs. The access model — `pid=host` + `SYS_PTRACE`, read-only mounts, no privileged — is **proven**.
- Real i915 field inventory captured (now a parser fixture, `internal/collect/gpu/testdata/i915_unraid_7_3_2.txt`): engines report cumulative busy **nanoseconds** (`drm-engine-render/video/video-enhance/copy: <n> ns`) — so Phase 2 interprets ns deltas between ticks, the i915 path, not the xe cycles path. Memory reports as `drm-*-system0` / `drm-*-stolen-system0` regions (KiB) — iGPU shares system RAM; there is no VRAM metric on this hardware. `drm-client-id` present (dedupe key confirmed).
- **Open item for Phase 2:** end-to-end PID→container mapping validation. The one client observed mapped to no container (`/proc/<pid>/cgroup` had no docker id) and had exited before it could be identified; no containerized GPU client was live during either run. Containers with `/dev/dri` on this box: jellyfin, Tunarr, Optimisarr. The extractor's forms are unit-tested; validate live during an active jellyfin transcode when building the Phase 2 GPU collector. Also expect legitimate host-side DRM clients (whatever this was) — the collector must bucket unmapped clients as "host" rather than dropping them.

## S2 — dynamix notify-file format: PASS (human-verified)

- `alert.WriteNotify`'s format (timestamp/event/subject/description/importance lines, atomic rename into `unread/`) was accepted by dynamix as-is: Scott saw the "Gantry spike S2" notification in the Unraid UI moments after the drop. Format is pinned; no changes needed. File remains in `unread/` until acknowledged — normal dynamix behavior.

## S3 — cgroup v2 via /host/sys rbind: PASS

- `cgroup.controllers` readable; **40 container cgroups** found under `/host/sys/fs/cgroup/docker/<id>/`; `cpu.stat` readable (fields incl. `nr_throttled`, `throttled_usec` — the CPU-contention signal for insights). The cgroup v2 fast path is the default; the docker-API fallback stays for v1 boxes.

## PSI (insights enabler) — absent by default, one-line opt-in exists

- `/proc/pressure` and per-cgroup `*.pressure` files are absent on stock Unraid 7.3.2 (boot line: `append initrd=/bzroot`). Per the Unraid 7.1.0 release notes, the kernel builds PSI with `CONFIG_PSI_DEFAULT_DISABLED` — adding **`psi=1`** to the syslinux append line enables it.
- **Decision for spec §16:** insights run in two tiers. Without PSI (stock): victim signals = cpu.stat throttling counters, io.stat latency/queue proxies, OOM/health events; culprit attribution unchanged. With `psi=1` (documented opt-in in Gantry's README/panel hint): full pressure-based victim signals. The GPU/array insight categories are unaffected by PSI either way.

## Raw output

Full probe output archived in the SDD workspace (`spike-run-output.txt`) and reflected in the fixture file above.

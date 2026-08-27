# Gantry backlog (Scott-requested, not yet scheduled)

## Container interaction map (requested 2026-08-27)
A web view visualizing how containers interact with each other — e.g. an app container and its database container: one doing work causes CPU / network / storage activity in the other. A "nice visualization" of those relationships and live influence.

Notes for planning:
- This is the visual front-end of spec §16 (cross-container impact insights): the same victim/culprit signals (PSI when enabled, throttling, per-device IO attribution — collected since Phase 2) plus correlation over the live ring can drive edge weights.
- Candidate shape: a force/graph layout (containers as nodes sized by CPU, edges weighted by correlated activity / shared-resource contention), time-scrubbable; click an edge → the evidence (aligned charts of the two containers).
- Additional signal candidates beyond §16's: docker network inspect (shared user-defined networks = who CAN talk), correlated net.rx on A vs net.tx on B within the same tick window, shared volume mounts (who shares storage paths).
- Depends on: nothing new to collect for a correlation-only v1; §16's insight engine (Phase 5) would upgrade edges from "correlated" to "attributed".
- Slot: after Phase 4 (alerts + CA release) — candidate flagship feature for the Phase 5 insights release alongside the rules engine.

## Per-container storage panel (requested 2026-08-27)
Container detail should show WHAT storage a container touches and how much: a container can mount several different storage systems (array shares, cache pools, individual disks). Show each mount (source path → mapped storage system: `/mnt/user/<share>` → share+backing, `/mnt/cache*` → pool, `/mnt/diskN` → disk), plus this container's live IO per backing device.

Design notes:
- Mounts: docker inspect already returns Mounts; extend registry Meta with mount sources (collector change, cheap) and surface in ContainerDTO meta.
- Path→storage mapping: pure resolver from mount source prefix → unraid entity (share/pool/disk); unraid collector already knows the fleet.
- Per-device IO: already collected per container as `live:io.<dev>.*` (live-ring only, deliberately excluded from frames). Needs an exposure path: either a per-container `io_devices` map in ContainerDTO built from live ring, or an /api/series exception for the container's own detail view.
- Storage-used-per-mount: no cheap kernel source (du is expensive); v1 = capacity context from the backing system (share/pool usage) + container writable-layer size (docker DiskUsage), honestly labeled.

## Container→system impact surfacing (requested 2026-08-27)
"Container X has high storage IO which is causing CPU load" — surface how a container impacts the overall system, on its detail page.

Design notes:
- This is spec §16 (insights) arriving by user demand. Lean v1 for the detail page, before the full rules engine: an "impact" panel showing this container's current share of host CPU / net / disk IO (per-device attribution already collected), iowait correlation (host cpu iowait vs this container's IO — iowait not yet collected per-core; host collector parses /proc/stat which HAS iowait — expose host `cpu.iowait_pct`), and flags when its share of a device's IO exceeds a threshold ("driving 62% of disk3 writes").
- Full causal statements ("causing") need §16's two-sided rules (victim signal + culprit attribution) — Phase 5; the lean panel should be honestly correlational ("high IO alongside elevated CPU iowait"), never claim causation without the engine.
- Slot: both items pair naturally as one "container storage & impact" phase right after the current UX iteration settles — they share the mounts/attribution plumbing. The interaction-map view (above) then builds on the same foundations.

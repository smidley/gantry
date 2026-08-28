# Phase 3 carry-ins (from the Phase 2 final review triage, 2026-08-26)

Inputs for the Phase 3 plan — none block Phase 2, all triaged fix-in-phase-3 by the final whole-branch review.

## Identity & lifetime hygiene (one ticket)
- hwmon metric names not unique per chip instance (two NVMe "Composite" sensors collapse into one series) — add an instance disambiguator.
- One shared `slugSegment` helper (`[a-z0-9_-]`) for every metric-name segment (hwmon labels, share names, device names, engine names) — share names with spaces/dots currently fracture metric names.
- Key eviction: `RateTracker` never evicts (GPU client-id churn = O(uptime) growth), same shape in `loggedFallback` and `store.ids` — evict alongside `Live.Evict` and on GPU client drop.

## Performance (before SSE fan-out)
- `Live.Snapshot()` under a single lock (snapshot assembly currently takes N+1 read locks; SSE × 32 clients would multiply it).
- `Ring.Since` allocates full ring capacity per call — matters at 15-window flush catch-up (~86MB transient).
- GPU full scan: readlink `/proc/<pid>/fd/*` for `/dev/dri` before opening fdinfo (~100× fewer opens); same shape for `moverRunning`'s comm sweep.
- xe `drm-engine-capacity-*` keys must be skipped before the non-ns once-log burns.

## Correctness / API polish
- `SnapshotDTO.Unraid` flat map loses entity provenance (docker.* vs array) — add the entity dimension.
- Stopped containers linger in the snapshot with empty State until rings age out (15 min) — filter or mark.
- Container recreation while the event stream had a gap can evict the NEW container's rings (evict-by-name on vanished id) — guard on "another live container holds this name".
- Clamp engine busy % at 100 (float overshoot ~100.001 seen live).
- `handleContainers` builds the full DTO to return one field.

## Testing / CI
- `AppendEvent` errors ignored at every call site — errcheck; CI runs only go vet + gofmt, spec §12 wants golangci-lint.
- `TestRetentionFromConfig` never asserts R3.
- Migration ordering: sort by parsed version, not filename (zero-padding trap).
- Runner: `reprobeEvery`/`backoffFloor` are test-mutable package vars — comment the no-t.Parallel constraint or inject via Registry.
- Host-side PSI missing-file case untested.

## Known-unvalidated (Phase 4 pre-release checklist)
- Parity speed derivation factor (mdResyncDb/Dt × 1024) — confirm during the first real parity check.
- nvidia collector end-to-end (no hardware available); cadence already set to 15s.
- Long soak (>15 min observed so far); API-fallback boxes: N sequential stats calls per tick under the 10s deadline can downgrade to "failing" on slow daemons — correct signal, watch for it.
- `parity.finish` event gains duration with Phase 4's parity-result work.

## Metric semantic changes
- 2026-08-28: per-container `cpu.pct` redefined from docker-stats' own per-core percent (100% = one full core, could read >100% on a multicore box) to a host-share percent (that core usage ÷ the host's own core count) -- the "container says 100%, host says 30%" confusion report. New `cpu.cores` metric carries the old per-core-style number instead (= old cpu.pct ÷ 100). Samples recorded before this date under the old meaning were left as-is: dev-phase acceptable, no migration.
- `cpu.cores` carries no `live:` prefix, so it flushes to every history tier like any other metric -- deliberate, not an oversight: it's kept for a historical per-container cores chart later.

## Image maintenance (Gantry's first write feature, image-maintenance branch)
- New endpoints: `GET /api/images` (list + in-use/unused/dangling classification, join is docker's own ImageList+ContainerList by id, tag-priority then ImageID fallback -- see `classifyImages`); `POST /api/images/remove` (body `{"ids":[...]}`, full/short docker ids only, never names/tags); `POST /api/images/prune` (body `{"mode":"dangling"|"unused"}` -- dangling hands off to the daemon's own `ImagesPrune`, unused loops this same package's own classification through `ImageRemove` so there's one source of truth for "unused", not two).
- Guardrails on both POST routes: `X-Gantry-Confirm: images` header required (missing -> 428; forces a CORS preflight, closing the no-auth cross-origin POST hole) and `GANTRY_READ_ONLY=1` kill switch (blocks with 403, GET unaffected). Every successful deletion appends an `image.removed` event.
- Fake mode (`GANTRY_FAKE_DATA=1`) carries its own dozen-image inventory that remove/prune actually mutate in memory, so the UI flow this unblocks (queued separately) is fully exercisable without a real daemon.

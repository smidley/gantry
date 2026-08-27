# Gantry backlog (Scott-requested, not yet scheduled)

## Container interaction map (requested 2026-08-27)
A web view visualizing how containers interact with each other — e.g. an app container and its database container: one doing work causes CPU / network / storage activity in the other. A "nice visualization" of those relationships and live influence.

Notes for planning:
- This is the visual front-end of spec §16 (cross-container impact insights): the same victim/culprit signals (PSI when enabled, throttling, per-device IO attribution — collected since Phase 2) plus correlation over the live ring can drive edge weights.
- Candidate shape: a force/graph layout (containers as nodes sized by CPU, edges weighted by correlated activity / shared-resource contention), time-scrubbable; click an edge → the evidence (aligned charts of the two containers).
- Additional signal candidates beyond §16's: docker network inspect (shared user-defined networks = who CAN talk), correlated net.rx on A vs net.tx on B within the same tick window, shared volume mounts (who shares storage paths).
- Depends on: nothing new to collect for a correlation-only v1; §16's insight engine (Phase 5) would upgrade edges from "correlated" to "attributed".
- Slot: after Phase 4 (alerts + CA release) — candidate flagship feature for the Phase 5 insights release alongside the rules engine.

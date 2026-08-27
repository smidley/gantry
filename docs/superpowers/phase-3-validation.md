# Phase 3 on-box validation — 2026-08-27, Unraid 7.3.2, 38 running containers

Deployment: `gantry:phase3` built ON the box from the public branch, replacing the Phase 2 container (same CA-template posture, port 8380). Image **13.1MB** with the full embedded UI.

## Verdicts

| Check | Result |
|---|---|
| UI live on real data | All 8 views verified in a real browser against the box — Overview (tiles ticking, array card, fleet 38/0/0, hint banners verbatim), Containers (health dots, live rates, CPU-desc sort), Top Consumers (real ranking), Storage (12 disk cards w/ temps + high-usage chips, pools, parity card), GPU, Events, Settings |
| GPU per-container attribution in the UI | Optimisarr: render 99.7% / video 37.9% / total 100.0% in the attribution table; engine chart renders 4 correctly-colored lines after ring warm-up |
| Soak | **10 hours** unattended (unplanned but real): healthy healthcheck, exactly 1 log line, SSE reconnect clean |
| Footprint after 10h + live UI client | **0.4% CPU / 41.3MB RSS** (budget ≤2% / ≤100MB) |
| Themes | Dark + light captured (docs/screenshots/); light-mode legibility ruling re-checked OK |
| Mobile | 375px capture clean, TabBar nav, no horizontal scroll |
| Time axes | Browser-local (verified against box UTC offset) |

## Notes / Phase 4 polish
- GPU engine chart shows "no engine activity for this range" during the first few seconds of live-ring warm-up — misleading copy for a cold ring; a "collecting…" state would be better.
- selfstat reads "ok" on the box (the dev-Mac flapping is non-Linux-only, as ruled).
- Screenshots in docs/screenshots/ are the CA-listing/README asset candidates.

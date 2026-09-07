# Performance validation

The app loads route code on demand and gives fingerprinted assets immutable caching. Live data uses a shared stream; history queries have metric, range, point, and time budgets. Container seeding already limits concurrent requests, and the Metrics chart defaults to five ranked series. Avoid adding a permanent client cache or batch endpoint without a measured bottleneck.

Run the read-only workload tool against a representative installation:

```sh
python3 scripts/profile_app.py --base-url https://gantry.example.com --cookie-file /secure/session-cookie --viewers 4 --seconds 300 --history-hours 24 --out profile.json
```

The cookie file contains the Cookie header value from an authorized session; protect it with mode 0600 and remove it afterward. The tool does not print the cookie or base URL. An expired session appears as an HTTP error, not a successful timing. History receipts include returned point counts so an empty response cannot masquerade as a meaningful aged-data benchmark.

Measure 50- and 200-container fleets with 1, 4, and 16 viewers and databases aged to 24 hours, 30 days, and the configured archive duration. Include a Docker API fallback run (cgroup fast path unavailable), ordinary collection, missing-source recovery, and each supported GPU collector. Record:

- Snapshot, history, and ranking p50/p95, errors, bytes, and returned points.
- Gantry CPU and resident memory from the receipt, plus container/host process metrics.
- SQLite main file and WAL size before and after at least 24 hours of operation.
- Browser interaction delays, long tasks, dropped frames, and memory after 30 minutes on Overview, Containers, Compare, and Metrics.
- Request counts for a route transition and a window change, separating fresh fetches from canceled requests.

The script generates one request per viewer per second, rotating through those three endpoint types. It does not simulate SSE or rendering; keep the corresponding number of real dashboard tabs open when measuring sustained collection and drawing. Pause background jobs only if the same condition is recorded for every comparison.

Compare runs on the same hardware and data. Investigate history fan-out only if it contributes a material share of latency or query CPU; then evaluate in-flight deduplication or a bounded batch endpoint. Preserve cancellation, authentication boundaries, offscreen animation suspension, and reduced motion.

A local fake-data receipt validates the harness and catches obvious regressions. It does not establish performance on an actual Unraid array, GPU hardware, or a long-lived production database.

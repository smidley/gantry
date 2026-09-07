# Security and deployment

## Trust boundaries

The full Unraid installation gives the Gantry process root, Docker's default capabilities plus `SYS_PTRACE`, host PID visibility, and the raw Docker socket. `:ro` on a Unix socket mount does not filter Docker API methods. A compromised process with that socket can affect the host. `GANTRY_READ_ONLY=1` prevents application cleanup and webhook configuration; it is not process isolation. Keep housekeeping and privileged GPU attribution as deliberate installation choices. [Docker's security model](https://docs.docker.com/engine/security/) explains the daemon boundary.

Monitoring still exposes operationally sensitive information. Docker inspect responses can include environment variables, and logs may contain secrets. Keep authentication enabled even with the restricted intermediary.

## Monitoring-only deployment

[deploy/monitoring/compose.yml](../deploy/monitoring/compose.yml) runs Gantry as UID/GID 65532 with all capabilities dropped, a read-only root filesystem, and no host PID namespace. A separate HAProxy process owns the raw Docker socket, has no network interface, and exposes a Unix socket on a volume shared only with Gantry. Its [allowlist](../deploy/monitoring/haproxy.cfg) permits GET/HEAD inventory, stats, inspect, logs, events, and disk usage; every mutation and other path is denied. No external Docker TCP API is exposed. Configuration syntax follows the [HAProxy manual](https://docs.haproxy.org/3.2/configuration.html).

From the repository root, prepare the writable application directory and validate the configuration:

```sh
sudo install -d -o 65532 -g 65532 -m 0700 deploy/monitoring/config
docker compose -f deploy/monitoring/compose.yml config
docker compose -f deploy/monitoring/compose.yml up --build -d
docker compose -f deploy/monitoring/compose.yml logs gantry
```

Enter the setup code from the local logs, or preseed credentials through your deployment's secret provisioning. Pin built Gantry and proxy images to reviewed immutable digests for production; the example uses an explicit proxy release and builds Gantry from the checkout.

This profile preserves Docker inventory and API fallback statistics. It intentionally omits cleanup, host process inspection, GPU process attribution, and Unraid notification writes. Add the read-only Unraid state mount only on an Unraid host. Missing collectors are reported in Diagnostics. Host proc/sys mounts remain sensitive read access; reduce them further if those metrics are unnecessary.

The proxy should deny `POST /containers/create`, `DELETE /images/...`, `GET /containers/.../archive`, and encoded or traversal variants before contacting Docker. Its accepted inventory paths must continue to work when the Docker SDK is upgraded. CI checks both the allowlist and denial cases against a disposable mock daemon; do not validate denials by attempting destructive actions on a real daemon.

## Network, TLS, and proxy authentication

For a native process, set `GANTRY_BIND_ADDRESS=127.0.0.1` behind a same-host proxy. In Docker, publish only `127.0.0.1:8380:8380` and leave Gantry's internal listener on the bridge. The [nginx example](../deploy/nginx.conf.example) terminates TLS, forwards the scheme, and disables buffering for streaming responses. Set your hostname and certificate paths before use. Built-in authentication stays enabled with `GANTRY_AUTH=auto`.

`GANTRY_AUTH=proxy` disables Gantry's login. Enable it only after the proxy authenticates **all** paths, including APIs and streams, and the direct Gantry port is inaccessible from client networks. Do not rely on forwarded headers from untrusted direct clients. `GANTRY_AUTH=none` is intended only for isolated development or an explicitly trusted environment.

## Sessions and recovery

First-run setup requires an owner code emitted to local logs; `GANTRY_SETUP_CODE` can supply a provisioning code of at least 16 characters. A stored account disables setup. Preseeded credentials bypass first-run setup. Passwords use argon2id; cookies are HTTP-only, SameSite, and Secure on HTTPS. Sessions have an 8-hour idle timeout and a 24-hour absolute lifetime. Browser restoration can preserve a session cookie after closing. Logout, expiry, and credential changes revoke ongoing live and follow-log access.

To recover a lost account, set both `GANTRY_USERNAME` and `GANTRY_PASSWORD` through the local container configuration and restart. This replaces the login and revokes existing sessions. Remove the provisioning variables afterward if future changes should be managed in Settings; removing them keeps the stored account.

## Request and storage budgets

Headers are capped at 16 KiB and ordinary responses have a 30-second write budget. Mutation bodies are bounded to 1 MiB (authentication uses a smaller limit), must contain a single JSON value, and have a 10-second read deadline. History requests have bounded metric lists, time ranges, result counts, and a 15-second query budget. Log readers are limited to 16 concurrent requests; SSE has its own existing client cap. Streaming writes time out after 10 seconds of backpressure and are interrupted on session/server cancellation.

Incident records save at most 16 series with at most 120 points each. The first detection excerpt survives subsequent refreshes; downsampling preserves bucket extrema. This is bounded evidence, not a complete recording. Older CPU/memory incidents are labeled as predating the corrected host-percentage attribution; their original records are preserved. Settings reports SQLite allocated page size excluding the transient WAL, and the configured size cap is a budget rather than a prediction of exact retention.

## Dependency triage — reviewed September 7, 2026

`GO-2026-4887` / [GHSA-x744-4wpc-v9h2](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2) concerns Docker Engine authorization plugins and oversized request bodies. `GO-2026-4883` / [GHSA-pxq6-2prw-chj9](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9) concerns plugin privilege validation during installation. Both upstream fixes are in Engine 29.3.1.

The Go database marks the imported Docker 28.5.2 module broadly. Gantry imports the API client and does not run Docker's daemon, authorization plugin handlers, or plugin-installation service. This is a documented applicability exception for these two IDs on this exact client version, expiring December 7, 2026. It does not assert that an older **host daemon** is safe. Update the actual host Engine separately; changing Gantry's client cannot patch it.

CI scans reachable Go symbols, npm dependencies, and the built runtime image’s OS packages. Go module/package notices without a reachable vulnerable symbol remain visible in the log; they do not use an exception. New findings and expired exceptions fail the relevant check. The exception file is version-specific and must be re-reviewed during a Docker SDK upgrade. Container OS findings have no blanket ignore. Release builds already publish an SBOM and provenance.

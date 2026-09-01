<div align="center">

<img src="https://raw.githubusercontent.com/smidley/gantry/main/template/gantry-icon.png" alt="" width="88" height="88">

# Gantry

**A Docker and server monitor built for Unraid. One container. Set a username and password the first time you open it.**

[![License: MIT](https://img.shields.io/badge/license-MIT-2f7fe0)](LICENSE)
[![Container image](https://img.shields.io/badge/image-ghcr.io%2Fsmidley%2Fgantry-475569?logo=docker&logoColor=white)](https://github.com/smidley/gantry/pkgs/container/gantry)

</div>

Install it, open the web UI, set a username and password the first time, and the dashboard is live: every container's CPU, memory, network, disk IO and GPU; your array and pools; and cross-container insights that explain, in plain language, when one container is slowing another or the array.

Most self-hosted monitors are a hub plus a per-host agent you deploy and pair — Beszel, for example. Gantry is a single container. Nothing to pair, no agent to install, no cloud account, no external database. The one login is a single local account stored on your own box. It reads your Docker socket, `/sys`, and the same Unraid state files the webGUI reads, and keeps its own small history in SQLite.

Gantry monitors read-only — it reads your containers, disks and array and never changes your array configuration. The one thing it can change, only when you ask, is Docker housekeeping from the Maintenance view (clearing stopped containers and unused images), behind a confirmation and disabled entirely by a single switch.

> Gantry is pre-release and under active development. The Community Applications listing is on its way; the `docker run` below works today.

<div align="center">
<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/overview-light.png" alt="Gantry overview: containers, storage and cross-container insights at a glance" width="100%">
</div>

<details>
<summary><b>More screenshots</b></summary>
<br>

**Insights** — the interaction map names which container is slowing another, with the evidence behind it.

<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/insights-light.png" alt="Insights: the interaction map and a cross-container finding" width="100%">

**Metrics** — per-resource leaderboards over a multi-line chart of every container against the host total.

<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/metrics-light.png" alt="Metrics: per-resource leaderboards and the multi-line chart" width="100%">

**Storage** — the array and pools with parity status, and every drive as a card with usage, temperature and errors.

<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/storage-light.png" alt="Storage: array, pools and per-drive cards" width="100%">

**Containers** — the sortable, filterable fleet with per-container CPU, memory, network, disk IO and GPU.

<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/containers-light.png" alt="Containers: the sortable, filterable fleet table" width="100%">

**Maintenance** — reclaim space from unused images and clear stopped containers, every deletion behind a confirmation.

<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/maintenance-light.png" alt="Maintenance: image and container cleanup" width="100%">

**Mobile** — the overview as cards, with a bottom nav, on a phone.

<img src="https://raw.githubusercontent.com/smidley/gantry/main/docs/screenshots/overview-mobile-dark.png" alt="Gantry overview on mobile" width="320">

</details>


## Features

- **Live fleet view.** Every container's CPU, memory, network, disk IO, PIDs, uptime and GPU in one sortable, filterable table — cards on mobile — each with an inline sparkline and a health dot. Compose projects group automatically; save your own custom groups.
- **Per-container drill-down.** Click any container for multi-line history — CPU (with throttling and allocation limits), memory, network, disk IO, per-engine GPU and PSI — from live down to 30 days, with event markers, an anomaly banner, its storage placement, and an embedded log viewer.
- **Metrics.** Per-resource leaderboards (CPU, memory, network, disk IO, GPU) over Now / 1h / 24h / 7d, average or peak, above a multi-line chart that overlays every container against the host total.
- **Storage.** Array and pools with parity status, ETA, speed and recent history; every drive as a card with its role, media type, capacity, temperature and error count; shares and Docker storage; per-drive charts for IO, usage or temperature.
- **GPU.** Per-engine utilization (render, video, video-enhance, copy) with per-container attribution — Intel and AMD work out of the box via DRM `fdinfo`, no `/dev/dri` passthrough and nothing privileged; Nvidia is optional and adds VRAM.
- **Insights.** An explainable cross-container engine that states, in a full sentence, when one container is likely slowing another or the array — with a confidence level, an interaction map, and an evidence drawer showing the actual numbers (culprit IO share, device utilization, await, victim stall). It runs on proxy signals without PSI, and uses kernel-measured stall when you enable `psi=1`.
- **Alerts.** Threshold and event rules with real hysteresis — separate trip and clear thresholds, and separate sustain and clear windows — delivered to Unraid's own notification center and/or outbound webhooks, with dedup, re-notify, silencing and flap-guard. A firing alert quotes the matching insight when one exists.
- **Maintenance.** See which images have an update available (with changelog links; Unraid's own tooling does the actual update), reclaim space from unused and dangling images, and remove stopped containers — every deletion behind a confirmation dialog that itemizes exactly what will go. `GANTRY_READ_ONLY=1` turns all of it off.
- **Compare.** Select any set of containers and chart them together — synced multi-line charts per metric, always-live group totals, and groups you can save.
- **The rest.** A command palette, an always-on connection-health indicator, acknowledge / silence / dismiss with preset durations, a first-run username and password login, light and dark themes, a reduced-motion preference, and Gantry graphing its own CPU and memory so you can see exactly what it costs.

## Install

### Community Applications

Once Gantry is listed, open the **Apps** tab in Unraid, search **Gantry**, and click **Install**. The template pre-fills every mount and flag below, so a stock install needs no edits — install it, open the web UI, done.

### docker run

Everything the Community Applications template does, spelled out, so it can be reproduced or audited by hand:

```sh
docker run -d \
  --name=gantry \
  --label net.unraid.docker.icon=https://raw.githubusercontent.com/smidley/gantry/main/template/gantry-icon.png \
  --pid=host \
  --cap-add=SYS_PTRACE \
  --cap-drop=ALL \
  -p 8380:8380 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /sys:/host/sys:ro \
  -v /var/local/emhttp:/unraid:ro \
  -v /tmp/notifications:/notify:rw \
  -v /var/lib/docker/unraid-update-status.json:/updates/unraid-update-status.json:ro \
  -v /mnt/user/appdata/gantry:/config:rw \
  --restart=unless-stopped \
  ghcr.io/smidley/gantry:latest
```

Then open `http://<your-unraid-ip>:8380/`. The first time, you'll set a username and password.

### What each mount is for

| Purpose | Host path | Container path | Mode | Why |
|---|---|---|---|---|
| Web UI | — | — | TCP `8380` | The dashboard. |
| Docker socket | `/var/run/docker.sock` | `/var/run/docker.sock` | ro | Container inventory, stats, health, logs and events — and the channel for the opt-in Maintenance cleanup you trigger yourself. |
| Host sysfs | `/sys` | `/host/sys` | ro | hwmon sensors, GPU/DRM info, and the cgroup v2 fast path for per-container accounting. |
| Unraid state | `/var/local/emhttp` | `/unraid` | ro | Array status, parity progress, disk/pool/share info — the same files the Unraid webGUI reads. |
| Notifications | `/tmp/notifications` | `/notify` | **rw** | Lets Gantry hand alerts to Unraid's own notification center. |
| Update status | `/var/lib/docker/unraid-update-status.json` | `/updates/unraid-update-status.json` | ro, optional | Container update-available flags. Missing or omitted: the flags just don't show, nothing else breaks. |
| Config | `/mnt/user/appdata/gantry` | `/config` | **rw** | Gantry's own SQLite database and settings — the only place it stores anything persistent. |

`/notify` and `/config` are the only writable mounts; everything else is mounted read-only. The extra parameters are `--pid=host` (so Gantry can attribute GPU and resource usage to the right container), `--cap-add=SYS_PTRACE` (to read other containers' `/proc/<pid>/fdinfo`), and `--cap-drop=ALL` (every other Linux capability removed). Gantry does not run `--privileged` and does not use host networking.

The `update-status` line is the one mount you can drop if that file doesn't exist on your system. Images are `linux/amd64`; every tagged release publishes semver-tagged images to [ghcr.io/smidley/gantry](https://github.com/smidley/gantry/pkgs/container/gantry).

See **[docs/install.md](docs/install.md)** for the line-by-line reference.

## Sign in

Gantry requires a login. The first time you open it, a one-time setup screen asks you to create a username and password; every visit after that is a normal login. It's a single local account stored on your own box (the password is argon2id-hashed, never stored in the clear) — no cloud, no external service. Signing in lasts until you close your browser. See [docs/install.md](docs/install.md#authentication) for the full reference.

- **Preseed it (headless / Community Applications).** Set both `GANTRY_USERNAME` and `GANTRY_PASSWORD` (the password masked in the CA form) to create the login at first boot and skip the setup screen. Minimum 8 characters. Changing either later changes the login and signs out every session; removing them never turns authentication off.
- **Run it open.** `GANTRY_AUTH=none` turns authentication off entirely — only for a fully trusted network. `GANTRY_AUTH=proxy` turns Gantry's own login off for installs already behind an authenticating reverse proxy (authelia, SWAG, nginx `auth_request`). Any other value keeps the login required.

## Optional setup

All optional — Gantry works with none of them.

- **PSI.** Add `psi=1` to your flash boot device's syslinux append line and reboot. Optional; it sharpens the Insights engine with kernel-measured stall data. See **[docs/psi.md](docs/psi.md)**.
- **Nvidia GPU.** Add `--runtime=nvidia` to Extra Parameters and set `NVIDIA_VISIBLE_DEVICES=all`. Without both, the GPU panel simply shows an enable hint — never an error. (Intel and AMD need nothing extra.)
- **`GANTRY_READ_ONLY=1`.** Makes every write-capable path — the Maintenance cleanups and webhook-target configuration — refuse to run, for a strictly look-don't-touch monitor.

## Security

- **Read-only for monitoring.** Gantry reads container stats, logs and container/disk state, and never touches your array configuration.
- **Maintenance is the only exception, and it's opt-in.** The only writes Gantry can make are the cleanup you trigger from the Maintenance view: removing dangling and unused images, and stopped containers. Every deletion is behind a confirmation dialog, never force-removes, never touches a running container, and never removes volumes. Setting `GANTRY_READ_ONLY=1` disables all of it.
- **Least privilege.** Gantry never runs `--privileged`, never uses host networking, and drops every Linux capability except `SYS_PTRACE`.
- **Required login.** A username and password — set on first run — protect the whole UI and live stream; sessions are argon2id-backed and end when you close your browser. Every mutating request additionally requires a custom header, so a drive-by web page can't reach the write paths even when authentication is turned off. Run open only on a trusted network with `GANTRY_AUTH=none`, or delegate auth to a reverse proxy with `GANTRY_AUTH=proxy`. Gantry serves plain HTTP — put a TLS-terminating proxy in front if you expose it beyond a trusted LAN.

## How it's built

Gantry is a single static Go binary in a `scratch` image — no base OS, no shell, no package manager, no runtime dependencies. It embeds a Svelte SPA, streams updates over Server-Sent Events instead of polling, and keeps history in an embedded SQLite database with configurable retention. It also graphs its own CPU and memory in Settings, so its footprint is never a mystery.

## Documentation

- **[docs/install.md](docs/install.md)** — the full install reference: every mount and flag, the required login and first-run setup, PSI, Nvidia, read-only, and the proxy/none auth modes.
- **[docs/psi.md](docs/psi.md)** — what Pressure Stall Information is, what Gantry uses it for, and how to enable it on Unraid.
- **[CHANGELOG.md](CHANGELOG.md)** — what shipped in each release.

## License

MIT — see [LICENSE](LICENSE). Gantry is a personal open-source project. Bug reports and questions are welcome in the [issue tracker](https://github.com/smidley/gantry/issues).

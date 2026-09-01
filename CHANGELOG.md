# Changelog

All notable changes to Gantry are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); Gantry
uses [Semantic Versioning](https://semver.org/).

`release.yml` extracts a version's section from this file verbatim as
the GitHub Release body, so a section lands here under its own `##
[x.y.z]` heading before that tag is pushed, not after.

## [0.1.2] - 2026-09-01

### Fixed

- **The container no longer fails to start on a stock Unraid install.**
  Under `--cap-drop=ALL` the process couldn't write into the `/config`
  appdata folder Unraid creates for it -- that folder is owned by the
  array (`nobody:users`), not root -- so it couldn't create its SQLite
  database and exited at startup with `open store: unable to open
  database file (14)`. The Community Applications template and the
  documented `docker run` now add `--cap-add=DAC_OVERRIDE` alongside the
  existing flags; that's the one capability the write needs, and a fresh
  install works with no edits. (#37)
- **The GPU collector no longer floods the log when `nvidia-smi` can't
  run.** On an Nvidia box with `--runtime=nvidia`, `nvidia-smi` is mounted
  into the image but can't execute against Gantry's minimal `scratch`
  base, which ships no dynamic loader -- the kernel returns a misleading
  `no such file or directory` for the missing ELF interpreter. Gantry now
  detects that case once at startup and shows the GPU source as a quiet,
  still-visible "unavailable" hint, instead of logging a failed poll every
  cycle. Working GPU monitoring and CPU-only boxes are unaffected.
  Actually running `nvidia-smi` from the image is a separate change still
  in progress. (#38)

### Changed

- If Gantry still can't open its database at startup, the error now names
  the config directory and points at the `--cap-add=DAC_OVERRIDE` fix,
  instead of printing a bare SQLite code.

## [0.1.1] - 2026-08-31

### Changed

- **Authentication is now required** -- a behavior change from 0.1.0's
  optional, off-by-default password gate. On first launch with no stored
  credential, Gantry shows a one-time setup screen to create a username
  and password; every later visit is a normal login. To run without
  authentication -- a fully trusted network, or a box already behind an
  authenticating reverse proxy -- set `GANTRY_AUTH=none` (explicitly
  open) or `GANTRY_AUTH=proxy` (the proxy authenticates). An unknown
  `GANTRY_AUTH` value fails safe to required.
- **Sessions now end when you close your browser.** The session cookie
  carries no fixed lifetime, replacing 0.1.0's 7-day sliding / 30-day
  maximum window. Server-side backstops still expire an idle session
  after 8 hours and any session after 24 hours, so a never-closed kiosk
  browser can't stay signed in forever.
- Login now takes a username as well as a password. A wrong username
  costs the same as a wrong password, so neither can be probed by timing.

### Added

- `GANTRY_USERNAME`, alongside the existing `GANTRY_PASSWORD`, to preseed
  the credential at boot for headless or Community Applications installs.
  Both must be set; an incomplete pair is ignored (with a logged warning)
  and the first-run setup screen applies. Changing either variable
  behaves like a credential change and signs out all sessions; removing
  them never turns authentication off.
- Settings → Access now changes the username as well as the password.

### Migration

- A 0.1.0 install that had set a password keeps working: on first boot
  under 0.1.1 its password-only credential is migrated to the username
  `admin` (change it in Settings → Access). Nobody is locked out and no
  action is required.

## [0.1.0] - 2026-08-31

### Added

- Docker collector: container inventory, live stats, health, logs, and
  events, with per-container GPU attribution against the host's own PID
  table (no `--privileged`, no host networking).
- Unraid collector: array, parity, disks, pools, and shares, read
  directly from `/var/local/emhttp` -- no Unraid API dependency.
- Host collector: CPU, memory, network, and Pressure Stall Information
  (PSI), plus per-container cgroup v2 accounting.
- SQLite-backed history with configurable retention and live updates
  over Server-Sent Events -- the UI never polls.
- Web UI: Overview, Containers, Container detail, Metrics, Storage,
  GPU, Insights, Alerts, Maintenance, Compare, Events, and Settings.
  Responsive, light and dark, with an all-clear Overview that expands to
  fill the space when nothing needs attention, live-gliding charts, a
  command palette, and a connection-health indicator.
- Insights: an explainable cross-container engine that states in plain
  language when one container is likely slowing another or the array --
  with a confidence level, an interaction map, and an evidence drawer
  showing the actual culprit IO share, device utilization, await, and
  victim stall. Runs on proxy signals without PSI; upgrades to
  kernel-measured stall when `psi=1` is enabled. Never pages by default.
- Metrics: per-resource leaderboards (CPU, memory, network, disk IO,
  GPU) over Now/1h/24h/7d, average or peak, above a multi-line chart
  overlaying every container against the host total.
- Compare: chart any set of containers together -- synced multi-line
  charts per metric, live group totals, and saved groups. Compose
  projects group automatically.
- Update-available badges with changelog links (Unraid's own tooling
  does the actual update), and acknowledge/silence/dismiss with preset
  durations so a known condition stops nagging.
- Alert engine: user-editable threshold and event rules with hysteresis
  (sustained-for and clear-for windows), delivered through Unraid's own
  notification spool and outbound webhooks, with dedup, re-notify,
  silencing, and flap-guard throttling. Display thresholds and alert
  thresholds are driven by the same numbers.
- Container and image maintenance: remove stopped containers, prune
  dangling images, guarded by an explicit confirm header and
  `GANTRY_READ_ONLY`.
- Optional single-password login: off by default (zero-config stays
  zero-config, with a quiet Settings reminder), first-class when on --
  argon2id-hashed password set via Settings or the masked
  `GANTRY_PASSWORD` template variable, digest-stored sliding sessions
  (7d, 30d cap), rate-limited and event-audited login, a minimal
  unauthenticated `healthz`, and `GANTRY_AUTH=proxy` for setups whose
  reverse proxy already authenticates. Removing the template variable
  never reopens the box; only Settings (current password required)
  turns the gate off.
- Cross-site request protection on every mutating API route: a custom
  header (`X-Requested-With: gantry`, or the maintenance routes'
  existing confirm header) is required whether or not a password is
  set -- scripts add one header; forms and drive-by pages can't.
- `GANTRY_FAKE_DATA=1` exercises every feature above, including the full
  alert lifecycle, with no Docker socket or Unraid box required.
- Single `scratch`-based image published to `ghcr.io/smidley/gantry` on
  every `v*` tag, plus a Community Applications template for a
  zero-edit Unraid install.

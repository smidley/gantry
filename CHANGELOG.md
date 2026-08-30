# Changelog

All notable changes to Gantry are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); Gantry
uses [Semantic Versioning](https://semver.org/).

`release.yml` extracts a version's section from this file verbatim as
the GitHub Release body, so a section lands here under its own `##
[x.y.z]` heading before that tag is pushed, not after.

## [0.1.0] - Unreleased

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
- Nine-view web UI: Overview, Containers, Container detail, GPU,
  Storage, Top Consumers, Events, Settings, and Alerts. Responsive,
  light and dark.
- Alert engine: user-editable threshold and event rules with hysteresis
  (sustained-for and clear-for windows), delivered through Unraid's own
  notification spool and outbound webhooks, with dedup, re-notify,
  silencing, and flap-guard throttling. Display thresholds and alert
  thresholds are driven by the same numbers.
- Container and image maintenance: remove stopped containers, prune
  dangling images, guarded by an explicit confirm header and
  `GANTRY_READ_ONLY`.
- `GANTRY_FAKE_DATA=1` exercises every feature above, including the full
  alert lifecycle, with no Docker socket or Unraid box required.
- Single `scratch`-based image published to `ghcr.io/smidley/gantry` on
  every `v*` tag, plus a Community Applications template for a
  zero-edit Unraid install.

# Changelog

All notable changes to Gantry are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); Gantry
uses [Semantic Versioning](https://semver.org/).

`release.yml` extracts a version's section from this file verbatim as
the GitHub Release body, so a section lands here under its own `##
[x.y.z]` heading before that tag is pushed, not after.

## [0.1.9] - 2026-09-02

### Changed

- **Fleet blocks are smaller again, and stopped containers have their
  own section.** Blocks cap at about half their 0.1.8 size: a dense fleet
  (a few dozen containers) renders as clean, unlabeled squares with the
  name on hover, and names appear automatically on smaller fleets with
  room for them. Stopped containers sit in a muted "N stopped" group
  below the running grid, at the same block size, and the group is
  simply absent when nothing is stopped. (#62)
- **The CPU, memory, network, and disk-IO tiles now sit at the top of
  the Overview**, pinned as a full-width row above the status headline.
  The disk bay schematic moves down into the Customize band as a regular
  module -- drag it or hide it like the others (its height follows your
  disk count, so it has no size steps). A layout saved before this change
  loads as-is, with the schematic appearing in its default spot. (#62)

## [0.1.8] - 2026-09-02

### Added

- **The Overview's "needs you" area is now a count, not a list.** One or
  two chips -- "N alerts" and "N contentions" -- each a link to the list
  behind it: alerts land on the Events page, which now opens with a
  "Needs you" strip (the entity, a plain-language reason, and the Ack
  control with its 1h/24h/7d presets), and contentions land on Insights.
  Acknowledged items still stay out of the counts, and a healthy box
  shows the same all-clear as before. (#58, #59)
- **The container fleet fills the space it has and sizes its blocks to
  the fleet.** Three containers render large; forty render small --
  always square, and never scrolling until blocks would drop below a
  legible floor. Bigger blocks earn the container's name and icon. (#58)

### Changed

- **A fleet block now glows for any busy metric, not just CPU.** Memory,
  disk IO, network, or GPU can light it, the hottest wins, and hovering
  names the driver ("glowing: GPU 100%"). The floors are absolute, not
  relative to the rest of the fleet, so an idle machine stays calm. The
  Containers view's "Active now" filter uses the same rule, so the
  glowing blocks and that list always agree. (#58)
- The insight dismiss control reads **"Dismiss"** rather than "Not
  useful". (#60)

### Fixed

- **An incident chart's shaded band now covers the evidence, not just
  the aftermath.** A sustained rule only fires once its whole evaluation
  window has crossed the threshold, so the spike that caused an insight
  sits *before* the fired timestamp -- the band shaded fired-to-resolved
  and missed it. It now extends back over the rule's own window; Fired
  and Resolved stay marked so the seam is still visible. (#60)
- **The interaction map's legend lists only the line styles actually on
  screen.** A single-style graph in the evidence drawer shows no legend
  at all -- the drawer's own confidence badge already says it. (#60)

## [0.1.7] - 2026-09-02

### Added

- **The Overview is now yours to arrange.** A Customize mode on the
  modules band: drag the Top Consumers, Recent Events, and metric-tile
  cards to reorder them within and between the two columns; hide the
  ones you don't want (they wait in a tray, one click to bring back);
  drag the divider between the columns to change the split (the keyboard
  works too); and set the two list-shaped cards to compact, normal, or
  tall. Everything saves on the box itself, so the layout follows you to
  any browser, and Reset restores the shipped arrangement. A saved
  layout keeps working when a future release adds a card -- the new card
  simply appears in its default spot. The status headline and fleet
  strip stay pinned: "needs you" can't be buried. Editing is desktop-
  only for now; the saved order still applies to the stacked mobile
  view. (#52, #55)
- **An insight's evidence drawer now shows the incident visually.**
  Clicking an insight -- history or active -- shows the interaction map
  as of that insight's own moment (every insight active at the same
  time, the clicked one emphasized, drawn from stored records, so it
  works even for containers that no longer exist) and charts of what the
  metrics actually did: the victim's suffering signal and the culprit's
  driving signal across the incident window, with the active span shaded
  and Fired/Resolved marked. A window older than what retention still
  holds says so plainly instead of showing an empty chart. (#54, #56)

### Fixed

- **Following a container's logs no longer goes silent when the
  container restarts.** Docker ends a follow stream when a container
  exits, and Gantry never re-attached. A follow now resumes on its own --
  across plain restarts and across updates that re-create the container
  under the same name -- with no duplicated and no missing lines, and a
  marker line shows where the restart happened. (#53)

## [0.1.6] - 2026-09-01

Test-suite reliability only -- nothing about the shipped app changes.

### Fixed

- **The Playwright suite no longer flakes under full-suite parallel
  load.** Two one-shot reads were hardened: the metrics-gpu-sum spec
  re-requests its captured `/api/series` body through Playwright's own
  HTTP client instead of trusting Chromium's best-effort CDP body cache
  (the same eviction mode #46 hardened the live-seed specs against in
  0.1.5), and the CPU core-budget ribbon spec retries its width
  measurement via `toPass` instead of sampling once mid-glide, where a
  re-ranked segment lands at full width while its neighbors are still
  easing toward their new sizes. Both still fail hard on a real
  regression.

## [0.1.5] - 2026-09-01

### Fixed

- **A stale copy of the install template can no longer crash Gantry at
  first start.** The template hardened the container with
  `--cap-drop=ALL` and re-added the one capability Gantry needs to write
  its database into Unraid's array-owned appdata folder -- but a stale
  Community Applications copy of the template kept the drop while losing
  the re-add, and a fresh install with that copy exited immediately with
  `unable to open database file (14)`. The template and docs now keep
  Docker's default capability set (plus `SYS_PTRACE`), so there is no
  explicit re-add left to lose. Existing installs keep working as-is;
  the defaults trade a bit of extra hardening for an install that can't
  break this way -- negligible next to the Docker socket the container
  already mounts.
- **Demo mode starts reliably again.** `GANTRY_FAKE_DATA=1` created its
  fake notification spool in the system temp directory and treated
  failure as fatal, so on an image with no usable temp directory the
  container logged `build alert dispatcher: create fake notify dir` and
  exited before ever coming up. The spool now falls back to a
  `fake-notify` folder next to the database -- a path already proven
  writable -- and if even that can't be created, Gantry starts anyway and
  shows the same unwritable-spool hint a real box without a `/notify`
  mount gets. A demo aid can no longer take down startup.

## [0.1.4] - 2026-09-01

### Fixed

- **A chart's hover readout no longer sticks after the pointer leaves.**
  On a container's detail page the metric charts share one crosshair, and
  a live update repaints them every frame. Leaving a chart could lose a
  race against that repaint and strand the "N ago" readout, which then
  reappeared and held on every chart with the mouse well away. The scrub
  now releases to live as soon as the pointer is off every chart.
- **The Community Applications template can be refreshed again.** Its
  `<TemplateURL>` pointed at the wrong path and returned 404, which
  stopped Unraid from ever pulling template updates. Nothing about an
  installed container changes; only the template's own update check.

## [0.1.3] - 2026-09-01

### Added

- **The Unraid server name now shows at the top of the sidebar**, so if
  you run Gantry on more than one box you can tell at a glance which one
  you're looking at. It reads the Unraid server identity from `var.ini`,
  falling back to the container's host hostname; when neither is
  available the sidebar reads exactly as before. (#39)

### Changed

- **The image is now built on a minimal
  [distroless](https://github.com/GoogleContainerTools/distroless) base**
  (`gcr.io/distroless/base-debian12`) instead of `scratch`, so Nvidia GPU
  monitoring actually works. The `nvidia-smi` the Nvidia container runtime
  injects (`--runtime=nvidia`) is a glibc, dynamically-linked binary, and
  `scratch` had no dynamic loader to exec it -- the gap 0.1.2 papered over
  by degrading the GPU source to a quiet "unavailable" hint (#38). The
  distroless base carries a glibc loader, so `nvidia-smi` now runs and
  Gantry reports per-container Nvidia VRAM. It stays one image for every
  GPU vendor -- Intel and AMD (DRM `fdinfo`, read in-process) and CPU-only
  boxes are unaffected -- and keeps the same single static binary and the
  same hardened surface: no shell, no package manager, nothing to log
  into. The image grows from roughly 14 MB to roughly 35 MB. Nvidia setup
  is unchanged (`--runtime=nvidia` plus `NVIDIA_VISIBLE_DEVICES=all`, no
  special tag); see [docs/nvidia.md](docs/nvidia.md). (#38)

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

# Installing Gantry

Gantry ships as a single container with no external dependencies. The
Community Applications template (`templates/gantry.xml`) pre-fills every
mount and flag below so a stock install needs zero edits. This page exists
so you can read exactly what the template does, line by line, before you
trust it with your Docker socket.

The template's icon (`template/gantry-icon.png`) renders from
`assets/icon/gantry.svg` -- edit the SVG and re-export if it ever needs
to change.

## What it mounts, and why

| Purpose | Host path | Container path | Mode | Why |
|---|---|---|---|---|
| Web UI | -- | -- | TCP `8380` | The dashboard. |
| Docker socket | `/var/run/docker.sock` | `/var/run/docker.sock` | ro | Container inventory, stats, health, logs, events. Read-only for monitoring; also how the Maintenance view removes dangling images and stopped containers when you ask it to (confirmable, off under `GANTRY_READ_ONLY`). |
| Host sysfs | `/sys` | `/host/sys` | ro | hwmon sensors, GPU/DRM info, the cgroup v2 fast path. |
| Unraid state | `/var/local/emhttp` | `/unraid` | ro | Array status, parity progress, disk/pool/share info -- the same files the Unraid webGUI reads. |
| Notifications | `/tmp/notifications` | `/notify` | **rw** | The only other read-write mount. Lets Gantry hand alerts to Unraid's own notification center. |
| Update status | `/var/lib/docker/unraid-update-status.json` | `/updates/unraid-update-status.json` | ro, optional | Container update-available flags. Missing or omitted: the flags just don't show, nothing else breaks. |
| Config | `/mnt/user/appdata/gantry` | `/config` | **rw** | Gantry's own SQLite database and settings. The only place it stores anything persistent. |

Everything is read-only except the notifications mount and the config
mount. Gantry does not run `--privileged`, does not use host networking,
and needs no cloud account. It does require a login -- a single local
username and password you set the first time you open it (see
[Authentication](#authentication)).

## Extra parameters

```
--pid=host --cap-add=SYS_PTRACE --cap-drop=ALL
```

- `--pid=host` -- shares the host's PID namespace so Gantry can attribute
  GPU and resource usage to the right container instead of falling back to
  host-only totals.
- `--cap-add=SYS_PTRACE` -- needed to read other containers' `/proc/<pid>/fdinfo`.
- `--cap-drop=ALL` -- every other Linux capability the container would
  otherwise get by default is removed. This is the one capability Gantry
  actually needs, not the default set.

## The equivalent `docker run`

Everything the CA template does, spelled out, so it can be reproduced (or
audited) by hand:

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

The update-status line is the one mount you can safely drop if that file
doesn't exist on your system -- Gantry degrades to simply not showing
update-available flags.

The `--label` line is the icon Unraid's Docker page (and Gantry's own
Containers view) shows for the container. A CA install sets it
automatically from the template's `<Icon>`; a hand-run container only
gets it if you spell it out.

## Optional: PSI (Pressure Stall Information)

Not a container setting -- a host kernel feature, off by default on stock
Unraid. Enabling it gives Gantry fuller CPU/memory/IO stall metrics. See
[docs/psi.md](psi.md) for the one-line syslinux change and how to verify
it's on. Nothing on the container side needs to change either way.

## Optional: Nvidia GPU

If you have an Nvidia GPU passed through with the Nvidia Driver plugin,
add `--runtime=nvidia` to Extra Parameters and set `NVIDIA_VISIBLE_DEVICES=all`.
Without both, the GPU panel simply shows its enable hint -- never an error.

## Authentication

Gantry requires a login. It's a single local account -- a username and a
password -- stored on your own box; there's no cloud account and no
external service. There are two ways the credential gets set:

- **First-run setup (the default).** The first time you open a box with
  no credential, Gantry shows a one-time setup screen: pick a username
  and a password (minimum 8 characters), and it signs you straight in.
  Every visit after that is a normal login. Change either later in
  **Settings → Access** (the current password is required; a change
  signs out every other session).
- **Preseed with `GANTRY_USERNAME` + `GANTRY_PASSWORD`** (the password
  masked in the CA form), for a headless or Community Applications
  install. Set **both** to create the login at first boot and skip the
  setup screen. They're applied at every container start: changing
  either changes the login and signs out every session; an unchanged
  pair changes nothing. **Both are required together** -- only one set
  (or a password under 8 characters) is ignored with a log line, and the
  box falls back to the setup screen rather than booting half-configured.

The first-run setup endpoint (`POST /api/auth/setup`) is reachable
without a session -- there's nothing yet to authenticate against -- but
only until a credential exists; afterward it answers 409. During that
first-boot window everything else is already gated (a data route with no
session gets a 401, which is exactly what shows the setup screen), and a
drive-by web page still can't reach it: every mutating route requires a
custom header no cross-site page can set. Set the credential promptly on
a network you don't fully trust.

One deliberate asymmetry: **removing `GANTRY_USERNAME`/`GANTRY_PASSWORD`
does NOT turn authentication off.** The stored login stays; auth is
mandatory. A template edit, a copy-paste of someone else's template, or
a CA update must never silently reopen the dashboard. To run without a
login, use `GANTRY_AUTH=none` below -- an explicit choice, not a side
effect. (A change made in Settings while the variables are still set
lasts only until the next restart re-applies them, and the Access card
says so.)

Upgrading from a 0.1.0 install that had a password set? It keeps
working: the password-only credential is migrated to the username
`admin` on first boot under 0.1.1 (change it in Settings → Access).
Nobody is locked out.

What the login actually does:

- Every API route and the live stream require a session. The session
  cookie is a **session cookie** (`HttpOnly`, `SameSite=Lax`, `Secure`
  when served through a TLS-terminating proxy, and no fixed lifetime),
  so it's cleared **when you close your browser**. Server-side backstops
  still expire an idle session after 8 hours and any session after 24
  hours, so a never-closed kiosk browser can't stay signed in forever.
  The token is stored only as a SHA-256 digest and the password only as
  an argon2id hash; the username is stored as entered (it isn't a
  secret).
- `GET /api/healthz` stays reachable without a session -- the Docker
  HEALTHCHECK and reverse-proxy checks depend on it -- but answers an
  unauthenticated caller `{"status":"ok"}` and nothing else. Version,
  uptime, and the per-source detail strings appear only once logged in.
- Login is rate-limited (5/minute per address, 20/minute overall) and
  audited: `auth.login_ok` / `auth.login_failed` events show up in the
  Events view, failures coalesced per address so a guessing run can't
  flood the feed. A wrong username costs exactly what a wrong password
  does, so neither can be probed by timing. The 20/minute ceiling is
  shared across every address, so a flood of guesses can temporarily
  block your own login too while it's happening. There is deliberately
  no hard lockout -- on a LAN, a lockout is a lever anything on the
  network could pull against you; the refilling limit bounds guessing
  just as well and recovers alone.
- The SPA and any script calling the API must send a custom header on
  mutating requests (enforced whether or not authentication is on -- it
  is what makes cross-site request forgery a dead end):

  ```sh
  curl -X POST http://tower:8380/api/auth/login \
    -H 'X-Requested-With: gantry' \
    -H 'Content-Type: application/json' \
    -d '{"username":"...","password":"..."}' -c cookies.txt
  curl -X PUT http://tower:8380/api/settings -b cookies.txt \
    -H 'X-Requested-With: gantry' \
    -H 'Content-Type: application/json' -d '{"retention":{...}}'
  ```

Gantry itself serves plain HTTP. On a trusted LAN that's the normal
deployment; if you expose it beyond one, put a TLS-terminating reverse
proxy in front rather than sending the password over cleartext hops.

### `GANTRY_AUTH=none`

The explicit opt-out: set `GANTRY_AUTH=none` and Gantry runs with **no
authentication at all** -- no setup screen, no login, the whole
dashboard open to anyone who can reach it. Only for a fully trusted
network. The custom-header cross-site defense still applies, so a
drive-by page still can't reach the write paths, but anyone on the
network can. This is the only way to run Gantry open.

### `GANTRY_AUTH=proxy`

For installs where a reverse proxy (authelia, SWAG, an nginx
`auth_request` setup) already authenticates every request before Gantry
sees it: set `GANTRY_AUTH=proxy` and Gantry's own gate switches off --
no setup or login screen, and the built-in credential routes answer 409
so the inert gate can't be mistaken for a working one. Any other value
of `GANTRY_AUTH` is logged and treated as `auto` (login required) -- an
auth typo fails closed, never open.

## `GANTRY_READ_ONLY`

Off by default. Set to `1` to make Gantry's write-capable paths (docker
mutations, webhook-target configuration) refuse to run, for anyone who
wants a strictly read-only monitor.

Read-only and the login are orthogonal by design: `GANTRY_READ_ONLY`
limits what a logged-in user can do, the login limits who gets in at
all. In particular, changing the username or password still works in
read-only mode -- being unable to *secure* a read-only box would invert
the switch's purpose. Use both for a locked, look-don't-touch monitor.

## Known gaps as of this template (pre-release checklist)

- **Support URL is a placeholder.** `<Support>` currently points at the
  GitHub issue tracker because no Unraid forum support thread exists yet.
  Create the thread and update `<Support>` in `templates/gantry.xml` before
  CA registration (see the design spec's packaging section).
- **Not yet dry-run installed on a real box.** This template has not been
  installed from a local path and confirmed to need zero edits -- that
  verification belongs to the on-box validation checklist, not this
  change.

# Installing Gantry

Gantry ships as a single container with no external dependencies. The
Community Applications template (`template/gantry.xml`) pre-fills every
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
| Docker socket | `/var/run/docker.sock` | `/var/run/docker.sock` | ro | Container inventory, stats, health, logs, events. Gantry never issues docker commands through it. |
| Host sysfs | `/sys` | `/host/sys` | ro | hwmon sensors, GPU/DRM info, the cgroup v2 fast path. |
| Unraid state | `/var/local/emhttp` | `/unraid` | ro | Array status, parity progress, disk/pool/share info -- the same files the Unraid webGUI reads. |
| Notifications | `/tmp/notifications` | `/notify` | **rw** | The only other read-write mount. Lets Gantry hand alerts to Unraid's own notification center. |
| Update status | `/var/lib/docker/unraid-update-status.json` | `/updates/unraid-update-status.json` | ro, optional | Container update-available flags. Missing or omitted: the flags just don't show, nothing else breaks. |
| Config | `/mnt/user/appdata/gantry` | `/config` | **rw** | Gantry's own SQLite database and settings. The only place it stores anything persistent. |

Everything is read-only except the notifications mount and the config
mount. Gantry does not run `--privileged`, does not use host networking,
and needs no account of any kind. A password is optional -- off by
default, one template variable or a Settings form away when you want it
(see [Optional: password protection](#optional-password-protection)).

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

## Optional: PSI (Pressure Stall Information)

Not a container setting -- a host kernel feature, off by default on stock
Unraid. Enabling it gives Gantry fuller CPU/memory/IO stall metrics. See
[docs/psi.md](psi.md) for the one-line syslinux change and how to verify
it's on. Nothing on the container side needs to change either way.

## Optional: Nvidia GPU

If you have an Nvidia GPU passed through with the Nvidia Driver plugin,
add `--runtime=nvidia` to Extra Parameters and set `NVIDIA_VISIBLE_DEVICES=all`.
Without both, the GPU panel simply shows its enable hint -- never an error.

## Optional: password protection

Gantry is a LAN appliance and ships open: install it, open the UI, done.
Settings shows a quiet reminder -- "no password set — anyone on your
network can view and manage this server" -- until you either accept that
trade or set a password. Two ways to set one:

- **The `GANTRY_PASSWORD` template variable** (masked in the CA form).
  Applied at every container start; changing the variable changes the
  password and signs out every session; an unchanged variable changes
  nothing. Minimum 8 characters -- a shorter value is rejected with a
  log line and the container boots open rather than half-locked.
- **Settings → Access**, entirely in the UI. Setting a password keeps
  the browser that set it signed in; changing it later requires the
  current password and signs out every other session.

One deliberate asymmetry to know about: **removing `GANTRY_PASSWORD`
from the template does NOT turn the password off.** The stored password
stays until you turn it off in Settings → Access (current password
required). This is intentional -- a template edit, a copy-paste of
someone else's template, or a CA update must never silently reopen the
dashboard. Same idea in the other direction: a change made in Settings
while the variable is still set lasts only until the next restart
re-applies the variable, and the Access card says so.

What the lock actually does:

- Every API route and the live stream require a login session. Sessions
  are 256-bit random cookies (`HttpOnly`, `SameSite=Lax`, `Secure` when
  served through a TLS-terminating proxy), valid for 7 days per visit
  and 30 days at the absolute most; the token is stored server-side
  only as a SHA-256 digest, and the password only as an argon2id hash.
- `GET /api/healthz` stays reachable without a session -- the Docker
  HEALTHCHECK and reverse-proxy checks depend on it -- but answers an
  unauthenticated caller `{"status":"ok"}` and nothing else. Version,
  uptime, and the per-source detail strings appear only once logged in.
- Login is rate-limited (5/minute per address, 20/minute overall) and
  audited: `auth.login_ok` / `auth.login_failed` events show up in the
  Events view, failures coalesced per address so a guessing run can't
  flood the feed. There is deliberately no hard lockout -- on a LAN,
  a lockout is a lever anything on the network could pull against you;
  the refilling limit bounds guessing just as well and recovers alone.
- The SPA and any script calling the API must send a custom header on
  mutating requests (this is enforced with or without a password --
  it is what makes cross-site request forgery a dead end):

  ```sh
  curl -X POST http://tower:8380/api/auth/login \
    -H 'X-Requested-With: gantry' \
    -H 'Content-Type: application/json' \
    -d '{"password":"..."}' -c cookies.txt
  curl -X PUT http://tower:8380/api/settings -b cookies.txt \
    -H 'X-Requested-With: gantry' \
    -H 'Content-Type: application/json' -d '{"retention":{...}}'
  ```

Gantry itself serves plain HTTP. On a trusted LAN that's the normal
deployment; if you expose it beyond one, put a TLS-terminating reverse
proxy in front rather than sending the password over cleartext hops.

### `GANTRY_AUTH=proxy`

For installs where a reverse proxy (authelia, SWAG, an nginx
`auth_request` setup) already authenticates every request before Gantry
sees it: set `GANTRY_AUTH=proxy` and Gantry's own gate switches off --
no login screen, no Settings nudge, and the built-in password routes
answer 409 so the inert gate can't be mistaken for a working one. Any
other value of `GANTRY_AUTH` is logged and treated as `auto` (the
password-controlled default) -- an auth typo fails closed, never open.

## `GANTRY_READ_ONLY`

Off by default. Set to `1` to make Gantry's write-capable paths (docker
mutations, webhook-target configuration) refuse to run, for anyone who
wants a strictly read-only monitor.

Read-only and the password are orthogonal by design: `GANTRY_READ_ONLY`
limits what a logged-in (or open-mode) user can do, `GANTRY_PASSWORD`
limits who gets in at all. In particular, password set/change/disable
still work in read-only mode -- being unable to *secure* a read-only
box would invert the switch's purpose. Use both for a locked,
look-don't-touch monitor.

## Known gaps as of this template (pre-release checklist)

- **Support URL is a placeholder.** `<Support>` currently points at the
  GitHub issue tracker because no Unraid forum support thread exists yet.
  Create the thread and update `<Support>` in `template/gantry.xml` before
  CA registration (see the design spec's packaging section).
- **Not yet dry-run installed on a real box.** This template has not been
  installed from a local path and confirmed to need zero edits -- that
  verification belongs to the on-box validation checklist, not this
  change.

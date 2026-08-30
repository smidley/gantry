# Installing Gantry

Gantry ships as a single container with no external dependencies. The
Community Applications template (`template/gantry.xml`) pre-fills every
mount and flag below so a stock install needs zero edits. This page exists
so you can read exactly what the template does, line by line, before you
trust it with your Docker socket.

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
and does not need a password or account of any kind.

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

## `GANTRY_READ_ONLY`

Off by default. Set to `1` to make Gantry's write-capable paths (docker
mutations, webhook-target configuration) refuse to run, for anyone who
wants a strictly read-only monitor.

## Known gaps as of this template (pre-release checklist)

- **Icon art is a placeholder.** `template/gantry-icon.png` is a plain
  flat-color monogram, not designed brand art. Replace it before
  submitting to Community Applications.
- **Support URL is a placeholder.** `<Support>` currently points at the
  GitHub issue tracker because no Unraid forum support thread exists yet.
  Create the thread and update `<Support>` in `template/gantry.xml` before
  CA registration (see the design spec's packaging section).
- **Not yet dry-run installed on a real box.** This template has not been
  installed from a local path and confirmed to need zero edits -- that
  verification belongs to the on-box validation checklist, not this
  change.

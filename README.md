# Gantry

A Docker and server monitor built for Unraid. One container. Zero configuration.

> Status: pre-release, under active development.

Design spec: [docs/superpowers/specs/2026-08-25-gantry-design.md](docs/superpowers/specs/2026-08-25-gantry-design.md)

## Run

```sh
docker run -d \
  --name gantry \
  --label net.unraid.docker.icon=https://raw.githubusercontent.com/smidley/gantry/main/template/gantry-icon.png \
  -p 8380:8380 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /var/local/emhttp:/unraid:ro \
  -v /mnt/user/appdata/gantry:/config \
  ghcr.io/smidley/gantry:latest
```

This is the minimal mount set for container and Unraid array/disk
visibility. A few more optional mounts and flags unlock per-container
GPU attribution and native Unraid notifications for the alert engine —
the full reference and a Community Applications template are coming
with the Phase 4 release.

Every tagged release (`v*`) publishes semver-tagged images (`linux/amd64`
only) to [ghcr.io/smidley/gantry](https://github.com/smidley/gantry/pkgs/container/gantry).

## Documentation

- [Changelog](CHANGELOG.md) — what shipped in each release.
- [PSI (Pressure Stall Information)](docs/psi.md) — what it is, what Gantry uses it for, and how to enable it on Unraid.

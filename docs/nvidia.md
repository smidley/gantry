# Nvidia GPUs

Gantry reports Nvidia GPU usage -- host-level utilization and memory, and
per-container VRAM -- on the same single image everyone else runs. This
page explains why the image is built the way it is, how to turn Nvidia
support on, and how to confirm it on real hardware.

Intel and AMD GPUs need none of this: Gantry reads their per-engine usage
straight from DRM `fdinfo` in its own process, with no child process and
no extra runtime. This page is only about Nvidia.

## Why the image carries a glibc base

Nvidia's stack doesn't expose GPU stats through `fdinfo`. The usual way
to read them is to run `nvidia-smi`, and on Unraid that binary isn't
baked into the image -- the Nvidia container runtime *injects* it (along
with the driver libraries) at container start, when you pass
`--runtime=nvidia`. The injected `nvidia-smi` is an ordinary glibc,
dynamically-linked executable: to run, the kernel first has to load its
ELF interpreter, `/lib64/ld-linux-x86-64.so.2`.

Gantry used to ship on `scratch` -- an empty image with nothing but the
Gantry binary (which is fine, because Gantry is a single static Go binary
that needs no loader itself). But `scratch` has no
`/lib64/ld-linux-x86-64.so.2`, so the moment Gantry tried to exec the
injected `nvidia-smi`, the kernel failed to find the interpreter and the
call died with `ENOENT` -- "no such file or directory", even though the
binary was right there. That is [issue #38]: the probe saw `nvidia-smi`
on `PATH` and reported the GPU as available, but every query failed.

The fix is to give the image a real dynamic loader. Gantry is now built
on `gcr.io/distroless/base-debian12` -- a minimal glibc/Debian userland
with the loader and libc present, but still no shell and no package
manager. The mounted `nvidia-smi` now has an interpreter to run under.
The image grows from roughly 14 MB to roughly 35 MB; nothing else about
it changes.

A musl base such as Alpine can't do this job: its loader is
`/lib/ld-musl-x86_64.so.1`, and musl is not ABI-compatible with the glibc
`nvidia-smi`, so the same `ENOENT` happens there as on `scratch`.

[issue #38]: https://github.com/smidley/gantry/issues/38

## Enabling it

1. Install the **Nvidia Driver** plugin from Community Applications and
   reboot if it asks you to. Confirm the host itself sees the card:

   ```sh
   nvidia-smi
   ```

   You should get the usual table of GPUs. If this fails, fix it before
   touching Gantry -- Gantry can only ever see what the host driver does.

2. On the Gantry container, add to **Extra Parameters**:

   ```
   --runtime=nvidia
   ```

   and set the variable:

   ```
   NVIDIA_VISIBLE_DEVICES=all
   ```

   (`all`, or a comma-separated list of GPU UUIDs to expose only some.)

That's the whole change. No special image or tag -- the standard image
already carries the loader. Without both settings the GPU panel just
shows an enable hint, never an error.

## What you get

- **Host GPU utilization and memory**, on the Metrics and GPU views, for
  the `nvidia0` entity.
- **Per-container VRAM** for any container running a GPU process, on that
  container's own drill-down and the GPU leaderboard. Attribution is by
  process: Gantry maps each compute process's PID to its owning container
  through `/proc/<pid>/cgroup` (the same host PID table it already uses,
  which is why `--pid=host` is in the standard template).

Two honest limits of the current version:

- **Per-container Nvidia data is VRAM and presence only** -- there is no
  per-container SM-utilization figure. `nvidia-smi`'s per-process CSV
  doesn't carry one; the host-level utilization gauge does.
- **The host-level gauge reads the first GPU only.** On a multi-GPU box
  (for example a Quadro P400 alongside a Tesla P4) the `nvidia0`
  utilization and memory gauges reflect GPU 0; per-container VRAM is still
  attributed correctly across every GPU. Multi-GPU host gauges are a
  later phase.

## Validating on real hardware

These steps are for someone with an actual Nvidia box (thank you). They
confirm the two things that can't be checked without a GPU: that the
injected `nvidia-smi` now execs inside the image, and that Gantry turns
its output into per-container GPU stats.

**0. Baseline -- the host driver works.**

```sh
nvidia-smi -L
```

Expect one line per GPU (e.g. `GPU 0: Quadro P400 (UUID: GPU-...)` and
`GPU 1: Tesla P4 (UUID: GPU-...)`).

**1. Get the image.** Once v0.1.3 is published, pull it:

```sh
docker pull ghcr.io/smidley/gantry:0.1.3
```

Validating before it's published? Build the branch instead:

```sh
git clone https://github.com/smidley/gantry
cd gantry && git checkout nvidia-image
docker build -t ghcr.io/smidley/gantry:0.1.3 .
```

**2. Run it with the Nvidia runtime.** This is the standard Unraid
`docker run` with two additions -- `--runtime=nvidia` and
`NVIDIA_VISIBLE_DEVICES=all`:

```sh
docker run -d \
  --name=gantry \
  --runtime=nvidia \
  -e NVIDIA_VISIBLE_DEVICES=all \
  --pid=host \
  --cap-add=SYS_PTRACE \
  -p 8380:8380 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /sys:/host/sys:ro \
  -v /var/local/emhttp:/unraid:ro \
  -v /tmp/notifications:/notify:rw \
  -v /mnt/user/appdata/gantry:/config:rw \
  --restart=unless-stopped \
  ghcr.io/smidley/gantry:0.1.3
```

**3. Prove the loader works -- the core check.** Run the injected
`nvidia-smi` from *inside* the container:

```sh
docker exec gantry nvidia-smi
```

On the old `scratch` image this died with "no such file or directory".
It should now print the full GPU table. That single command is the
whole point of this change: a glibc `nvidia-smi` executing inside
Gantry's image.

**4. Check Gantry's own view.**

```sh
curl -s http://localhost:8380/api/healthz | grep -o '"nvidia":"[a-z-]*"'
```

Expect `"nvidia":"ok"` (not `"not-applicable"` and not a detail string).

**5. See it in the UI.** Open `http://<your-unraid-ip>:8380/`, sign in,
and open the **GPU** view. The `nvidia0` GPU should show live
utilization and memory.

**6. Confirm per-container attribution.** Start (or already have) a
container that actually uses the GPU -- a Plex/Jellyfin transcode, an
Ollama or other CUDA workload -- and give it a moment of real GPU work.
On the GPU leaderboard and that container's drill-down, its Nvidia VRAM
should appear. No GPU workload running is not a failure; it just means
there's nothing to attribute yet, and only the host `nvidia0` gauges
move.

**What to report back:** the output of step 3 (or the error, if any),
whether `healthz` shows `"nvidia":"ok"`, and whether a known GPU workload
showed up against the right container in step 6. Those three answers
cover everything the no-hardware proof below can't.

## Appendix: proving the loader fix without a GPU

You don't need an Nvidia card to prove the *loader* half -- only to prove
the actual GPU query. The check below shows a glibc, dynamically-linked
binary failing to exec on `scratch` (and on Alpine) but succeeding on the
distroless base, using a stand-in for `nvidia-smi`.

Create a two-line C program that stands in for the injected binary --
same class of binary (glibc, dynamically linked), so it needs the same
loader:

```c
/* fake-nvidia-smi.c */
#include <stdio.h>
int main(void) { printf("23, 4096\n"); return 0; }
```

Build it and drop it into each base as `nvidia-smi`, then try to run it:

```sh
# compile a glibc, dynamically-linked binary (confirm with: file ./nvidia-smi)
docker run --rm -v "$PWD":/w -w /w debian:12-slim \
  sh -c 'apt-get update >/dev/null && apt-get install -y gcc file >/dev/null && \
         gcc -O2 -o nvidia-smi fake-nvidia-smi.c && file nvidia-smi'

# scratch: no loader -> exec fails ENOENT (this was issue #38)
printf 'FROM scratch\nCOPY nvidia-smi /usr/bin/nvidia-smi\nENTRYPOINT ["/usr/bin/nvidia-smi"]\n' \
  | docker build -t smitest:scratch -f - . && docker run --rm smitest:scratch; echo "exit=$?"

# distroless/base-debian12: has the loader -> prints "23, 4096"
printf 'FROM gcr.io/distroless/base-debian12\nCOPY nvidia-smi /usr/bin/nvidia-smi\nENTRYPOINT ["/usr/bin/nvidia-smi"]\n' \
  | docker build -t smitest:distroless -f - . && docker run --rm smitest:distroless; echo "exit=$?"
```

The `scratch` run fails with `no such file or directory` (the missing
interpreter) and a non-zero exit; the distroless run prints `23, 4096`
and exits 0. That is exactly the difference this image change buys, at
the point where Gantry execs `nvidia-smi`.

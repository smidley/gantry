# PSI (Pressure Stall Information)

Gantry's Settings page and Overview banner show a hint when the `pressure` source isn't `ok`: *"PSI disabled — add `psi=1` to the syslinux append line to enable."* This page explains what that means and whether you should bother.

## What PSI is

PSI (Pressure Stall Information) is a Linux kernel accounting feature. For CPU, memory, and IO, it tracks the share of time tasks spent **stalled waiting** for that resource, as opposed to actually using it.

That's a different number from the ones you're used to. "40% CPU used" and "stalled waiting for CPU 30% of the time" are two different measurements that can both be true of the same container at the same time — one says how busy it kept the resource, the other says how much its own work was held up by contention for it. A container can show low CPU% and still be starved (blocked behind something else) if it isn't actually the one holding the resource; PSI is what tells you that directly, instead of you having to infer it from side effects.

## What Gantry uses it for

**Today:** when enabled, Gantry records PSI (`some avg10`, i.e. rolling 10-second stall percentage) for CPU, IO, and memory — per container and for the host as a whole. That's it for now; nothing else in the UI changes.

**Later:** PSI is the ground-truth "victim" signal for Gantry's planned cross-container impact insights (e.g. "qbittorrent is saturating disk3 — jellyfin was stalled 38% of the last 10 minutes"). PSI is a direct, kernel-measured stall — not an inference. Without it, that engine still runs, but it falls back to weaker, indirect signals (CPU throttling counters, per-device IO share, parity-speed baselines) instead of a measured stall on the victim side. Enabling PSI now means Gantry has been accumulating that history by the time the insights engine ships; enabling it later just means a shorter lookback on day one.

## How to enable it on Unraid

Stock Unraid ships PSI compiled into the kernel but **disabled by default** (`CONFIG_PSI_DEFAULT_DISABLED`) — the kernel supports it, but nothing turns it on until you ask. That's the entire reason this flag exists.

1. Unraid webGUI → **Main** → click your **Flash** device (the boot device) → **Syslinux configuration**.
2. Find the default boot entry and add `psi=1` to its `append` line. For example:

   ```
   append psi=1 initrd=/bzroot
   ```

   If the line already has other flags, just add `psi=1` alongside them (order doesn't matter).
3. Apply, then **reboot** — PSI is a boot-time kernel parameter; there's no live-enable.

Gantry needs no configuration of its own. After reboot, it notices `/proc/pressure` on its next probe automatically: the pressure hint clears from the banner and Settings, and the `pressure` source flips to `ok`.

## Overhead

Negligible. PSI is a kernel accounting feature, not a monitoring agent — it's already computing scheduler statistics either way; this just also tracks stall time and exposes it under `/proc/pressure` and each cgroup's `*.pressure` files. Upstream's own measurements put the added scheduler overhead at well under 1%. See the [kernel PSI documentation](https://docs.kernel.org/accounting/psi.html) for the authoritative description and measurements.

## How to verify it's on

On the Unraid host:

```
cat /proc/pressure/io
```

- **Enabled:** prints `some avg10=... avg60=... avg300=... total=...` (and a second `full` line). Actual numbers will vary; zeros are fine, it just means nothing's stalled right now.
- **Disabled:** the file doesn't exist — `cat` reports `No such file or directory`.

`/proc/pressure/cpu` and `/proc/pressure/memory` behave the same way. Gantry itself uses the presence of `/proc/pressure/io` as its own enabled/disabled check.

## How to disable it

Remove `psi=1` from the same `append` line and reboot. There's no per-boot toggle beyond that.

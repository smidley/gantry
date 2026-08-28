<!--
  StorageDeviceRow: one backing device's live read/write rate in
  ContainerDetail's Storage section -- its own component, mirroring
  TopBarRow/ContainerRow's own precedent, so each row's read/write Tween
  survives the parent's 2s poll instead of restarting from zero every
  tick (a Tween created inline in the parent's {#each} wouldn't persist
  across re-renders the same way).

  Read/write colors: --series-1 (read) / --series-4 (write) -- the same
  pair the Disk IO chart directly above this in the same view now uses
  too (the app-wide directional swap: Scott's own call, an upload/write
  direction shouldn't read as alarm-adjacent, and --series-2's orange sat
  too close to status-serious/critical's own reds for that). This row
  was born on the new pair before that swap landed everywhere else.

  Row density/border: the "rail row" convention Storage.svelte's own
  disk list and Containers' mobile card list already use (a hairline
  between rows instead of a gap-separated stack) -- see this file's own
  first/last-child rules below.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';
  import { fmtRate } from '../lib/format';
  import { diskMetaKind } from '../lib/disks';

  // entry: one DeviceIODTO ({device, label, kind, read_bps, write_bps})
  // -- see api.ts's StorageDeviceDTO. label/kind are unraid.
  // ResolveDeviceLabel's own output (backend); kind is '' whenever it
  // isn't known (every loop device, and any device this fleet's
  // disks.ini doesn't cover), same "don't trust the network" narrowing
  // disks.ts's diskKind already applies to DiskMetaDTO's own Kind.
  let { entry } = $props();

  // rawSecondary shows the actual device name alongside a friendlier
  // label, but only when the two actually differ -- a device
  // ResolveDeviceLabel couldn't resolve to anything better than itself
  // (label === device, the "else raw" case) would otherwise show its
  // own name twice ("sda (sda)").
  let rawSecondary = $derived(entry.label !== entry.device ? entry.device : null);
  let kind = $derived(diskMetaKind(entry.kind));

  let readTween = new Tween(untrack(() => entry.read_bps), { duration: liveStore.glideMs, easing: linear });
  let writeTween = new Tween(untrack(() => entry.write_bps), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    const reduced = prefersReducedMotion.current;
    readTween.set(entry.read_bps, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
  });
  $effect(() => {
    const reduced = prefersReducedMotion.current;
    writeTween.set(entry.write_bps, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
  });

  const KIND_LABEL = { hdd: 'HDD', ssd: 'SSD', nvme: 'NVMe', usb: 'USB' };
</script>

<div class="storage-device">
  <span class="storage-device__name">
    {entry.label}
    {#if rawSecondary}<span class="storage-device__raw">{rawSecondary}</span>{/if}
    {#if kind}<span class="storage-device__kind storage-device__kind--{kind}">{KIND_LABEL[kind]}</span>{/if}
  </span>
  <span class="storage-device__rate">
    <span class="storage-device__swatch storage-device__swatch--read" aria-hidden="true"></span>
    Read <span class="tabular-nums">{fmtRate(readTween.current)}</span>
  </span>
  <span class="storage-device__rate">
    <span class="storage-device__swatch storage-device__swatch--write" aria-hidden="true"></span>
    Write <span class="tabular-nums">{fmtRate(writeTween.current)}</span>
  </span>
</div>

<style>
  .storage-device {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.4rem 1rem;
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.8rem;
  }
  .storage-device:first-child {
    padding-top: 0;
  }
  /* No :last-child border removal here -- StorageTotalRow (ContainerDetail's
     own Live IO section) always follows the last one of these when this
     component renders at all, so every row's own hairline (including
     the last device's) stays as the divider leading into that summary
     row instead. */
  .storage-device__name {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-family: var(--font-mono);
    color: var(--ink);
    min-width: 5rem;
  }
  .storage-device__raw {
    color: var(--ink-2);
    font-size: 0.72rem;
  }
  /* Kind tint: same four-way vocabulary/palette as Storage.svelte's own
     storage-disk__media--<kind> badges (hdd deliberately gets no
     override -- the plain neutral chip color below is its own "color",
     the ordinary/majority case). Kept as a plain text pill here, not
     that component's own glyph+label pair -- one device row is a much
     smaller/denser context than a disk card. */
  .storage-device__kind {
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    color: var(--ink-2);
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    white-space: nowrap;
  }
  .storage-device__kind--ssd {
    color: var(--series-3);
    background: color-mix(in oklab, var(--series-3) 12%, transparent);
  }
  .storage-device__kind--nvme {
    color: var(--series-1);
    background: color-mix(in oklab, var(--series-1) 12%, transparent);
  }
  .storage-device__kind--usb {
    color: var(--series-4);
    background: color-mix(in oklab, var(--series-4) 14%, transparent);
  }
  .storage-device__rate {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    color: var(--ink-2);
    white-space: nowrap;
  }
  .storage-device__swatch {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 2px;
    flex-shrink: 0;
  }
  .storage-device__swatch--read {
    background: var(--series-1);
  }
  .storage-device__swatch--write {
    background: var(--series-4);
  }
</style>

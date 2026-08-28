<!--
  StorageTotalRow: ContainerDetail's Storage section's own "how much IO
  is this container doing, at a glance" line -- read+write summed across
  every one of its own backing devices (StorageDeviceRow's own rows,
  directly above this in the same section). Its own component, same
  reason StorageDeviceRow is: a Tween declared inline in the parent's
  template would restart from zero on every 2s poll instead of gliding
  from its own last value.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';
  import { fmtRate } from '../lib/format';

  // devices: storageData.devices, the same array StorageDeviceRow's own
  // {#each} renders one row per -- see api.ts's StorageDeviceDTO.
  let { devices } = $props();

  let totalRead = $derived(devices.reduce((sum, d) => sum + d.read_bps, 0));
  let totalWrite = $derived(devices.reduce((sum, d) => sum + d.write_bps, 0));

  let readTween = new Tween(untrack(() => totalRead), { duration: liveStore.glideMs, easing: linear });
  let writeTween = new Tween(untrack(() => totalWrite), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    const reduced = prefersReducedMotion.current;
    readTween.set(totalRead, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
  });
  $effect(() => {
    const reduced = prefersReducedMotion.current;
    writeTween.set(totalWrite, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
  });
</script>

<div class="storage-total">
  <span class="storage-total__name">Total</span>
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
  /* No border of its own -- the last StorageDeviceRow's own hairline
     (directly above, same parent) already divides the device list from
     this summary; bold weight (below) is what marks it as a derived
     total rather than one more device among equals. */
  .storage-total {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.4rem 1rem;
    padding: 0.4rem 0 0;
    font-size: 0.8rem;
  }
  .storage-total__name {
    font-weight: 600;
    color: var(--ink);
    min-width: 5rem;
  }
  /* .storage-device__rate/__swatch are StorageDeviceRow's own scoped
     classes -- Svelte scopes styles per component, so these need their
     own (identical) declarations here to render the same read/write
     rate rhythm. */
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

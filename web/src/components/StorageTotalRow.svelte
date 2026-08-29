<!--
  StorageTotalRow: ContainerDetail's Storage section's own "how much IO
  is this container doing, at a glance" line -- read+write summed across
  EVERY one of its own backing devices, not just the ones the noise rule
  (ContainerDetail.svelte's visibleDevices) currently shows -- an idle,
  hidden device still contributed 0, and staying truthful about that
  matters more than staying in visual lockstep with whichever rows
  happen to be rendered above it. Its own component, same reason
  StorageDeviceRow is: a Tween declared inline in the parent's template
  would restart from zero on every 2s poll instead of gliding from its
  own last value.

  Grid row: display:contents, same technique as StorageDeviceRow -- see
  its own doc. "Total" spans the label/device/kind columns (nothing
  meaningful to put in the other two for a summary row); read/write land
  in the same two right-aligned columns every device row uses.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { motion } from '../lib/motion.svelte';
  import { live as liveStore } from '../lib/sse.svelte';
  import { fmtRate } from '../lib/format';

  // devices: storageData.devices (ALL of them -- see this file's own
  // doc above), the same array StorageDeviceRow's own {#each} draws its
  // (possibly narrower) visible subset from -- see api.ts's
  // StorageDeviceDTO.
  let { devices } = $props();

  let totalRead = $derived(devices.reduce((sum, d) => sum + d.read_bps, 0));
  let totalWrite = $derived(devices.reduce((sum, d) => sum + d.write_bps, 0));

  let readTween = new Tween(untrack(() => totalRead), { duration: liveStore.glideMs, easing: linear });
  let writeTween = new Tween(untrack(() => totalWrite), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    const reduced = motion.reduced;
    readTween.set(totalRead, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
  });
  $effect(() => {
    const reduced = motion.reduced;
    writeTween.set(totalWrite, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
  });
</script>

<div class="storage-total">
  <span class="storage-total__name">Total</span>
  <span class="storage-device__value tabular-nums" aria-label={`Read ${fmtRate(readTween.current)}`}>
    <span class="storage-device__swatch storage-device__swatch--read" aria-hidden="true"></span>
    {fmtRate(readTween.current)}
  </span>
  <span class="storage-device__value tabular-nums" aria-label={`Write ${fmtRate(writeTween.current)}`}>
    <span class="storage-device__swatch storage-device__swatch--write" aria-hidden="true"></span>
    {fmtRate(writeTween.current)}
  </span>
</div>

<style>
  .storage-total {
    display: contents;
  }
  /* No border on the label cell -- the last StorageDeviceRow's own
     hairline (directly above, same parent grid) already divides the
     device list from this summary; bold weight is what marks it as a
     derived total rather than one more device among equals. Spans the
     label/device/kind columns (grid-column, below) since there's
     nothing meaningful to put in the other two for a summary row. */
  .storage-total__name {
    grid-column: span 3;
    padding: 0.4rem 0 0;
    font-weight: 600;
    color: var(--ink);
    font-size: 0.8rem;
  }
  /* .storage-device__value/__swatch are StorageDeviceRow's own scoped
     classes -- Svelte scopes styles per component, so these need their
     own (identical) declarations here to render the same right-aligned
     read/write rhythm, lined up with every device row above. */
  .storage-device__value {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 0.35rem;
    padding: 0.4rem 0 0;
    color: var(--ink-2);
    font-size: 0.8rem;
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
  /* Narrow phones: same wrapping-flex fallback as StorageDeviceRow's own
     -- see its doc, and ContainerDetail.svelte's for why the grid itself
     doesn't fit at all below this width. */
  @media (max-width: 36rem) {
    .storage-total {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      gap: 0.3rem 0.9rem;
      padding: 0.4rem 0 0;
    }
    .storage-total__name {
      padding: 0;
    }
    .storage-device__value {
      flex: 0 0 auto;
      justify-content: flex-start;
      padding: 0;
    }
  }
</style>

<!--
  StorageDeviceRow: one backing device's live read/write rate in
  ContainerDetail's Storage section -- its own component, mirroring
  TopBarRow/ContainerRow's own precedent, so each row's read/write Tween
  survives the parent's 2s poll instead of restarting from zero every
  tick (a Tween created inline in the parent's {#each} wouldn't persist
  across re-renders the same way).

  Read/write colors: --series-1 (read) / --series-4 (write) -- NOT the
  Disk IO chart's own --series-1/--series-2 pair directly above this in
  the same view. Scott's own call: an upload/write direction shouldn't
  read as alarm-adjacent, and --series-2's orange sits too close to
  status-serious/critical's own reds for that. This row is born on the
  new pair; the chart (and every other existing read/write surface)
  swaps over in a separate, later pass -- until then the two legends
  disagreeing on Write's color in this same view is expected, not a bug.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';
  import { fmtRate } from '../lib/format';

  // entry: one DeviceIODTO ({device, read_bps, write_bps}) -- see
  // api.ts's StorageDeviceDTO.
  let { entry } = $props();

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
</script>

<div class="storage-device">
  <span class="storage-device__name">{entry.device}</span>
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
    gap: 0.6rem 1rem;
    font-size: 0.8rem;
  }
  .storage-device__name {
    font-family: var(--font-mono);
    color: var(--ink);
    min-width: 5rem;
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

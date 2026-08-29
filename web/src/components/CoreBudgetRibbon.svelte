<!--
  CoreBudgetRibbon: the CPU breakdown page's own hero -- one horizontal
  bar, hostCores wide, with a tick mark at every core boundary and one
  segment per top-consuming container (see lib/coreBudget.ts's
  buildCoreBudget for the segment math this only renders), an "Others"
  bucket for the long tail, an unattributed-host segment, and whatever's
  left as free headroom.

  Segments live-glide their own width (transition-duration bound to the
  shared driver's glideMs, same plain-CSS-transition approach Storage.
  svelte's own parity/usage bars already use -- no per-segment Tween
  needed for one interpolated property) and collapse to instant under
  prefers-reduced-motion.

  Hover/focus names whichever segment is under the pointer/tab stop --
  icon (a real container's own, when known) + name + ~cores -- in a
  small floating label above the bar, the same visual language
  TimeChart's own marker-hover label uses.
-->
<script>
  import { motion } from '../lib/motion.svelte';
  import { live as liveStore } from '../lib/sse.svelte';
  import { fmtCores } from '../lib/format';
  import ContainerIcon from './ContainerIcon.svelte';

  // hostCores/segments/freeCores: buildCoreBudget's own output, computed
  // by the caller (Top Consumers' CPU breakdown) off the live frame --
  // this component only renders it. icons (additive, optional): name ->
  // icon URL, for the hover label's ContainerIcon; a segment with no
  // entry (Others/Unattributed, or a name icons doesn't cover) just
  // renders without one.
  let { hostCores = 0, segments = [], freeCores = 0, icons = {} } = $props();

  let glideMs = $derived(motion.reduced ? 0 : liveStore.glideMs);

  let hoveredKey = $state(null);
  let hoveredSegment = $derived(segments.find((s) => s.key === hoveredKey) ?? null);

  function widthPct(cores) {
    return hostCores > 0 ? (cores / hostCores) * 100 : 0;
  }

  // segmentAriaLabel avoids a dangling ", " when fmtCores hides a
  // near-zero figure (below its own 0.05-core floor -- see format.ts's
  // doc) rather than printing a misleading "≈0.0 cores".
  function segmentAriaLabel(seg) {
    const cores = fmtCores(seg.cores);
    return cores ? `${seg.label}, ${cores}` : seg.label;
  }
</script>

{#if hostCores > 0}
  <div class="core-ribbon">
    <div class="core-ribbon__bar" role="group" aria-label={`CPU core budget, ${hostCores} cores`}>
      {#each segments as seg (seg.key)}
        <button
          type="button"
          class="core-ribbon__segment"
          style="width: {widthPct(seg.cores)}%; background: {seg.colorVar}; transition-duration: {glideMs}ms"
          aria-label={segmentAriaLabel(seg)}
          onmouseenter={() => (hoveredKey = seg.key)}
          onmouseleave={() => (hoveredKey = null)}
          onfocus={() => (hoveredKey = seg.key)}
          onblur={() => (hoveredKey = null)}
        ></button>
      {/each}
      {#if freeCores > 0}
        <div
          class="core-ribbon__free"
          style="width: {widthPct(freeCores)}%; transition-duration: {glideMs}ms"
          aria-hidden="true"
        ></div>
      {/if}
      <div class="core-ribbon__ticks" aria-hidden="true">
        {#each Array.from({ length: Math.max(0, hostCores - 1) }) as _, i (i)}
          <span class="core-ribbon__tick" style="left: {((i + 1) / hostCores) * 100}%"></span>
        {/each}
      </div>
    </div>
    <div class="core-ribbon__label" class:core-ribbon__label--visible={!!hoveredSegment}>
      {#if hoveredSegment}
        {#if icons[hoveredSegment.key] !== undefined}
          <ContainerIcon name={hoveredSegment.key} icon={icons[hoveredSegment.key]} size={14} />
        {/if}
        <span>{hoveredSegment.label}</span>
        <span class="tabular-nums">{fmtCores(hoveredSegment.cores)}</span>
      {/if}
    </div>
  </div>
{/if}

<style>
  .core-ribbon {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .core-ribbon__bar {
    position: relative;
    height: 28px;
    border-radius: 6px;
    background: color-mix(in oklab, var(--ink) 6%, transparent);
    overflow: hidden;
    display: flex;
  }
  /* A <button>, not a styled <div> -- gets focusability/hover semantics
     for free (see the a11y fix this replaced), so this is chrome-reset
     only: no border/padding/font of its own, background stays whatever
     the inline style above sets. */
  .core-ribbon__segment {
    position: relative;
    height: 100%;
    min-width: 2px;
    flex-shrink: 0;
    border: none;
    padding: 0;
    font: inherit;
    transition-property: width;
    transition-timing-function: linear;
    cursor: pointer;
  }
  .core-ribbon__segment:hover,
  .core-ribbon__segment:focus-visible {
    filter: brightness(1.15);
  }
  .core-ribbon__free {
    height: 100%;
    flex-shrink: 0;
    transition-property: width;
    transition-timing-function: linear;
  }
  /* Per-core tick marks -- a plain absolutely-positioned overlay, not
     part of the flex flow, so they read as fixed gridlines regardless
     of how the segments underneath happen to divide the bar. */
  .core-ribbon__ticks {
    position: absolute;
    inset: 0;
    pointer-events: none;
  }
  .core-ribbon__tick {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 1px;
    background: color-mix(in oklab, var(--page) 55%, transparent);
  }
  /* Fixed-height label row, always present in layout (opacity-toggled,
     not conditionally rendered) so the bar's own position never shifts
     when a hover starts/ends. */
  .core-ribbon__label {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 1.2rem;
    font-size: 0.8rem;
    color: var(--ink-2);
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .core-ribbon__label--visible {
    opacity: 1;
  }
</style>

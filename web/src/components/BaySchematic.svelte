<!--
  BaySchematic: D2's miniature array visualization -- one usage-
  proportional bar per disk/pool entity. Deliberately dumb: it knows
  nothing about WHY an entry is flagged (that's Overview's own anomaly
  logic), only how to draw what it's handed -- the same "reads straight
  off the array/disk data" reusability the design's own recommendation
  calls for when this eventually promotes to a full Storage module.

  Fills use the existing --seq-100..--seq-700 ramp (seqStep, shared with
  Storage's own disk-usage bars and the old ArrayCard's pool rows) --
  zero new colors. Each bar is a real link into #/storage (click-through)
  with hover AND keyboard-focus revealing a small label below the bars
  (slot, device, temp -- threshold-colored, matching Storage.svelte's own
  banding -- and used/total), CoreBudgetRibbon's own hover-label
  convention, so the strip's richer detail survives for anyone not
  hovering with a mouse. aria-label alone (title dropped -- the label
  row below is the real tooltip now) still carries the essential text
  for anyone using a screen reader.

  A flagged bar carries no floating callout text (corrective pass: a
  variable-length string like "95.0% capacity" centered over a 13px bar
  reliably collided with its neighbors). The outline alone marks it
  visually; the reason lives in the aria-label (for anyone using a
  screen reader) and, in full, in the "Needs a look" row right above
  this module -- the flagged unit's own color plus that row's text
  already carry the information, so nothing here needs to repeat it
  visibly.

  Fill height glides on every pct change (perpetual-glide motion pass --
  previously a bare unanimated style binding, the one live surface the
  pass's own inventory found with no easing at all, snapping every ~2s
  tick) via a plain CSS transition rather than a Tween/headState: a
  transition retargeted before finishing already continues from
  whatever height is CURRENTLY on screen, the same "never jump to the
  old target" contract streamdriver.ts's own doc spells out for
  Tween/headState, for free -- no custom state needed for a single
  interpolated CSS property. glideMs (live.glideMs, or 0 under reduced
  motion) is the one piece that still comes from the shared driver.
-->
<script>
  import { fmtBytes, fmtPct } from '../lib/format';
  import { seqStep } from '../lib/metrics';
  import { band, bandToken } from '../lib/thresholds';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';

  // entries: [{ slot, pct, flagged?, calloutText?, kind?, device?,
  // tempState, usedBytes, freeBytes }] -- pct is a plain 0-100 number
  // (diskUsagePct's own range); flagged/calloutText are the caller's own
  // anomaly decision, not derived here. device/tempState/usedBytes/
  // freeBytes back the hover/focus label only -- the bar itself still
  // only ever draws pct+kind, unchanged. kind draws a distinct, per-
  // kind-colored top-cap stroke, independent of the flagged outline and
  // the usage-proportional fill -- a type signal, not a health one.
  // Absent/"hdd" (the ordinary/majority case) draws no cap at all.
  let { entries = [] } = $props();

  // glideMs: see the module doc above -- the CSS transition on each
  // bar's own fill reads this straight off the shared driver.
  let glideMs = $derived(prefersReducedMotion.current ? 0 : liveStore.glideMs);

  const KIND_LABEL = { ssd: ', solid state', nvme: ', NVMe', usb: ', USB flash' };

  function labelFor(d) {
    const media = KIND_LABEL[d.kind] ?? '';
    const base = `${d.slot}: ${fmtPct(d.pct)} used${media}`;
    return d.calloutText ? `${base} — ${d.calloutText}` : base;
  }

  let hoveredSlot = $state(null);
  let hoveredEntry = $derived(entries.find((d) => d.slot === hoveredSlot) ?? null);

  // tempTint mirrors Storage.svelte's own banding exactly (nvme gets the
  // hotter-tolerant family) so the same disk reads the same temp color
  // on both surfaces.
  function tempTint(d) {
    if (d.tempState?.kind !== 'reading') return undefined;
    return bandToken(band(d.kind === 'nvme' ? 'disk.temp.nvme' : 'disk.temp', d.tempState.celsius));
  }
  function tempText(d) {
    if (!d.tempState) return null;
    if (d.tempState.kind === 'reading') return `${d.tempState.celsius.toFixed(1)}°C`;
    return d.tempState.kind === 'spun-down' ? 'Spun down' : 'No sensor';
  }
</script>

{#if entries.length > 0}
  <div class="bay-schematic">
    <span class="microlabel">Array &middot; {entries.length} member{entries.length === 1 ? '' : 's'}</span>
    <div class="bay-schematic__bars">
      {#each entries as d (d.slot)}
        <a
          class="bay-schematic__bar"
          class:bay-schematic__bar--flag={!!d.flagged}
          class:bay-schematic__bar--ssd={d.kind === 'ssd'}
          class:bay-schematic__bar--nvme={d.kind === 'nvme'}
          class:bay-schematic__bar--usb={d.kind === 'usb'}
          href="#/storage"
          aria-label={labelFor(d)}
          onmouseenter={() => (hoveredSlot = d.slot)}
          onmouseleave={() => (hoveredSlot = null)}
          onfocus={() => (hoveredSlot = d.slot)}
          onblur={() => (hoveredSlot = null)}
        >
          <span
            class="bay-schematic__fill"
            style={`height: ${Math.min(100, Math.max(0, d.pct))}%; background: ${seqStep(d.pct)}; transition-duration: ${glideMs}ms`}
          ></span>
        </a>
      {/each}
    </div>
    <div class="bay-schematic__label" class:bay-schematic__label--visible={!!hoveredEntry}>
      {#if hoveredEntry}
        <span>{hoveredEntry.slot}</span>
        {#if hoveredEntry.device}<span class="bay-schematic__label-muted">{hoveredEntry.device}</span>{/if}
        {#if tempText(hoveredEntry)}
          <span class="tabular-nums" style={tempTint(hoveredEntry) ? `color: ${tempTint(hoveredEntry)}` : undefined}>
            {tempText(hoveredEntry)}
          </span>
        {/if}
        {#if hoveredEntry.usedBytes !== undefined && hoveredEntry.freeBytes !== undefined}
          <span class="tabular-nums bay-schematic__label-muted">
            {fmtBytes(hoveredEntry.usedBytes)} / {fmtBytes(hoveredEntry.usedBytes + hoveredEntry.freeBytes)}
          </span>
        {/if}
      {/if}
    </div>
  </div>
{/if}

<style>
  .bay-schematic {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .bay-schematic__bars {
    display: flex;
    align-items: flex-end;
    gap: 6px;
    height: 130px;
    flex-wrap: wrap;
  }
  .bay-schematic__bar {
    position: relative;
    width: 22px;
    height: 100%;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    border-radius: 2px;
    flex-shrink: 0;
    display: block;
  }
  .bay-schematic__bar:hover,
  .bay-schematic__bar:focus-visible {
    filter: brightness(1.1);
  }
  .bay-schematic__bar--flag {
    outline: 2px solid var(--status-warning);
    outline-offset: 1px;
  }
  /* Type signal, not a health one -- a --series-* token (never a
     --status-* one) so it reads as "different kind of member," not as
     another severity color, and stays legible alongside a flagged bar's
     own outline. One color per kind, matching Storage's own type-badge
     mapping exactly so the same disk reads the same identity on both
     views; hdd (the ordinary/majority case) gets no cap at all. */
  .bay-schematic__bar--ssd {
    border-top: 3px solid var(--series-3);
  }
  .bay-schematic__bar--nvme {
    border-top: 3px solid var(--series-1);
  }
  .bay-schematic__bar--usb {
    border-top: 3px solid var(--series-4);
  }
  .bay-schematic__fill {
    position: absolute;
    bottom: 0;
    left: 0;
    width: 100%;
    display: block;
    /* duration is inline (transition-duration, above) -- the shared
       driver's own live glideMs, per pct-changing frame. */
    transition-property: height;
    transition-timing-function: linear;
    border-radius: 1px 1px 0 0;
  }
  /* Fixed-height label row, always present in layout (opacity-toggled,
     not conditionally rendered) so the bars' own position never shifts
     when a hover/focus starts or ends -- CoreBudgetRibbon's own
     hover-label convention, same as FleetStrip's. */
  .bay-schematic__label {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 0.5rem;
    min-height: 1.2rem;
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--ink);
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .bay-schematic__label--visible {
    opacity: 1;
  }
  .bay-schematic__label-muted {
    font-weight: 400;
    color: var(--ink-2);
  }
</style>

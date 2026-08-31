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
  import { motion } from '../lib/motion.svelte';
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
  let { entries = [], summary = null } = $props();

  // glideMs: see the module doc above -- the CSS transition on each
  // bar's own fill reads this straight off the shared driver.
  let glideMs = $derived(motion.reduced ? 0 : liveStore.glideMs);

  const KIND_LABEL = { ssd: ', solid state', nvme: ', NVMe', usb: ', USB flash' };

  function labelFor(d) {
    const media = KIND_LABEL[d.kind] ?? '';
    const base = `${d.slot}: ${fmtPct(d.pct)} used${media}`;
    return d.calloutText ? `${base} — ${d.calloutText}` : base;
  }

  let hoveredSlot = $state(null);
  let hoveredEntry = $derived(entries.find((d) => d.slot === hoveredSlot) ?? null);
  let flaggedCount = $derived(entries.filter((d) => d.flagged).length);

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

  function kindText(kind) {
    if (kind === 'ssd') return 'SSD';
    if (kind === 'nvme') return 'NVMe';
    if (kind === 'usb') return 'USB';
    return 'HDD';
  }
</script>

{#if entries.length > 0}
  <section class="bay-schematic" aria-labelledby="bay-schematic-title">
    <div class="bay-schematic__head">
      <div>
        <h3 id="bay-schematic-title" class="bay-schematic__title">Storage array</h3>
        <p class="bay-schematic__summary">
          {entries.length} device{entries.length === 1 ? '' : 's'}
          <span aria-hidden="true">&middot;</span>
          {#if flaggedCount > 0}
            <strong>{flaggedCount} need{flaggedCount === 1 ? 's' : ''} attention</strong>
          {:else}
            <span>All within normal range</span>
          {/if}
        </p>
      </div>
      <a class="bay-schematic__link" href="#/storage">View details <span aria-hidden="true">&rarr;</span></a>
    </div>
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
          <span class="bay-schematic__bar-head">
            <span class="bay-schematic__slot">{d.slot}</span>
            <span class="bay-schematic__pct tabular-nums">{fmtPct(d.pct)}</span>
          </span>
          <span class="bay-schematic__track" aria-hidden="true">
            <span
              class="bay-schematic__fill"
              style={`width: ${Math.min(100, Math.max(0, d.pct))}%; background: ${seqStep(d.pct)}; transition-duration: ${glideMs}ms`}
            ></span>
          </span>
          <span class="bay-schematic__meta">
            {#if d.flagged}
              <span class="bay-schematic__warning">Needs attention</span>
            {:else}
              <span>{kindText(d.kind)}</span>
            {/if}
            {#if tempText(d)}
              <span class="tabular-nums" style={tempTint(d) ? `color: ${tempTint(d)}` : undefined}>{tempText(d)}</span>
            {/if}
          </span>
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
      {:else if summary}
        <span class="bay-schematic__reassurance"><i aria-hidden="true">&check;</i>{summary}</span>
      {:else}
        <span class="bay-schematic__label-muted">Usage bars show how full each device is.</span>
      {/if}
    </div>
  </section>
{/if}

<style>
  /* Region-sizing pass (Scott: the schematic's fixed footprint stranded
     dead space beside it): the component now sizes to its own content
     with a max instead of claiming its container's full width -- a
     small array renders a small schematic, and whatever module shares
     the region can actually use the rest. max-width keeps a huge array
     from forcing horizontal scroll: the bars still wrap, and because
     the fixed height moved off the row and onto each BAR, a wrapped
     second row stacks below at full bar height instead of overflowing
     a fixed-height row box (the old height:130px on __bars clipped
     exactly that case). */
  .bay-schematic {
    display: flex;
    flex-direction: column;
    gap: 0.8rem;
    width: 100%;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: color-mix(in oklab, var(--surface) 78%, transparent);
  }
  .bay-schematic__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .bay-schematic__title {
    margin: 0;
    color: var(--ink);
    font-size: 0.92rem;
    font-weight: 650;
    letter-spacing: -0.015em;
  }
  .bay-schematic__summary {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem;
    margin: 0.3rem 0 0;
    color: var(--ink-2);
    font-size: 0.76rem;
  }
  .bay-schematic__summary strong {
    color: var(--status-warning);
    font-weight: 650;
  }
  .bay-schematic__link {
    flex-shrink: 0;
    color: var(--accent);
    font-size: 0.76rem;
    font-weight: 600;
    text-decoration: none;
  }
  .bay-schematic__link:hover {
    color: var(--accent-strong);
  }
  .bay-schematic__bars {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(145px, 100%), 1fr));
    gap: 0.55rem;
  }
  .bay-schematic__bar {
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
    min-width: 0;
    padding: 0.65rem 0.7rem;
    border: 1px solid color-mix(in oklab, var(--border) 82%, transparent);
    border-radius: 9px;
    background: var(--surface-soft);
    color: inherit;
    text-decoration: none;
    transition:
      border-color 150ms ease,
      background-color 150ms ease,
      transform 150ms ease;
  }
  .bay-schematic__bar:hover,
  .bay-schematic__bar:focus-visible {
    border-color: color-mix(in oklab, var(--accent) 38%, var(--border));
    background: color-mix(in oklab, var(--accent) 5%, var(--surface-soft));
    transform: translateY(-1px);
  }
  .bay-schematic__bar--flag {
    border-color: color-mix(in oklab, var(--status-warning) 70%, var(--border));
    box-shadow: inset 3px 0 0 var(--status-warning);
  }
  /* Type signal, not a health one -- a --series-* token (never a
     --status-* one) so it reads as "different kind of member," not as
     another severity color, and stays legible alongside a flagged bar's
     own outline. One color per kind, matching Storage's own type-badge
     mapping exactly so the same disk reads the same identity on both
     views; hdd (the ordinary/majority case) gets no cap at all. */
  .bay-schematic__bar-head,
  .bay-schematic__meta {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .bay-schematic__slot {
    overflow: hidden;
    color: var(--ink);
    font-size: 0.78rem;
    font-weight: 650;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bay-schematic__pct {
    flex-shrink: 0;
    color: var(--ink);
    font-size: 0.76rem;
    font-weight: 600;
  }
  .bay-schematic__track {
    position: relative;
    display: block;
    height: 7px;
    overflow: hidden;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .bay-schematic__fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    display: block;
    /* duration is inline (transition-duration, above) -- the shared
       driver's own live glideMs, per pct-changing frame. */
    transition-property: width;
    transition-timing-function: linear;
    border-radius: inherit;
  }
  .bay-schematic__meta {
    min-height: 1rem;
    color: var(--ink-2);
    font-size: 0.68rem;
  }
  .bay-schematic__warning {
    color: var(--status-warning);
    font-weight: 650;
  }
  /* Fixed-height label row, always present in layout (opacity-toggled,
     not conditionally rendered) so the bars' own position never shifts
     when a hover/focus starts or ends -- CoreBudgetRibbon's own
     hover-label convention, same as FleetStrip's. width:0 +
     min-width:100% keeps the label's own text OUT of the component's
     intrinsic width (percentages are ignored during fit-content
     sizing, then resolve against the bars' final width): without it,
     hovering a long slot/device/temp line would widen the whole
     fit-content schematic mid-hover. The text wraps within the bars'
     width instead. */
  .bay-schematic__label {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 0.5rem;
    min-height: 1.2rem;
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--ink);
    opacity: 0.78;
    transition:
      color 150ms ease,
      opacity 150ms ease;
  }
  .bay-schematic__label--visible {
    opacity: 1;
    color: var(--ink);
  }
  .bay-schematic__label-muted {
    font-weight: 400;
    color: var(--ink-2);
  }
  .bay-schematic__reassurance {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--ink-2);
    font-weight: 400;
  }
  .bay-schematic__reassurance i {
    display: inline-grid;
    width: 16px;
    height: 16px;
    place-items: center;
    border-radius: 50%;
    background: color-mix(in oklab, var(--status-good) 14%, transparent);
    color: var(--status-good);
    font-size: 0.68rem;
    font-style: normal;
    font-weight: 700;
  }

  @media (max-width: 27rem) {
    .bay-schematic__head {
      gap: 0.65rem;
    }
  }
</style>

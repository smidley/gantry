<!--
  StatTile: label, big value, optional unit, optional sparkline, optional
  status dot -- the basic building block for Overview's top row and
  similar summary strips in later views.

  value2/unit2/label2 (additive, optional -- Task 14) render a second,
  smaller readout line below the hero number: Overview's Net and Disk IO
  tiles each need "two rates in one tile" (down+up, read+write), which
  the original single value/unit shape had no room for. The sparkline
  stays single-series either way (charting value's own direction) --
  cramming two lines into a 28px-tall inline chart would be noise, not
  signal, so the second rate is text-only. value2 is never touched by
  hover-scrub below -- it has no sparkline of its own to scrub against.

  liveValue/formatValue (additive -- hover-scrub) replace what used to be
  a single pre-formatted `value` string: StatTile now owns the hero
  number's OWN Tween (moved here from Overview/Settings' own top-level
  tweens, which fed it a string) so it has a raw number to ease FROM/TO
  for both the ordinary live cadence and the fast scrub-follow one --
  formatValue renders whichever raw number is currently live.

  Scrubbing is synced across every mounted scrub-aware surface (Scott's
  own requirement: scrubbing one metric auto-scrubs the others at the
  same instant) via lib/scrubbus.svelte's shared bus -- StatTile doesn't
  care whether ITS OWN sparkline is the one being hovered or some OTHER
  tile/row is; it just renders its own metric's value at the bus's
  shared ts whenever one is published, same as every other owner.

  bare (additive, optional -- D2 Overview) swaps the card-chrome
  presentation for a borderless instrument-rail row (label and value on
  one baseline, a bottom hairline instead of a bordered box): Overview's
  metrics rail uses this; every other caller (Settings' own two tiles)
  leaves it false and renders byte-for-byte as before. Nothing about the
  underlying mechanism changes either way -- same Tween, same scrub-bus
  wiring, same Sparkline instance -- `bare` only picks which markup
  arrangement wraps the identical value/sparkline snippets below, so
  hover-scrub and the live tween keep working identically in both modes.
-->
<script>
  import { Tween } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { untrack } from 'svelte';
  import { fmtRelTime } from '../lib/format';
  import { nearestPointAt } from '../lib/scrub';
  import { scrubBus } from '../lib/scrubbus.svelte';
  import HealthDot from './HealthDot.svelte';
  import Sparkline from './Sparkline.svelte';

  const LIVE_TWEEN_MS = 400;
  const SCRUB_TWEEN_MS = 120;

  let {
    label,
    liveValue,
    formatValue = (v) => String(v),
    unit = '',
    sparklinePoints = undefined,
    sparklineColor = 'var(--series-1)',
    status = undefined,
    value2 = undefined,
    unit2 = '',
    label2 = '',
    bare = false,
  } = $props();

  // scrubHit is null while live; {ts, value} whenever the shared bus has
  // a published ts AND this tile's own sparklinePoints has a nearest
  // sample for it -- {ts, value} is THIS metric's own reading at the
  // bus's shared instant, independent of whichever surface actually
  // published it. numberTween's own target/duration below switch on it,
  // so the hero number always eases FROM wherever it currently is,
  // whichever mode it's easing toward (Tween.set's own re-seed-from-
  // .current contract -- see streamdriver.ts's doc for the same point
  // made about svelte/motion's Tween generally).
  let scrubHit = $derived(scrubBus.ts === null || !sparklinePoints ? null : nearestPointAt(sparklinePoints, scrubBus.ts));
  let numberTween = new Tween(untrack(() => liveValue ?? 0), { duration: LIVE_TWEEN_MS, easing: cubicOut });

  $effect(() => {
    const reduced = prefersReducedMotion.current;
    if (scrubHit) {
      numberTween.set(scrubHit.value, { duration: reduced ? 0 : SCRUB_TWEEN_MS, easing: cubicOut });
    } else {
      numberTween.set(liveValue ?? 0, { duration: reduced ? 0 : LIVE_TWEEN_MS, easing: cubicOut });
    }
  });

  // chipText retains its last real value across scrubHit going back to
  // null (rather than blanking instantly) so the corner chip's own CSS
  // fade-out (below) fades out its last real reading in place, matching
  // the dot/hairline's identical treatment in Sparkline.
  let chipText = $state('');
  $effect(() => {
    if (scrubHit) chipText = fmtRelTime(scrubHit.ts, Date.now());
  });
</script>

{#snippet valueBlock()}
  <span class="stat-tile__number tabular-nums">{formatValue(numberTween.current)}</span>
  {#if unit}<span class="stat-tile__unit">{unit}</span>{/if}
{/snippet}

{#snippet value2Block()}
  {#if label2}<span class="stat-tile__value2-label">{label2}</span>{/if}
  {value2}
  {#if unit2}<span class="stat-tile__unit">{unit2}</span>{/if}
{/snippet}

<div class="stat-tile" class:card={!bare} class:stat-tile--bare={bare}>
  {#if bare}
    <div class="stat-tile__row">
      <span class="stat-tile__row-label">
        <span class="microlabel">{label}</span>
        {#if status}<HealthDot {status} />{/if}
      </span>
      <span class="stat-tile__row-value">
        <span class="microlabel stat-tile__chip" class:stat-tile__chip--visible={!!scrubHit}>{chipText}</span>
        <span class="stat-tile__value">{@render valueBlock()}</span>
      </span>
    </div>
    {#if sparklinePoints}
      <Sparkline points={sparklinePoints} color={sparklineColor} />
    {/if}
    {#if value2 !== undefined}
      <div class="stat-tile__value2 tabular-nums">{@render value2Block()}</div>
    {/if}
  {:else}
    <div class="stat-tile__head">
      <span class="microlabel">{label}</span>
      {#if status}<HealthDot {status} />{/if}
    </div>
    <span class="microlabel stat-tile__chip" class:stat-tile__chip--visible={!!scrubHit}>{chipText}</span>
    <div class="stat-tile__value">{@render valueBlock()}</div>
    {#if value2 !== undefined}
      <div class="stat-tile__value2 tabular-nums">{@render value2Block()}</div>
    {/if}
    {#if sparklinePoints}
      <Sparkline points={sparklinePoints} color={sparklineColor} />
    {/if}
  {/if}
</div>

<style>
  .stat-tile {
    position: relative;
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0;
    transition: border-color 150ms ease;
  }
  /* Subtle hover affordance for the whole tile -- border only, per the
     design's own restraint (no shadows/gradients); a plain :hover so
     it responds to the pointer being anywhere over the tile, including
     its own sparkline sub-region, not just a scrub-specific hotspot. */
  .stat-tile:hover {
    border-color: color-mix(in oklab, var(--series-1) 35%, transparent);
  }
  .stat-tile__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  /* bare (D2's instrument rail): no card box at all -- a hairline seam
     between rows instead, computed off --ink like every other hairline
     in this app (SourcesBanner, EventFeedItem, ...), not a new token. */
  .stat-tile--bare {
    padding: 1.05rem 0;
    border-radius: 0;
    border: none;
    box-shadow: none;
    background: transparent;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 14%, transparent);
  }
  .stat-tile--bare:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
  .stat-tile--bare:first-child {
    padding-top: 0;
  }
  .stat-tile--bare:hover {
    border-color: color-mix(in oklab, var(--series-1) 35%, transparent);
  }
  .stat-tile__row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.75rem;
  }
  .stat-tile__row-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .stat-tile__row-value {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }
  /* The chip floats over the card layout (see .stat-tile__chip below) so
     it never reserves space there -- in the rail row it instead sits
     inline just left of the value, in normal flow, since the row's own
     baseline already has room and there's no card edge for it to hug. */
  .stat-tile--bare .stat-tile__chip {
    position: static;
    top: auto;
    right: auto;
  }
  .stat-tile--bare .stat-tile__number {
    font-size: 1.45rem;
  }
  .stat-tile__chip {
    position: absolute;
    top: 0.6rem;
    right: 0.9rem;
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .stat-tile__chip--visible {
    opacity: 1;
  }
  .stat-tile__value {
    display: flex;
    align-items: baseline;
    gap: 0.35rem;
  }
  .stat-tile__number {
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 1.75rem;
    color: var(--ink);
    line-height: 1;
  }
  .stat-tile__unit {
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--ink-2);
  }
  .stat-tile__value2 {
    display: flex;
    align-items: baseline;
    gap: 0.3rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
    color: var(--ink-2);
    margin-top: -0.25rem;
  }
  .stat-tile__value2-label {
    color: var(--ink-2);
  }
</style>

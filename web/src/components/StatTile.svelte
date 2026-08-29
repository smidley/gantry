<!--
  StatTile: label, big value, optional unit, optional sparkline, optional
  status dot -- the basic building block for Overview's top row and
  similar summary strips in later views.

  liveValue2/formatValue2/unit2/label2 (additive, optional -- Task 14;
  scrub-parity corrective pass) render a second, smaller readout line
  below the hero number: Overview's Net and Disk IO tiles each need "two
  rates in one tile" (down+up, read+write), which the original single
  value/unit shape had no room for.

  sparklineColor2 (additive, optional -- dual-line directional charts:
  Scott's own ask, "for charts that show two different metrics... there
  should be multiple lines with different colors for each metric") is
  what makes the sparkline itself dual-series too, not just the text
  line: whenever value2Points is given, it rides straight through to
  Sparkline's own points2/color2 as a second line, --series-4 by
  default -- down/read (sparklineColor's own default --series-1) and
  up/write, one chart, two colors, matching the app-wide convention.
  Degrades to the original single-series sparkline exactly as before
  when a caller has no value2Points to give it.

  value2Points (optional) is what makes value2 scrub-aware: without it,
  value2 just live-ticks off liveValue2 same as before. WITH it, value2
  gets its own numberTween/scrubHit pair, an exact mirror of the primary
  value's own mechanism below, reading the SAME bus.ts -- Scott's own
  correction: the hero number used to pin correctly while scrubbing but
  the secondary rate kept live-ticking, since it had no ring of its own
  to look a past instant up in. Every caller that has one (Overview's
  Network/Disk IO rows) passes it; a caller with no natural ring for its
  second value (none exist today) can still pass liveValue2 alone and
  get a plain live-only reading, same as the pre-fix behavior.

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

  bandFor (additive, optional -- status-colored values, Scott: "colors of
  numbers for metrics should reflect their status") classifies whatever's
  CURRENTLY DISPLAYED (numberTween.current -- live or scrub-pinned alike)
  into a Band, tinting the hero number accordingly; see its own doc below
  for why the classifier itself is the caller's business, not this
  component's.

  bare (additive, optional -- D2 Overview) swaps the card-chrome
  presentation for a borderless instrument-rail row: microlabel on the
  left, the big value with its optional value2 stacked directly beneath
  it (both right-aligned) on the right, and a full-width sparkline below
  that -- a bottom hairline separates ROWS, never drawn between a row's
  own value and its value2 (Scott's own correction: the two used to be
  split across the sparkline, reading as if value2 belonged to the NEXT
  row). Every other caller (Settings' own two tiles) leaves `bare` false
  and renders byte-for-byte as before. Nothing about the underlying
  mechanism changes either way -- same Tween, same scrub-bus wiring,
  same Sparkline instance -- `bare` only picks which markup arrangement
  wraps the identical value/sparkline snippets below, so hover-scrub and
  the live tween keep working identically in both modes.
-->
<script>
  import { Tween } from 'svelte/motion';
  import { cubicOut, linear } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { untrack } from 'svelte';
  import { fmtRelTime } from '../lib/format';
  import { bandToken } from '../lib/thresholds';
  import { nearestPointAt } from '../lib/scrub';
  import { scrubBus } from '../lib/scrubbus.svelte';
  import { live as liveStore } from '../lib/sse.svelte';
  import HealthDot from './HealthDot.svelte';
  import Sparkline from './Sparkline.svelte';

  // The live glide's own duration comes from liveStore.glideMs (the
  // shared driver's freshly-measured cadence EMA -- see streamdriver.ts's
  // "Cadence-driven glide" doc), never a fixed guess; its curve is
  // `linear`, not `cubicOut` -- a front-loaded curve settles well before
  // the next real sample and sits frozen, which is what reads as a
  // pulse. Scrub-follow (SCRUB_TWEEN_MS/cubicOut) is untouched: that's a
  // pointer-driven interaction, not tied to arrival cadence at all.
  const SCRUB_TWEEN_MS = 120;

  // BARE_SPARKLINE_HEIGHT: the D2 instrument rail's own charts read as a
  // real instrument, not a decorative crumb (Scott, round 2: still "not
  // tall enough for the graphs to look good" at the previous 52px) --
  // roughly 3.4x Sparkline's own 28px default. Card-mode tiles (Settings)
  // are unaffected, they never pass this.
  const BARE_SPARKLINE_HEIGHT = 96;

  let {
    label,
    liveValue,
    formatValue = (v) => String(v),
    unit = '',
    sparklinePoints = undefined,
    sparklineColor = 'var(--series-1)',
    sparklineColor2 = 'var(--series-4)',
    status = undefined,
    liveValue2 = undefined,
    value2Points = undefined,
    formatValue2 = (v) => String(v),
    unit2 = '',
    label2 = '',
    bare = false,
    // href (additive, optional -- metric breakdown pages): makes the
    // whole tile a link, e.g. Overview's rail tiles into "#/top/cpu" --
    // undefined (every non-Overview caller) renders the plain <div> this
    // always was.
    href = undefined,
    // bandFor (additive, optional -- status-colored values): a (value) =>
    // Band classifier for the (unpaired) hero number, e.g.
    // `(v) => band('host.cpu', v)` -- called with whatever's CURRENTLY
    // DISPLAYED (numberTween.current, below), so a scrub-pinned reading
    // gets the same banding a live one does, not the raw liveValue prop.
    // The family itself stays the caller's own business (thresholds.ts),
    // not StatTile's -- this component only ever knows Band -> color
    // (bandToken). undefined (every metric thresholds.ts leaves
    // deliberately unbanded) keeps plain ink. Ignored when the tile is
    // paired (liveValue2 set): a directional pair's own tint always
    // wins, and net/io/gpu are themselves deliberately unbanded anyway.
    bandFor = undefined,
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
  let numberTween = new Tween(untrack(() => liveValue ?? 0), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    const reduced = prefersReducedMotion.current;
    if (scrubHit) {
      numberTween.set(scrubHit.value, { duration: reduced ? 0 : SCRUB_TWEEN_MS, easing: cubicOut });
    } else {
      numberTween.set(liveValue ?? 0, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
    }
  });

  // value2's own scrub-aware tween -- an exact mirror of the primary
  // value's pair above, reading the same shared bus.ts against ITS OWN
  // ring (value2Points) rather than sparklinePoints, so scrubbing pins
  // EVERY number on the tile at once, not just the hero one. Degrades
  // to a plain live-only reading (no scrub pin) when a caller has no
  // ring to pass -- scrubHit2 is simply always null in that case, the
  // same "no sparklinePoints" degradation the primary value already
  // has.
  let scrubHit2 = $derived(scrubBus.ts === null || !value2Points ? null : nearestPointAt(value2Points, scrubBus.ts));
  let number2Tween = new Tween(untrack(() => liveValue2 ?? 0), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    if (liveValue2 === undefined) return;
    const reduced = prefersReducedMotion.current;
    if (scrubHit2) {
      number2Tween.set(scrubHit2.value, { duration: reduced ? 0 : SCRUB_TWEEN_MS, easing: cubicOut });
    } else {
      number2Tween.set(liveValue2, { duration: reduced ? 0 : liveStore.glideMs, easing: linear });
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

  // paired: this tile carries a directional pair (down/up, read/write),
  // not one hero number with an unrelated annotation -- the bare rail's
  // equal-size/tinted treatment below only makes sense for that case
  // (Scott: "values should not be different sizes... [they're] peers,
  // not primary/secondary"). CPU/Memory's lone value never sets
  // liveValue2, so this stays false there and both keep plain ink.
  let paired = $derived(liveValue2 !== undefined);

  // pairedTint resolves a series color var into a text-safe tone: mixed
  // toward --ink so it darkens in light mode and lightens in dark mode,
  // the direction that improves contrast against the page in BOTH
  // themes -- --series-4 (amber) read under AA-large contrast on its own
  // in light mode, this is what fixes that without inventing a new token
  // just for this. Only applied in bare/paired mode; the sparkline LINE
  // itself keeps the raw series color unchanged.
  function pairedTint(colorVar) {
    return `color: color-mix(in oklab, ${colorVar} 70%, var(--ink) 30%)`;
  }

  // valueStyle: paired's own tint wins outright (a directional pair is
  // never also a banded metric -- thresholds.ts's own doc); otherwise
  // bandFor(numberTween.current), when given -- reading the TWEEN, not
  // liveValue directly, is what makes a scrub-pinned number band the
  // same way a live one does; otherwise plain ink, the original default.
  let valueStyle = $derived.by(() => {
    if (paired) return pairedTint(sparklineColor);
    if (!bandFor) return undefined;
    const token = bandToken(bandFor(numberTween.current));
    return token ? `color: ${token}` : undefined;
  });
</script>

{#snippet valueBlock()}
  <span class="stat-tile__number tabular-nums">{formatValue(numberTween.current)}</span>
  {#if unit}<span class="stat-tile__unit">{unit}</span>{/if}
{/snippet}

{#snippet value2Block()}
  {#if label2}<span class="stat-tile__value2-label">{label2}</span>{/if}
  {formatValue2(number2Tween.current)}
  {#if unit2}<span class="stat-tile__unit">{unit2}</span>{/if}
{/snippet}

<svelte:element
  this={href ? 'a' : 'div'}
  {href}
  class="stat-tile"
  class:card={!bare}
  class:stat-tile--bare={bare}
  class:stat-tile--link={!!href}
>
  {#if bare}
    <span class="microlabel stat-tile__chip" class:stat-tile__chip--visible={!!scrubHit}>{chipText}</span>
    <div class="stat-tile__row">
      <span class="stat-tile__row-label">
        <span class="microlabel">{label}</span>
        {#if status}<HealthDot {status} />{/if}
      </span>
      <div class="stat-tile__row-value-stack">
        <span
          class="stat-tile__value"
          class:stat-tile__value--paired={paired}
          class:stat-tile__value--tinted={!paired && !!valueStyle}
          style={valueStyle}>{@render valueBlock()}</span
        >
        {#if liveValue2 !== undefined}
          <span class="stat-tile__value2 stat-tile__value2--paired tabular-nums" style={pairedTint(sparklineColor2)}
            >{@render value2Block()}</span
          >
        {/if}
      </div>
    </div>
    {#if sparklinePoints}
      <Sparkline
        points={sparklinePoints}
        color={sparklineColor}
        points2={value2Points}
        color2={sparklineColor2}
        height={BARE_SPARKLINE_HEIGHT}
      />
    {/if}
  {:else}
    <div class="stat-tile__head">
      <span class="microlabel">{label}</span>
      {#if status}<HealthDot {status} />{/if}
    </div>
    <span class="microlabel stat-tile__chip" class:stat-tile__chip--visible={!!scrubHit}>{chipText}</span>
    <div class="stat-tile__value" class:stat-tile__value--tinted={!paired && !!valueStyle} style={valueStyle}>
      {@render valueBlock()}
    </div>
    {#if liveValue2 !== undefined}
      <div class="stat-tile__value2 tabular-nums">{@render value2Block()}</div>
    {/if}
    {#if sparklinePoints}
      <Sparkline points={sparklinePoints} color={sparklineColor} points2={value2Points} color2={sparklineColor2} />
    {/if}
  {/if}
</svelte:element>

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
  /* href (metric breakdown pages): plain-anchor reset -- the tile's own
     children already carry their own colors, an inherited blue/underline
     from the bare <a> would only fight them. */
  .stat-tile--link {
    color: inherit;
    text-decoration: none;
    cursor: pointer;
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
    /* Tightened from 1.05rem now that the sparkline itself (below) carries
       far more visual weight -- the same generous frame around a now-96px
       chart read as leftover padding, not an intentional rail row. */
    padding: 0.75rem 0;
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
  /* value + value2 stack directly on top of each other, right-aligned --
     the two must never be separated by the sparkline (that's what read
     as value2 belonging to the NEXT row); both live in this one block,
     ABOVE the sparkline, so the row's own hairline (on .stat-tile--bare
     itself, below everything) is the only line anywhere near either. */
  .stat-tile__row-value-stack {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.15rem;
  }
  /* The chip floats over the whole row (top-right, within its own
     padding) so it never reserves space or disturbs the value stack's
     layout -- same "overlay, don't push" contract as the card variant
     below, just anchored to the bare row's own box instead. */
  .stat-tile--bare .stat-tile__chip {
    top: 0;
    right: 0;
  }
  .stat-tile--bare .stat-tile__number {
    font-size: 1.6rem;
  }
  /* Paired value1/value2 (down/up, read/write) read as PEERS, not a
     hero-plus-secondary-annotation pair: value2 matches value1's own
     bare font-size, and both pick up their tint (set inline via
     pairedTint on the wrapping span, above) via `inherit` overrides on
     their own children -- .stat-tile__number/-value2-label otherwise
     hardcode ink/ink-2, which would otherwise win over an ancestor's
     inline color (an element's own explicit color always beats an
     inherited one, regardless of the ancestor's specificity). */
  .stat-tile__value--paired .stat-tile__number {
    color: inherit;
  }
  /* bandFor (status-colored values): same `inherit` override, same
     reason -- .stat-tile__number's own hardcoded ink would otherwise win. */
  .stat-tile__value--tinted .stat-tile__number {
    color: inherit;
  }
  /* Compound (.stat-tile__value2 AND .stat-tile__value2--paired), not
     just the modifier alone -- same specificity trick .stat-tile--bare
     .stat-tile__number already uses above, needed here so this reliably
     beats .stat-tile__value2's own 0.9rem base rule regardless of which
     one happens to come first in the stylesheet. */
  .stat-tile__value2.stat-tile__value2--paired {
    font-size: 1.6rem;
  }
  .stat-tile__value2--paired .stat-tile__value2-label {
    color: inherit;
  }
  /* Sparklines are a real instrument here, not a decorative crumb --
     floored at 120px regardless of how uPlot's own initial-width read
     happens to land, so the rail's own charts always breathe. */
  .stat-tile--bare :global(.sparkline) {
    min-width: 7.5rem;
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
    font-size: 0.9rem;
    color: var(--ink-2);
    margin-top: -0.25rem;
  }
  /* The stack's own gap (above) replaces the card variant's negative
     margin-top -- that value was tuned to pull value2 up snug against
     the hero number when it followed the number directly in normal
     flow; here it would fight the stack's gap and crowd the two lines
     into each other instead. */
  .stat-tile__row-value-stack .stat-tile__value2 {
    margin-top: 0;
  }
  .stat-tile__value2-label {
    color: var(--ink-2);
  }
</style>

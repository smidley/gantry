<!--
  TopBarRow: one leaderboard row, split out of TopBarList (mirroring
  ContainerRow's own precedent -- see its doc) so a live row can own its
  OWN Tween across re-renders. TopBarList's {#each} keys on entity+metric
  (see its own doc for why metric is part of the key too), so the SAME
  TopBarRow instance (and therefore the same Tween) survives a value tick
  or a re-rank, and only a genuine leave/enter -- an entity dropping off
  or climbing onto the leaderboard, OR the metric itself switching --
  tears one down. A Tween instantiated straight inside the parent's
  {#each} block would get recreated (and its glide restarted from zero)
  on every value tick instead.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';
  import ContainerIcon from './ContainerIcon.svelte';

  // formatSecondary (additive, optional): a quiet second line under the
  // value, tweened off row.secondary the same way row.value already is --
  // today only CPU rows carry one (row.secondary = cpu.cores), via
  // topFromFrame's own resourceSecondaryMetricKey. Undefined for every
  // other resource, or on a fetched (non-"now") row, which topFromStore
  // never attaches one to.
  //
  // formatDirection/directionLabels (additive, optional -- Top Consumers
  // view's attribution page): when row.direction is set, these render a
  // [down/read, up/write] PAIR instead of value+secondary -- both at the
  // value's own size/weight (peers, not primary/secondary, same rule
  // StatTile's paired tiles follow), each tinted its own series color. A
  // row only ever carries ONE of row.secondary/row.direction in practice
  // (topFromFrame's own doc), so the two renderings never compete.
  //
  // linkable (row.linkable, default true): false renders a plain name --
  // no link, no icon -- for a summary row that isn't a real container
  // (the attribution page's own "unattributed" row).
  let {
    row,
    maxValue,
    formatValue,
    formatSecondary = undefined,
    formatDirection = undefined,
    directionLabels = undefined,
    linkFor,
    live = false,
  } = $props();
  let linkable = $derived(row.linkable !== false);

  // row.entity is always a container name (every leaderboard on this app
  // is per-container -- see topFromFrame's own doc) -- reading its icon
  // straight off the live frame here, the same ambient-store pattern
  // ContainerRow/ContainerDetail already use, is simpler than threading
  // a new prop through TopBarList/TopConsumers/Overview for a single
  // derived lookup. liveStore is this same import aliased (see below);
  // this component's own `live` prop (the animate-or-not flag) already
  // owns the bare name `live`.
  let icon = $derived(liveStore.frame?.containers?.[row.entity]?.icon);

  // untrack: this is a deliberate ONE-TIME read of row's initial value
  // to seed the Tween -- every value AFTER this (including the very
  // first live tick) flows through the $effect below instead, which
  // reads row.value fresh on every run. Without untrack, Svelte's
  // compiler flags this exact pattern (state_referenced_locally) since
  // reading a prop directly in a non-reactive position is usually a
  // mistake; here it's the documented Tween constructor contract (seed
  // value, not a live binding).
  let valueTween = new Tween(untrack(() => row.value), { duration: liveStore.glideMs, easing: linear });

  // Fetched/historical windows stay fully static (no interpolation, per
  // the smooth-streaming design) -- duration collapses to 0 whenever
  // `live` is false, which makes .set() apply the new value to .current
  // synchronously (see svelte/motion's own Tween.set: duration===0 skips
  // the animation loop entirely), same as reduced motion. The live
  // duration/curve (liveStore.glideMs/linear) are the perpetual-glide
  // motion pass's own retiming -- see streamdriver.ts's "Cadence-driven
  // glide" doc: a fixed guessed duration with a front-loaded curve is
  // what used to settle well before the next real tick and sit frozen.
  $effect(() => {
    const target = row.value;
    const reduced = prefersReducedMotion.current;
    valueTween.set(target, { duration: live && !reduced ? liveStore.glideMs : 0, easing: linear });
  });

  // secondaryTween mirrors valueTween exactly, off row.secondary instead --
  // same live/reduced-motion duration rule. Only ever ticks for a row
  // that HAS a secondary (see the $effect's own guard); a row with none
  // just leaves this at its seed value, never rendered (formatSecondary
  // gates the template below).
  let secondaryTween = new Tween(untrack(() => row.secondary ?? 0), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    if (row.secondary === undefined) return;
    const reduced = prefersReducedMotion.current;
    secondaryTween.set(row.secondary, { duration: live && !reduced ? liveStore.glideMs : 0, easing: linear });
  });

  let secondaryText = $derived(
    formatSecondary && row.secondary !== undefined ? formatSecondary(secondaryTween.current) : '',
  );

  // direction0Tween/direction1Tween mirror secondaryTween exactly, one
  // per side of row.direction -- [down/read, up/write], see the module
  // doc above. Both seed/tick together (a row either has the whole pair
  // or neither half, per topFromFrame's own all-or-nothing attach).
  let direction0Tween = new Tween(untrack(() => row.direction?.[0] ?? 0), { duration: liveStore.glideMs, easing: linear });
  let direction1Tween = new Tween(untrack(() => row.direction?.[1] ?? 0), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    if (!row.direction) return;
    const reduced = prefersReducedMotion.current;
    const duration = live && !reduced ? liveStore.glideMs : 0;
    direction0Tween.set(row.direction[0], { duration, easing: linear });
    direction1Tween.set(row.direction[1], { duration, easing: linear });
  });

  // Clamped to 100: maxValue is usually the list's own max (nothing can
  // exceed it), but a fixed scale (TopBarList's scaleMax, e.g. gpu's 100)
  // has no such guarantee -- a container using more than one GPU engine
  // at once can sum past 100, and its bar must still stop at the track's
  // own edge rather than overflow it.
  let widthPct = $derived(maxValue > 0 ? Math.min(100, Math.max(0, (valueTween.current / maxValue) * 100)) : 0);
</script>

<li class="top-bar-list__row">
  <svelte:element
    this={linkable ? 'a' : 'span'}
    class="top-bar-list__name"
    href={linkable ? linkFor(row.entity) : undefined}
    title={row.entity}
  >
    {#if linkable}<ContainerIcon name={row.entity} {icon} size={16} />{/if}
    <span class="top-bar-list__name-text">{row.entity}</span>
  </svelte:element>
  <div class="top-bar-list__track">
    <div class="top-bar-list__bar" style="width: {widthPct}%"></div>
  </div>
  {#if row.direction && formatDirection}
    <div class="top-bar-list__value-stack">
      <span class="top-bar-list__value tabular-nums" style="color: var(--series-1)">
        {#if directionLabels}{directionLabels[0]}{/if} {formatDirection(direction0Tween.current)}
      </span>
      <span class="top-bar-list__value tabular-nums" style="color: var(--series-4)">
        {#if directionLabels}{directionLabels[1]}{/if} {formatDirection(direction1Tween.current)}
      </span>
    </div>
  {:else}
    <div class="top-bar-list__value-stack">
      <span class="top-bar-list__value tabular-nums">{formatValue(valueTween.current)}</span>
      {#if secondaryText}
        <span class="top-bar-list__secondary tabular-nums">{secondaryText}</span>
      {/if}
    </div>
  {/if}
</li>

<style>
  .top-bar-list__row {
    display: grid;
    grid-template-columns: minmax(5rem, 9rem) 1fr auto;
    align-items: center;
    gap: 0.6rem;
  }
  .top-bar-list__name {
    color: var(--ink);
    text-decoration: none;
    font-size: 0.85rem;
    overflow: hidden;
    min-height: 40px;
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }
  .top-bar-list__name-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Scoped to the anchor tag only -- a non-linkable row (the attribution
     page's own "unattributed" summary) renders this same class on a
     plain <span>, which must not pick up a hover affordance implying
     it's clickable. */
  a.top-bar-list__name:hover {
    text-decoration: underline;
  }
  .top-bar-list__track {
    position: relative;
    height: 18px;
    background: color-mix(in oklab, var(--ink) 6%, transparent);
    border-radius: 4px;
  }
  .top-bar-list__bar {
    position: absolute;
    inset: 0 auto 0 0;
    background: var(--series-1);
    border-radius: 4px;
    min-width: 2px;
    transition: filter 150ms ease;
  }
  /* value + secondary stack right-aligned in the row's third column --
     secondary (additive, CPU-only) sits directly below, never splitting
     the two across any other element. */
  .top-bar-list__value-stack {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.1rem;
  }
  .top-bar-list__value {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink);
    white-space: nowrap;
    text-align: right;
  }
  .top-bar-list__secondary {
    font-family: var(--font-mono);
    font-size: 0.68rem;
    color: var(--ink-2);
    white-space: nowrap;
  }
  /* Bars are current-state, not time series -- no scrubbing, just
     animated emphasis on hover (brightness + a weight-up on the value
     already sitting right next to it). */
  .top-bar-list__row:hover .top-bar-list__bar {
    filter: brightness(1.15);
  }
  .top-bar-list__row:hover .top-bar-list__value {
    font-weight: 700;
  }
</style>

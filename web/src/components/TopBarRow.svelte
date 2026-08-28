<!--
  TopBarRow: one leaderboard row, split out of TopBarList (mirroring
  ContainerRow's own precedent -- see its doc) so a live row can own its
  OWN Tween across re-renders. TopBarList's {#each rows as row
  (row.entity)} keys on entity, so the SAME TopBarRow instance (and
  therefore the same Tween) survives a value tick or a re-rank, and only
  a genuine leave/enter -- an entity dropping off or climbing onto the
  leaderboard -- tears one down. A Tween instantiated straight inside the
  parent's {#each} block would get recreated (and its ease restarted
  from zero) on every value tick instead.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';
  import ContainerIcon from './ContainerIcon.svelte';

  const TWEEN_MS = 400;

  // formatSecondary (additive, optional): a quiet second line under the
  // value, tweened off row.secondary the same way row.value already is --
  // today only CPU rows carry one (row.secondary = cpu.cores), via
  // topFromFrame's own resourceSecondaryMetricKey. Undefined for every
  // other resource, or on a fetched (non-"now") row, which topFromStore
  // never attaches one to.
  let { row, maxValue, formatValue, formatSecondary = undefined, linkFor, live = false } = $props();

  // row.entity is always a container name (every leaderboard on this app
  // is per-container -- see topFromFrame's own doc) -- reading its icon
  // straight off the live frame here, the same ambient-store pattern
  // ContainerRow/ContainerDetail already use, is simpler than threading
  // a new prop through TopBarList/TopConsumers/Overview for a single
  // derived lookup. Renamed to liveStore on import only because this
  // component's own `live` prop (the animate-or-not flag) already owns
  // that name.
  let icon = $derived(liveStore.frame?.containers?.[row.entity]?.icon);

  // untrack: this is a deliberate ONE-TIME read of row's initial value
  // to seed the Tween -- every value AFTER this (including the very
  // first live tick) flows through the $effect below instead, which
  // reads row.value fresh on every run. Without untrack, Svelte's
  // compiler flags this exact pattern (state_referenced_locally) since
  // reading a prop directly in a non-reactive position is usually a
  // mistake; here it's the documented Tween constructor contract (seed
  // value, not a live binding).
  let valueTween = new Tween(untrack(() => row.value), { duration: TWEEN_MS, easing: cubicOut });

  // Fetched/historical windows stay fully static (no interpolation, per
  // the smooth-streaming design) -- duration collapses to 0 whenever
  // `live` is false, which makes .set() apply the new value to .current
  // synchronously (see svelte/motion's own Tween.set: duration===0 skips
  // the animation loop entirely), same as reduced motion.
  $effect(() => {
    const target = row.value;
    const reduced = prefersReducedMotion.current;
    valueTween.set(target, { duration: live && !reduced ? TWEEN_MS : 0, easing: cubicOut });
  });

  // secondaryTween mirrors valueTween exactly, off row.secondary instead --
  // same live/reduced-motion duration rule. Only ever ticks for a row
  // that HAS a secondary (see the $effect's own guard); a row with none
  // just leaves this at its seed value, never rendered (formatSecondary
  // gates the template below).
  let secondaryTween = new Tween(untrack(() => row.secondary ?? 0), { duration: TWEEN_MS, easing: cubicOut });

  $effect(() => {
    if (row.secondary === undefined) return;
    const reduced = prefersReducedMotion.current;
    secondaryTween.set(row.secondary, { duration: live && !reduced ? TWEEN_MS : 0, easing: cubicOut });
  });

  let secondaryText = $derived(
    formatSecondary && row.secondary !== undefined ? formatSecondary(secondaryTween.current) : '',
  );

  let widthPct = $derived(maxValue > 0 ? (valueTween.current / maxValue) * 100 : 0);
</script>

<li class="top-bar-list__row">
  <a class="top-bar-list__name" href={linkFor(row.entity)} title={row.entity}>
    <ContainerIcon name={row.entity} {icon} size={16} />
    <span class="top-bar-list__name-text">{row.entity}</span>
  </a>
  <div class="top-bar-list__track">
    <div class="top-bar-list__bar" style="width: {widthPct}%"></div>
  </div>
  <div class="top-bar-list__value-stack">
    <span class="top-bar-list__value tabular-nums">{formatValue(valueTween.current)}</span>
    {#if secondaryText}
      <span class="top-bar-list__secondary tabular-nums">{secondaryText}</span>
    {/if}
  </div>
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
  .top-bar-list__name:hover {
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

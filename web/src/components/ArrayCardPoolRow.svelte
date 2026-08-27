<!--
  ArrayCardPoolRow: one pool-usage row, split out of ArrayCard the same
  way ContainerRow/TopBarRow are split out of their own parents (see
  their docs) -- a Tween needs to live in ITS OWN component instance so
  Svelte's keyed {#each pools as pool (pool.slot)} preserves it across a
  value tick (ArrayCard's own `pools` is a freshly-allocated array of
  freshly-allocated objects every single derivation) rather than
  recreating it -- which would restart the ease from zero every frame --
  and only tears it down if the slot itself actually disappears.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { fmtBytes, fmtPct } from '../lib/format';
  import { seqStep } from '../lib/metrics';

  const TWEEN_MS = 400;

  let { pool } = $props();

  // untrack: see TopBarRow's identical pattern -- a deliberate one-time
  // seed read; every update after this flows through the $effect below.
  let pctTween = new Tween(untrack(() => pool.pct), { duration: TWEEN_MS, easing: cubicOut });

  $effect(() => {
    pctTween.set(pool.pct, { duration: prefersReducedMotion.current ? 0 : TWEEN_MS, easing: cubicOut });
  });
</script>

<div class="array-card__pool-row">
  <span class="array-card__pool-name">{pool.slot}</span>
  <div class="array-card__pool-track">
    <div class="array-card__pool-fill" style="width: {pctTween.current}%; background: {seqStep(pctTween.current)}"></div>
  </div>
  <span class="tabular-nums array-card__pool-pct">{fmtPct(pctTween.current)}</span>
  <span class="tabular-nums array-card__pool-bytes">{fmtBytes(pool.used)} / {fmtBytes(pool.total)}</span>
</div>

<style>
  .array-card__pool-row {
    display: grid;
    grid-template-columns: 4.5rem 1fr auto auto;
    align-items: center;
    gap: 0.5rem;
  }
  .array-card__pool-name {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .array-card__pool-track {
    height: 8px;
    border-radius: 4px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .array-card__pool-fill {
    height: 100%;
    transition: filter 150ms ease;
  }
  .array-card__pool-pct {
    font-size: 0.75rem;
    min-width: 3em;
    text-align: right;
  }
  /* Bars are current-state, not time series -- no scrubbing, just
     animated emphasis on hover; pct+bytes are already shown adjacent,
     so this is emphasis only, no new value display. */
  .array-card__pool-row:hover .array-card__pool-fill {
    filter: brightness(1.15);
  }
  .array-card__pool-row:hover .array-card__pool-pct {
    font-weight: 700;
  }
  .array-card__pool-bytes {
    font-size: 0.72rem;
    color: var(--ink-2);
    white-space: nowrap;
  }
</style>

<!--
  StatTile: label, big value, optional unit, optional sparkline, optional
  status dot -- the basic building block for Overview's top row and
  similar summary strips in later views.
-->
<script>
  import HealthDot from './HealthDot.svelte';
  import Sparkline from './Sparkline.svelte';

  let { label, value, unit = '', sparklinePoints = undefined, sparklineColor = 'var(--series-1)', status = undefined } = $props();
</script>

<div class="card stat-tile">
  <div class="stat-tile__head">
    <span class="microlabel">{label}</span>
    {#if status}<HealthDot {status} />{/if}
  </div>
  <div class="stat-tile__value">
    <span class="stat-tile__number tabular-nums">{value}</span>
    {#if unit}<span class="stat-tile__unit">{unit}</span>{/if}
  </div>
  {#if sparklinePoints}
    <Sparkline points={sparklinePoints} color={sparklineColor} />
  {/if}
</div>

<style>
  .stat-tile {
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0;
  }
  .stat-tile__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
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
</style>

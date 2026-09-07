<script>
  import { describeAnomaly } from '../lib/overviewStatus';
  import HealthDot from './HealthDot.svelte';
  let { status, chips = [] } = $props();
  let concerns = $derived(status.anomalies.slice(0, 3).map(describeAnomaly));
</script>

<section class="card overview-health overview__headline-zone" class:overview__headline-zone--clear={status.ok} aria-label="Server health" data-pinned="headline">
  <div class="overview-health__summary">
    <h2><HealthDot status={status.ok ? 'good' : 'warning'} /><span class="overview__headline-text">{status.headline}</span></h2>
    {#if chips.length}<div class="overview__chips overview__attention">
      {#each chips as chip (chip.bucket)}
        <a class="overview__chip" data-chip={chip.bucket} href={chip.href} aria-label={chip.ariaLabel}><span class="overview__chip-count">{chip.count}</span> <span class="overview__chip-noun">{chip.noun}</span></a>
      {/each}
    </div>{/if}
  </div>
  {#if concerns.length}
    <ul class="overview-health__issues">
      {#each concerns as concern}
        <li>
          <HealthDot status={concern.severity} />
          <div><strong>{concern.title}</strong>{#if concern.detail}<p>{concern.detail}</p>{/if}</div>
          <a href={concern.href ?? '#/alerts'} aria-label={`Inspect ${concern.title}`}>Inspect <span aria-hidden="true">→</span></a>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .overview-health { padding: 1rem 1.2rem; }
  .overview-health__summary { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
  h2 { margin: 0; font-size: 1.05rem; display: flex; align-items: center; gap: .6rem; }
  .overview__chips { display: flex; gap: .5rem; flex-wrap: wrap; }
  .overview__chip { padding: .4rem .7rem; min-height: 36px; border-radius: 8px; background: var(--surface-soft); font-size: .8125rem; color: var(--ink); text-decoration: none; }
  .overview-health__issues { list-style: none; padding: 0; margin: .7rem 0 0; }
  li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: start; gap: .65rem; padding: .75rem 0; border-top: 1px solid var(--border); }
  li strong { font-size: .875rem; font-weight: 600; }
  li p { font-size: .8125rem; color: var(--ink-2); margin: .15rem 0 0; }
  li a { min-height: 36px; display: inline-flex; align-items: center; gap: .3rem; font-size: .8125rem; color: var(--accent-strong); }
  @media (max-width: 480px) { li { grid-template-columns: auto minmax(0, 1fr); } li a { grid-column: 2; } }
</style>

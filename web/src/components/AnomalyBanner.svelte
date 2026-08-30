<!--
  AnomalyBanner: Container Detail's "why does this need me" module
  (Scott: "if I click into a container that says it needs me, I expect
  to see some explanation about why it needs me instead of having to try
  and figure out what's alerting"). Purely presentational -- every
  decision (whether to show at all, the headline text, which evidence
  lines) is already made by lib/containerAnomaly.ts before this ever
  mounts; the caller simply doesn't render this component when there's
  no active anomaly.

  Status-tinted the same way SourcesBanner's own prominent variant is
  (border+background color-mix off the severity token), so a critical
  banner reads distinctly more urgent than a serious/warning one, not
  just "colored".
-->
<script>
  import HealthDot from './HealthDot.svelte';

  let { severity, headline, evidence = [], onJumpToLogs } = $props();
</script>

<div class="card anomaly-banner anomaly-banner--{severity}" role="alert">
  <div class="anomaly-banner__row">
    <HealthDot status={severity} />
    <strong class="anomaly-banner__headline">{headline}</strong>
    <button type="button" class="anomaly-banner__jump" onclick={onJumpToLogs}>Jump to logs &rarr;</button>
  </div>
  {#if evidence.length > 0}
    <ul class="anomaly-banner__evidence">
      {#each evidence as e (e.ts)}
        <li>
          <span>{e.text}</span>
          <span class="microlabel anomaly-banner__time">{e.relTime}</span>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="anomaly-banner__evidence-empty">No recent events recorded for this container.</p>
  {/if}
</div>

<style>
  .anomaly-banner {
    padding: 0.75rem 0.9rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .anomaly-banner--critical {
    border-color: color-mix(in oklab, var(--status-critical) 45%, transparent);
    background: color-mix(in oklab, var(--status-critical) 8%, var(--surface));
  }
  .anomaly-banner--serious {
    border-color: color-mix(in oklab, var(--status-serious) 45%, transparent);
    background: color-mix(in oklab, var(--status-serious) 8%, var(--surface));
  }
  .anomaly-banner--warning {
    border-color: color-mix(in oklab, var(--status-warning) 45%, transparent);
    background: color-mix(in oklab, var(--status-warning) 8%, var(--surface));
  }
  .anomaly-banner__row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.6rem;
  }
  .anomaly-banner__headline {
    flex: 1;
    min-width: 0;
    font-size: 0.95rem;
    color: var(--ink);
  }
  .anomaly-banner__jump {
    min-height: 40px;
    padding: 0 0.6rem;
    background: transparent;
    border: none;
    color: var(--series-1);
    font-size: 0.8rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .anomaly-banner__jump:hover {
    text-decoration: underline;
  }
  .anomaly-banner__evidence {
    margin: 0;
    padding: 0 0 0 1.3rem;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .anomaly-banner__evidence li {
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .anomaly-banner__time {
    margin-left: 0.4em;
  }
  .anomaly-banner__evidence-empty {
    margin: 0;
    font-size: 0.82rem;
    color: var(--ink-2);
  }
</style>

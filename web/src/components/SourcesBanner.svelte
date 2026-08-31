<!--
  SourcesBanner: surfaces any collector source that isn't "ok" (its
  Detail string explains why -- unavailable hardware, an unmounted path,
  etc.). Per spec: docker degrading is prominent and NOT dismissible (the
  fleet view depends on it); every other source gets a quiet, dismissible
  hint. Dismissal is per-source and resets on reload -- this is a hint,
  not a standing alert queue.

  NVIDIA presence gate (Scott: "I don't have an nvidia GPU, so this
  shouldn't be showing up for me"): a source reporting sourceStatus.ts's
  own SOURCE_NOT_APPLICABLE sentinel (NvidiaCollector.Probe, when no
  NVIDIA GPU is on the box at all) is filtered out by degradedSources
  itself -- it's neither "ok" nor a fixable hint, so this banner stays
  silent for it entirely. Settings' own sources list still shows it, as
  a quiet ok-styled row with its own explanatory copy.
-->
<script>
  import HealthDot from './HealthDot.svelte';
  import { degradedSources } from '../lib/sourceStatus';

  let { sources = {} } = $props();
  let dismissed = $state(new Set());

  let degraded = $derived(degradedSources(sources));
  let dockerDegraded = $derived(degraded.find(([name]) => name === 'docker'));
  let otherDegraded = $derived(degraded.filter(([name]) => name !== 'docker' && !dismissed.has(name)));

  function dismiss(name) {
    dismissed = new Set(dismissed);
    dismissed.add(name);
  }
</script>

{#if dockerDegraded || otherDegraded.length > 0}
  <details class="card sources-panel" open={!!dockerDegraded}>
    <summary class="sources-panel__summary">
      <span class="sources-panel__summary-icon" class:sources-panel__summary-icon--critical={!!dockerDegraded} aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 9v4M12 17h.01"/><path d="M10.3 4.4 2.6 18a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 4.4a2 2 0 0 0-3.4 0Z"/></svg>
      </span>
      <span class="sources-panel__summary-copy">
        <strong>{dockerDegraded ? 'A core data source needs attention' : `${otherDegraded.length} optional ${otherDegraded.length === 1 ? 'source' : 'sources'} unavailable`}</strong>
        <small>{dockerDegraded ? 'Some monitoring data may be incomplete.' : 'Gantry is monitoring everything else normally.'}</small>
      </span>
      <span class="sources-panel__chevron" aria-hidden="true">
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m6 8 4 4 4-4"/></svg>
      </span>
    </summary>

    <div class="sources-panel__body">
      {#if dockerDegraded}
        <div class="sources-banner sources-banner--prominent" role="alert">
          <HealthDot status="critical" label="docker" />
          <span class="sources-banner__detail">{dockerDegraded[1]}</span>
        </div>
      {/if}

      {#each otherDegraded as [name, detail] (name)}
        <div class="sources-banner">
          <HealthDot status="warning" label={name} />
          <span class="sources-banner__detail">
            {detail}
            {#if name === 'pressure'}
              <a
                class="sources-banner__learn-more"
                href="https://github.com/smidley/gantry/blob/main/docs/psi.md"
                target="_blank"
                rel="noopener"
              >
                Learn more &rarr;
              </a>
            {/if}
          </span>
          <button type="button" class="sources-banner__dismiss" onclick={() => dismiss(name)} aria-label="Dismiss {name} notice">
            &times;
          </button>
        </div>
      {/each}
    </div>
  </details>
{/if}

<style>
  .sources-panel {
    overflow: hidden;
    background: color-mix(in oklab, var(--surface) 90%, var(--accent-soft));
  }
  .sources-panel__summary {
    min-height: 58px;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.65rem 0.9rem;
    cursor: pointer;
    list-style: none;
  }
  .sources-panel__summary::-webkit-details-marker {
    display: none;
  }
  .sources-panel__summary-icon {
    width: 32px;
    height: 32px;
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 9px;
    color: var(--status-warning);
    background: color-mix(in oklab, var(--status-warning) 12%, transparent);
  }
  .sources-panel__summary-icon--critical {
    color: var(--status-critical);
    background: color-mix(in oklab, var(--status-critical) 12%, transparent);
  }
  .sources-panel__summary-icon svg {
    width: 17px;
    height: 17px;
  }
  .sources-panel__summary-copy {
    min-width: 0;
    display: flex;
    flex: 1;
    flex-direction: column;
  }
  .sources-panel__summary-copy strong {
    color: var(--ink);
    font-size: 0.82rem;
    font-weight: 650;
  }
  .sources-panel__summary-copy small {
    color: var(--ink-2);
    font-size: 0.73rem;
  }
  .sources-panel__chevron {
    display: inline-flex;
    width: 20px;
    height: 20px;
    color: var(--ink-3);
    transition: transform 160ms ease;
  }
  .sources-panel[open] .sources-panel__chevron {
    transform: rotate(180deg);
  }
  .sources-panel__body {
    padding: 0 0.9rem 0.75rem;
  }
  .sources-banner {
    padding: 0.62rem 0;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    border-top: 1px solid var(--border);
  }
  .sources-banner--prominent {
    border-color: color-mix(in oklab, var(--status-critical) 26%, var(--border));
  }
  .sources-banner__detail {
    flex: 1;
    font-size: 0.85rem;
    color: var(--ink-2);
    min-width: 0;
  }
  .sources-banner__learn-more {
    margin-left: 0.4em;
    color: var(--accent);
    text-decoration: none;
    white-space: nowrap;
  }
  .sources-banner__learn-more:hover {
    text-decoration: underline;
  }
  .sources-banner__dismiss {
    min-width: 32px;
    min-height: 32px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    color: var(--ink-2);
    font-size: 1.1rem;
    cursor: pointer;
    flex-shrink: 0;
  }
  .sources-banner__dismiss:hover {
    color: var(--ink);
  }
  @media (max-width: 34rem) {
    .sources-panel__summary-copy small {
      display: none;
    }
    .sources-banner {
      align-items: flex-start;
    }
  }
</style>

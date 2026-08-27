<!--
  SourcesBanner: surfaces any collector source that isn't "ok" (its
  Detail string explains why -- unavailable hardware, an unmounted path,
  etc.). Per spec: docker degrading is prominent and NOT dismissible (the
  fleet view depends on it); every other source gets a quiet, dismissible
  hint. Dismissal is per-source and resets on reload -- this is a hint,
  not a standing alert queue.
-->
<script>
  import HealthDot from './HealthDot.svelte';

  let { sources = {} } = $props();
  let dismissed = $state(new Set());

  let degraded = $derived(Object.entries(sources).filter(([, detail]) => detail !== 'ok'));
  let dockerDegraded = $derived(degraded.find(([name]) => name === 'docker'));
  let otherDegraded = $derived(degraded.filter(([name]) => name !== 'docker' && !dismissed.has(name)));

  function dismiss(name) {
    dismissed = new Set(dismissed);
    dismissed.add(name);
  }
</script>

{#if dockerDegraded}
  <div class="card sources-banner sources-banner--prominent" role="alert">
    <HealthDot status="critical" label="docker" />
    <span class="sources-banner__detail">{dockerDegraded[1]}</span>
  </div>
{/if}

{#each otherDegraded as [name, detail] (name)}
  <div class="card sources-banner">
    <HealthDot status="warning" label={name} />
    <span class="sources-banner__detail">{detail}</span>
    <button type="button" class="sources-banner__dismiss" onclick={() => dismiss(name)} aria-label="Dismiss {name} notice">
      &times;
    </button>
  </div>
{/each}

<style>
  .sources-banner {
    padding: 0.6rem 0.85rem;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.6rem;
  }
  .sources-banner--prominent {
    border-color: color-mix(in oklab, var(--status-critical) 45%, transparent);
    background: color-mix(in oklab, var(--status-critical) 8%, var(--surface));
  }
  .sources-banner__detail {
    flex: 1;
    font-size: 0.85rem;
    color: var(--ink-2);
    min-width: 0;
  }
  .sources-banner__dismiss {
    min-width: 40px;
    min-height: 40px;
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
</style>

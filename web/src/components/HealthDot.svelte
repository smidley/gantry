<!--
  HealthDot: a colored status dot, always paired with a text label
  (visible when `label` is given, screen-reader-only otherwise) --
  status color alone never carries meaning on its own, per the dataviz
  status rule.
-->
<script>
  let { status, label } = $props();

  const STATUS_TEXT = {
    good: 'Good',
    warning: 'Warning',
    serious: 'Serious',
    critical: 'Critical',
  };
</script>

<span class="health-dot" title={label ?? STATUS_TEXT[status]}>
  <span class="health-dot__shape health-dot--{status}" aria-hidden="true"></span>
  {#if label}
    <span class="health-dot__label">{label}</span>
  {:else}
    <span class="sr-only">{STATUS_TEXT[status]}</span>
  {/if}
</span>

<style>
  .health-dot {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }
  .health-dot__shape {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .health-dot--good {
    background: var(--status-good);
  }
  .health-dot--warning {
    background: var(--status-warning);
  }
  .health-dot--serious {
    background: var(--status-serious);
  }
  .health-dot--critical {
    background: var(--status-critical);
  }
  .health-dot__label {
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--ink-2);
    white-space: nowrap;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>

<!--
  ImpactPanel: ContainerDetail's own "how is this container touching
  the rest of the fleet" section (Phase 5 Task 12, the backlog's
  "Container->system impact surfacing," now with the engine behind it).
  Slots beside the existing Storage section, reusing its own
  fetchContainerStorage plumbing for the share strip's per-device rows.

  Two directions, always both, always labelled (never just "related
  findings" lumped together): "Being slowed by" (this container is the
  NAMED victim) and "Slowing" (this container is a culprit, alone or
  shared). Both read the statement text verbatim off the finding itself
  -- one source of truth for the wording, never a second hand-written
  paraphrase that could drift from what the engine actually said.

  Below them, an ALWAYS-PRESENT share strip (no engine required, honest
  at every moment): this container's current share of host CPU, of
  host memory, of each device it touches, and of each GPU engine it
  uses -- the lean correlational view the backlog asked for, rendered
  as plain track+fill bars. This is deliberately the one section of
  this panel that never has an empty state: even a perfectly idle
  container still HAS a (zero) CPU/memory share, and showing 0% is more
  honest than hiding the row.
-->
<script>
  import { fmtPct } from '../lib/format';
  import { insightsAffecting, insightsCausedBy } from '../lib/insights';
  import { eventHref } from '../lib/eventHref';

  // insights: the live frame's own insights.active (compact, statement
  // included) -- no separate fetch. devices/gpuEngines/cpuPct/memPct
  // are all pre-extracted by ContainerDetail (which already holds the
  // storage fetch + the live frame in scope) rather than re-derived
  // here, keeping this component a plain presentational leaf.
  let { containerName, insights = [], cpuPct = undefined, memPct = undefined, devices = [], gpuEngines = [] } = $props();

  let affecting = $derived(insightsAffecting(insights, containerName));
  let causedBy = $derived(insightsCausedBy(insights, containerName));
  let hasFindings = $derived(affecting.length > 0 || causedBy.length > 0);

  function otherEntityHref(inst) {
    // "Being slowed by" links to the culprit; "Slowing" links to the
    // victim when one is named -- either way, the OTHER party in the
    // relationship, not this page's own container again.
    return eventHref('container', inst.culprit || inst.culprits?.split(',')[0] || '');
  }

</script>

<div class="card impact-panel">
  <span class="microlabel">Impact</span>

  {#if !hasFindings}
    <p class="impact-panel__calm">Not affecting or affected by other containers right now.</p>
  {:else}
    <div class="impact-panel__direction">
      <span class="impact-panel__direction-label">Being slowed by</span>
      {#if affecting.length === 0}
        <p class="microlabel impact-panel__empty-direction">Nothing is slowing this container right now.</p>
      {:else}
        <ul class="impact-panel__rows">
          {#each affecting as inst (inst.id)}
            {@const href = otherEntityHref(inst)}
            <li class="impact-panel__row">
              <span class="impact-panel__chip impact-panel__chip--{inst.confidence}">{inst.confidence === 'confirmed' ? 'Confirmed' : 'Likely'}</span>
              <span class="impact-panel__statement">
                {#if href}<a {href}>{inst.statement}</a>{:else}{inst.statement}{/if}
              </span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <div class="impact-panel__direction">
      <span class="impact-panel__direction-label">Slowing</span>
      {#if causedBy.length === 0}
        <p class="microlabel impact-panel__empty-direction">Not slowing any other container right now.</p>
      {:else}
        <ul class="impact-panel__rows">
          {#each causedBy as inst (inst.id)}
            {@const href = eventHref(inst.victim_kind, inst.victim)}
            <li class="impact-panel__row">
              <span class="impact-panel__chip impact-panel__chip--{inst.confidence}">{inst.confidence === 'confirmed' ? 'Confirmed' : 'Likely'}</span>
              <span class="impact-panel__statement">
                {#if href}<a {href}>{inst.statement}</a>{:else}{inst.statement}{/if}
              </span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}

  <div class="impact-panel__direction">
    <span class="impact-panel__direction-label">Current share</span>
    <div class="impact-panel__share-strip">
      {#if cpuPct !== undefined}
        <div class="impact-panel__share-row">
          <span class="impact-panel__share-label">Host CPU</span>
          <div class="impact-panel__share-track"><div class="impact-panel__share-fill" style="width: {Math.min(100, cpuPct)}%"></div></div>
          <span class="impact-panel__share-value tabular-nums">{fmtPct(cpuPct)}</span>
        </div>
      {/if}
      {#if memPct !== undefined}
        <div class="impact-panel__share-row">
          <span class="impact-panel__share-label">Host memory</span>
          <div class="impact-panel__share-track"><div class="impact-panel__share-fill" style="width: {Math.min(100, memPct)}%"></div></div>
          <span class="impact-panel__share-value tabular-nums">{fmtPct(memPct)}</span>
        </div>
      {/if}
      {#each devices as d (d.device)}
        <div class="impact-panel__share-row">
          <span class="impact-panel__share-label" title={d.device}>{d.label}</span>
          <div class="impact-panel__share-track"><div class="impact-panel__share-fill" style="width: {Math.min(100, d.sharePct)}%"></div></div>
          <span class="impact-panel__share-value tabular-nums">{fmtPct(d.sharePct)}</span>
        </div>
      {/each}
      {#each gpuEngines as g (g.engine)}
        <div class="impact-panel__share-row">
          <span class="impact-panel__share-label">GPU {g.engine}</span>
          <div class="impact-panel__share-track"><div class="impact-panel__share-fill" style="width: {Math.min(100, g.busyPct)}%"></div></div>
          <span class="impact-panel__share-value tabular-nums">{fmtPct(g.busyPct)}</span>
        </div>
      {/each}
      {#if cpuPct === undefined && memPct === undefined && devices.length === 0 && gpuEngines.length === 0}
        <p class="microlabel impact-panel__empty-direction">No live share data yet.</p>
      {/if}
    </div>
  </div>
</div>

<style>
  .impact-panel {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .impact-panel__calm {
    margin: 0;
    color: var(--ink-2);
  }
  .impact-panel__direction {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .impact-panel__direction-label {
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--ink-2);
  }
  .impact-panel__empty-direction {
    margin: 0;
  }
  .impact-panel__rows {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .impact-panel__row {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }
  .impact-panel__chip {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    border: 1px solid currentColor;
  }
  .impact-panel__chip--likely {
    color: var(--ink-2);
  }
  .impact-panel__chip--confirmed {
    color: var(--status-warning);
  }
  .impact-panel__statement {
    font-size: 0.85rem;
    color: var(--ink);
  }
  .impact-panel__statement a {
    color: inherit;
    text-decoration: underline;
    text-decoration-color: color-mix(in oklab, var(--ink) 30%, transparent);
  }
  .impact-panel__share-strip {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .impact-panel__share-row {
    display: grid;
    grid-template-columns: minmax(4.5rem, 7rem) 1fr auto;
    align-items: center;
    gap: 0.6rem;
  }
  .impact-panel__share-label {
    font-size: 0.8rem;
    color: var(--ink-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .impact-panel__share-track {
    position: relative;
    height: 14px;
    background: color-mix(in oklab, var(--ink) 6%, transparent);
    border-radius: 4px;
  }
  .impact-panel__share-fill {
    position: absolute;
    inset: 0 auto 0 0;
    background: var(--series-1);
    border-radius: 4px;
    min-width: 2px;
  }
  .impact-panel__share-value {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink);
    text-align: right;
    min-width: 3.2rem;
  }
</style>

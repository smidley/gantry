<!--
  TopBarList: a horizontal-bar leaderboard -- Overview's compact Top
  Consumers module and the full Top Consumers view both render one of
  these. Bar length is relative to the list's own max value; every bar is
  the SAME categorical hue (--series-1) -- per dataviz's "color follows
  entity, not rank" rule, a leaderboard's entities churn from one window
  to the next, so per-rank color would misleadingly suggest a stable
  identity. The row label (a link to the container's detail page) is what
  actually carries identity. Value renders in ink, in its own fixed
  column right after the track -- immediately at the bar's end rather
  than floating at the fill's exact edge, which would either clip at
  high percentages or need JS text-width measurement to avoid it -- as
  already-formatted text (formatValue); this component has no unit
  knowledge of its own. emptyMessage (additive, optional) lets a caller
  give its own empty state a window-specific direction (e.g. "No data in
  the last 7 days yet.") rather than the generic default.
-->
<script>
  let {
    rows = [],
    formatValue = (v) => String(v),
    linkFor = (entity) => `#/containers/${encodeURIComponent(entity)}`,
    emptyMessage = 'No data for this window yet.',
  } = $props();

  let maxValue = $derived(rows.reduce((m, r) => Math.max(m, r.value), 0));
</script>

{#if rows.length === 0}
  <p class="microlabel top-bar-list__empty">{emptyMessage}</p>
{:else}
  <ol class="top-bar-list">
    {#each rows as row (row.entity)}
      <li class="top-bar-list__row">
        <a class="top-bar-list__name" href={linkFor(row.entity)} title={row.entity}>{row.entity}</a>
        <div class="top-bar-list__track">
          <div class="top-bar-list__bar" style="width: {maxValue > 0 ? (row.value / maxValue) * 100 : 0}%"></div>
        </div>
        <span class="top-bar-list__value tabular-nums">{formatValue(row.value)}</span>
      </li>
    {/each}
  </ol>
{/if}

<style>
  .top-bar-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    margin: 0;
    padding: 0;
    list-style: none;
  }
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
    text-overflow: ellipsis;
    white-space: nowrap;
    min-height: 40px;
    display: flex;
    align-items: center;
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
  }
  .top-bar-list__value {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink);
    white-space: nowrap;
    text-align: right;
  }
  .top-bar-list__empty {
    margin: 0;
  }
</style>

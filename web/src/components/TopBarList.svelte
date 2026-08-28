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

  live (additive, optional, default false -- smooth-streaming mechanism
  3) opts a caller into tweening each row's own bar width AND value --
  TopConsumers passes `live={windowKey === 'now'}` (only Now recomputes
  every SSE frame; every fetched window must stay static), while
  Overview's always-live compact module just passes true. Each row's own
  Tween actually lives in TopBarRow (see its doc for why).

  formatSecondary (additive, optional): passed straight through to every
  row -- see TopBarRow's own doc for what it renders and when.

  formatDirection/directionLabels (additive, optional -- Top Consumers
  view's attribution page): passed straight through to every row too --
  see TopBarRow's own doc. Only meaningful for a row that actually
  carries row.direction (topFromFrame's own opt-in), so Overview's
  compact module (never opts in) is unaffected either way.

  metricKey (additive, optional, default '' -- fixes a real bug: switching
  the Top Consumers resource tab left a row showing the PRIOR resource's
  value, tweening oddly toward the new one instead of reading correctly):
  folded into the {#each} block's own key alongside row.entity below.
  Without it, an entity present on more than one resource's leaderboard
  (a container that's simultaneously top-5 on CPU AND memory, say) keeps
  the SAME TopBarRow instance -- and therefore the same Tween, mid-glide
  toward the OLD metric's value -- across a resource switch, since the
  key only ever recognized entity identity, never metric identity. Every
  caller that can switch resource (TopConsumers, Overview's compact
  module) passes its own current resource; a caller with a fixed,
  never-switching resource can leave this at its default -- entity alone
  is a perfectly stable key there.
  scaleMax (additive, optional): the denominator a bar's width reads
  against. Given (cpu/gpu's fixed 100, mem's derived host-total-bytes --
  see topFromFrame's resourceScaleMax), every row's bar reads as an
  absolute fraction of the machine, so a quiet 6.5% consumer draws a bar
  6.5% full rather than a nearly-full one just because nothing else is
  busy right now. Omitted (net/io, or mem before a host stat has landed)
  falls back to the previous relative-to-the-list's-own-max behavior --
  net/io have no natural ceiling, so "busiest of what's showing" is the
  only scale that ever made sense for them.
-->
<script>
  import TopBarRow from './TopBarRow.svelte';

  let {
    rows = [],
    formatValue = (v) => String(v),
    formatSecondary = undefined,
    formatDirection = undefined,
    directionLabels = undefined,
    linkFor = (entity) => `#/containers/${encodeURIComponent(entity)}`,
    emptyMessage = 'No data for this window yet.',
    live = false,
    scaleMax = undefined,
    metricKey = '',
  } = $props();

  let maxValue = $derived(scaleMax ?? rows.reduce((m, r) => Math.max(m, r.value), 0));
</script>

{#if rows.length === 0}
  <p class="microlabel top-bar-list__empty">{emptyMessage}</p>
{:else}
  <ol class="top-bar-list">
    {#each rows as row (`${row.entity}::${metricKey}`)}
      <TopBarRow {row} {maxValue} {formatValue} {formatSecondary} {formatDirection} {directionLabels} {linkFor} {live} />
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
  .top-bar-list__empty {
    margin: 0;
  }
</style>

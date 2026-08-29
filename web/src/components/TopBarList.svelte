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

  Row order (live only): displayRows re-sorts `rows` by each entity's
  last-tick value rather than the brand-new one just computed -- see
  lib/topFromFrame.ts's reorderByLastDisplayedValue for the full "rank
  must track the display, not the still-gliding-toward-it target" fix
  this exists for. previousValues is this component instance's own
  persistent state (mutated in place across ticks, deliberately plain --
  not $state -- since nothing here reads it directly; only the effect's
  own re-sorted OUTPUT, displayRows, needs to be reactive).

  Reordering itself animates (Scott: "when items change place in
  something like top consumers... make the transition flow smooth
  instead of just a hard swap or new entry") -- animate:flip glides a row
  that's still present to its new position; transition:fade covers a row
  actually entering/leaving the list (a container crossing onto or off
  the leaderboard). Both collapse to 0 under prefers-reduced-motion.

  graceRows (live only, see topFromFrame's own doc) is why this actually
  animates instead of "visibly doing nothing": topFromFrame's top-N cut
  has no memory, so a container hovering right at the cutoff crosses it
  on nearly every tick, and a key that LEAVES `rows` then comes straight
  BACK next tick makes Svelte link the new intro to the still-running
  outro as its "counterpart" (so a reversed transition resumes smoothly
  instead of jumping) -- but the resumed duration is `configured * |t2 -
  t1|`, and t1 here is the counterpart's own barely-progressed position,
  so it rounds to ~0: an instant, invisible pop instead of a fade, and
  the row never cleanly finishes its outro either, leaking an
  invisible opacity:0 `<li>` that piles up for the rest of the page's
  life (confirmed live: accumulating stale Animation objects on rows
  that were never removed). graceRows keeps a briefly-dropped entity in
  `displayRows` (frozen at its last value) for one extra tick instead of
  handing it straight to Svelte as a real removal, so a boundary flicker
  never reaches the transition system as an outro+intro pair at all --
  the same "don't let a flicker at the edge look like two events" fix
  shape as reorderByLastDisplayedValue's own doc, just for MEMBERSHIP
  instead of RANK. The events feed's own animate:flip+transition:fade
  (Overview.svelte/Events.svelte) never needed this: it only ever grows
  one row at a time, with no value-threshold cutoff to bounce across.
-->
<script>
  import { flip } from 'svelte/animate';
  import { fade } from 'svelte/transition';
  import { prefersReducedMotion } from 'svelte/motion';
  import { reorderByLastDisplayedValue, withGracePeriod } from '../lib/topFromFrame';
  import TopBarRow from './TopBarRow.svelte';

  // FLIP_DURATION_MS: modest, per the ask -- long enough to read as a
  // glide, short enough not to lag behind the next tick.
  const FLIP_DURATION_MS = 250;

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
  let flipDuration = $derived(prefersReducedMotion.current ? 0 : FLIP_DURATION_MS);

  const previousValues = new Map();
  const graceState = { lastSeenRow: new Map(), lastPresentTick: new Map(), tick: 0 };
  let displayRows = $state([]);
  $effect(() => {
    displayRows = live
      ? reorderByLastDisplayedValue(withGracePeriod(rows, graceState, metricKey), previousValues, metricKey)
      : rows;
  });
</script>

{#if rows.length === 0}
  <p class="microlabel top-bar-list__empty">{emptyMessage}</p>
{:else}
  <ol class="top-bar-list">
    {#each displayRows as row (`${row.entity}::${metricKey}`)}
      <li animate:flip={{ duration: flipDuration }} transition:fade={{ duration: flipDuration }}>
        <TopBarRow {row} {maxValue} {formatValue} {formatSecondary} {formatDirection} {directionLabels} {linkFor} {live} />
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
  .top-bar-list__empty {
    margin: 0;
  }
</style>

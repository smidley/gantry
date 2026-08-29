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

  Row order (live only): the CALLER is responsible for handing `rows`
  already stable -- see lib/rankStability.ts's stableTopN, which ranks by
  a rolling average instead of the instant sample, gates membership
  changes behind a few consecutive ticks of real evidence, and only
  adopts a newly-changed order at most once every ~10s. This component
  used to own that smoothing itself (a reorder-by-last-value plus a
  one-tick membership grace period), but real-box churn among a dozen
  near-tied containers defeated both -- see the git history for the
  gory details. Pushing the stability decision up to where the data is
  computed (once per resource, not once per list) is also what lets the
  Metrics page's hero chart -- a totally different rendering, no
  TopBarList in sight -- share the exact same stable ranking.

  Reordering itself animates (Scott: "when items change place in
  something like top consumers... make the transition flow smooth
  instead of just a hard swap or new entry") -- animate:flip glides a row
  that's still present to its new position; in:fade/out:fade cover a row
  actually entering/leaving the list (a container crossing onto or off
  the leaderboard). Both collapse to 0 under reduced motion, per
  motion.svelte's own resolved preference (system/on/off -- Settings).

  in:fade/out:fade, not one combined transition:fade: a single
  transition: directive is bidirectional -- Svelte links a fresh intro to
  a still-running outro on the SAME key as its own "reverse" counterpart,
  which is exactly what let a row stick at a dead, never-cleaned-up
  opacity:0 whenever two changes landed close together (confirmed live,
  the old grace period's own failure mode, and still reproducible here
  on a rare multi-row reorder even after the caller's own stability layer
  cut membership changes down to one every ~10s). Separate in:/out:
  directives never link this way -- each is independent, one-shot, so
  there's no "reverse" to accidentally resume, matching the events feed's
  own animate:flip+in:fly/out:fade (Overview.svelte/Events.svelte).
-->
<script>
  import { onMount } from 'svelte';
  import { flip } from 'svelte/animate';
  import { fade } from 'svelte/transition';
  import { motion } from '../lib/motion.svelte';
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
  let flipDuration = $derived(motion.reduced ? 0 : FLIP_DURATION_MS);

  // sweepStale is a defensive backstop, not the real fix: Svelte's own
  // outro-then-remove bookkeeping for THIS keyed each-block (animate:flip
  // plus in:/out:fade on rows whose membership churns, however rarely)
  // has, even now that churn is rare, occasionally left one of two
  // things behind -- confirmed live, root cause not fully pinned down:
  // a departed row's own <li> never actually removed (stuck fully
  // opaque, orphaned), or a CURRENT row's own intro never finishing
  // (stuck fully transparent despite being a real, present member).
  // Since `rows` is always the authoritative answer to "what should be
  // showing," a pass that (a) removes any list item whose own key isn't
  // in the current `rows`, and (b) releases any CURRENT row's stuck,
  // already-`finished` animations back to its plain unanimated (opacity
  // 1) state, can't ever misfire against something actually still
  // mid-transition -- at worst it's a no-op. Runs on a plain interval
  // rather than off a `rows`-keyed $effect deliberately: `rows` gets a
  // new array every live tick even when nothing changed, so a timer
  // re-armed inside that effect would keep getting cancelled and
  // rescheduled well before it ever actually fires. onMount's own
  // interval survives every tick untouched, and only ever reads the
  // CURRENT rows/metricKey (plain reactive bindings, read fresh each
  // firing) -- so it still can't remove or unfreeze anything real.
  const SWEEP_INTERVAL_MS = 1000;
  let listEl = $state();
  onMount(() => {
    const interval = setInterval(() => {
      const el = listEl;
      if (!el) return;
      const keys = new Set(rows.map((r) => `${r.entity}::${metricKey}`));
      for (const li of Array.from(el.children)) {
        if (!keys.has(li.dataset.rowKey)) {
          li.remove();
          continue;
        }
        // A CURRENT row stuck away from opacity 1 has an intro that
        // never finished -- cancel every animation on it (regardless of
        // playState: a stuck one isn't reliably reporting 'finished')
        // and force the plain, unanimated state directly.
        if (parseFloat(getComputedStyle(li).opacity) < 0.99) {
          for (const anim of li.getAnimations()) anim.cancel();
          li.style.opacity = '1';
        }
      }
    }, SWEEP_INTERVAL_MS);
    return () => clearInterval(interval);
  });
</script>

{#if rows.length === 0}
  <p class="microlabel top-bar-list__empty">{emptyMessage}</p>
{:else}
  <ol class="top-bar-list" bind:this={listEl}>
    {#each rows as row (`${row.entity}::${metricKey}`)}
      <li
        animate:flip={{ duration: flipDuration }}
        in:fade={{ duration: flipDuration }}
        out:fade={{ duration: flipDuration }}
        data-row-key={`${row.entity}::${metricKey}`}
      >
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

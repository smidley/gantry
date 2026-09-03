<!--
  Events: a filterable feed over /api/events -- kind multi-select (a
  fixed known-kinds list; none checked means no kind filter at all, not
  "show nothing"), an entity text filter (exact match, matching the
  API's own `entity = ?` semantics -- see fetchEvents/api_history.go),
  and a time-range preset. "Load more" pages backward via a before-
  cursor (to = the oldest loaded event's ts, minus one so that inclusive
  boundary isn't re-fetched) -- see loadMore's own doc for the one
  known edge case that cursor shape accepts, per the brief's own spec.

  Auto-refresh: this is a monitoring page, so it can't rely on the user
  ever re-triggering the filter effect below -- left open, it would show
  the same page-load snapshot forever. refreshFirstPage() re-fetches just
  the first page on a 30s interval and on window focus, the same
  onMount + setInterval + focus-listener shape Overview's loadEvents and
  Storage's loadParityHistory both already use for their own out-of-frame
  event fetches. See refreshFirstPage's own doc for why a background
  refresh REPLACES the whole list (discarding any extra "Load more" pages)
  rather than trying to preserve or re-fetch them.

  Needs you strip (the counts pass's own open question, finally
  answered -- Overview's alerts chip promises "click on the number and
  then be brought to a list of items that need attention," and this is
  that list): the SAME derivation Overview's chip reads
  (deriveOverviewStatus), scoped down to attentionCounts.ts's alerts
  bucket -- every kind except an insight-backed contention, which the
  chip's OTHER half already sends to Insights instead. Wired
  independently of Overview's own identical derivation rather than
  sharing a hook: this view otherwise reads no live-frame state at all,
  and the duplication is small enough that a shared module would cost
  more than it saved for its only two call sites. One CalloutRow per
  anomaly, the exact per-item row Overview itself rendered before the
  counts pass -- Ack control included, same store, so acking a row here
  drops the Overview headline's own count on the very next reactive
  tick, no reload (acks.svelte.ts is a shared singleton). The strip
  disappears the instant its last row is acked or the concern itself
  clears; it never renders at all while the bucket is empty, so a
  healthy fleet costs this page nothing.
-->
<script>
  import { onMount } from 'svelte';
  import { flip } from 'svelte/animate';
  import { fade, fly } from 'svelte/transition';
  import { motion } from '../lib/motion.svelte';
  import { fetchEvents } from '../lib/api';
  import { debounce } from '../lib/debounce';
  import { live } from '../lib/sse.svelte';
  import { acks } from '../lib/acks.svelte';
  import { deriveOverviewStatus } from '../lib/overviewStatus';
  import { alertsBucketAnomalies } from '../lib/attentionCounts';
  import { unhealthyContainerNames } from '../lib/containerStatus';
  import EventFeedItem from '../components/EventFeedItem.svelte';
  import CalloutRow from '../components/CalloutRow.svelte';

  // FEED_MOTION_MS: modest, matching TopBarList's own flip duration --
  // long enough to read as a glide/fly, short enough not to lag behind
  // the next arrival. 0 under reduced motion (Scott's own ask -- motion.svelte).
  const FEED_MOTION_MS = 250;

  const PAGE_LIMIT = 200;
  const REFRESH_MS = 30_000;
  // ENTITY_DEBOUNCE_MS coalesces the entity text field's per-keystroke
  // changes into one request 300ms after typing pauses -- see I2's own
  // fix note: an un-debounced free-text filter fired one /api/events call
  // PER KEYSTROKE. Kind checkboxes and the time-range preset stay
  // immediate (below, the fetch effect reads selectedKinds/timePreset
  // directly): only free-text typing produces the rapid-fire changes a
  // debounce is for.
  const ENTITY_DEBOUNCE_MS = 300;

  // KNOWN_KINDS is the fixed vocabulary the filter row's checkboxes
  // cover -- every event kind any collector/fake generator can emit
  // (see store.Event call sites across docker/registry.go, unraid's
  // var.go/disks.go, and fake.go), in the task's own given order.
  const KNOWN_KINDS = [
    'container.start',
    'container.die',
    'container.oom',
    'container.health',
    'array.state',
    'parity.start',
    'parity.finish',
    'disk.errors',
    'alert.fired',
    'alert.resolved',
    'insight.detected',
    'insight.resolved',
  ];
  const TIME_PRESETS = [
    { key: '1h', label: '1h', seconds: 3600 },
    { key: '24h', label: '24h', seconds: 86400 },
    { key: '7d', label: '7d', seconds: 7 * 86400 },
    { key: 'all', label: 'All', seconds: null },
  ];

  let feedMotionMs = $derived(motion.reduced ? 0 : FEED_MOTION_MS);

  // --- Needs you (this view's own top-of-file doc) ------------------------
  //
  // Overview's own identical derivation, re-wired here rather than
  // shared: unhealthyNames off the live frame's containers, the rest of
  // OverviewStatusInput straight off the frame's own matching blocks,
  // acks.list from the same shared store CalloutRow's Ack control
  // writes to. alertsBucketAnomalies then trims deriveOverviewStatus's
  // full anomaly list down to the one bucket this page can show as rows
  // -- a contention stays Insights' own to explain.
  let unhealthyNames = $derived(unhealthyContainerNames(live.frame?.containers ?? {}));
  let overviewStatus = $derived(
    deriveOverviewStatus({
      unhealthyNames,
      arrayStarted: live.frame?.unraid?.array?.['array.started'],
      disks: live.frame?.disks,
      sources: live.frame?.sources,
      alerts: live.frame?.alerts?.firing,
      acks: acks.list,
      insights: live.frame?.insights?.active,
    }),
  );
  let needsYouAnomalies = $derived(alertsBucketAnomalies(overviewStatus.anomalies));

  let selectedKinds = $state(new Set());
  let entityFilter = $state('');
  let timePreset = $state('24h');

  // debouncedEntity is what the fetch effect below actually reads --
  // starts equal to entityFilter's own initial '' so the first, on-mount
  // fetch isn't delayed. setDebouncedEntity is created once (a fresh
  // debounce() per render would never coalesce anything, since each
  // instance's internal timer would be independent).
  let debouncedEntity = $state('');
  const setDebouncedEntity = debounce((v) => {
    debouncedEntity = v;
  }, ENTITY_DEBOUNCE_MS);

  let events = $state([]);
  let loading = $state(false);
  let failed = $state(false);
  let hasMore = $state(false);

  function toggleKind(kind) {
    const next = new Set(selectedKinds);
    if (next.has(kind)) next.delete(kind);
    else next.add(kind);
    selectedKinds = next;
  }

  function presetFrom(preset) {
    const p = TIME_PRESETS.find((t) => t.key === preset);
    return p?.seconds == null ? undefined : Math.floor(Date.now() / 1000) - p.seconds;
  }

  // activeController tracks whichever request -- the filter effect's
  // own, or a loadMore() call -- is currently in flight, so a filter
  // change or a second loadMore click while one is already running
  // always aborts the earlier request first, the same "never let a
  // superseded request win just because it resolves last" contract
  // ContainerDetail's own history effect documents.
  let activeController = null;
  function abortInFlight() {
    activeController?.abort();
    activeController = null;
  }

  // Schedules debouncedEntity to follow entityFilter ENTITY_DEBOUNCE_MS
  // after typing pauses -- kept as its own effect (rather than folded
  // into the fetch effect below) so kind/preset changes stay immediate:
  // they're read directly off their own state in the fetch effect, never
  // through this debounce.
  $effect(() => {
    setDebouncedEntity(entityFilter.trim());
  });

  // Stale-response race: changing a kind checkbox, the (debounced) entity
  // filter, or the time preset fast enough that an earlier /api/events
  // call is still in flight when a newer one starts must not let the
  // earlier response win. See ContainerDetail.svelte's matching effect
  // for the full mechanics this mirrors (abort-on-cleanup runs before the
  // next call to this same effect; a stale request's .catch ignores
  // AbortError instead of clearing already-newer state).
  $effect(() => {
    const kinds = Array.from(selectedKinds);
    const entity = debouncedEntity;
    const preset = timePreset;

    abortInFlight();
    const controller = new AbortController();
    activeController = controller;
    loading = true;
    failed = false;
    fetchEvents({
      kinds: kinds.length > 0 ? kinds : undefined,
      entity: entity || undefined,
      from: presetFrom(preset),
      limit: PAGE_LIMIT,
      signal: controller.signal,
    })
      .then((result) => {
        events = result;
        hasMore = result.length === PAGE_LIMIT;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer filter change
        events = [];
        hasMore = false;
        failed = true;
      })
      .finally(() => {
        if (activeController === controller) {
          loading = false;
          activeController = null;
        }
      });
    return () => controller.abort();
  });

  // loadMore pages backward from the oldest currently-loaded event.
  // Known edge case (accepted -- this is the brief's own "before = min
  // ts of loaded" cursor, not a different scheme of my own devising):
  // if more events share that exact oldest timestamp than fit in one
  // page, the ones that didn't make this page are skipped on the next
  // one (to = minTs-1 excludes the whole second, not just the rows
  // already seen) -- rare in practice, and not worth a heavier id-based
  // cursor for a feed this low-stakes.
  async function loadMore() {
    if (loading || events.length === 0) return;
    const kinds = Array.from(selectedKinds);
    const entity = entityFilter.trim();
    const preset = timePreset;
    const minTs = Math.min(...events.map((e) => e.TS));

    abortInFlight();
    const controller = new AbortController();
    activeController = controller;
    loading = true;
    failed = false;
    try {
      const older = await fetchEvents({
        kinds: kinds.length > 0 ? kinds : undefined,
        entity: entity || undefined,
        from: presetFrom(preset),
        to: minTs - 1,
        limit: PAGE_LIMIT,
        signal: controller.signal,
      });
      events = [...events, ...older];
      hasMore = older.length === PAGE_LIMIT;
    } catch (err) {
      if (err?.name !== 'AbortError') failed = true; // superseded requests are silently ignored
    } finally {
      if (activeController === controller) {
        loading = false;
        activeController = null;
      }
    }
  }

  // refreshFirstPage silently re-fetches the first page on the current
  // filters, on a timer and on window focus -- see this file's top-of-
  // file doc for why a monitoring page needs this at all. Two choices
  // that need naming, both made in favor of "simple and provably
  // correct" over "clever":
  //
  // 1. No loading/failed toggling, and a transient failure just leaves
  //    the last-good list showing -- matching Overview's loadEvents /
  //    Storage's loadParityHistory background-refresh convention. loading
  //    is reserved for user-driven actions (a filter change, a Load More
  //    click); flipping it on a silent 30s timer would flash the whole
  //    list to "Loading…" for no user-visible reason.
  // 2. On success this REPLACES the entire events array with the fresh
  //    first page, discarding any extra pages the user had reached via
  //    Load More, rather than trying to keep or re-fetch them. Keeping
  //    them by array position (fresh page 1 + old events.slice(PAGE_LIMIT))
  //    is NOT actually correct: if any events arrived since the last
  //    full load, the fresh first page's oldest row shifts, opening a gap
  //    (events that fall between the new page's cutoff and the old
  //    second page's start that were never fetched) with no way to
  //    detect it from array position alone. Re-fetching every already-
  //    loaded page instead (chaining `to` cursors the way loadMore
  //    itself does) IS correct, but is a background loop of N requests
  //    for what's meant to be a lightweight silent refresh -- not the
  //    simpler option. Resetting to a fresh, fully self-consistent first
  //    page has no seam to get wrong; the user can just click Load More
  //    again if they want to go further back.
  //
  // Uses the same abortInFlight()/activeController dance as the filter
  // effect and loadMore -- so a filter change or a Load More click that
  // happens to land while this is in flight still correctly wins (this
  // request gets aborted, its catch swallows that silently), and this
  // refresh correctly aborts either of THEM if a tick lands mid-flight.
  async function refreshFirstPage() {
    const kinds = Array.from(selectedKinds);
    const entity = entityFilter.trim();
    const preset = timePreset;

    abortInFlight();
    const controller = new AbortController();
    activeController = controller;
    try {
      const result = await fetchEvents({
        kinds: kinds.length > 0 ? kinds : undefined,
        entity: entity || undefined,
        from: presetFrom(preset),
        limit: PAGE_LIMIT,
        signal: controller.signal,
      });
      events = result;
      hasMore = result.length === PAGE_LIMIT;
    } catch {
      // Aborted (superseded by a real user action) or a transient
      // network failure -- either way, leave the last-good list showing
      // rather than blanking it; the next tick or focus tries again.
    } finally {
      if (activeController === controller) activeController = null;
    }
  }

  onMount(() => {
    // Acks load once per page load (acks.svelte.ts's own contract) --
    // ensureLoaded() is a no-op if Overview (or this view, on an earlier
    // mount) already triggered it, so the needs-you derivation above
    // sees every standing acknowledgement from first render either way.
    acks.ensureLoaded();
    // A route change alone leaves the window at whatever scrollY the
    // PREVIOUS page had -- the hash router does no scroll management of
    // its own (router.ts's own doc). That would defeat the one thing
    // Overview's alerts chip promises ("click on the number and then be
    // brought to a list of items that need attention"): landing here
    // scrolled past the strip the chip pointed at. This view's own
    // mount is the one place that can put a fresh arrival back at its
    // own top, strip included.
    window.scrollTo(0, 0);
    const interval = setInterval(refreshFirstPage, REFRESH_MS);
    window.addEventListener('focus', refreshFirstPage);
    return () => {
      clearInterval(interval);
      window.removeEventListener('focus', refreshFirstPage);
    };
  });
</script>

<div class="events-view">
  <h1 class="page-title">Events</h1>

  {#if needsYouAnomalies.length > 0}
    <section class="card events-view__attention">
      <span class="microlabel">Needs you</span>
      <div class="events-view__attention-rows">
        {#each needsYouAnomalies as anomaly, i (i)}
          <div class="events-view__attention-row">
            <CalloutRow {anomaly} />
          </div>
        {/each}
      </div>
    </section>
  {/if}

  <div class="card events-view__filters">
    <fieldset class="events-view__kinds">
      <legend class="microlabel">Kind</legend>
      {#each KNOWN_KINDS as kind (kind)}
        <label class="events-view__kind-checkbox">
          <input type="checkbox" checked={selectedKinds.has(kind)} onchange={() => toggleKind(kind)} />
          <span>{kind}</span>
        </label>
      {/each}
    </fieldset>

    <div class="events-view__filter-row">
      <label class="events-view__entity-field">
        <span class="microlabel">Entity</span>
        <input type="text" placeholder="Entity name (exact match)…" bind:value={entityFilter} />
      </label>

      <div class="segmented" role="group" aria-label="Time range">
        {#each TIME_PRESETS as p (p.key)}
          <button
            type="button"
            class="segmented__btn"
            class:segmented__btn--active={timePreset === p.key}
            onclick={() => (timePreset = p.key)}
          >
            {p.label}
          </button>
        {/each}
      </div>
    </div>
  </div>

  <div class="card events-view__list">
    {#if failed}
      <p class="microlabel events-view__error">Couldn't load events. Try again shortly.</p>
    {:else if events.length === 0 && !loading}
      <p class="microlabel events-view__empty">No events match these filters.</p>
    {:else}
      <div class="events-view__items">
        {#each events as event (event.ID)}
          <div
            animate:flip={{ duration: feedMotionMs }}
            in:fly={{ y: -12, duration: feedMotionMs }}
            out:fade={{ duration: feedMotionMs }}
          >
            <EventFeedItem {event} showAbsoluteTime />
          </div>
        {/each}
      </div>
      {#if loading}
        <p class="microlabel events-view__loading">Loading…</p>
      {:else if hasMore}
        <button type="button" class="events-view__load-more" onclick={loadMore} disabled={loading}>Load more</button>
      {/if}
    {/if}
  </div>
</div>

<style>
  .events-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  /* Needs you: a .card like every other section on this page, tinted the
     same way SourcesBanner's own .sources-panel is (a background-only
     override -- .card's border stays the default) so this reads as
     "pay attention here" without inventing a second bordered-box idiom.
     Rows stack with a hairline between them (EventFeedItem's own
     per-row convention below it), not the gap-only spacing the old
     Overview attention band used -- this strip can hold more than a
     couple of rows, and dividers are what keep a longer stack scannable. */
  .events-view__attention {
    padding: 0.85rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    background: color-mix(in oklab, var(--status-warning) 8%, var(--surface-raised));
  }
  .events-view__attention-rows {
    display: flex;
    flex-direction: column;
  }
  .events-view__attention-row {
    padding: 0.45rem 0;
  }
  .events-view__attention-row:not(:last-child) {
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .events-view__filters {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .events-view__kinds {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem 1rem;
    border: none;
    margin: 0;
    padding: 0;
  }
  .events-view__kinds legend {
    padding: 0;
    margin-right: 0.25rem;
  }
  .events-view__kind-checkbox {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    min-height: 40px;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink-2);
  }
  .events-view__kind-checkbox input {
    width: 16px;
    height: 16px;
  }
  .events-view__filter-row {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    gap: 1rem;
  }
  .events-view__entity-field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }
  .events-view__entity-field input {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.9rem;
    min-width: 14rem;
  }
  .events-view__list {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .events-view__error {
    color: var(--status-warning);
    margin: 0;
  }
  .events-view__empty,
  .events-view__loading {
    margin: 0;
  }
  .events-view__items {
    display: flex;
    flex-direction: column;
  }
  .events-view__load-more {
    align-self: center;
    min-height: 40px;
    padding: 0 1.25rem;
    margin-top: 0.4rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.85rem;
    cursor: pointer;
  }
  .events-view__load-more:hover {
    background: color-mix(in oklab, var(--ink) 6%, transparent);
  }
</style>

<!--
  GPUEntityCard: one GPU entity's engine-utilization TimeChart -- its own
  component (rather than a block inside the GPU view's own {#each}) so it
  can call liveRing once per engine during ITS OWN initialization, the
  same reason ContainerRow is its own component (see its doc): a runes-
  using helper called from inside a PARENT {#each} block doesn't get the
  right setup-phase timing, but a child component instantiated per list
  item does.

  activeRange is a prop, driven by the GPU view's own page-level range
  picker (shared across every entity card, matching Task 19's "range
  picker sharing ContainerDetail's control" contract) -- but the
  non-live fetch itself is still per-entity (kind=gpu, entity=<this
  one>), so it lives here, one AbortController-guarded effect per card,
  mirroring ContainerDetail's exact stale-response-race handling.
-->
<script>
  import { fetchSeries } from '../lib/api';
  import { fmtPct } from '../lib/format';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { GPU_ENTITY_ENGINE_ORDER } from '../lib/metrics';
  import TimeChart from './TimeChart.svelte';

  const LIVE_WINDOW_SEC = 900;
  const RANGE_SECONDS = { '1h': 3600, '24h': 86400, '7d': 7 * 86400, '30d': 30 * 86400 };
  const SERIES_VAR = {
    render: '--series-1',
    video: '--series-2',
    'video-enhance': '--series-3',
    copy: '--series-4',
    gpu: '--series-1', // Nvidia's solo pseudo-engine -- never co-present with the other four
  };
  const METRIC_FOR = (engine) => `engine.${engine}.busy_pct`;

  let { entity, activeRange, syncKey } = $props();

  // Live rings: one per fixed engine slot, unconditionally -- same
  // rationale as ContainerDetail's ALL_METRICS rings: which ones end up
  // with any points (and therefore get charted) is a display-time
  // decision, not a ring-creation-time one.
  const liveRings = {};
  for (const engine of GPU_ENTITY_ENGINE_ORDER) {
    liveRings[engine] = liveRing((f) => f.gpu?.[entity]?.[METRIC_FOR(engine)], LIVE_WINDOW_SEC);
  }

  // liveSeedPending gates the empty-state message below: while true, a
  // truly-empty live ring stays silent instead of flashing "No engine
  // activity for this range" the instant this card mounts, before the
  // seed fetch (typically ~100ms) has even had a chance to say whether
  // there's real history or not -- see the template's own doc. Flips
  // false once the seed settles either way (found data, found none, or
  // failed); each entity's card mounts once per {#each} key (see the GPU
  // view's own keyed block), so this -- like the seed fetch itself --
  // only ever needs to run once.
  let liveSeedPending = $state(true);

  // Seed every engine's live ring from server history on mount, once.
  // Runs regardless of activeRange (same rationale as ContainerDetail's
  // matching effect) so switching back to Live later finds it already
  // filled. Zipped by INDEX against GPU_ENTITY_ENGINE_ORDER rather than
  // keyed by result.metric: QuerySeries guarantees exactly one
  // SeriesResult per requested metric, in request order (see its own
  // doc), and metrics below is built from this same order.
  $effect(() => {
    const gpuEntity = entity;
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    const controller = new AbortController();
    fetchSeries({
      kind: 'gpu',
      entity: gpuEntity,
      metrics: GPU_ENTITY_ENGINE_ORDER.map(METRIC_FOR),
      from,
      to,
      signal: controller.signal,
    })
      .then((results) => {
        GPU_ENTITY_ENGINE_ORDER.forEach((engine, i) => {
          liveRings[engine]?.seed(seriesPointsToRing(results[i]?.points ?? []));
        });
        liveSeedPending = false;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // unmounted before the seed resolved -- nothing left to update
        liveSeedPending = false;
      });
    return () => controller.abort();
  });

  // fetchedSeries/fetchInFlight/fetchFailed + the AbortController effect
  // below are identical in shape to ContainerDetail.svelte's own history
  // effect (see its doc for the full stale-response-race reasoning):
  // switching range (or the entity itself changing, which never actually
  // happens here since each card is keyed on a fixed entity name, but is
  // still a correct dependency to guard) fast enough that an earlier
  // /api/series call is still in flight when a newer one starts must not
  // let the earlier response win just because it resolves last.
  let fetchedSeries = $state({});
  let fetchInFlight = $state(false);
  let fetchFailed = $state(false);

  $effect(() => {
    const range = activeRange;
    const gpuEntity = entity;
    if (range === 'live') {
      fetchedSeries = {};
      fetchFailed = false;
      fetchInFlight = false;
      return;
    }
    const seconds = RANGE_SECONDS[range];
    const to = Math.floor(Date.now() / 1000);
    const from = to - seconds;
    const controller = new AbortController();
    fetchInFlight = true;
    fetchFailed = false;
    fetchSeries({
      kind: 'gpu',
      entity: gpuEntity,
      metrics: GPU_ENTITY_ENGINE_ORDER.map(METRIC_FOR),
      from,
      to,
      signal: controller.signal,
    })
      .then((results) => {
        const byMetric = {};
        for (const r of results) byMetric[r.metric] = r.points;
        fetchedSeries = byMetric;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer range switch
        fetchedSeries = {};
        fetchFailed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) fetchInFlight = false;
      });
    return () => controller.abort();
  });

  function pointsFor(engine) {
    return activeRange === 'live' ? liveRings[engine].points : (fetchedSeries[METRIC_FOR(engine)] ?? []);
  }
  // >1, not merely >0: see ContainerDetail.svelte's matching hasPoints
  // for why -- a single point can't show a trend, and (reproduced live
  // building this view) uPlot's own x-axis tick-picker mishandles a
  // genuinely single-point time domain regardless of TimeChart's own
  // xRange padding, which only smooths over 2+ close-together points.
  function hasPoints(engine) {
    return pointsFor(engine).length > 1;
  }

  // series: one per engine that actually has data -- this is what makes
  // the Nvidia-only shape (only "gpu" ever has points) render as a
  // single "gpu"-labeled series rather than needing any special-cased
  // detection: the same generic filter naturally produces either a
  // 1-series or up-to-4-series chart depending on which fixed slots the
  // entity's own metrics actually populate.
  let series = $derived(
    GPU_ENTITY_ENGINE_ORDER.filter((engine) => hasPoints(engine)).map((engine) => ({
      label: engine,
      points: pointsFor(engine),
      colorVar: SERIES_VAR[engine],
    })),
  );
</script>

<div class="card gpu-entity-card">
  <div class="gpu-entity-card__head">
    <span class="microlabel">GPU entity</span>
    <span class="gpu-entity-card__name">{entity}</span>
  </div>
  {#if fetchFailed}
    <p class="microlabel gpu-entity-card__error">Couldn't load history for this range. Try again shortly.</p>
  {:else if fetchInFlight}
    <p class="microlabel gpu-entity-card__loading">Loading…</p>
  {:else if series.length > 0}
    <TimeChart {series} formatValue={fmtPct} {syncKey} live={activeRange === 'live'} />
  {:else if activeRange === 'live' && liveSeedPending}
    <!-- Live ring is still cold AND we don't yet know whether the seed
         found real history -- rendering nothing here (rather than the
         empty message below) avoids a false "no engine activity" flash
         before the seed (~100ms on LAN) has actually settled. -->
  {:else}
    <p class="microlabel gpu-entity-card__empty">No engine activity for this range.</p>
  {/if}
</div>

<style>
  .gpu-entity-card {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .gpu-entity-card__head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .gpu-entity-card__name {
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 0.95rem;
    color: var(--ink);
  }
  .gpu-entity-card__error {
    color: var(--status-warning);
    margin: 2rem 0;
    text-align: center;
  }
  .gpu-entity-card__loading,
  .gpu-entity-card__empty {
    margin: 2rem 0;
    text-align: center;
  }
</style>

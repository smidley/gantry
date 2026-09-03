<!--
  InsightDetail: one finding's whole evidence page (#/insights/:id) --
  the owner's own ask, "the evidence pop up on the insights page should
  just open a whole new page instead of trying to pack all of the info
  into the little popup." Everything the evidence drawer used to cram
  into a 30rem modal, with room: the statement AS the page title, the
  fact chips, the incident's own timeline charts and the interaction map
  side by side at desktop width, then the evidence table and the dismiss
  controls.

  Reached only by clicking an Insights row or a map edge (never through
  nav) -- the container-detail precedent, down to App.svelte keying this
  component on the id param so navigating from one insight straight to
  another (the map's own edges do exactly that) fully remounts rather
  than reusing an instance with half of the previous finding's state
  still loaded.

  Data, in one pass and then left alone: GET /api/insights/{id} for the
  finding itself (the SSE frame's own active items never carry evidence
  -- Task 9's frame contract), then the same two client-side assemblies
  the drawer performed, with the same SNAPSHOT semantics and for the
  same reason -- reading a moment from an hour ago should not shift
  under the reader:
    - the map (insights.ts' drawerMapAnchor + selectOverlappingInsights
      + buildInsightGraph over GET /api/insights union GET
      /api/insights/history?from=<anchor>): who else was contending at
      this insight's own instant, this one's edges emphasized
      (focusInsightId) and every concurrent other muted but present.
      Rendered FULL size here, not `compact` -- the cap that variant
      exists for was the drawer's own layout, which is gone.
    - the charts (incidentChart.ts' planIncidentCharts over the existing
      GET /api/series): how bad and when. TimeChart's own default height
      here, again for the room the drawer didn't have.

  Three terminal states, each with the back link so the page is never a
  dead end: loading, "couldn't load" (a real fetch failure), and "no
  such insight" (a 404, or an id that could never be one at all --
  lib/insightDetail.ts' parseInsightId/isNotFoundError).
-->
<script>
  import { onMount, untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { fetchInsight, fetchInsights, fetchInsightHistory, dismissInsight, fetchSeries } from '../lib/api';
  import {
    confidenceLabel,
    activeDuration,
    drawerMapAnchor,
    selectOverlappingInsights,
    buildInsightGraph,
  } from '../lib/insights';
  import { evidenceRows, formatInstant, isNotFoundError, parseInsightId } from '../lib/insightDetail';
  import { incidentChartWindow, incidentBand, incidentMarkers, hasChartableData, planIncidentCharts } from '../lib/incidentChart';
  import { sumSeriesByMetric } from '../lib/metrics';
  import { fmtPct, fmtRate } from '../lib/format';
  import InteractionMap from '../components/InteractionMap.svelte';
  import TimeChart from '../components/TimeChart.svelte';

  let { id: rawID } = $props();

  // OVERLAP_HISTORY_FETCH_LIMIT: the endpoint's own maximum
  // (api_insights.go's maxInsightHistoryLimit) rather than its smaller
  // default -- this is a targeted, one-shot query, not a "load more"
  // page a reader paces out, and under-fetching would silently
  // under-draw the map. A system with more than 500 resolutions AFTER
  // this insight's own anchor is the one accepted edge case it can
  // miss; this insight's own row is unioned in separately regardless
  // (see loadMap), so its own edge never disappears even then.
  const OVERLAP_HISTORY_FETCH_LIMIT = 500;
  const EMPTY_GRAPH = { nodes: [], edges: [] };
  // CHART_FORMATTERS maps incidentChart.ts' own plain-string
  // ChartFormatter enum (pct/rate/plain) to the real format.ts function
  // TimeChart's formatValue prop wants -- a lookup table here, not on
  // ChartPlan itself, so that module stays pure and trivially
  // comparable in its own tests (no closures to compare).
  const CHART_FORMATTERS = { pct: fmtPct, rate: fmtRate, plain: undefined };
  const DISMISS_PRESETS = [
    { label: '1d', days: 1 },
    { label: '7d', days: 7 },
    { label: '30d', days: 30 },
  ];

  // insightID is parsed ONCE: App.svelte keys this component on the id
  // param, so it's stable for the whole instance lifetime and a
  // navigation to another insight remounts rather than re-props (the
  // ContainerDetail precedent). untrack makes that deliberate
  // one-time read explicit -- TopConsumers' own initialResource seed
  // follows the identical contract.
  const insightID = untrack(() => parseInsightId(rawID));

  let insight = $state(null);
  let loading = $state(insightID !== null);
  let loadFailed = $state(false);
  let notFound = $state(insightID === null);

  // alive guards every post-await state write: a reader who clicks a
  // map edge navigates straight to ANOTHER insight's page, unmounting
  // this instance while its own fetches are still in flight.
  let alive = true;

  let graph = $state(EMPTY_GRAPH);
  let statementsById = $state({});
  let mapLoading = $state(insightID !== null);

  let charts = $state([]);
  let chartXDomain = $state(undefined);
  let chartMarkers = $state([]);
  let chartBand = $state(undefined);
  let chartsLoading = $state(insightID !== null);

  let dismissError = $state(null);

  // nowSec ticks only for the "active for" chip -- the map and the
  // charts each anchor to the second they were BUILT (see their own
  // loaders) and deliberately do not follow it.
  let nowSec = $state(Math.floor(Date.now() / 1000));

  let rows = $derived(evidenceRows(insight?.evidence));
  let otherUsers = $derived(insight?.evidence?.other_users ?? []);

  onMount(() => {
    const t = setInterval(() => (nowSec = Math.floor(Date.now() / 1000)), 1000);
    if (insightID !== null) load();
    return () => {
      alive = false;
      clearInterval(t);
    };
  });

  async function load() {
    try {
      const data = await fetchInsight(insightID);
      if (!alive) return;
      insight = data;
    } catch (err) {
      if (!alive) return;
      if (isNotFoundError(err)) notFound = true;
      else loadFailed = true;
    } finally {
      if (alive) loading = false;
    }
    if (insight) {
      loadMap(insight);
    } else {
      mapLoading = false;
      chartsLoading = false;
    }
  }

  // chartsStarted: a plain let, not $state -- a one-way latch read only
  // inside the effect that sets it, never rendered.
  let chartsStarted = false;
  // The charts (alone on this page) need the LIVE frame: planIncidentCharts
  // resolves a disk's slot to its current raw device name off disk_meta and
  // a GPU engine's victim entity off the frame's own gpu keys (there is no
  // per-instance record of either on the stored row). On a cold deep link
  // into this page that frame hasn't arrived yet, so they wait for it
  // rather than planning against an empty join and silently dropping a
  // line -- everything else renders immediately. The wait is bounded in
  // practice by the fetch above having just succeeded: a server answering
  // /api/insights is a server about to push a frame.
  $effect(() => {
    if (chartsStarted || !insight || !live.frame) return;
    chartsStarted = true;
    loadCharts(insight);
  });

  // loadMap assembles this page's map data: drawerMapAnchor picks the
  // single instant (now for an active insight, its own fired_at for a
  // resolved one), then the pool tested against that anchor is `inst`
  // itself (always present regardless of either fetch below --
  // guarantees "if only this insight was active, the map legitimately
  // shows just that culprit-to-victim pair" even if a fetch fails or
  // truncates) union the currently active set WITH evidence
  // (fetchInsights -- the live frame's own trimmed copies never carry
  // evidence, and a share_pct-less edge would silently lose its width
  // signal) union every resolved insight whose OWN resolution is at or
  // after the anchor.
  async function loadMap(inst) {
    const anchor = drawerMapAnchor(inst, Math.floor(Date.now() / 1000));
    const [activeRows, historyRows] = await Promise.all([
      fetchInsights()
        .then((r) => r.active)
        .catch(() => []),
      fetchInsightHistory({ from: anchor, limit: OVERLAP_HISTORY_FETCH_LIMIT }).catch(() => []),
    ]);
    if (!alive) return;
    const overlap = selectOverlappingInsights([inst, ...activeRows, ...historyRows], anchor);
    graph = buildInsightGraph(overlap);
    statementsById = Object.fromEntries(overlap.map((i) => [i.id, i.statement]));
    mapLoading = false;
  }

  // loadCharts assembles this page's incident chart(s): planIncidentCharts
  // maps this insight's own rule_id to which real metrics to chart (see
  // the $effect above for the two live-frame joins it needs), then every
  // line across every returned chart is grouped by its own (kind, entity)
  // pair first, so a pair more than one line needs is still only fetched
  // once. hasChartableData decides each chart's real-vs-fallback rendering
  // straight off the RAW per-pair fetch result, before sumSeriesByMetric
  // folds multiple metrics into one line's points -- an empty result is
  // never an error here, only a quiet "nothing to show."
  async function loadCharts(inst) {
    const xDomain = incidentChartWindow(inst, Math.floor(Date.now() / 1000));
    const plans = planIncidentCharts(inst, {
      diskMeta: live.frame?.disk_meta ?? {},
      gpuEntities: Object.keys(live.frame?.gpu ?? {}),
    });

    const pairs = new Map(); // "<kind>|<entity>" -> {kind, entity, metrics: Set<string>}
    for (const plan of plans) {
      for (const line of plan.lines) {
        const key = `${line.kind}|${line.entity}`;
        if (!pairs.has(key)) pairs.set(key, { kind: line.kind, entity: line.entity, metrics: new Set() });
        for (const m of line.metrics) pairs.get(key).metrics.add(m);
      }
    }

    const settled = await Promise.all(
      [...pairs.entries()].map(([key, { kind, entity, metrics }]) =>
        fetchSeries({ kind, entity, metrics: [...metrics], from: xDomain[0], to: xDomain[1] })
          .then((results) => [key, results])
          .catch(() => [key, []]),
      ),
    );
    if (!alive) return;

    const resultsByPair = new Map(settled);
    const hasDataByPair = new Map([...resultsByPair].map(([key, results]) => [key, hasChartableData(results)]));

    charts = plans.map((plan) => ({
      ...plan,
      hasData: plan.lines.some((line) => hasDataByPair.get(`${line.kind}|${line.entity}`)),
      series: plan.lines.map((line) => {
        const results = resultsByPair.get(`${line.kind}|${line.entity}`) ?? [];
        const byMetric = Object.fromEntries(results.map((r) => [r.metric, r.points]));
        return { label: line.label, colorVar: line.colorVar, points: sumSeriesByMetric(byMetric, line.metrics) };
      }),
    }));
    chartXDomain = xDomain;
    chartMarkers = incidentMarkers(inst);
    chartBand = incidentBand(inst, Math.floor(Date.now() / 1000));
    chartsLoading = false;
  }

  // openInsight: a map edge on THIS page navigates to the clicked
  // insight's own page, replacing this one -- browsing between related
  // findings, which is what the drawer's own "re-target the modal"
  // behaviour becomes once evidence is a real page.
  function openInsight(nextID) {
    window.location.hash = `#/insights/${nextID}`;
  }

  async function dismiss(days) {
    dismissError = null;
    try {
      await dismissInsight(insight.id, days);
      window.location.hash = '#/insights';
    } catch (err) {
      dismissError = err instanceof Error ? err.message : String(err);
    }
  }
</script>

<div class="insight-detail">
  <div class="insight-detail__head">
    <a class="insight-detail__back" href="#/insights">&larr; Back to Insights</a>
    {#if insight}
      <h1 class="page-title insight-detail__title">{insight.statement}</h1>
      <div class="insight-detail__facts">
        <span class="insight-detail__chip insight-detail__chip--{insight.confidence}">{confidenceLabel(insight.confidence)}</span>
        <span class="microlabel">{insight.tier === 'psi' ? 'PSI tier' : 'tier 1 (proxy)'}</span>
        <span class="microlabel">
          {insight.state === 'active'
            ? `active for ${activeDuration(insight.started_at, nowSec)}`
            : `resolved (${insight.resolve_reason}) after ${activeDuration(insight.started_at, insight.resolved_at)}`}
        </span>
        <span class="microlabel tabular-nums">Started {formatInstant(insight.started_at)}</span>
        {#if insight.resolved_at}
          <span class="microlabel tabular-nums">Ended {formatInstant(insight.resolved_at)}</span>
        {/if}
      </div>
    {:else}
      <h1 class="page-title insight-detail__title">Evidence</h1>
    {/if}
  </div>

  {#if loading}
    <p class="microlabel insight-detail__state">Loading…</p>
  {:else if notFound}
    <p class="insight-detail__state">That insight doesn't exist — it may have been pruned, or the link may be wrong.</p>
  {:else if loadFailed}
    <p class="insight-detail__state insight-detail__error">Couldn't load this insight's evidence. Try again shortly.</p>
  {:else if insight}
    <div class="insight-detail__columns">
      <div class="card insight-detail__panel insight-detail__charts">
        <span class="microlabel">Incident timeline</span>
        {#if chartsLoading}
          <p class="microlabel">Loading…</p>
        {:else if charts.length === 0}
          <p class="microlabel insight-detail__chart-empty">This rule has no chartable signal.</p>
        {:else}
          {#each charts as chart (chart.key)}
            <div class="insight-detail__chart">
              <span class="microlabel insight-detail__chart-title">{chart.title}</span>
              {#if chart.hasData}
                <TimeChart
                  series={chart.series}
                  formatValue={CHART_FORMATTERS[chart.formatter]}
                  markers={chartMarkers}
                  band={chartBand}
                  xDomain={chartXDomain}
                />
              {:else}
                <p class="microlabel insight-detail__chart-empty">History for this window isn't available.</p>
              {/if}
            </div>
          {/each}
        {/if}
      </div>

      <div class="card insight-detail__panel insight-detail__map">
        <span class="microlabel">Interaction map</span>
        {#if mapLoading}
          <p class="microlabel">Loading…</p>
        {:else}
          <InteractionMap {graph} {statementsById} tier={insight.tier} onOpenInsight={openInsight} focusInsightId={insight.id} />
        {/if}
      </div>
    </div>

    <div class="card insight-detail__panel insight-detail__evidence-card">
      <span class="microlabel">Evidence</span>
      {#if rows.length > 0}
        <dl class="insight-detail__evidence">
          {#each rows as row (row.key)}
            <dt>{row.label}</dt>
            <dd class="tabular-nums">{row.text}</dd>
          {/each}
        </dl>
      {:else}
        <p class="microlabel insight-detail__chart-empty">This finding carries no measured numbers of its own.</p>
      {/if}
      {#if otherUsers.length > 0}
        <p class="microlabel insight-detail__other-users">Also touching this resource: {otherUsers.join(', ')}</p>
      {/if}
    </div>

    <div class="card insight-detail__panel insight-detail__dismiss">
      <span class="microlabel">Dismiss</span>
      <div class="insight-detail__dismiss-row">
        {#each DISMISS_PRESETS as p (p.days)}
          <button type="button" onclick={() => dismiss(p.days)}>{p.label}</button>
        {/each}
      </div>
      {#if dismissError}<p class="insight-detail__error">{dismissError}</p>{/if}
    </div>
  {/if}
</div>

<style>
  .insight-detail {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .insight-detail__head {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  /* Plain understated text link, the compare__hint-link treatment --
     a way back, not a peer of the page's own actions. */
  .insight-detail__back {
    align-self: flex-start;
    color: var(--series-1);
    font-size: 0.82rem;
  }
  .insight-detail__title {
    margin: 0;
  }
  .insight-detail__facts {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  /* Confidence chip -- the same shape Insights' own rows use, restated
     here because Svelte scopes a component's styles to that component.
     Colour is never the only signal: the label itself reads
     "confirmed"/"likely". */
  .insight-detail__chip {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    border: 1px solid currentColor;
  }
  .insight-detail__chip--likely {
    color: var(--ink-2);
  }
  .insight-detail__chip--confirmed {
    color: var(--status-warning);
  }
  .insight-detail__state {
    margin: 0.5rem 0;
    color: var(--ink-2);
  }
  .insight-detail__error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-critical);
  }
  /* Two columns at desktop width -- the whole point of this page over
     the drawer it replaced: the timeline and the map are two answers to
     the same question (how bad and when, vs. who) and read better side
     by side than stacked. */
  .insight-detail__columns {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
    align-items: start;
  }
  /* min-width:0 -- a grid item's default min-width:auto lets the uPlot
     canvas inside act as its track's minimum, pinning the track at a
     stale width the ResizeObserver can then never shrink; the
     ContainerDetail__chart-card idiom, same reason. */
  .insight-detail__panel {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    min-width: 0;
  }
  /* The app's standard narrow breakpoint (Overview/ContainerDetail both
     collapse their own grids here): one column below 768px. */
  @media (max-width: 47.9375rem) {
    .insight-detail__columns {
      grid-template-columns: 1fr;
    }
  }
  .insight-detail__chart {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .insight-detail__chart-title {
    color: var(--ink-2);
  }
  .insight-detail__chart-empty {
    margin: 0;
    padding: 0.75rem 0;
  }
  .insight-detail__evidence {
    margin: 0;
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 0.35rem 1rem;
  }
  .insight-detail__evidence dt {
    color: var(--ink-2);
    font-size: 0.82rem;
  }
  .insight-detail__evidence dd {
    margin: 0;
    font-size: 0.85rem;
    color: var(--ink);
    text-align: right;
  }
  .insight-detail__other-users {
    margin: 0;
  }
  .insight-detail__dismiss-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .insight-detail__dismiss-row button {
    min-height: 36px;
    padding: 0 0.7rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
</style>

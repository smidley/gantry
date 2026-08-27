<!--
  ContainerDetail: one container's header, range-scoped charts (CPU,
  memory, network, IO, GPU per-engine, PSI -- the last three only when
  present), event markers, a metadata card, and its log viewer.

  "Unknown container" is handled compositionally, not as one hard gate:
  if the container isn't currently in the live frame, the header/
  metadata show a muted "not currently running" note instead of
  fabricating values, but the range picker and charts still render --
  each chart already shows its own "no data" empty state when it has
  nothing to plot, which for a truly never-seen name means every chart
  (and the header note) agree there's nothing here, composing into the
  same friendly outcome the brief asks for without a separate
  is-this-name-real? pre-check.
-->
<script>
  import { untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fetchEvents, fetchSeries } from '../lib/api';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { containerHealthStatus } from '../lib/containerStatus';
  import { GPU_ENGINE_ORDER } from '../lib/metrics';
  import { eventsToMarkers } from '../lib/eventMarkers';

  import HealthDot from '../components/HealthDot.svelte';
  import TimeChart from '../components/TimeChart.svelte';
  import LogViewer from '../components/LogViewer.svelte';

  let { name } = $props();

  const SYNC_KEY = 'container-detail';
  const LIVE_WINDOW_SEC = 900;
  const RANGES = [
    { key: 'live', label: 'Live · 15m' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
  ];
  const RANGE_SECONDS = { '1h': 3600, '24h': 86400, '7d': 7 * 86400, '30d': 30 * 86400 };

  // Every metric this page might ever chart, fetched together in one
  // /api/series call per range switch (see the $effect below) -- gpu/psi
  // entries are dynamic-presence ones: a chart section only renders when
  // at least one of its metrics actually has points.
  const ALL_METRICS = [
    'cpu.pct',
    'cpu.throttled_pct',
    'mem.bytes',
    'net.rx_bps',
    'net.tx_bps',
    'io.read_bps',
    'io.write_bps',
    'gpu.render.busy_pct',
    'gpu.video.busy_pct',
    'gpu.video-enhance.busy_pct',
    'gpu.copy.busy_pct',
    'psi.cpu.some_pct',
    'psi.io.some_pct',
  ];

  let activeRange = $state('live');

  // Live rings: one per metric, unconditionally -- which ones actually
  // end up with any points (and therefore get charted) is a display-time
  // decision, not a ring-creation-time one (creating $state/$effect
  // pairs conditionally, after the fact, isn't how runes work).
  const liveRings = {};
  for (const metric of ALL_METRICS) {
    liveRings[metric] = liveRing((f) => f.containers?.[name]?.metrics?.[metric], LIVE_WINDOW_SEC);
  }

  // Seed every live ring from server history on mount, once: `name` is
  // stable for this component's whole lifetime (App.svelte's own {#key}
  // wrapper fully remounts ContainerDetail on a container-name change --
  // see its doc), so this effect's only real dependency never varies,
  // same as the events effect below. Runs regardless of which range tab
  // is active so a later switch BACK to Live finds it already filled,
  // not empty-then-refetching. A failed/empty seed leaves every ring
  // exactly as unseeded as it is today (see mergeSeed's own doc) -- no
  // error banner, no new skeleton state, just today's cold start.
  $effect(() => {
    const containerName = name;
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    const controller = new AbortController();
    fetchSeries({ kind: 'container', entity: containerName, metrics: ALL_METRICS, from, to, signal: controller.signal })
      .then((results) => {
        for (const r of results) liveRings[r.metric]?.seed(seriesPointsToRing(r.points));
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // unmounted (or -- can't actually happen, name is stable -- superseded) before the seed resolved
      });
    return () => controller.abort();
  });

  // fetchedSeries holds the non-live ranges' /api/series result, keyed by
  // metric -- refetched ONLY when activeRange (or name) changes, never
  // live-appended, per the range picker's own contract.
  let fetchedSeries = $state({});
  let fetchInFlight = $state(false);
  let fetchFailed = $state(false);

  // Stale-response race: switching range (or container name) fast enough
  // that an earlier /api/series call is still in flight when a newer one
  // starts must not let the earlier response win just because it happens
  // to resolve last -- e.g. Live->1h->24h in quick succession must not
  // let 1h's response land after 24h's and silently show 1h's data under
  // 24h's active button. The effect's own cleanup (the returned function)
  // runs BEFORE the next call to this same effect, so aborting there
  // guarantees every superseded request is cancelled before its
  // replacement's fetch even starts. The aborted request's own .then is
  // never reached (fetch rejects instead); its .catch recognizes the
  // abort via err?.name and ignores it rather than clearing already-newer
  // state, and its .finally only clears fetchInFlight when IT wasn't the
  // one aborted (a stale request's finally firing after its replacement
  // has already set fetchInFlight=true must not clobber that back to
  // false).
  $effect(() => {
    const range = activeRange;
    const containerName = name;
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
    fetchSeries({ kind: 'container', entity: containerName, metrics: ALL_METRICS, from, to, signal: controller.signal })
      .then((results) => {
        const byMetric = {};
        for (const r of results) byMetric[r.metric] = r.points;
        fetchedSeries = byMetric;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer range/name switch
        fetchedSeries = {};
        fetchFailed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) fetchInFlight = false;
      });
    return () => controller.abort();
  });

  // pointsFor reads either shape (live ring 2-tuples [ts,val], or fetched
  // 3-tuples [ts,avg,max]) -- TimeChart only ever destructures the first
  // two elements of each point, so both shapes work unchanged.
  function pointsFor(metric) {
    return activeRange === 'live' ? liveRings[metric].points : (fetchedSeries[metric] ?? []);
  }
  // hasPoints requires >1 point, not merely >0: a single point can't
  // show a trend anyway (there's no line to draw, just one dot), and --
  // reproduced live while building the GPU view -- uPlot's own x-axis
  // tick-picker mishandles a genuinely single-point (zero-width) time
  // domain, rendering nonsensical year-granularity gridlines with no
  // visible data rather than the correct few-second range. TimeChart's
  // own xRange padding (see its doc) handles every OTHER too-narrow
  // case (2+ close-together points); this is the one gap that needs
  // fixing at the call site instead, since there is no meaningful
  // "point 2" to pad around yet.
  function hasPoints(metric) {
    return pointsFor(metric).length > 1;
  }
  function hasNonzero(metric) {
    return pointsFor(metric).some(([, v]) => v > 0);
  }

  let cpuSeries = $derived.by(() => {
    const s = [{ label: 'CPU', points: pointsFor('cpu.pct'), colorVar: '--series-1' }];
    if (hasNonzero('cpu.throttled_pct')) s.push({ label: 'Throttled', points: pointsFor('cpu.throttled_pct'), colorVar: '--series-2' });
    return s;
  });
  let memSeries = $derived([{ label: 'Memory', points: pointsFor('mem.bytes'), colorVar: '--series-1' }]);
  let netSeries = $derived([
    { label: 'Down', points: pointsFor('net.rx_bps'), colorVar: '--series-1' },
    { label: 'Up', points: pointsFor('net.tx_bps'), colorVar: '--series-2' },
  ]);
  let ioSeries = $derived([
    { label: 'Read', points: pointsFor('io.read_bps'), colorVar: '--series-1' },
    { label: 'Write', points: pointsFor('io.write_bps'), colorVar: '--series-2' },
  ]);
  const GPU_SERIES_VAR = { render: '--series-1', video: '--series-2', 'video-enhance': '--series-3', copy: '--series-4' };
  let gpuSeries = $derived.by(() =>
    GPU_ENGINE_ORDER.filter((engine) => hasPoints(`gpu.${engine}.busy_pct`)).map((engine) => ({
      label: engine,
      points: pointsFor(`gpu.${engine}.busy_pct`),
      colorVar: GPU_SERIES_VAR[engine],
    })),
  );
  let psiSeries = $derived.by(() => {
    const s = [];
    if (hasPoints('psi.cpu.some_pct')) s.push({ label: 'CPU', points: pointsFor('psi.cpu.some_pct'), colorVar: '--series-1' });
    if (hasPoints('psi.io.some_pct')) s.push({ label: 'IO', points: pointsFor('psi.io.some_pct'), colorVar: '--series-2' });
    return s;
  });

  // events/markers: fetched once per container name (not per range switch
  // -- a generous recency-ordered limit comfortably covers every range
  // this page offers, and TimeChart's own draw hook already clips
  // markers outside the visible x-domain, so over-fetching is harmless).
  let events = $state([]);
  $effect(() => {
    const containerName = name;
    fetchEvents({ entity: containerName, limit: 200 })
      .then((result) => {
        events = result;
      })
      .catch(() => {
        events = [];
      });
  });
  let markers = $derived(eventsToMarkers(events));

  let c = $derived(live.frame?.containers?.[name]);
  let frameTs = $derived(live.frame?.ts ?? 0);
  let startedAt = $derived(c?.metrics?.['meta.started_at']);
  let restarts = $derived(c?.metrics?.['meta.restart_count']);
</script>

<div class="container-detail">
  <div class="container-detail__header">
    <div class="container-detail__identity">
      <h1 class="page-title container-detail__title">{name}</h1>
      {#if c}
        <HealthDot status={containerHealthStatus(c.state, c.health)} label={c.state} />
      {:else if live.frameCount > 0}
        <span class="microlabel container-detail__not-live">Not currently running</span>
      {/if}
    </div>
    {#if c}
      <div class="container-detail__facts">
        <span class="tabular-nums">{c.image}</span>
        <span class="tabular-nums">
          Uptime {startedAt !== undefined ? fmtDuration(frameTs - startedAt) : '—'}
        </span>
        <span class="tabular-nums">Restarts {restarts ?? '—'}</span>
      </div>
    {/if}
  </div>

  <div class="container-detail__range-picker" role="group" aria-label="Time range">
    {#each RANGES as r (r.key)}
      <button
        type="button"
        class="container-detail__range-btn"
        class:container-detail__range-btn--active={activeRange === r.key}
        onclick={() => (activeRange = r.key)}
      >
        {r.label}
      </button>
    {/each}
  </div>

  {#if fetchFailed}
    <p class="microlabel container-detail__fetch-error">Couldn't load history for this range. Try again shortly.</p>
  {:else if fetchInFlight}
    <p class="microlabel container-detail__loading">Loading…</p>
  {/if}

  <div class="container-detail__charts">
    <div class="card container-detail__chart-card">
      <span class="microlabel">CPU</span>
      {#if hasPoints('cpu.pct')}
        <TimeChart series={cpuSeries} formatValue={fmtPct} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} />
      {:else}
        <p class="microlabel container-detail__empty">No CPU data for this range.</p>
      {/if}
    </div>
    <div class="card container-detail__chart-card">
      <span class="microlabel">Memory</span>
      {#if hasPoints('mem.bytes')}
        <TimeChart series={memSeries} formatValue={fmtBytes} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} />
      {:else}
        <p class="microlabel container-detail__empty">No memory data for this range.</p>
      {/if}
    </div>
    <div class="card container-detail__chart-card">
      <span class="microlabel">Network</span>
      {#if hasPoints('net.rx_bps') || hasPoints('net.tx_bps')}
        <TimeChart series={netSeries} formatValue={fmtRate} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} />
      {:else}
        <p class="microlabel container-detail__empty">No network data for this range.</p>
      {/if}
    </div>
    <div class="card container-detail__chart-card">
      <span class="microlabel">Disk IO</span>
      {#if hasPoints('io.read_bps') || hasPoints('io.write_bps')}
        <TimeChart series={ioSeries} formatValue={fmtRate} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} />
      {:else}
        <p class="microlabel container-detail__empty">No disk IO data for this range.</p>
      {/if}
    </div>
    {#if gpuSeries.length > 0}
      <div class="card container-detail__chart-card">
        <span class="microlabel">GPU</span>
        <TimeChart series={gpuSeries} formatValue={fmtPct} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} />
      </div>
    {/if}
    {#if psiSeries.length > 0}
      <div class="card container-detail__chart-card">
        <span class="microlabel">Pressure (PSI)</span>
        <TimeChart series={psiSeries} formatValue={fmtPct} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} />
      </div>
    {/if}
  </div>

  <div class="card container-detail__metadata">
    <span class="microlabel">Metadata</span>
    <dl class="container-detail__meta-list">
      <dt>Image</dt>
      <dd>{c?.image ?? '—'}</dd>
      <dt>State</dt>
      <dd>{c?.state ?? 'not currently running'}</dd>
      <dt>Started</dt>
      <dd>{startedAt !== undefined ? new Date(startedAt * 1000).toLocaleString() : '—'}</dd>
      <dt>Restarts</dt>
      <dd>{restarts ?? '—'}</dd>
    </dl>
  </div>

  <div class="card container-detail__logs">
    <span class="microlabel">Logs</span>
    <LogViewer {name} />
  </div>
</div>

<style>
  .container-detail {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .container-detail__header {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .container-detail__identity {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .container-detail__title {
    margin: 0;
  }
  .container-detail__not-live {
    color: var(--status-warning);
  }
  .container-detail__facts {
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--ink-2);
  }
  .container-detail__range-picker {
    display: flex;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .container-detail__range-btn {
    min-height: 40px;
    padding: 0 0.85rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink-2);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .container-detail__range-btn--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    border-color: var(--series-1);
    color: var(--series-1);
    font-weight: 500;
  }
  .container-detail__fetch-error {
    color: var(--status-warning);
  }
  .container-detail__loading {
    margin: 0;
  }
  .container-detail__charts {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
  }
  @media (max-width: 47.9375rem) {
    .container-detail__charts {
      grid-template-columns: 1fr;
    }
  }
  .container-detail__chart-card {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .container-detail__empty {
    margin: 2rem 0;
    text-align: center;
  }
  .container-detail__metadata,
  .container-detail__logs {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .container-detail__meta-list {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  .container-detail__meta-list dt {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .container-detail__meta-list dd {
    margin: 0;
    font-size: 0.85rem;
  }
</style>

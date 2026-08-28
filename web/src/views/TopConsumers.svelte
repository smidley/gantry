<!--
  TopConsumers: the full per-metric attribution page. Leaderboards by
  resource (CPU/Memory/Network/Disk IO/GPU) and window (Now/1h/24h/7d),
  with an Average/Peak toggle for every window except Now -- Now derives
  client-side from the live frame (the same resource->metric mapping the
  backend uses for window=now, see lib/topFromFrame.ts) and stays LIVE;
  every other window calls /api/top once per resource/window/agg change.

  cpu/mem/net/io additionally get a host-total header (big value + a
  full-size live/history chart, dual-line for net/io) and a trailing
  "unattributed" row (host total minus the COMPLETE container list's own
  sum, clamped >=0) -- see lib/topFromFrame.ts's own doc for why gpu
  deliberately has neither: a busy_pct is inherently per-engine/per-
  device, with no single honest whole-machine number to subtract from.
  The list itself is complete here (every container with a sample), not
  the top-5 Overview's compact module shows.
-->
<script>
  import { onMount, untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fetchSeries, fetchSnapshot, fetchTop } from '../lib/api';
  import { fmtBytes, fmtCores, fmtPct, fmtRate } from '../lib/format';
  import { keysByPattern, niceCeiling, sumMetricsByPattern, sumSeriesPoints } from '../lib/metrics';
  import { buildCoreBudget } from '../lib/coreBudget';
  import {
    hostSeriesMetricKeys,
    hostTotalNow,
    isTopResource,
    reduceSeriesPoints,
    resourceDirectionKeys,
    resourceScaleMax,
    TOP_RESOURCES,
    topFromFrame,
    unattributedValue,
  } from '../lib/topFromFrame';
  import TopBarList from '../components/TopBarList.svelte';
  import TimeChart from '../components/TimeChart.svelte';
  import CoreBudgetRibbon from '../components/CoreBudgetRibbon.svelte';

  // initialResource: App.svelte's route table passes $route.params.resource
  // straight through (the "#/top/:resource" pattern -- see router.ts),
  // same convention as ContainerDetail's own name prop. Read ONCE to seed
  // `resource` below, not kept live-bound -- once this view has mounted,
  // its own tab clicks own the selection, the same "seed once" contract
  // TopBarRow's Tween already uses for row.value.
  let { initialResource = undefined } = $props();

  const WINDOWS = [
    { key: 'now', label: 'Now' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
  ];
  // Resources whose /api/top value is a SUM of more than one underlying
  // metric (net = rx+tx, io = read+write, gpu = every engine's busy_pct)
  // -- the peak-of-a-sum caption below only applies to these; it also
  // covers the host-total header's own peak-of-a-sum math for net/io,
  // not just the container rows.
  const SUMMED_RESOURCES = new Set(['net', 'io', 'gpu']);
  const FORMATTERS = { cpu: fmtPct, mem: fmtBytes, net: fmtRate, io: fmtRate, gpu: fmtPct };
  // SECONDARY_FORMATTERS mirrors FORMATTERS but is deliberately partial --
  // only cpu rows carry a secondary value (topFromFrame's own
  // resourceSecondaryMetricKey; a fetched, non-"now" window's rows never
  // have one either way, see fetchTop's TopRow shape), so every other key
  // is simply absent rather than mapped to a no-op formatter.
  const SECONDARY_FORMATTERS = { cpu: fmtCores };
  const WINDOW_LABEL = { '1h': 'the last hour', '24h': 'the last 24 hours', '7d': 'the last 7 days' };

  // ROW_DIRECTION_LABELS (leaderboard rows, dense) vs CHART_DIRECTION_
  // LABELS (the header chart's own legend, one word each) -- same split
  // Overview/ContainerDetail already use for these two contexts.
  const ROW_DIRECTION_LABELS = { net: ['↓', '↑'], io: ['r', 'w'] };
  const CHART_DIRECTION_LABELS = { net: ['Down', 'Up'], io: ['Read', 'Write'] };
  const RESOURCE_LABEL = { cpu: 'CPU', mem: 'Memory', net: 'Network', io: 'Disk IO' };
  const UNATTRIBUTED_LABEL = { cpu: 'Unattributed (host)', mem: 'Unattributed (host)', net: 'Unattributed (host)', io: 'Unattributed (host/array)' };
  // HOST_DIRECTION_PATTERNS: net/io's host-level keys have no fixed name
  // (host.go emits one per device/interface) -- discovered at fetch time
  // via keysByPattern, same as Overview's own rail-tile seeding.
  const HOST_DIRECTION_PATTERNS = { net: [['net', '.rx_bps'], ['net', '.tx_bps']], io: [['diskio', '.read_bps'], ['diskio', '.write_bps']] };

  // COMPLETE_LIST_LIMIT: the attribution page shows every container with
  // a sample, not a top-5/top-10 cut -- comfortably above any realistic
  // fleet size (matches the backend's own topSumFetchLimit's reasoning).
  const COMPLETE_LIST_LIMIT = 500;
  const LIVE_WINDOW_SEC = 900;

  let resource = $state(untrack(() => (isTopResource(initialResource) ? initialResource : 'cpu')));
  let windowKey = $state('now');
  let agg = $state('avg');

  let isDirectional = $derived(!!resourceDirectionKeys(resource));
  // hasHostTotal: cpu/mem/net/io only -- gpu never gets the header/
  // unattributed row (see the module doc above).
  let hasHostTotal = $derived(resource in RESOURCE_LABEL);

  // nowRows is a plain $derived (not gated behind an effect) so it stays
  // live: it recomputes on every frame AND on a resource change, unlike
  // fetchedRows below, which intentionally does NOT depend on live.frame
  // at all. direction is opted in for net/io -- see topFromFrame's own
  // doc for why Overview's compact module (a separate call site) never
  // sees this.
  let nowRows = $derived(topFromFrame(live.frame, resource, COMPLETE_LIST_LIMIT, { direction: isDirectional }));

  let fetchedRows = $state([]);
  let loading = $state(false);
  let failed = $state(false);

  // fetchedHostTotal: the header's own value+chart for a non-"now" window
  // -- {value, direction?, points, points2?} or undefined (no host
  // history for this window/resource at all). Populated by the SAME
  // effect that fetches fetchedRows below, so both always reflect the
  // same resource/window/agg selection.
  let fetchedHostTotal = $state(undefined);

  // fetchHostTotal mirrors hostTotalNow's shape for a FETCHED window: a
  // fixed-key resource (cpu/mem) asks /api/series directly; a directional
  // one (net/io) first discovers the CURRENT per-device key names off a
  // snapshot (no fixed name exists to ask by), then sums each side's own
  // reduced value across devices -- reduceSeriesPoints per metric, summed
  // after, not the other way around, so a peak reduction stays "sum of
  // each device's own peak," matching topFromStore's/the peak-sum
  // caption's own semantics. points/points2 (2-tuple, TimeChart-ready)
  // are the chart's own combined line, summed point-by-point instead
  // (sumSeriesPoints) -- a visual, not the number the header prints.
  async function fetchHostTotal(res, from, to, aggMode, signal) {
    const fixedKeys = hostSeriesMetricKeys(res);
    if (fixedKeys) {
      const results = await fetchSeries({ kind: 'host', entity: '', metrics: fixedKeys, from, to, signal });
      const points = results[0]?.points ?? [];
      const value = reduceSeriesPoints(points, aggMode);
      return value === undefined ? undefined : { points, value };
    }
    const dirPatterns = HOST_DIRECTION_PATTERNS[res];
    if (!dirPatterns) return undefined; // gpu: no host total at all
    const snapshot = await fetchSnapshot();
    const keySets = dirPatterns.map(([prefix, suffix]) => keysByPattern(snapshot.host, prefix, suffix));
    const allKeys = keySets.flat();
    if (allKeys.length === 0) return undefined;
    const results = await fetchSeries({ kind: 'host', entity: '', metrics: allKeys, from, to, signal });
    const byMetric = {};
    for (const r of results) byMetric[r.metric] = r.points;
    function sideValue(keys) {
      const vals = keys.map((k) => reduceSeriesPoints(byMetric[k] ?? [], aggMode)).filter((v) => v !== undefined);
      return vals.length === 0 ? undefined : vals.reduce((a, b) => a + b, 0);
    }
    const valueA = sideValue(keySets[0]);
    const valueB = sideValue(keySets[1]);
    if (valueA === undefined && valueB === undefined) return undefined;
    const d0 = valueA ?? 0;
    const d1 = valueB ?? 0;
    const pointsA = sumSeriesPoints(keySets[0].map((k) => byMetric[k] ?? []));
    const pointsB = sumSeriesPoints(keySets[1].map((k) => byMetric[k] ?? []));
    return { points: pointsA, points2: pointsB, value: d0 + d1, direction: [d0, d1] };
  }

  // Stale-response race: flipping resource/window/agg fast enough that
  // an earlier /api/top call is still in flight when a newer one starts
  // must not let the earlier response win just because it resolves
  // last -- e.g. CPU->Memory->Network in quick succession must not leave
  // Network's tab active but showing CPU's numbers. See ContainerDetail's
  // matching effect for the full mechanics (abort-on-cleanup runs before
  // the next call to this same effect; a stale request's .catch ignores
  // AbortError instead of clearing already-newer state; its .finally
  // only clears `loading` when IT wasn't the one aborted).
  $effect(() => {
    const r = resource;
    const w = windowKey;
    const a = agg;
    if (w === 'now') {
      failed = false;
      loading = false;
      fetchedHostTotal = undefined;
      return;
    }
    const seconds = { '1h': 3600, '24h': 86400, '7d': 7 * 86400 }[w];
    const to = Math.floor(Date.now() / 1000);
    const from = to - seconds;
    const controller = new AbortController();
    loading = true;
    failed = false;
    Promise.all([
      fetchTop({ resource: r, window: w, agg: a, limit: COMPLETE_LIST_LIMIT, signal: controller.signal }),
      hasHostTotal ? fetchHostTotal(r, from, to, a, controller.signal) : Promise.resolve(undefined),
    ])
      .then(([result, hostTotal]) => {
        fetchedRows = result;
        fetchedHostTotal = hostTotal;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer resource/window/agg switch
        fetchedRows = [];
        fetchedHostTotal = undefined;
        failed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) loading = false;
      });
    return () => controller.abort();
  });

  // --- Header live rings (window="now" only) ------------------------------
  //
  // One ring per possible header metric, created unconditionally (same
  // "creation can't be conditional" rule ContainerDetail's ALL_METRICS
  // rings follow) -- which ones actually get READ is a display-time
  // decision (headerChartSeries below), driven by `resource`.
  let cpuTotalRing = liveRing((f) => f.host?.['cpu.total']);
  let memUsedRing = liveRing((f) => f.host?.['mem.used_bytes']);
  let netRxRing = liveRing((f) => sumMetricsByPattern(f.host, 'net', '.rx_bps'));
  let netTxRing = liveRing((f) => sumMetricsByPattern(f.host, 'net', '.tx_bps'));
  let ioReadRing = liveRing((f) => sumMetricsByPattern(f.host, 'diskio', '.read_bps'));
  let ioWriteRing = liveRing((f) => sumMetricsByPattern(f.host, 'diskio', '.write_bps'));

  // Seeds only the CURRENTLY selected resource's own ring(s) -- cheap (one
  // or two /api/series metrics, not all six up front) -- re-seeding on
  // every resource switch. Each branch only ever writes to ITS OWN
  // resource's ring(s), so a stale response landing after a switch away
  // is harmless (nothing reads that ring while a different one is
  // active) -- no abort-race guard needed the way the rows effect above
  // needs one.
  $effect(() => {
    const r = resource;
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    const controller = new AbortController();
    if (r === 'cpu') {
      fetchSeries({ kind: 'host', entity: '', metrics: ['cpu.total'], from, to, signal: controller.signal })
        .then((results) => cpuTotalRing.seed(seriesPointsToRing(results[0]?.points ?? [])))
        .catch(() => {});
    } else if (r === 'mem') {
      fetchSeries({ kind: 'host', entity: '', metrics: ['mem.used_bytes'], from, to, signal: controller.signal })
        .then((results) => memUsedRing.seed(seriesPointsToRing(results[0]?.points ?? [])))
        .catch(() => {});
    } else if (r === 'net' || r === 'io') {
      const patterns = HOST_DIRECTION_PATTERNS[r];
      fetchSnapshot()
        .then((snapshot) => {
          const rxKeys = keysByPattern(snapshot.host, patterns[0][0], patterns[0][1]);
          const txKeys = keysByPattern(snapshot.host, patterns[1][0], patterns[1][1]);
          return fetchSeries({ kind: 'host', entity: '', metrics: [...rxKeys, ...txKeys], from, to, signal: controller.signal }).then(
            (results) => {
              const byMetric = {};
              for (const res of results) byMetric[res.metric] = res.points;
              const ringA = sumSeriesPoints(rxKeys.map((k) => byMetric[k] ?? []));
              const ringB = sumSeriesPoints(txKeys.map((k) => byMetric[k] ?? []));
              if (r === 'net') {
                netRxRing.seed(ringA);
                netTxRing.seed(ringB);
              } else {
                ioReadRing.seed(ringA);
                ioWriteRing.seed(ringB);
              }
            },
          );
        })
        .catch(() => {});
    }
    return () => controller.abort();
  });

  let headerChartSeries = $derived.by(() => {
    if (!hasHostTotal) return [];
    if (windowKey === 'now') {
      switch (resource) {
        case 'cpu':
          return [{ label: 'CPU', points: cpuTotalRing.points, colorVar: '--series-1' }];
        case 'mem':
          return [{ label: 'Memory', points: memUsedRing.points, colorVar: '--series-1' }];
        case 'net':
          return [
            { label: 'Down', points: netRxRing.points, colorVar: '--series-1' },
            { label: 'Up', points: netTxRing.points, colorVar: '--series-4' },
          ];
        case 'io':
          return [
            { label: 'Read', points: ioReadRing.points, colorVar: '--series-1' },
            { label: 'Write', points: ioWriteRing.points, colorVar: '--series-4' },
          ];
        default:
          return [];
      }
    }
    if (!fetchedHostTotal) return [];
    if (fetchedHostTotal.points2) {
      const [labelA, labelB] = CHART_DIRECTION_LABELS[resource] ?? ['A', 'B'];
      return [
        { label: labelA, points: fetchedHostTotal.points, colorVar: '--series-1' },
        { label: labelB, points: fetchedHostTotal.points2, colorVar: '--series-4' },
      ];
    }
    return [{ label: RESOURCE_LABEL[resource], points: fetchedHostTotal.points, colorVar: '--series-1' }];
  });

  // hostTotal: the header's own {value, direction?}, whichever window is
  // active -- live-computed for Now, the fetched result otherwise.
  let hostTotal = $derived(
    !hasHostTotal ? undefined : windowKey === 'now' ? hostTotalNow(live.frame, resource) : fetchedHostTotal,
  );

  // --- Core-budget ribbon (CPU breakdown's own hero, Now only -- it's an
  // instantaneous snapshot of cpu.cores, not something a fetched window's
  // avg/peak aggregate can answer) --------------------------------------
  let showRibbon = $derived(resource === 'cpu' && windowKey === 'now');
  let hostCores = $derived(live.frame?.host?.['cpu.count'] ?? 0);
  let coreBudgetContainers = $derived(
    Object.entries(live.frame?.containers ?? {}).map(([name, c]) => ({ name, cores: c.metrics['cpu.cores'] ?? 0 })),
  );
  let coreBudget = $derived(buildCoreBudget(hostCores, hostTotal?.value ?? 0, coreBudgetContainers));
  let coreBudgetIcons = $derived(
    Object.fromEntries(Object.entries(live.frame?.containers ?? {}).map(([name, c]) => [name, c.icon])),
  );

  let containerRows = $derived(windowKey === 'now' ? nowRows : fetchedRows);

  // unattributedRow: host total minus the COMPLETE container list's own
  // sum, clamped >=0 -- "only where host history exists" per hostTotal
  // itself being undefined otherwise. A non-"now" window's rows never
  // carry .direction (see topFromFrame's own doc: the backend has no
  // per-direction /api/top equivalent), so the pinned row only splits
  // into a down/up (or read/write) pair when Now itself does.
  let unattributedRow = $derived.by(() => {
    if (!hostTotal) return null;
    const containersSum = containerRows.reduce((s, r) => s + r.value, 0);
    const value = unattributedValue(hostTotal.value, containersSum);
    const row = { entity: UNATTRIBUTED_LABEL[resource], value, linkable: false };
    if (windowKey === 'now' && isDirectional && hostTotal.direction) {
      const sum0 = containerRows.reduce((s, r) => s + (r.direction?.[0] ?? 0), 0);
      const sum1 = containerRows.reduce((s, r) => s + (r.direction?.[1] ?? 0), 0);
      row.direction = [unattributedValue(hostTotal.direction[0], sum0), unattributedValue(hostTotal.direction[1], sum1)];
    }
    return row;
  });

  let rows = $derived(unattributedRow ? [...containerRows, unattributedRow] : containerRows);

  // scaleMax/scaleCeilingLabel: same nice-1-2-5-ceiling fix as Overview's
  // own compact module (see metrics.ts's niceCeiling doc) for net/io's
  // otherwise-unbounded bars -- shown once, in the header, rather than
  // per row (every row already shares one scale).
  let scaleMax = $derived.by(() => {
    const base = resourceScaleMax(resource, live.frame);
    if (base !== undefined) return base;
    if (resource === 'net' || resource === 'io') {
      return niceCeiling(Math.max(0, ...rows.map((r) => r.value)));
    }
    return undefined;
  });
  let scaleCeilingLabel = $derived(
    (resource === 'net' || resource === 'io') && scaleMax ? `Bars scaled to ≤ ${FORMATTERS[resource](scaleMax)}` : null,
  );

  let showAggToggle = $derived(windowKey !== 'now');
  let showPeakSumCaption = $derived(windowKey !== 'now' && agg === 'peak' && SUMMED_RESOURCES.has(resource));
  let emptyMessage = $derived(
    windowKey === 'now' ? 'No live data yet.' : `No data in ${WINDOW_LABEL[windowKey] ?? 'this window'} yet.`,
  );
</script>

<div class="top-consumers">
  <h1 class="page-title">Top Consumers</h1>

  <div class="top-consumers__controls">
    <div class="segmented" role="tablist" aria-label="Resource">
      {#each TOP_RESOURCES as r (r.key)}
        <button
          type="button"
          role="tab"
          aria-selected={resource === r.key}
          class="segmented__btn"
          class:segmented__btn--active={resource === r.key}
          onclick={() => (resource = r.key)}
        >
          {r.label}
        </button>
      {/each}
    </div>

    <div class="top-consumers__row">
      <div class="segmented" role="group" aria-label="Window">
        {#each WINDOWS as w (w.key)}
          <button
            type="button"
            class="segmented__btn"
            class:segmented__btn--active={windowKey === w.key}
            onclick={() => (windowKey = w.key)}
          >
            {w.label}
          </button>
        {/each}
      </div>

      {#if showAggToggle}
        <div class="segmented" role="group" aria-label="Aggregation">
          <button
            type="button"
            class="segmented__btn"
            class:segmented__btn--active={agg === 'avg'}
            onclick={() => (agg = 'avg')}
          >
            Average
          </button>
          <button
            type="button"
            class="segmented__btn"
            class:segmented__btn--active={agg === 'peak'}
            onclick={() => (agg = 'peak')}
          >
            Peak
          </button>
        </div>
      {/if}
    </div>
  </div>

  {#if showPeakSumCaption}
    <p class="microlabel top-consumers__caption">
      Peak sums each direction's own peak &mdash; a simultaneous-peak upper bound.
    </p>
  {/if}

  {#if hasHostTotal && hostTotal}
    <div class="card top-consumers__header">
      <div class="top-consumers__header-head">
        <span class="microlabel">{RESOURCE_LABEL[resource]} &middot; host total</span>
        {#if isDirectional && hostTotal.direction}
          <div class="top-consumers__header-values">
            <span class="top-consumers__header-value" style="color: var(--series-1)">
              {ROW_DIRECTION_LABELS[resource]?.[0]} {FORMATTERS[resource](hostTotal.direction[0])}
            </span>
            <span class="top-consumers__header-value" style="color: var(--series-4)">
              {ROW_DIRECTION_LABELS[resource]?.[1]} {FORMATTERS[resource](hostTotal.direction[1])}
            </span>
          </div>
        {:else}
          <span class="top-consumers__header-value">{FORMATTERS[resource](hostTotal.value)}</span>
        {/if}
      </div>
      {#if scaleCeilingLabel}
        <p class="microlabel top-consumers__scale">{scaleCeilingLabel}</p>
      {/if}
      {#if showRibbon}
        <CoreBudgetRibbon {hostCores} segments={coreBudget.segments} freeCores={coreBudget.freeCores} icons={coreBudgetIcons} />
      {/if}
      {#if headerChartSeries.length > 0}
        <TimeChart series={headerChartSeries} formatValue={FORMATTERS[resource]} live={windowKey === 'now'} height={260} />
      {/if}
    </div>
  {/if}

  <div class="card top-consumers__panel">
    {#if failed}
      <p class="microlabel top-consumers__error">Couldn't load this window. Try again shortly.</p>
    {:else if loading}
      <p class="microlabel top-consumers__loading">Loading…</p>
    {:else}
      <TopBarList
        {rows}
        formatValue={FORMATTERS[resource]}
        formatSecondary={SECONDARY_FORMATTERS[resource]}
        formatDirection={isDirectional ? FORMATTERS[resource] : undefined}
        directionLabels={ROW_DIRECTION_LABELS[resource]}
        {emptyMessage}
        live={windowKey === 'now'}
        {scaleMax}
        metricKey={resource}
      />
    {/if}
  </div>
</div>

<style>
  .top-consumers {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .top-consumers__controls {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .top-consumers__row {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .top-consumers__caption {
    margin: 0;
  }
  .top-consumers__header {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .top-consumers__header-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .top-consumers__scale {
    margin: 0;
    text-align: right;
  }
  .top-consumers__header-value {
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 2rem;
    color: var(--ink);
  }
  .top-consumers__header-values {
    display: flex;
    gap: 1.25rem;
  }
  .top-consumers__header-values .top-consumers__header-value {
    font-size: 1.5rem;
  }
  .top-consumers__panel {
    padding: 1rem;
  }
  .top-consumers__error {
    margin: 0;
    color: var(--status-warning);
  }
  .top-consumers__loading {
    margin: 0;
  }
</style>

<!--
  TopConsumers: leaderboards by resource (CPU/Memory/Network/Disk IO/GPU)
  and window (Now/1h/24h/7d), with an Average/Peak toggle for every
  window except Now. Now derives client-side from the live frame (the
  same resource->metric mapping the backend uses for window=now, see
  lib/topFromFrame.ts) and stays LIVE -- it recomputes on every SSE frame,
  unlike Container Detail's historical ranges, which fetch once per
  switch. Every other window calls /api/top once per resource/window/agg
  change.
-->
<script>
  import { live } from '../lib/sse.svelte';
  import { fetchTop } from '../lib/api';
  import { fmtBytes, fmtPct, fmtRate } from '../lib/format';
  import { topFromFrame } from '../lib/topFromFrame';
  import TopBarList from '../components/TopBarList.svelte';

  const RESOURCES = [
    { key: 'cpu', label: 'CPU' },
    { key: 'mem', label: 'Memory' },
    { key: 'net', label: 'Network' },
    { key: 'io', label: 'Disk IO' },
    { key: 'gpu', label: 'GPU' },
  ];
  const WINDOWS = [
    { key: 'now', label: 'Now' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
  ];
  // Resources whose /api/top value is a SUM of more than one underlying
  // metric (net = rx+tx, io = read+write, gpu = every engine's busy_pct)
  // -- the peak-of-a-sum caption below only applies to these.
  const SUMMED_RESOURCES = new Set(['net', 'io', 'gpu']);
  const FORMATTERS = { cpu: fmtPct, mem: fmtBytes, net: fmtRate, io: fmtRate, gpu: fmtPct };
  const WINDOW_LABEL = { '1h': 'the last hour', '24h': 'the last 24 hours', '7d': 'the last 7 days' };

  let resource = $state('cpu');
  let windowKey = $state('now');
  let agg = $state('avg');

  // nowRows is a plain $derived (not gated behind an effect) so it stays
  // live: it recomputes on every frame AND on a resource change, unlike
  // fetchedRows below, which intentionally does NOT depend on live.frame
  // at all.
  let nowRows = $derived(topFromFrame(live.frame, resource, 10));

  let fetchedRows = $state([]);
  let loading = $state(false);
  let failed = $state(false);

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
      return;
    }
    const controller = new AbortController();
    loading = true;
    failed = false;
    fetchTop({ resource: r, window: w, agg: a, limit: 10, signal: controller.signal })
      .then((result) => {
        fetchedRows = result;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer resource/window/agg switch
        fetchedRows = [];
        failed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) loading = false;
      });
    return () => controller.abort();
  });

  let rows = $derived(windowKey === 'now' ? nowRows : fetchedRows);
  let showAggToggle = $derived(windowKey !== 'now');
  let showPeakSumCaption = $derived(windowKey !== 'now' && agg === 'peak' && SUMMED_RESOURCES.has(resource));
  let emptyMessage = $derived(
    windowKey === 'now' ? 'No live data yet.' : `No data in ${WINDOW_LABEL[windowKey] ?? 'this window'} yet.`,
  );
</script>

<div class="top-consumers">
  <h1 class="page-title">Top Consumers</h1>

  <div class="top-consumers__controls">
    <div class="top-consumers__tabs" role="tablist" aria-label="Resource">
      {#each RESOURCES as r (r.key)}
        <button
          type="button"
          role="tab"
          aria-selected={resource === r.key}
          class="top-consumers__tab"
          class:top-consumers__tab--active={resource === r.key}
          onclick={() => (resource = r.key)}
        >
          {r.label}
        </button>
      {/each}
    </div>

    <div class="top-consumers__row">
      <div class="top-consumers__segmented" role="group" aria-label="Window">
        {#each WINDOWS as w (w.key)}
          <button
            type="button"
            class="top-consumers__segment"
            class:top-consumers__segment--active={windowKey === w.key}
            onclick={() => (windowKey = w.key)}
          >
            {w.label}
          </button>
        {/each}
      </div>

      {#if showAggToggle}
        <div class="top-consumers__segmented" role="group" aria-label="Aggregation">
          <button
            type="button"
            class="top-consumers__segment"
            class:top-consumers__segment--active={agg === 'avg'}
            onclick={() => (agg = 'avg')}
          >
            Average
          </button>
          <button
            type="button"
            class="top-consumers__segment"
            class:top-consumers__segment--active={agg === 'peak'}
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

  <div class="card top-consumers__panel">
    {#if failed}
      <p class="microlabel top-consumers__error">Couldn't load this window. Try again shortly.</p>
    {:else if loading}
      <p class="microlabel top-consumers__loading">Loading…</p>
    {:else}
      <TopBarList {rows} formatValue={FORMATTERS[resource]} {emptyMessage} live={windowKey === 'now'} />
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
  .top-consumers__tabs {
    display: flex;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .top-consumers__tab {
    min-height: 40px;
    padding: 0 0.9rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink-2);
    font-size: 0.85rem;
    cursor: pointer;
  }
  .top-consumers__tab--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    border-color: var(--series-1);
    color: var(--series-1);
    font-weight: 500;
  }
  .top-consumers__row {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .top-consumers__segmented {
    display: inline-flex;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 6px;
    overflow: hidden;
  }
  .top-consumers__segment {
    min-height: 40px;
    padding: 0 0.75rem;
    border: none;
    border-right: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink-2);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .top-consumers__segment:last-child {
    border-right: none;
  }
  .top-consumers__segment--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-weight: 500;
  }
  .top-consumers__caption {
    margin: 0;
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

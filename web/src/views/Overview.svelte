<!--
  Overview: the landing page. Top row of live stat tiles, the array
  summary, a fleet health summary, a compact top-5 CPU consumers module,
  a recent-events feed, an optional GPU strip, and a sources-degradation
  banner. Everything except the events feed reads straight off the live
  SSE frame -- no fetch, no polling -- per the view's own "live-first"
  contract.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { fmtPct, fmtRate } from '../lib/format';
  import { sumMetricsByPattern } from '../lib/metrics';
  import { topFromFrame } from '../lib/topFromFrame';
  import { fetchEvents } from '../lib/api';

  import StatTile from '../components/StatTile.svelte';
  import HealthDot from '../components/HealthDot.svelte';
  import ArrayCard from '../components/ArrayCard.svelte';
  import GPUStrip from '../components/GPUStrip.svelte';
  import SourcesBanner from '../components/SourcesBanner.svelte';
  import TopBarList from '../components/TopBarList.svelte';
  import EventFeedItem from '../components/EventFeedItem.svelte';

  const EVENTS_POLL_MS = 30_000;

  let cpuRing = liveRing((f) => f.host?.['cpu.total']);
  let memRing = liveRing((f) => f.host?.['mem.used_pct']);
  let netRxRing = liveRing((f) => f.host?.['net.rx_bps']);
  let ioReadRing = liveRing((f) => sumMetricsByPattern(f.host, 'diskio', '.read_bps'));

  let host = $derived(live.frame?.host ?? {});
  let ioRead = $derived(sumMetricsByPattern(host, 'diskio', '.read_bps'));
  let ioWrite = $derived(sumMetricsByPattern(host, 'diskio', '.write_bps'));

  let containerEntries = $derived(Object.entries(live.frame?.containers ?? {}));
  let runningCount = $derived(containerEntries.filter(([, c]) => c.state === 'running').length);
  let stoppedCount = $derived(containerEntries.filter(([, c]) => c.state !== 'running').length);
  let unhealthyNames = $derived(
    containerEntries
      .filter(([, c]) => c.health === 'unhealthy')
      .map(([name]) => name)
      .sort(),
  );
  let unhealthyExpanded = $state(false);

  let topCPU = $derived(topFromFrame(live.frame, 'cpu', 5));

  // events: unlike everything else on this view, events are NOT in the
  // SSE frame at all (see sse.svelte.ts's own doc) -- fetched once on
  // mount, then re-fetched on a 30s poll and on window focus (a tab that
  // was backgrounded for a while shouldn't show a stale feed the moment
  // it's looked at again).
  let events = $state([]);

  async function loadEvents() {
    try {
      events = await fetchEvents({ limit: 8 });
    } catch {
      // A transient fetch failure leaves the last-good feed showing
      // rather than blanking it -- the next poll or focus tries again.
    }
  }

  onMount(() => {
    loadEvents();
    const interval = setInterval(loadEvents, EVENTS_POLL_MS);
    window.addEventListener('focus', loadEvents);
    return () => {
      clearInterval(interval);
      window.removeEventListener('focus', loadEvents);
    };
  });
</script>

<div class="overview">
  <h1 class="page-title">Overview</h1>
  <SourcesBanner sources={live.frame?.sources ?? {}} />

  <div class="overview__tiles">
    <StatTile label="CPU" value={fmtPct(host['cpu.total'] ?? 0)} sparklinePoints={cpuRing.points} />
    <StatTile label="Memory" value={fmtPct(host['mem.used_pct'] ?? 0)} sparklinePoints={memRing.points} />
    <StatTile
      label="Network"
      value={`↓ ${fmtRate(host['net.rx_bps'] ?? 0)}`}
      value2={fmtRate(host['net.tx_bps'] ?? 0)}
      label2="↑"
      sparklinePoints={netRxRing.points}
    />
    <StatTile
      label="Disk IO"
      value={`r ${fmtRate(ioRead)}`}
      value2={fmtRate(ioWrite)}
      label2="w"
      sparklinePoints={ioReadRing.points}
    />
  </div>

  <div class="overview__grid">
    <ArrayCard array={live.frame?.unraid?.array ?? {}} disks={live.frame?.disks ?? {}} ts={live.frame?.ts ?? 0} />

    <div class="card overview__fleet">
      <span class="microlabel">Fleet</span>
      <div class="overview__fleet-counts">
        <div class="overview__fleet-count">
          <HealthDot status="good" />
          <span class="tabular-nums">{runningCount}</span>
          <span class="overview__fleet-label">running</span>
        </div>
        <div class="overview__fleet-count">
          <HealthDot status="critical" />
          <span class="tabular-nums">{unhealthyNames.length}</span>
          <span class="overview__fleet-label">unhealthy</span>
        </div>
        <div class="overview__fleet-count">
          <HealthDot status="warning" />
          <span class="tabular-nums">{stoppedCount}</span>
          <span class="overview__fleet-label">stopped</span>
        </div>
      </div>
      {#if unhealthyNames.length > 0}
        <button type="button" class="overview__fleet-toggle" onclick={() => (unhealthyExpanded = !unhealthyExpanded)}>
          {unhealthyExpanded ? 'Hide' : 'Show'} unhealthy containers
        </button>
        {#if unhealthyExpanded}
          <ul class="overview__unhealthy-list">
            {#each unhealthyNames as name (name)}
              <li><a href={`#/containers/${encodeURIComponent(name)}`}>{name}</a></li>
            {/each}
          </ul>
        {/if}
      {/if}
    </div>

    <div class="card overview__top">
      <div class="overview__top-head">
        <span class="microlabel">Top consumers &middot; CPU</span>
        <a href="#/top" class="overview__top-link">View all &rarr;</a>
      </div>
      <TopBarList rows={topCPU} formatValue={fmtPct} />
    </div>

    <div class="card overview__events">
      <span class="microlabel">Recent events</span>
      {#if events.length === 0}
        <p class="microlabel overview__events-empty">No events yet.</p>
      {:else}
        <div class="overview__events-list">
          {#each events as event (event.ID)}
            <EventFeedItem {event} />
          {/each}
        </div>
      {/if}
    </div>
  </div>

  <GPUStrip gpu={live.frame?.gpu ?? {}} />
</div>

<style>
  .overview {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .overview__tiles {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 0.75rem;
  }
  @media (max-width: 47.9375rem) {
    .overview__tiles {
      grid-template-columns: repeat(2, 1fr);
    }
  }
  .overview__grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
    align-items: start;
  }
  /* A grid item's default min-width is auto (effectively its content's
     min-content size), not 0 -- ArrayCard's pool rows and the Fleet
     card's counts row both have content wide enough that, right around
     the md breakpoint (768px, where the 2-column layout is already
     active but the content column itself is still fairly narrow), that
     min-content size exceeded the actual grid track width and pushed
     the WHOLE PAGE wider (reproduced live at exactly 768px -- the Fleet
     card's third count visibly ran off the viewport edge). :global(*)
     reaches ArrayCard's own root element too, a different component
     with its own Svelte scope hash that a plain child selector here
     wouldn't otherwise match. */
  .overview__grid > :global(*) {
    min-width: 0;
  }
  @media (max-width: 47.9375rem) {
    .overview__grid {
      grid-template-columns: 1fr;
    }
  }
  .overview__fleet,
  .overview__top,
  .overview__events {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .overview__fleet-counts {
    flex-wrap: wrap;
    display: flex;
    gap: 1.25rem;
  }
  .overview__fleet-count {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.9rem;
  }
  .overview__fleet-label {
    color: var(--ink-2);
    font-size: 0.8rem;
  }
  .overview__fleet-toggle {
    align-self: flex-start;
    background: transparent;
    border: none;
    color: var(--series-1);
    font-size: 0.8rem;
    cursor: pointer;
    padding: 0.4rem 0;
    min-height: 40px;
  }
  .overview__unhealthy-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .overview__unhealthy-list a {
    color: var(--ink);
    font-size: 0.85rem;
  }
  .overview__top-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .overview__top-link {
    font-size: 0.78rem;
    color: var(--series-1);
    text-decoration: none;
  }
  .overview__events-empty {
    margin: 0;
  }
  .overview__events-list {
    display: flex;
    flex-direction: column;
  }
</style>

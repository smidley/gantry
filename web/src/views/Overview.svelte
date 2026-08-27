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
  import { Tween } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fmtPct, fmtRate } from '../lib/format';
  import { keysByPattern, sumMetricsByPattern, sumSeriesPoints } from '../lib/metrics';
  import { topFromFrame } from '../lib/topFromFrame';
  import { fetchEvents, fetchSeries, fetchSnapshot } from '../lib/api';

  import StatTile from '../components/StatTile.svelte';
  import HealthDot from '../components/HealthDot.svelte';
  import ArrayCard from '../components/ArrayCard.svelte';
  import GPUStrip from '../components/GPUStrip.svelte';
  import SourcesBanner from '../components/SourcesBanner.svelte';
  import TopBarList from '../components/TopBarList.svelte';
  import EventFeedItem from '../components/EventFeedItem.svelte';

  const EVENTS_POLL_MS = 30_000;
  const TWEEN_MS = 400;
  const LIVE_WINDOW_SEC = 900;

  let cpuRing = liveRing((f) => f.host?.['cpu.total']);
  let memRing = liveRing((f) => f.host?.['mem.used_pct']);
  // netRxRing sums real mode's per-interface "net.<iface>.rx_bps" keys
  // (host.go never writes a flat "net.rx_bps" -- only fake mode does,
  // the degenerate single-match case sumMetricsByPattern's own doc
  // describes) -- this tile read a flat key directly until now, which
  // meant it always read 0 on real hardware. Matches ioReadRing's own
  // pattern-sum below exactly.
  let netRxRing = liveRing((f) => sumMetricsByPattern(f.host, 'net', '.rx_bps'));
  let ioReadRing = liveRing((f) => sumMetricsByPattern(f.host, 'diskio', '.read_bps'));

  // Seed all four sparklines from server history on mount, once. cpu/mem
  // are each a single fixed host metric, fetched straight by name.
  // net/io both sum a PATTERN of per-device keys instead (sumMetricsByPattern,
  // live-side) with no fixed name to fetch by itself, so their history
  // needs the CURRENT exact key names first -- fetchSnapshot() answers
  // that synchronously, without waiting on (or racing) live.frame's own
  // first SSE frame, the same discovery sumMetricsByPattern itself does
  // at read time off whatever frame it's handed. keysByPattern is that
  // discovery step's own pure sibling (same prefix+suffix rule), used
  // here because seeding needs the CONCRETE key names to ask
  // /api/series for, not just a live sum.
  onMount(() => {
    const controller = new AbortController();
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    fetchSnapshot()
      .then((snapshot) => {
        const netRxKeys = keysByPattern(snapshot.host, 'net', '.rx_bps');
        const readKeys = keysByPattern(snapshot.host, 'diskio', '.read_bps');
        const metrics = ['cpu.total', 'mem.used_pct', ...netRxKeys, ...readKeys];
        return fetchSeries({ kind: 'host', entity: '', metrics, from, to, signal: controller.signal }).then((results) => {
          const byMetric = {};
          for (const r of results) byMetric[r.metric] = r.points;
          cpuRing.seed(seriesPointsToRing(byMetric['cpu.total'] ?? []));
          memRing.seed(seriesPointsToRing(byMetric['mem.used_pct'] ?? []));
          netRxRing.seed(sumSeriesPoints(netRxKeys.map((k) => byMetric[k] ?? [])));
          ioReadRing.seed(sumSeriesPoints(readKeys.map((k) => byMetric[k] ?? [])));
        });
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // unmounted before the seed resolved
        // A failed discovery/seed fetch leaves every sparkline exactly
        // as unseeded as it is today -- no error banner, no new skeleton
        // state, just today's cold start.
      });
    return () => controller.abort();
  });

  let host = $derived(live.frame?.host ?? {});
  let netRx = $derived(sumMetricsByPattern(host, 'net', '.rx_bps'));
  let netTx = $derived(sumMetricsByPattern(host, 'net', '.tx_bps'));
  let ioRead = $derived(sumMetricsByPattern(host, 'diskio', '.read_bps'));
  let ioWrite = $derived(sumMetricsByPattern(host, 'diskio', '.write_bps'));

  // Tweened numbers (mechanism 3, smooth-streaming): the top-row stat
  // tiles ease toward each new SSE value over TWEEN_MS rather than
  // snapping every 2s. The raw number is what's tweened; format.ts still
  // does all the display formatting below, just fed a smoothed number
  // instead of the instantaneous one (see streamdriver's own design doc).
  function tweenTo(tween, value) {
    tween.set(value, { duration: prefersReducedMotion.current ? 0 : TWEEN_MS, easing: cubicOut });
  }

  // netTxTween/ioWriteTween are value2's own live tween -- StatTile's
  // hero number (value/liveValue below) now owns its OWN Tween
  // internally (hover-scrub needs a raw number to ease toward/from), but
  // value2 has no sparkline to scrub against and stays exactly as it was.
  let netTxTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let ioWriteTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });

  $effect(() => tweenTo(netTxTween, netTx));
  $effect(() => tweenTo(ioWriteTween, ioWrite));

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

  // eventsSeedPending gates the "No events yet." message below the same
  // way ContainerDetail/GPUEntityCard's own liveSeedPending gates their
  // chart cards: while true, a truly-empty `events` stays silent instead
  // of flashing that message the instant this view mounts (or remounts,
  // navigating away and back), before the very first loadEvents() below
  // has had a chance to resolve. Only ever flips false once, on that
  // first resolution (success or failure) -- a later poll/focus refresh
  // finding zero events is a real "No events yet.", not a pending state.
  let eventsSeedPending = $state(true);

  async function loadEvents() {
    try {
      events = await fetchEvents({ limit: 8 });
    } catch {
      // A transient fetch failure leaves the last-good feed showing
      // rather than blanking it -- the next poll or focus tries again.
    } finally {
      eventsSeedPending = false;
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
    <StatTile label="CPU" liveValue={host['cpu.total'] ?? 0} formatValue={fmtPct} sparklinePoints={cpuRing.points} />
    <StatTile label="Memory" liveValue={host['mem.used_pct'] ?? 0} formatValue={fmtPct} sparklinePoints={memRing.points} />
    <StatTile
      label="Network"
      liveValue={netRx}
      formatValue={(v) => `↓ ${fmtRate(v)}`}
      value2={fmtRate(netTxTween.current)}
      label2="↑"
      sparklinePoints={netRxRing.points}
    />
    <StatTile
      label="Disk IO"
      liveValue={ioRead}
      formatValue={(v) => `r ${fmtRate(v)}`}
      value2={fmtRate(ioWriteTween.current)}
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
      <TopBarList rows={topCPU} formatValue={fmtPct} live={true} />
    </div>

    <div class="card overview__events">
      <span class="microlabel">Recent events</span>
      {#if eventsSeedPending}
        <!-- first loadEvents() call hasn't settled yet -- see eventsSeedPending's own doc -->
      {:else if events.length === 0}
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

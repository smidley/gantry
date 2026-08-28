<!--
  Overview: the landing page, D2 ("plain-reading anchor") design --
  see .superpowers/design-exploration/{direction-d2.md,mockup-d2.html}.
  A plain-language status headline (with a countable fleet strip as its
  own evidence) replaces the earlier fleet-summary card; an attention
  section right under the headline's own sublines promotes whatever
  actually needs a look, connected purely by proximity (no frame, no
  leader line -- see the corrective-pass note below), with a miniature
  array bay schematic when a disk itself is the reason; the top-row stat
  tiles become a quiet instrument rail. The GPU strip is unchanged in
  substance, restyled only. Top Consumers gained its own compact CPU/
  Mem/Net/IO/GPU switcher (see overview__top-switcher's own doc) on top
  of that same restyle. Everything still reads straight off the live SSE
  frame -- no fetch, no polling -- except the events feed, exactly as
  before.

  Density pass (Scott: "reduce the amount of wasted space... panels
  should fit together nicely"): headline-zone+rail and Top Consumers+
  Recent events used to be two separate two-column grids stacked on top
  of each other, which left the headline zone's own column dead below
  the fleet strip whenever the rail (four sparkline tiles) ran taller
  than it, a common case in the healthy/no-attention state. See
  overview__body's own doc for the two-independent-column fix.

  Corrective pass (post-deploy, Scott's own read: "there's lines in the
  middle of text that are not used"): the first cut over-decorated this
  -- a tick ruler that measured nothing, a dotted leader line that ran
  straight through the sublines' own text and left a dead void above
  "Needs a look", and orphaned corner-bracket glyphs, all deleted
  outright. The one rule that survived: a line either separates two
  real regions or encodes real data, or it doesn't exist.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fmtBytes, fmtCores, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { keysByPattern, sumMetricsByPattern, sumSeriesPoints, parityIsRunning, etaFromProgress } from '../lib/metrics';
  import { diskKind, diskUsagePct, sortDiskEntities } from '../lib/disks';
  import { isTopResource, resourceScaleMax, TOP_RESOURCES, topFromFrame } from '../lib/topFromFrame';
  import { fetchEvents, fetchSeries, fetchSnapshot } from '../lib/api';
  import { calloutTextBySlot, deriveOverviewStatus, describeAnomaly, worstSeverity } from '../lib/overviewStatus';

  import StatTile from '../components/StatTile.svelte';
  import FleetStrip from '../components/FleetStrip.svelte';
  import BaySchematic from '../components/BaySchematic.svelte';
  import SourcesBanner from '../components/SourcesBanner.svelte';
  import GPUStrip from '../components/GPUStrip.svelte';
  import TopBarList from '../components/TopBarList.svelte';
  import EventFeedItem from '../components/EventFeedItem.svelte';

  const EVENTS_POLL_MS = 30_000;
  const LIVE_WINDOW_SEC = 900;

  // Top consumers module: same formatter-per-resource mapping
  // TopConsumers.svelte's own full page uses, kept local to each view
  // (a formatting concern, not a derivation one -- TOP_RESOURCES itself,
  // the shared part, lives in topFromFrame.ts).
  const TOP_FORMATTERS = { cpu: fmtPct, mem: fmtBytes, net: fmtRate, io: fmtRate, gpu: fmtPct };
  // TOP_SECONDARY_FORMATTERS mirrors TOP_FORMATTERS but is deliberately
  // partial -- only cpu rows carry a secondary value (topFromFrame's own
  // resourceSecondaryMetricKey), so every other key is simply absent
  // rather than mapped to a no-op formatter.
  const TOP_SECONDARY_FORMATTERS = { cpu: fmtCores };

  // topResource persists across reloads (ask: switchable, remembered) the
  // same way theme.svelte.ts's own preference does -- read once at
  // module-init time, guarded for the same SSR/no-localStorage cases.
  const TOP_RESOURCE_STORAGE_KEY = 'gantry.topResource';
  function loadStoredTopResource() {
    if (typeof localStorage === 'undefined') return 'cpu';
    const stored = localStorage.getItem(TOP_RESOURCE_STORAGE_KEY);
    return isTopResource(stored) ? stored : 'cpu';
  }
  let topResource = $state(loadStoredTopResource());
  function selectTopResource(key) {
    topResource = key;
    if (typeof localStorage !== 'undefined') localStorage.setItem(TOP_RESOURCE_STORAGE_KEY, key);
  }

  let cpuRing = liveRing((f) => f.host?.['cpu.total']);
  let memRing = liveRing((f) => f.host?.['mem.used_pct']);
  // netRxRing/ioReadRing back the rail's own hero numbers; netTxRing/
  // ioWriteRing (scrub-parity corrective pass) back each row's SECOND
  // value the same way -- both sum real mode's per-device keys (host.go
  // never writes a flat "net.rx_bps"/"net.tx_bps" -- only fake mode
  // does, the degenerate single-match case sumMetricsByPattern's own
  // doc describes). Every one of these four rings feeds a StatTile
  // prop that can be looked up at the scrub bus's shared ts -- without
  // its own ring, a value can only ever show "right now," which is
  // exactly the bug this pass fixes for the two second values.
  let netRxRing = liveRing((f) => sumMetricsByPattern(f.host, 'net', '.rx_bps'));
  let netTxRing = liveRing((f) => sumMetricsByPattern(f.host, 'net', '.tx_bps'));
  let ioReadRing = liveRing((f) => sumMetricsByPattern(f.host, 'diskio', '.read_bps'));
  let ioWriteRing = liveRing((f) => sumMetricsByPattern(f.host, 'diskio', '.write_bps'));

  // Seed all six sparkline/scrub rings from server history on mount,
  // once. cpu/mem are each a single fixed host metric, fetched straight
  // by name. net/io all sum a PATTERN of per-device keys instead
  // (sumMetricsByPattern, live-side) with no fixed name to fetch by
  // itself, so their history needs the CURRENT exact key names first --
  // fetchSnapshot() answers that synchronously, without waiting on (or
  // racing) live.frame's own first SSE frame, the same discovery
  // sumMetricsByPattern itself does at read time off whatever frame
  // it's handed. keysByPattern is that discovery step's own pure
  // sibling (same prefix+suffix rule), used here because seeding needs
  // the CONCRETE key names to ask /api/series for, not just a live sum.
  onMount(() => {
    const controller = new AbortController();
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    fetchSnapshot()
      .then((snapshot) => {
        const netRxKeys = keysByPattern(snapshot.host, 'net', '.rx_bps');
        const netTxKeys = keysByPattern(snapshot.host, 'net', '.tx_bps');
        const readKeys = keysByPattern(snapshot.host, 'diskio', '.read_bps');
        const writeKeys = keysByPattern(snapshot.host, 'diskio', '.write_bps');
        const metrics = ['cpu.total', 'mem.used_pct', ...netRxKeys, ...netTxKeys, ...readKeys, ...writeKeys];
        return fetchSeries({ kind: 'host', entity: '', metrics, from, to, signal: controller.signal }).then((results) => {
          const byMetric = {};
          for (const r of results) byMetric[r.metric] = r.points;
          cpuRing.seed(seriesPointsToRing(byMetric['cpu.total'] ?? []));
          memRing.seed(seriesPointsToRing(byMetric['mem.used_pct'] ?? []));
          netRxRing.seed(sumSeriesPoints(netRxKeys.map((k) => byMetric[k] ?? [])));
          netTxRing.seed(sumSeriesPoints(netTxKeys.map((k) => byMetric[k] ?? [])));
          ioReadRing.seed(sumSeriesPoints(readKeys.map((k) => byMetric[k] ?? [])));
          ioWriteRing.seed(sumSeriesPoints(writeKeys.map((k) => byMetric[k] ?? [])));
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

  // --- Fleet -------------------------------------------------------------

  let containerEntries = $derived(Object.entries(live.frame?.containers ?? {}));
  let runningCount = $derived(containerEntries.filter(([, c]) => c.state === 'running').length);
  let stoppedCount = $derived(containerEntries.filter(([, c]) => c.state !== 'running').length);
  let unhealthyNames = $derived(
    containerEntries
      .filter(([, c]) => c.health === 'unhealthy')
      .map(([name]) => name)
      .sort(),
  );
  let fleetContainers = $derived(containerEntries.map(([name, c]) => ({ name, state: c.state, health: c.health })));

  let fleetSentence = $derived.by(() => {
    const total = containerEntries.length;
    const noun = total === 1 ? 'container' : 'containers';
    return runningCount === total ? `${total} ${noun}, all running.` : `${total} ${noun}, ${runningCount} running.`;
  });

  let topRows = $derived(topFromFrame(live.frame, topResource, 5));

  // --- Array/disks (moved in from the old ArrayCard, which D2 folds into
  // a quiet headline subline instead of a dedicated card -- see
  // direction-d2.md/mockup-d2.html, which shows no parity-progress/mover
  // card at all on Overview) -------------------------------------------

  let unraidArray = $derived(live.frame?.unraid?.array ?? {});
  let started = $derived(unraidArray['array.started']);
  let parityPct = $derived(unraidArray['parity.progress_pct']);
  // parityIsRunning treats an explicit 0 (the wire value var.go/fake.go
  // both write on finish) as idle, not merely "key present" -- see its
  // own doc.
  let parityRunning = $derived(parityIsRunning(parityPct));
  let moverRunning = $derived(unraidArray['mover.running'] === 1);

  // eta is derived purely from parity.progress_pct's own rate of change
  // across live frames -- see etaFromProgress's doc. prevParitySample is
  // plain (non-reactive) instance state: it only needs to survive
  // between $effect runs, never to trigger one itself.
  let prevParitySample = null;
  let parityEta = $state(null);
  $effect(() => {
    const ts = live.frame?.ts ?? 0;
    if (!parityRunning || parityPct === undefined) {
      prevParitySample = null;
      parityEta = null;
      return;
    }
    if (prevParitySample) {
      parityEta = etaFromProgress(prevParitySample.ts, prevParitySample.pct, ts, parityPct);
    }
    prevParitySample = { ts, pct: parityPct };
  });

  let arrayStateSentence = $derived.by(() => {
    if (started === 0) return 'Array is stopped.';
    if (started === undefined) return 'Array state unknown.';
    if (parityRunning) {
      return `Parity check running · ${fmtPct(parityPct)}${parityEta !== null ? `, ETA ${fmtDuration(parityEta)}` : ''}.`;
    }
    return `Array started · mover ${moverRunning ? 'running' : 'idle'}.`;
  });

  let disks = $derived(live.frame?.disks ?? {});
  let diskMeta = $derived(live.frame?.disk_meta ?? {});

  let hottestDisk = $derived.by(() => {
    let best = null;
    for (const [slot, m] of Object.entries(disks)) {
      const t = m['temp.c'];
      if (t !== undefined && (!best || t > best.temp)) best = { slot, temp: t };
    }
    return best;
  });
  let hottestSentence = $derived(
    hottestDisk ? `${hottestDisk.slot} warmest · ${hottestDisk.temp.toFixed(1)}°C.` : null,
  );

  // diskEntries: every disk/pool entity with a filesystem view, same
  // "has fs.used_bytes AND fs.free_bytes" rule the old ArrayCard's own
  // `pools` used (see its doc) -- correctly excludes parity, includes
  // ordinary data disks, pools, and the boot flash device alike.
  let diskEntries = $derived.by(() => {
    const names = sortDiskEntities(Object.keys(disks));
    const out = [];
    for (const slot of names) {
      const pct = diskUsagePct(disks[slot]);
      if (pct !== null) out.push({ slot, pct, kind: diskKind(diskMeta[slot], disks[slot]) });
    }
    return out;
  });

  // --- Status headline + attention module ---------------------------------

  let overviewStatus = $derived(
    deriveOverviewStatus({
      unhealthyNames,
      stoppedCount,
      arrayStarted: started,
      disks,
      sources: live.frame?.sources ?? {},
    }),
  );
  let statusColor = $derived(`var(--status-${worstSeverity(overviewStatus.anomalies)})`);

  // The bay schematic (and its "N other members are within normal
  // range" closing line) only earns a place in the attention module when
  // a disk itself is one of the flagged reasons -- showing it alongside
  // an anomaly that was never about the array (an unhealthy container,
  // say) would assert a reassurance ("array members are fine") nobody
  // asked about.
  let diskAnomalies = $derived(
    overviewStatus.anomalies.filter((a) => a.kind === 'disk-usage' || a.kind === 'disk-errors'),
  );
  let showBaySchematic = $derived(diskAnomalies.length > 0 && diskEntries.length > 0);

  let baySchematicEntries = $derived.by(() => {
    if (!showBaySchematic) return [];
    const calloutBySlot = calloutTextBySlot(diskAnomalies);
    return diskEntries.map((d) => ({
      slot: d.slot,
      pct: d.pct,
      flagged: calloutBySlot.has(d.slot),
      calloutText: calloutBySlot.get(d.slot),
      kind: d.kind,
    }));
  });

  let closingLine = $derived.by(() => {
    if (!showBaySchematic) return null;
    const flaggedCount = new Set(overviewStatus.flaggedDiskSlots).size;
    const rest = diskEntries.length - flaggedCount;
    if (rest <= 0) return null; // every member is flagged -- nothing left to reassure about
    return rest === 1 ? '1 other array member is within normal range.' : `${rest} other array members are within normal range.`;
  });

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

  <div class="overview__body">
    <div class="overview__col overview__col-left">
      <div class="overview__headline-zone">
        <div class="overview__headline-row">
          <span
            class="overview__headline-dot"
            class:overview__headline-dot--pulse={!overviewStatus.ok}
            style={`background:${statusColor}; color:${statusColor}`}
            aria-hidden="true"
          ></span>
          <h2 class="overview__headline-text">{overviewStatus.headline}</h2>
        </div>
        <div class="overview__headline-subs">
          <p class="overview__sub-line">{fleetSentence}</p>
          <FleetStrip containers={fleetContainers} />
          <p class="overview__sub-line overview__sub-line--quiet">{arrayStateSentence}</p>
          {#if hottestSentence}
            <p class="overview__sub-line overview__sub-line--quiet">{hottestSentence}</p>
          {/if}
        </div>

        {#if !overviewStatus.ok}
          <section class="overview__attention">
            <span class="microlabel">Needs a look</span>
            {#each overviewStatus.anomalies as anomaly, i (i)}
              {@const text = describeAnomaly(anomaly)}
              <div class="overview__attn-row">
                <span class="overview__attn-dot" style={`background:var(--status-${text.severity})`} aria-hidden="true"
                ></span>
                <div>
                  <div class="overview__attn-title">
                    {#if text.linkContainer}
                      <a href={`#/containers/${encodeURIComponent(text.linkContainer)}`}>{text.title}</a>
                    {:else}
                      {text.title}
                    {/if}
                  </div>
                  {#if text.detail}
                    <div class="overview__attn-detail">{text.detail}</div>
                  {/if}
                </div>
              </div>
            {/each}

            {#if showBaySchematic}
              <BaySchematic entries={baySchematicEntries} />
            {/if}

            {#if closingLine}
              <p class="overview__closing-line">{closingLine}</p>
            {/if}
          </section>
        {/if}
      </div>

      <div class="card overview__top">
        <div class="overview__top-head">
          <span class="microlabel">Top consumers</span>
          <a href={`#/top/${topResource}`} class="overview__top-link">View all &rarr;</a>
        </div>
        <div class="overview__top-switcher" role="tablist" aria-label="Top consumers metric">
          {#each TOP_RESOURCES as r (r.key)}
            <button
              type="button"
              role="tab"
              aria-selected={topResource === r.key}
              class="overview__top-switch"
              class:overview__top-switch--active={topResource === r.key}
              onclick={() => selectTopResource(r.key)}
            >
              {r.shortLabel}
            </button>
          {/each}
        </div>
        <TopBarList
          rows={topRows}
          formatValue={TOP_FORMATTERS[topResource]}
          formatSecondary={TOP_SECONDARY_FORMATTERS[topResource]}
          live={true}
          scaleMax={resourceScaleMax(topResource, live.frame)}
        />
      </div>
    </div>

    <div class="overview__col overview__col-right">
      <div class="overview__metrics-rail">
        <StatTile bare label="CPU" liveValue={host['cpu.total'] ?? 0} formatValue={fmtPct} sparklinePoints={cpuRing.points} />
        <StatTile
          bare
          label="Memory"
          liveValue={host['mem.used_pct'] ?? 0}
          formatValue={fmtPct}
          sparklinePoints={memRing.points}
        />
        <StatTile
          bare
          label="Network"
          liveValue={netRx}
          formatValue={(v) => `↓ ${fmtRate(v)}`}
          sparklinePoints={netRxRing.points}
          liveValue2={netTx}
          value2Points={netTxRing.points}
          formatValue2={fmtRate}
          label2="↑"
        />
        <StatTile
          bare
          label="Disk IO"
          liveValue={ioRead}
          formatValue={(v) => `r ${fmtRate(v)}`}
          sparklinePoints={ioReadRing.points}
          liveValue2={ioWrite}
          value2Points={ioWriteRing.points}
          formatValue2={fmtRate}
          label2="w"
        />
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
  </div>

  <GPUStrip gpu={live.frame?.gpu ?? {}} />
</div>

<style>
  .overview {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }

  /* --- Body: two independent-height columns ------------------------

     A real CSS grid here (as this used to be, in two separate grids --
     one for headline+rail, another below for Top Consumers+Events)
     forces each ROW to the height of its tallest column: the rail
     routinely runs taller than the headline zone (four live-sparkline
     tiles vs. a few lines of text), which left the headline zone's own
     column dead below the fleet strip in the healthy state -- reported
     live, "there's wasted space in the left column." Two plain flex
     columns have no such row to share: each stacks its own two blocks
     (headline zone then Top Consumers; rail then Recent events) to
     its OWN height, so Top Consumers flows straight up against
     whichever is shorter, and the columns' bottoms are free to differ. */

  .overview__body {
    display: flex;
    align-items: flex-start;
    gap: 2.5rem;
    padding-bottom: 1.5rem;
  }
  .overview__col {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
    min-width: 0;
  }
  .overview__col-left {
    flex: 1.25 1 0;
  }
  .overview__col-right {
    flex: 1 1 0;
  }
  @media (max-width: 47.9375rem) {
    .overview__body {
      flex-direction: column;
      gap: 1.5rem;
    }
  }

  .overview__headline-zone {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }

  .overview__headline-row {
    display: flex;
    align-items: center;
    gap: 0.7rem;
  }
  .overview__headline-dot {
    position: relative;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .overview__headline-dot--pulse::after {
    content: '';
    position: absolute;
    inset: -7px;
    border-radius: 50%;
    border: 1px solid currentColor;
    opacity: 0;
    animation: overview-headline-ping 2.6s ease-out infinite;
  }
  @keyframes overview-headline-ping {
    0% {
      opacity: 0;
      transform: scale(0.96);
    }
    8% {
      opacity: 0.6;
      transform: scale(1);
    }
    32% {
      opacity: 0;
      transform: scale(1.12);
    }
    100% {
      opacity: 0;
      transform: scale(1.12);
    }
  }
  .overview__headline-text {
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 2rem;
    line-height: 1.1;
    margin: 0;
    color: var(--ink);
  }
  @media (max-width: 47.9375rem) {
    .overview__headline-text {
      font-size: 1.6rem;
    }
  }

  .overview__headline-subs {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .overview__sub-line {
    margin: 0;
    font-size: 0.94rem;
    color: var(--ink);
  }
  .overview__sub-line--quiet {
    color: var(--ink-2);
    font-size: 0.88rem;
  }

  .overview__metrics-rail {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  /* --- Attention: plain content, connected to the headline by
     PROXIMITY alone -- it's just the next thing in the headline-zone's
     own flex column, spaced by that column's own gap, same as every
     other subline above it. No frame, no brackets, no leader line: the
     one rule surviving the corrective pass is that a line either
     separates two real regions or encodes real data. -------------- */

  .overview__attention {
    display: flex;
    flex-direction: column;
    gap: 0.9rem;
  }
  .overview__attn-row {
    display: flex;
    align-items: flex-start;
    gap: 0.7rem;
  }
  .overview__attn-dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex-shrink: 0;
    margin-top: 0.35em;
  }
  .overview__attn-title {
    font-weight: 600;
    font-size: 1.02rem;
    color: var(--ink);
  }
  .overview__attn-title a {
    color: inherit;
  }
  .overview__attn-detail {
    color: var(--ink-2);
    font-size: 0.88rem;
    margin-top: 0.2rem;
  }
  .overview__closing-line {
    color: var(--ink-2);
    font-size: 0.88rem;
    margin: 0;
  }

  /* --- Top Consumers / Recent events (each now stacked in its own
     column above -- see overview__body's own doc) ------------------- */

  .overview__top,
  .overview__events {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
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
  .overview__top-switcher {
    display: inline-flex;
    align-self: flex-start;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 6px;
    overflow: hidden;
  }
  .overview__top-switch {
    min-height: 28px;
    padding: 0 0.6rem;
    border: none;
    border-right: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    cursor: pointer;
  }
  .overview__top-switch:last-child {
    border-right: none;
  }
  .overview__top-switch--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-weight: 600;
  }
  .overview__events-empty {
    margin: 0;
  }
  .overview__events-list {
    display: flex;
    flex-direction: column;
  }
</style>

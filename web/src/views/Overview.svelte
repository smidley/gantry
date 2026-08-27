<!--
  Overview: the landing page, D2 ("plain-reading anchor") design --
  see .superpowers/design-exploration/{direction-d2.md,mockup-d2.html}.
  A plain-language status headline (with a countable fleet strip as its
  own evidence) replaces the earlier fleet-summary card; a bracket-framed
  attention module promotes whatever actually needs a look, with a
  miniature array bay schematic when a disk itself is the reason; the
  top-row stat tiles become a quiet instrument rail. Top Consumers,
  Recent events, and the GPU strip are unchanged in substance, restyled
  only. Everything still reads straight off the live SSE frame -- no
  fetch, no polling -- except the events feed, exactly as before.
-->
<script>
  import { onMount, tick } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { keysByPattern, sumMetricsByPattern, sumSeriesPoints, parityIsRunning, etaFromProgress } from '../lib/metrics';
  import { diskUsagePct, sortDiskEntities } from '../lib/disks';
  import { topFromFrame } from '../lib/topFromFrame';
  import { fetchEvents, fetchSeries, fetchSnapshot } from '../lib/api';
  import { deriveOverviewStatus, describeAnomaly, worstSeverity } from '../lib/overviewStatus';

  import StatTile from '../components/StatTile.svelte';
  import FleetStrip from '../components/FleetStrip.svelte';
  import BaySchematic from '../components/BaySchematic.svelte';
  import SourcesBanner from '../components/SourcesBanner.svelte';
  import GPUStrip from '../components/GPUStrip.svelte';
  import TopBarList from '../components/TopBarList.svelte';
  import EventFeedItem from '../components/EventFeedItem.svelte';

  const EVENTS_POLL_MS = 30_000;
  const TWEEN_MS = 400;
  const LIVE_WINDOW_SEC = 900;
  // Matches the leader line's own CSS breakpoint (.overview__leader's
  // `display` toggle) -- md, 48rem/768px, same threshold the sidebar
  // itself swaps for the mobile TabBar at.
  const LEADER_MIN_WIDTH = 768;

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

  let topCPU = $derived(topFromFrame(live.frame, 'cpu', 5));

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
      if (pct !== null) out.push({ slot, pct });
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
    const calloutBySlot = new Map();
    for (const a of diskAnomalies) {
      calloutBySlot.set(a.slot, describeAnomaly(a).detail);
    }
    return diskEntries.map((d) => ({
      slot: d.slot,
      pct: d.pct,
      flagged: calloutBySlot.has(d.slot),
      calloutText: calloutBySlot.get(d.slot),
    }));
  });

  let closingLine = $derived.by(() => {
    if (!showBaySchematic) return null;
    const flaggedCount = new Set(overviewStatus.flaggedDiskSlots).size;
    const rest = diskEntries.length - flaggedCount;
    if (rest <= 0) return null; // every member is flagged -- nothing left to reassure about
    return rest === 1 ? '1 other array member is within normal range.' : `${rest} other array members are within normal range.`;
  });

  // --- Leader line (headline dot -> attention module) ---------------------
  //
  // An absolutely-positioned SVG overlay measures two real DOM nodes
  // (getBoundingClientRect) and redraws a dotted elbowed path between
  // them -- same technique as mockup-d2.html's own script, upgraded per
  // direction-d.md's own noted risk: a ResizeObserver on the stage
  // (catches content-driven reflow -- a longer anomaly list changes the
  // stage's own height) alongside a window resize listener (viewport
  // changes), rather than resize-only. Hidden below LEADER_MIN_WIDTH,
  // matching the CSS breakpoint that hides .overview__leader itself.
  let stageEl = $state(null);
  let headlineDotEl = $state(null);
  let attentionFrameEl = $state(null);
  let leaderGeometry = $state(null);

  function drawLeader() {
    if (!stageEl || !headlineDotEl || !attentionFrameEl || window.innerWidth < LEADER_MIN_WIDTH) {
      leaderGeometry = null;
      return;
    }
    const stageRect = stageEl.getBoundingClientRect();
    const dotRect = headlineDotEl.getBoundingClientRect();
    const frameRect = attentionFrameEl.getBoundingClientRect();

    const startX = dotRect.left - stageRect.left + dotRect.width / 2;
    const startY = dotRect.top - stageRect.top + dotRect.height / 2;
    const endX = frameRect.left - stageRect.left + 14;
    const endY = frameRect.top - stageRect.top;
    const midY = endY - 22;

    leaderGeometry = { w: stageRect.width, h: stageRect.height, startX, startY, midY, endX, endY };
  }

  onMount(() => {
    drawLeader();
    const ro = new ResizeObserver(() => drawLeader());
    if (stageEl) ro.observe(stageEl);
    window.addEventListener('resize', drawLeader);
    return () => {
      ro.disconnect();
      window.removeEventListener('resize', drawLeader);
    };
  });

  // statusOk/anomalyCount are deliberately their own primitive $derived
  // values, not just `overviewStatus.ok`/`.anomalies.length` read
  // straight off the compound object below: overviewStatus itself is a
  // brand-new object every SSE frame (disks/sources are read off
  // live.frame, which is reassigned wholesale every tick), but a
  // $derived's own downstream notification is cut off whenever its
  // OUTPUT is unchanged -- true for a primitive number/boolean even
  // when its upstream recomputed, not true for a freshly-allocated
  // object, which never equals its predecessor by reference. Depending
  // on these two (plus showBaySchematic, already boolean) instead of
  // overviewStatus directly is what keeps the effect below from
  // re-measuring on every single live frame.
  let statusOk = $derived(overviewStatus.ok);
  let anomalyCount = $derived(overviewStatus.anomalies.length);

  // Direct backstop for the very first paint after the attention module
  // itself mounts/unmounts or changes shape -- the ResizeObserver above
  // catches the same change, but only on its own next-frame callback;
  // this re-measures as soon as Svelte has actually applied the DOM
  // update (tick), rather than waiting on that.
  $effect(() => {
    statusOk;
    anomalyCount;
    showBaySchematic;
    tick().then(drawLeader);
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

  <div class="overview__stage" bind:this={stageEl}>
    <div class="overview__hero">
      <div class="overview__headline-zone">
        <div class="overview__headline-scale" aria-hidden="true">
          {#each Array.from({ length: 17 }) as _, i (i)}
            <span class:overview__headline-tick--tall={i % 4 === 0}></span>
          {/each}
        </div>
        <div class="overview__headline-row">
          <span
            class="overview__headline-dot"
            class:overview__headline-dot--pulse={!overviewStatus.ok}
            style={`background:${statusColor}; color:${statusColor}`}
            bind:this={headlineDotEl}
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
      </div>

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
          value2={fmtRate(netTxTween.current)}
          label2="↑"
          sparklinePoints={netRxRing.points}
        />
        <StatTile
          bare
          label="Disk IO"
          liveValue={ioRead}
          formatValue={(v) => `r ${fmtRate(v)}`}
          value2={fmtRate(ioWriteTween.current)}
          label2="w"
          sparklinePoints={ioReadRing.points}
        />
      </div>
    </div>

    {#if !overviewStatus.ok}
      <section class="overview__attention" bind:this={attentionFrameEl}>
        <span class="overview__attention-corner overview__attention-corner--tl"></span>
        <span class="overview__attention-corner overview__attention-corner--tr"></span>
        <span class="overview__attention-corner overview__attention-corner--bl"></span>
        <span class="overview__attention-corner overview__attention-corner--br"></span>

        <div class="overview__attention-body">
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
        </div>
      </section>

      {#if leaderGeometry}
        <svg
          class="overview__leader"
          viewBox={`0 0 ${leaderGeometry.w} ${leaderGeometry.h}`}
          width={leaderGeometry.w}
          height={leaderGeometry.h}
          aria-hidden="true"
        >
          <path
            class="overview__leader-path"
            d={`M${leaderGeometry.startX},${leaderGeometry.startY} L${leaderGeometry.startX},${leaderGeometry.midY} L${leaderGeometry.endX},${leaderGeometry.midY} L${leaderGeometry.endX},${leaderGeometry.endY}`}
          />
          <circle
            class="overview__leader-dot"
            cx={leaderGeometry.startX}
            cy={leaderGeometry.startY}
            r="2"
            style={`fill:${statusColor}`}
          />
        </svg>
      {/if}
    {/if}
  </div>

  <div class="overview__grid">
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
    gap: 1.25rem;
  }

  /* --- Stage / hero row -------------------------------------------- */

  .overview__stage {
    position: relative;
  }
  .overview__hero {
    display: grid;
    grid-template-columns: 1.25fr 1fr;
    gap: 2.5rem;
    /* start, not the mockup's own center: the real rail carries live
       sparklines on every row (Network/Disk IO's existing rings are
       preserved, not dropped to match the mockup's text-only versions
       of those two rows), so it's routinely taller than the headline
       zone -- centering two columns of very different heights left an
       awkward, asymmetric gap above the shorter one (reproduced live).
       Aligning both to the top reads cleanly regardless of which
       column ends up taller. */
    align-items: start;
    padding-bottom: 1.5rem;
  }
  @media (max-width: 47.9375rem) {
    .overview__hero {
      grid-template-columns: 1fr;
      gap: 1.5rem;
    }
  }

  .overview__headline-zone {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }

  /* The tick scale is the ring's own bezel, unrolled into a line -- pure
     atmosphere (the same job the page's corner brackets do elsewhere),
     never meant to be read as data. */
  .overview__headline-scale {
    display: flex;
    justify-content: space-between;
    align-items: flex-end;
    width: 100%;
    max-width: 25rem;
    height: 10px;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 14%, transparent);
  }
  .overview__headline-scale span {
    width: 1px;
    height: 5px;
    background: var(--ink-2);
    opacity: 0.45;
  }
  .overview__headline-scale span.overview__headline-tick--tall {
    height: 9px;
    opacity: 0.75;
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

  /* --- Attention module ---------------------------------------------- */

  .overview__attention {
    position: relative;
    padding: 1.1rem 1.35rem 1.25rem;
    margin-top: 0.25rem;
  }
  .overview__attention-corner {
    position: absolute;
    width: 12px;
    height: 12px;
  }
  .overview__attention-corner--tl {
    top: 0;
    left: 0;
    border-top: 1.5px solid var(--ink-2);
    border-left: 1.5px solid var(--ink-2);
  }
  .overview__attention-corner--tr {
    top: 0;
    right: 0;
    border-top: 1.5px solid var(--ink-2);
    border-right: 1.5px solid var(--ink-2);
  }
  .overview__attention-corner--bl {
    bottom: 0;
    left: 0;
    border-bottom: 1.5px solid var(--ink-2);
    border-left: 1.5px solid var(--ink-2);
  }
  .overview__attention-corner--br {
    bottom: 0;
    right: 0;
    border-bottom: 1.5px solid var(--ink-2);
    border-right: 1.5px solid var(--ink-2);
  }
  .overview__attention-body {
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

  .overview__leader {
    position: absolute;
    left: 0;
    top: 0;
    width: 100%;
    height: 100%;
    pointer-events: none;
    z-index: 1;
    display: none;
  }
  .overview__leader-path {
    fill: none;
    stroke: color-mix(in oklab, var(--ink) 45%, transparent);
    stroke-width: 1.25;
    stroke-dasharray: 4 3;
  }
  @media (min-width: 48rem) {
    .overview__leader {
      display: block;
    }
  }

  /* --- Supporting grid (Top Consumers / Recent events) ---------------- */

  .overview__grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
    align-items: start;
  }
  .overview__grid > :global(*) {
    min-width: 0;
  }
  @media (max-width: 47.9375rem) {
    .overview__grid {
      grid-template-columns: 1fr;
    }
  }
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
  .overview__events-empty {
    margin: 0;
  }
  .overview__events-list {
    display: flex;
    flex-direction: column;
  }
</style>

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
  than it, a common case in the healthy/no-attention state. Fixed with
  two independent flex columns sharing the whole page (superseded by
  the Balance pass below, which replaced that shared-column split
  entirely -- see its own doc).

  Corrective pass (post-deploy, Scott's own read: "there's lines in the
  middle of text that are not used"): the first cut over-decorated this
  -- a tick ruler that measured nothing, a dotted leader line that ran
  straight through the sublines' own text and left a dead void above
  "Needs a look", and orphaned corner-bracket glyphs, all deleted
  outright. The one rule that survived: a line either separates two
  real regions or encodes real data, or it doesn't exist.

  Header compaction pass (Scott: "lots of wasted space here" -- the
  headline/fleet-strip/facts/callouts/schematic stack was spending a
  full screen height): the status band -- headline's own facts on the
  left, fleet strip + array schematic stacked on the right -- is now a
  two-column row at >=768px (overview__status-band), not a single
  vertical stack; the schematic itself moved out of the conditional
  attention section into that right column, always visible rather than
  only during a disk anomaly; and each attention row collapsed from a
  title line plus a separate detail line into one inline sentence.
  Mobile (<768px) keeps the original vertical stack.

  Balance pass (Scott: "the dashboard overview needs some work. sections
  are arranged oddly with lots of wasted space and some items are not
  intuitive and odd sizes."): two independent problems, fixed
  separately. First, the status band's two columns never matched height
  -- fleet strip + bay schematic routinely ran 150-250px taller than the
  three plain-text fact lines beside them, and "Needs a look" used to be
  a THIRD block, full-width, below the whole band -- so it only started
  once the taller visuals column finished, leaving a dead gap under the
  short facts column the entire time (confirmed live: a 175px void).
  Attention now lives inside overview__status-facts, right after the
  fact lines it explains -- adjacent to the headline it's about, per
  Scott's own framing, and the gap is gone because there's no longer a
  full-width block waiting on the taller column. Second,
  overview__body's shared two-column split (headline-zone+Top Consumers
  stacked left, rail+events stacked right -- the Density pass above) put
  Top Consumers and the events feed in unrelated columns for no reason
  tied to either one's own content: Top Consumers ended up width-
  starved (labels/bars/values cramped into ~640px) while the rail's own
  column ran nearly DOUBLE the opposite column's total height, confirmed
  live at 1287px vs 659px. overview__modules-band replaces that: Top
  Consumers and Recent events now share one wide lane, stacked (so they
  never compete for width), and the rail -- four bare label+sparkline
  rows, the one module that's genuinely narrow by nature -- gets its own
  dedicated lane instead of an arbitrary 50/50 split. overview__body/
  col-left/col-right are gone entirely; the status band and modules band
  are now two independent full-width rows, each free to pick its own
  column split.
-->
<script>
  import { onMount, untrack } from 'svelte';
  import { flip } from 'svelte/animate';
  import { fade, fly } from 'svelte/transition';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { motion } from '../lib/motion.svelte';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fmtBytes, fmtCores, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import {
    keysByPattern,
    sumMetricsByPattern,
    sumSeriesPoints,
    parityIsRunning,
    etaFromProgress,
    niceCeiling,
  } from '../lib/metrics';
  import { diskKind, diskTempState, diskUsagePct, sortDiskEntities } from '../lib/disks';
  import { containerRunState, unhealthyContainerNames } from '../lib/containerStatus';
  import { isTopResource, resourceScaleMax, TOP_RESOURCES, topFromFrame } from '../lib/topFromFrame';
  import { createRankStabilityState, stableTopN } from '../lib/rankStability';
  import { fetchEvents, fetchSeries, fetchSnapshot } from '../lib/api';
  import { acks } from '../lib/acks.svelte';
  import { calloutTextBySlot, deriveOverviewStatus, worstSeverity } from '../lib/overviewStatus';
  import { band } from '../lib/thresholds';

  import CalloutRow from '../components/CalloutRow.svelte';
  import StatTile from '../components/StatTile.svelte';
  import FleetStrip from '../components/FleetStrip.svelte';
  import BaySchematic from '../components/BaySchematic.svelte';
  import SourcesBanner from '../components/SourcesBanner.svelte';
  import GPUStrip from '../components/GPUStrip.svelte';
  import TopBarList from '../components/TopBarList.svelte';
  import EventFeedItem from '../components/EventFeedItem.svelte';

  const EVENTS_POLL_MS = 30_000;
  const LIVE_WINDOW_SEC = 900;
  // FEED_MOTION_MS: the events feed's own flip/fly/fade duration --
  // modest, matching TopBarList's identical constant (Scott's own ask:
  // "make the transition... flow smooth instead of just a hard swap or
  // new entry"). 0 under prefers-reduced-motion.
  const FEED_MOTION_MS = 250;

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

  let feedMotionMs = $derived(motion.reduced ? 0 : FEED_MOTION_MS);

  let host = $derived(live.frame?.host ?? {});
  let netRx = $derived(sumMetricsByPattern(host, 'net', '.rx_bps'));
  let netTx = $derived(sumMetricsByPattern(host, 'net', '.tx_bps'));
  let ioRead = $derived(sumMetricsByPattern(host, 'diskio', '.read_bps'));
  let ioWrite = $derived(sumMetricsByPattern(host, 'diskio', '.write_bps'));

  // --- Fleet -------------------------------------------------------------

  // created (never-started) containers -- ephemeral CI-runner spawns are
  // the live example that prompted this -- are excluded from the fleet
  // strip entirely: nothing to monitor, and a churny burst of them
  // would otherwise flood it. containerRunState (not the raw state
  // string) decides what counts as running, so "created" is never
  // conflated with a real stop; the Containers view lists created
  // containers separately (see its own partition).
  let containerEntries = $derived(Object.entries(live.frame?.containers ?? {}));
  let unhealthyNames = $derived(unhealthyContainerNames(live.frame?.containers ?? {}));
  let fleetContainers = $derived(
    containerEntries
      .filter(([, c]) => containerRunState(c.state) !== 'created')
      .map(([name, c]) => ({
        name,
        state: c.state,
        health: c.health,
        icon: c.icon,
        cpuPct: c.metrics['cpu.pct'],
        memBytes: c.metrics['mem.bytes'],
      })),
  );

  // TOP_MODULE_LIMIT: this module's own top-N cut, per the D2 compact-
  // module brief. ALL_PRESENT_LIMIT feeds topFromFrame instead -- rank
  // stability (rankStability.ts) needs every present container's own
  // instant value to compute a correct rolling average and to let a real
  // challenger be seen climbing BEFORE it's already inside the naive
  // top-5, not just TOP_MODULE_LIMIT's own cut re-applied one metric
  // late; stableTopN does the actual top-N cut itself, after averaging.
  const TOP_MODULE_LIMIT = 5;
  const ALL_PRESENT_LIMIT = 500;
  const topRankState = createRankStabilityState();
  let topRows = $derived(
    stableTopN(
      topFromFrame(live.frame, topResource, ALL_PRESENT_LIMIT),
      topRankState,
      topResource,
      TOP_MODULE_LIMIT,
      live.frame?.ts ?? 0,
    ),
  );

  // topScaleMax/topScaleCeilingLabel: net/io have no fixed 0-100 ceiling
  // (resourceScaleMax's own doc), so instead of the leaderboard's OWN max
  // (a quiet fleet then reads as maxed out -- Scott: "NET can obviously
  // go higher, but it looks like it's maxed out"), scale against a nice
  // 1-2-5 ceiling at least as big as the current max. One label for the
  // whole module (not per row): every row already shares one scale.
  let topScaleMax = $derived.by(() => {
    const base = resourceScaleMax(topResource, live.frame);
    if (base !== undefined) return base;
    if (topResource === 'net' || topResource === 'io') {
      return niceCeiling(Math.max(0, ...topRows.map((r) => r.value)));
    }
    return undefined;
  });
  let topScaleCeilingLabel = $derived(
    (topResource === 'net' || topResource === 'io') && topScaleMax ? `Scale ≤ ${fmtRate(topScaleMax)}` : null,
  );

  // --- Array/disks (moved in from the old ArrayCard, which D2 folds into
  // a quiet headline subline instead of a dedicated card -- see
  // direction-d2.md/mockup-d2.html, which shows no parity-progress/mover
  // card at all on Overview) -------------------------------------------

  let unraidArray = $derived(live.frame?.unraid?.array ?? {});
  let started = $derived(unraidArray['array.started']);
  let parityPct = $derived(unraidArray['parity.progress_pct']);
  // parityPctTween glides the percentage arrayStateSentence displays
  // (perpetual-glide motion pass -- previously a bare fmtPct(parityPct),
  // the "ArrayCard subline" the pass's own inventory found snapping
  // every ~2s tick with no easing at all). Plain live-only glide, same
  // duration/curve contract as every other live surface
  // (live.glideMs/linear, see streamdriver.ts's own doc) -- there's no
  // scrub mechanism for this sentence to mirror.
  let parityPctTween = new Tween(untrack(() => parityPct ?? 0), { duration: live.glideMs, easing: linear });
  $effect(() => {
    const reduced = motion.reduced;
    parityPctTween.set(parityPct ?? 0, { duration: reduced ? 0 : live.glideMs, easing: linear });
  });
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
      return `Parity check running · ${fmtPct(parityPctTween.current)}${parityEta !== null ? `, ETA ${fmtDuration(parityEta)}` : ''}.`;
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
      const metrics = disks[slot];
      const pct = diskUsagePct(metrics);
      if (pct !== null) {
        out.push({
          slot,
          pct,
          kind: diskKind(diskMeta[slot], metrics),
          device: diskMeta[slot]?.device,
          tempState: diskTempState(metrics),
          usedBytes: metrics['fs.used_bytes'],
          freeBytes: metrics['fs.free_bytes'],
        });
      }
    }
    return out;
  });

  // --- Status headline + attention module ---------------------------------

  let overviewStatus = $derived(
    deriveOverviewStatus({
      unhealthyNames,
      arrayStarted: started,
      disks,
      sources: live.frame?.sources ?? {},
      alerts: live.frame?.alerts?.firing ?? [],
      acks: acks.list,
      insights: live.frame?.insights?.active ?? [],
    }),
  );
  let statusColor = $derived(`var(--status-${worstSeverity(overviewStatus.anomalies)})`);

  let diskAnomalies = $derived(
    overviewStatus.anomalies.filter((a) => a.kind === 'disk-usage' || a.kind === 'disk-errors'),
  );
  let diskAlertBySlot = $derived.by(
    () =>
      new Map(
        (live.frame?.alerts?.firing ?? [])
          .filter((alert) => !alert.silenced && alert.kind === 'disk' && alert.entity)
          .map((alert) => [alert.entity, alert.summary || alert.rule_name]),
      ),
  );

  // baySchematicEntries: the schematic is now always in the status
  // band's own right column (header-compaction pass), not gated behind
  // a disk anomaly -- an empty diskEntries just maps to an empty array,
  // which BaySchematic's own `{#if entries.length > 0}` already renders
  // as nothing.
  let baySchematicEntries = $derived.by(() => {
    const calloutBySlot = calloutTextBySlot(diskAnomalies);
    return diskEntries
      .map((d) => ({
        slot: d.slot,
        pct: d.pct,
        flagged: calloutBySlot.has(d.slot) || diskAlertBySlot.has(d.slot),
        calloutText: calloutBySlot.get(d.slot) ?? diskAlertBySlot.get(d.slot),
        kind: d.kind,
        device: d.device,
        tempState: d.tempState,
        usedBytes: d.usedBytes,
        freeBytes: d.freeBytes,
      }))
      .sort((a, b) => Number(b.flagged) - Number(a.flagged) || b.pct - a.pct || a.slot.localeCompare(b.slot));
  });

  // closingLine only appears once something has actually been flagged --
  // reassuring about "the rest" of an array nobody raised a concern
  // about in the first place would be a non sequitur.
  let closingLine = $derived.by(() => {
    const flaggedCount = new Set(overviewStatus.flaggedDiskSlots).size;
    if (flaggedCount === 0) return null;
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
    // acks load once alongside the first events fetch -- the list rides
    // outside the SSE frame (see acks.svelte.ts), so the derivation
    // above sees every standing acknowledgement from first render.
    acks.ensureLoaded();
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

  <div class="card overview__headline-zone">
    <div class="overview__headline-row">
      <span
        class="overview__headline-dot"
        class:overview__headline-dot--pulse={!overviewStatus.ok}
        style={`background:${statusColor}; color:${statusColor}`}
        aria-hidden="true"
      ></span>
      <h2 class="overview__headline-text">{overviewStatus.headline}</h2>
    </div>
    <div class="overview__status-band">
      <div class="overview__status-facts">
        <p class="overview__sub-line overview__sub-line--quiet">{arrayStateSentence}</p>
        {#if hottestSentence}
          <p class="overview__sub-line overview__sub-line--quiet">{hottestSentence}</p>
        {/if}

        {#if !overviewStatus.ok}
          <section class="overview__attention">
            <span class="microlabel">Needs a look</span>
            {#each overviewStatus.anomalies as anomaly, i (i)}
              <CalloutRow {anomaly} />
            {/each}
          </section>
        {/if}
      </div>
      <div class="overview__status-visuals">
        <FleetStrip containers={fleetContainers} />
        <BaySchematic entries={baySchematicEntries} summary={closingLine} />
      </div>
    </div>
  </div>

  <div class="overview__modules-band">
    <div class="overview__modules-wide">
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
        {#if topScaleCeilingLabel}
          <p class="microlabel overview__top-scale">{topScaleCeilingLabel}</p>
        {/if}
        <TopBarList
          rows={topRows}
          formatValue={TOP_FORMATTERS[topResource]}
          formatSecondary={TOP_SECONDARY_FORMATTERS[topResource]}
          live={true}
          scaleMax={topScaleMax}
          metricKey={topResource}
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
              <div
                animate:flip={{ duration: feedMotionMs }}
                in:fly={{ y: -12, duration: feedMotionMs }}
                out:fade={{ duration: feedMotionMs }}
              >
                <EventFeedItem {event} />
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>

    <div class="overview__modules-narrow">
      <div class="card overview__metrics-rail">
        <StatTile
          bare
          href="#/top/cpu"
          label="CPU"
          liveValue={host['cpu.total'] ?? 0}
          formatValue={fmtPct}
          sparklinePoints={cpuRing.points}
          bandFor={(v) => band('host.cpu', v)}
        />
        <StatTile
          bare
          href="#/top/mem"
          label="Memory"
          liveValue={host['mem.used_pct'] ?? 0}
          formatValue={fmtPct}
          sparklinePoints={memRing.points}
          bandFor={(v) => band('host.mem', v)}
        />
        <StatTile
          bare
          href="#/top/net"
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
          href="#/top/io"
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
    </div>
  </div>

  <GPUStrip gpu={live.frame?.gpu ?? {}} gpuMeta={live.frame?.gpu_meta ?? {}} />
</div>

<style>
  .overview {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  /* --- Modules band: Top Consumers + Recent events share one wide
     lane (stacked, so the two never compete for width -- the Balance
     pass's own doc, top of file), the rail gets its own narrower lane
     -- four bare label+sparkline rows are the one module here that's
     genuinely narrow by nature, not a module that lost a fight for
     space. Plain flex, same "independent-height columns" reasoning the
     old overview__body had (no shared grid row to force either lane to
     the other's height): modules-wide (Top Consumers + Events, ~490px
     combined) and modules-narrow (the rail, ~660px, four fixed-height
     sparklines) are never going to match, and don't need to. -------- */
  .overview__modules-band {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }
  .overview__modules-wide {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    flex: 1.6 1 0;
    min-width: 0;
  }
  .overview__modules-narrow {
    display: flex;
    flex-direction: column;
    flex: 1 1 0;
    min-width: 0;
  }
  @media (max-width: 47.9375rem) {
    .overview__modules-band {
      flex-direction: column;
      gap: 1.5rem;
    }
  }

  .overview__headline-zone {
    display: flex;
    flex-direction: column;
    gap: 1.15rem;
    min-width: 0;
    padding: clamp(1.15rem, 2vw, 1.6rem);
    overflow: hidden;
    background:
      radial-gradient(circle at 92% 5%, color-mix(in oklab, var(--accent) 11%, transparent), transparent 18rem),
      var(--surface-raised);
  }

  .overview__headline-row {
    display: flex;
    align-items: center;
    gap: 0.7rem;
  }
  .overview__headline-dot {
    position: relative;
    width: 11px;
    height: 11px;
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
    font-size: clamp(1.75rem, 3vw, 2.45rem);
    line-height: 1.1;
    letter-spacing: -0.045em;
    margin: 0;
    color: var(--ink);
  }
  @media (max-width: 47.9375rem) {
    .overview__headline-text {
      font-size: 1.6rem;
    }
  }

  /* --- Status band: facts+attention (left) + fleet strip/schematic
     (right) at >=768px (unified with every other view's own mobile
     breakpoint, dropped from the header-compaction pass's one-off
     64rem), one vertical stack below it. overview__status-facts' own
     gap (0.6rem, the Balance pass) now separates BOTH the three fact
     lines from each other AND the last of them from "Needs a look"
     (moved in from a separate full-width block below the band -- see
     that pass's own doc) -- looser than the header-compaction pass's
     original 0.35rem (tuned for fact lines alone), tight enough that
     the three lines still read as one group, loose enough that
     attention reads as its own paragraph rather than a fourth fact
     line. ---- */
  .overview__status-band {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }
  @media (min-width: 48rem) {
    .overview__status-band {
      flex-direction: row;
      align-items: flex-start;
      gap: 2rem;
    }
    .overview__status-facts,
    .overview__status-visuals {
      flex: 1 1 0;
      min-width: 0;
    }
  }
  .overview__status-facts {
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
  }
  .overview__status-visuals {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
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
    padding: 1.2rem;
  }

  /* --- Attention: plain content, connected to the headline by
     PROXIMITY alone -- it's just the next thing in the headline-zone's
     own flex column, spaced by that column's own gap, same as every
     other subline above it. No frame, no brackets, no leader line: the
     one rule surviving the corrective pass is that a line either
     separates two real regions or encodes real data. Each row is one
     CalloutRow (title + inline reason + ack control -- its own doc);
     this section only owns the shared container they stack in. ------ */

  .overview__attention {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
    margin-top: 0.6rem;
    padding: 0.9rem 1rem;
    border-radius: 11px;
    background: color-mix(in oklab, var(--status-warning) 8%, var(--surface-muted));
    border: 1px solid color-mix(in oklab, var(--status-warning) 20%, var(--border));
  }
  /* --- Top Consumers / Recent events (each now stacked in its own
     column above -- see overview__body's own doc) ------------------- */

  .overview__top,
  .overview__events {
    padding: 1.2rem;
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
    color: var(--accent);
    text-decoration: none;
  }
  .overview__top-scale {
    margin: 0;
    text-align: right;
  }
  .overview__top-switcher {
    display: inline-flex;
    gap: 3px;
    padding: 3px;
    align-self: flex-start;
    border: 1px solid var(--border);
    border-radius: 9px;
    overflow: hidden;
    background: var(--surface-soft);
  }
  .overview__top-switch {
    min-height: 30px;
    padding: 0 0.6rem;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    cursor: pointer;
  }
  .overview__top-switch--active {
    background: var(--surface);
    color: var(--accent-strong);
    font-weight: 600;
    box-shadow: 0 1px 2px color-mix(in oklab, var(--ink) 12%, transparent);
  }
  .overview__events-empty {
    margin: 0;
  }
  .overview__events-list {
    display: flex;
    flex-direction: column;
  }
</style>

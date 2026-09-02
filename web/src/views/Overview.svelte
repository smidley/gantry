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

  Adaptive all-clear pass (Scott: "When there is nothing that needs
  attention... the other sections should be expanded to use the
  available space and then we won't need to scroll down so far to see
  other things"): with zero callouts the status band's left column has
  nothing left to say -- the array facts live in BaySchematic's own
  head now (facts-relocation pass) and the attention section doesn't
  exist -- so the whole two-column band is conditional on
  !overviewStatus.ok. All-clear instead renders the headline card as a
  compact strip (overview__headline-zone--clear) and promotes the fleet
  strip + bay schematic into overview__clear-band, a full-width row of
  their own (side by side at >=64rem, stacked below), pulling the
  modules band and GPU strip up the page by roughly the dead column's
  height. With callouts present the attention layout above stays
  exactly as it was.

  Customize pass (the ask: let a user rearrange the Overview): the
  modules band -- and ONLY the modules band -- became rearrangeable. Its
  cards are no longer written out in a fixed order; each is one entry in
  a keyed {#each} over lib/overviewLayout.ts's ordered id lists, which
  are persisted server-side (GET/PUT /api/layout/overview, the
  /api/groups whole-document precedent) so an arrangement follows the
  owner across browsers rather than living in one browser's localStorage
  the way topResource/theme/motion do.

  What is deliberately NOT rearrangeable: the status band (headline,
  attention callouts, fleet strip, bay schematic) and the all-clear band
  above it, plus the GPU strip below. The "needs you" surface must not be
  buryable -- a layout gesture that can hide the reason you opened the
  page is a bug with a UI, not a feature.

  Two rules the rest of this file leans on:

  1. KEYED, POSITION-ONLY MOTION. Modules are keyed by module id and
     animate with `animate:flip` alone. There are deliberately no
     in:/out: transitions on the module list: an interrupted outro
     strands DOM nodes, and these particular nodes own live uPlot
     canvases (the rail's four sparklines, TopBarList's own rows). A
     keyed move RELOCATES the existing element rather than destroying
     and recreating it, so those charts keep streaming straight through
     a reorder -- which is the whole reason the drag is hand-rolled
     against Svelte's own keyed each instead of handed to a library that
     wants to own the DOM.

  2. VISIBILITY, NOT POSITION, drives the adaptive expansion. The
     all-clear band already grew the fleet strip to full width whenever
     the bay schematic had nothing to draw; the modules band now follows
     the same principle -- a lane with no visible modules is not
     rendered at all in normal mode, so the surviving lane takes the
     whole band. Nothing anywhere keys off "the rail is the second
     column" or "events sits under Top Consumers".
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
  import { isDefaultOverviewLayout, overviewModuleLabel } from '../lib/overviewLayout';
  import { overviewLayout } from '../lib/overviewLayout.svelte';
  import { dropTargetAt } from '../lib/dragReorder';

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

  // Both array fact lines render inside BaySchematic's own head now
  // (Scott: "Move the warmest disk reading into the storage array
  // section along with the array started mover idle status") -- card
  // sublines, not prose, so no trailing periods. The derivations stay
  // here because they read what the schematic deliberately doesn't:
  // the parity tween below, and the FULL disks map (hottestDisk scans
  // parity members too, which have no filesystem and so no bar).
  let arrayStateSentence = $derived.by(() => {
    if (started === 0) return 'Array is stopped';
    if (started === undefined) return 'Array state unknown';
    if (parityRunning) {
      return `Parity check running · ${fmtPct(parityPctTween.current)}${parityEta !== null ? `, ETA ${fmtDuration(parityEta)}` : ''}`;
    }
    return `Array started · mover ${moverRunning ? 'running' : 'idle'}`;
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
    hottestDisk ? `${hottestDisk.slot} warmest at ${hottestDisk.temp.toFixed(1)}°C` : null,
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

  // --- Customize: the modules band's own edit mode ------------------------

  // DRAG_MOTION_MS: the flip duration for a committed move -- the same
  // register as FEED_MOTION_MS above, long enough to read as a move and
  // short enough not to sit between the user and their next gesture. 0
  // under the app's own Animations setting / OS reduced motion, like
  // every other animated surface here (motion.svelte.ts).
  const DRAG_MOTION_MS = 200;
  // CUSTOMIZE_MEDIA is the same 48rem desktop/mobile split every other
  // breakpoint in this view uses. Editing is desktop-only for v1: a
  // two-lane pointer drag has no good touch story yet, and inventing one
  // badly is worse than not offering it. The SAVED arrangement still
  // applies on mobile -- the lanes just stack, wide then narrow, each in
  // its saved order (the band's own flex-direction rule below).
  const CUSTOMIZE_MEDIA = '(min-width: 48rem)';

  let editing = $state(false);
  // canCustomize starts true so a server-rendered/first paint doesn't
  // flash the affordance in and out; onMount replaces it with the real
  // match immediately.
  let canCustomize = $state(true);

  let layoutDoc = $derived(overviewLayout.doc);
  let wideIds = $derived(layoutDoc.wide);
  let narrowIds = $derived(layoutDoc.narrow);
  let hiddenIds = $derived(layoutDoc.hidden);
  let layoutIsDefault = $derived(isDefaultOverviewLayout(layoutDoc));
  let dragMotionMs = $derived(motion.reduced ? 0 : DRAG_MOTION_MS);

  // Rule 2 in the top-of-file doc: an empty lane isn't a lane. In edit
  // mode both always render (an emptied lane still has to be a drop
  // target, or the last module dragged out of it could never go back);
  // in normal mode the empty one is simply absent, and the survivor --
  // the only flex child left -- takes the whole band on its own.
  let showWideLane = $derived(editing || wideIds.length > 0);
  let showNarrowLane = $derived(editing || narrowIds.length > 0);

  onMount(() => {
    overviewLayout.ensureLoaded();
    if (typeof window === 'undefined' || !window.matchMedia) return;
    const mq = window.matchMedia(CUSTOMIZE_MEDIA);
    canCustomize = mq.matches;
    const onChange = (e) => {
      canCustomize = e.matches;
      // A window narrowed mid-edit would otherwise strand the page in
      // edit mode: the Done button hides behind the same media query
      // that hid Customize, leaving dashed outlines and no way out.
      if (!e.matches) exitEditing();
    };
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  });

  function toggleEditing() {
    if (editing) exitEditing();
    else editing = true;
  }

  // exitEditing only leaves edit mode -- there is no unsaved state to
  // commit or discard. Every gesture (drop, hide, show, reset) already
  // persisted itself through the store's own debounced PUT.
  function exitEditing() {
    endDrag();
    editing = false;
  }

  // --- Drag ---------------------------------------------------------------
  //
  // Hand-rolled on pointer events, no dependency: two vertical lists of
  // card-height targets is a midpoint comparison (lib/dragReorder.ts) and
  // a pointer capture. The pure geometry lives in that module; what's
  // here is the plumbing around it.
  //
  // dragId/dragDelta/dropTarget are reactive -- they drive the lifted
  // card's transform and the drop indicator. Everything below them is a
  // plain snapshot the template never reads, so it deliberately isn't
  // $state.
  let dragId = $state(null);
  let dragDelta = $state({ x: 0, y: 0 });
  let dropTarget = $state(null);

  let lanesEl;
  let dragGeometry = [];
  let dragOrigin = null;
  let dragScrollY = 0;
  let dragPointerId = null;
  let dragHandle = null;

  // captureGeometry snapshots both lanes and the vertical midpoint of
  // every card EXCEPT the one being dragged, once, at pointerdown --
  // deliberately never re-measured mid-drag. Nothing in the band
  // actually reflows during a drag (the lifted card moves by transform
  // and stays in flow; the drop indicator is absolutely positioned), so
  // a frozen snapshot is exactly as true as a live measurement would be,
  // while removing any chance of the indicator's own arrival shifting
  // the very targets it was computed from.
  //
  // Excluding the dragged card is what makes dropTargetAt's index
  // directly usable as a splice position -- see moveOverviewModule's own
  // doc. slotTops are LANE-LOCAL (the indicator is positioned inside the
  // lane) and there is always one more of them than there are midpoints:
  // above the first card, one per gap, and below the last.
  function captureGeometry(draggedId) {
    const columns = [];
    if (!lanesEl) return columns;
    for (const lane of lanesEl.querySelectorAll('[data-lane]')) {
      const laneRect = lane.getBoundingClientRect();
      const cards = [...lane.querySelectorAll(':scope > .overview__module')]
        .filter((el) => el.dataset.module !== draggedId)
        .map((el) => el.getBoundingClientRect());
      const slotTops = [cards.length > 0 ? cards[0].top - laneRect.top : 0];
      for (let i = 1; i < cards.length; i++) {
        slotTops.push((cards[i - 1].bottom + cards[i].top) / 2 - laneRect.top);
      }
      if (cards.length > 0) slotTops.push(cards[cards.length - 1].bottom - laneRect.top);
      columns.push({
        column: lane.dataset.lane,
        left: laneRect.left,
        right: laneRect.right,
        midpoints: cards.map((r) => r.top + r.height / 2),
        slotTops,
      });
    }
    return columns;
  }

  let dropIndicatorTop = $derived.by(() => {
    if (!dropTarget) return 0;
    const col = dragGeometry.find((c) => c.column === dropTarget.column);
    if (!col || col.slotTops.length === 0) return 0;
    return col.slotTops[Math.min(dropTarget.index, col.slotTops.length - 1)];
  });

  function startDrag(event, id) {
    // Primary contact only: a right- or middle-click on the handle is
    // not a drag. (Touch and pen both report button 0.)
    if (!editing || dragId !== null || event.button !== 0) return;
    event.preventDefault(); // no text selection, no native image drag
    dragGeometry = captureGeometry(id);
    dragOrigin = { x: event.clientX, y: event.clientY };
    dragScrollY = window.scrollY;
    dragPointerId = event.pointerId;
    dragHandle = event.currentTarget;
    dragHandle.setPointerCapture(event.pointerId);
    dragDelta = { x: 0, y: 0 };
    dropTarget = null;
    dragId = id;
  }

  function dragMove(event) {
    if (dragId === null || event.pointerId !== dragPointerId) return;
    // The page can scroll under a held pointer. clientY is
    // viewport-relative and the captured geometry is frozen in the
    // viewport coordinates of pointerdown, so every comparison happens
    // in THAT frame: add back however far the page has scrolled since.
    const scrolled = window.scrollY - dragScrollY;
    dragDelta = { x: event.clientX - dragOrigin.x, y: event.clientY - dragOrigin.y + scrolled };
    dropTarget = dropTargetAt(event.clientX, event.clientY + scrolled, dragGeometry);
  }

  function dragUp(event) {
    if (dragId === null || event.pointerId !== dragPointerId) return;
    const id = dragId;
    const target = dropTarget;
    // Clear the gesture BEFORE committing, in the same synchronous
    // block: both are state changes in one flush, so animate:flip
    // measures the card where the user actually released it (transform
    // still applied on the old render) and glides it into its new slot
    // instead of jumping there.
    endDrag();
    if (target) overviewLayout.move(id, target.column, target.index);
  }

  // endDrag clears the gesture without committing -- the shared path for
  // a cancelled pointer, an Escape, leaving edit mode, and (once it has
  // read what it needs) a successful drop.
  function endDrag() {
    if (dragHandle && dragPointerId !== null && dragHandle.hasPointerCapture?.(dragPointerId)) {
      dragHandle.releasePointerCapture(dragPointerId);
    }
    dragId = null;
    dropTarget = null;
    dragDelta = { x: 0, y: 0 };
    dragGeometry = [];
    dragOrigin = null;
    dragPointerId = null;
    dragHandle = null;
  }

  // Escape cancels an in-flight drag, the universal "never mind" for a
  // gesture already under way. Bound only while one is actually running
  // so the page has no stray global key handler the rest of the time.
  $effect(() => {
    if (dragId === null) return;
    const onKeydown = (e) => {
      if (e.key === 'Escape') endDrag();
    };
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });
</script>

<!-- statusVisuals: the fleet strip + bay schematic pair, rendered in
  exactly one of two homes -- the attention band's right column, or the
  all-clear's own full-width band (see the adaptive all-clear pass in
  the top-of-file doc). Each sits in its own __visual-slot so the two
  layouts only differ in how the slots flow, and the schematic's slot
  disappears with it (BaySchematic renders nothing for zero entries)
  rather than holding an empty half open. -->
{#snippet statusVisuals()}
  <div class="overview__visual-slot">
    <FleetStrip containers={fleetContainers} />
  </div>
  {#if baySchematicEntries.length > 0}
    <div class="overview__visual-slot">
      <BaySchematic
        entries={baySchematicEntries}
        summary={closingLine}
        stateLine={arrayStateSentence}
        warmestLine={hottestSentence}
      />
    </div>
  {/if}
{/snippet}

<!-- eyeIcon: the hide/show toggle's own glyph, drawn rather than
  imported (this app ships no icon set) -- open for "this is visible,
  click to hide", struck through for "hidden, click to bring it back". -->
{#snippet eyeIcon(struck)}
  <svg viewBox="0 0 20 20" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
    <path d="M1.5 10S4.6 4.5 10 4.5 18.5 10 18.5 10 15.4 15.5 10 15.5 1.5 10 1.5 10Z" />
    <circle cx="10" cy="10" r="2.6" />
    {#if struck}<path d="M3 17 17 3" />{/if}
  </svg>
{/snippet}

<!-- moduleLane renders one of the modules band's two lanes from its own
  ordered id list.

  The {#each} is keyed by module id and carries animate:flip and NOTHING
  else -- no in:/out: (see rule 1 in the top-of-file doc). Its body is a
  single element, which animate: requires and which the drop indicator
  below therefore sits OUTSIDE the each block to respect.

  The indicator is absolutely positioned rather than being a real
  placeholder element spliced into the list: a spliced-in gap reflows
  every card below it mid-drag, which moves the very targets the pointer
  is being compared against. This draws the same information -- here is
  the slot you are about to land in -- with the band's geometry
  completely still. -->
{#snippet moduleLane(column, ids)}
  <div class="overview__modules-lane overview__modules-{column}" data-lane={column}>
    {#each ids as id (id)}
      <div
        class="overview__module"
        class:overview__module--editing={editing}
        class:overview__module--dragging={dragId === id}
        data-module={id}
        style={dragId === id ? `transform: translate3d(${dragDelta.x}px, ${dragDelta.y}px, 0)` : ''}
        animate:flip={{ duration: editing ? dragMotionMs : 0 }}
      >
        {#if editing}
          <div class="overview__module-tools">
            <button
              type="button"
              class="overview__module-btn overview__module-grip"
              aria-label={`Move ${overviewModuleLabel(id)}`}
              onpointerdown={(e) => startDrag(e, id)}
              onpointermove={dragMove}
              onpointerup={dragUp}
              onpointercancel={endDrag}
            >
              <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">
                <circle cx="6" cy="3" r="1.4" /><circle cx="10" cy="3" r="1.4" />
                <circle cx="6" cy="8" r="1.4" /><circle cx="10" cy="8" r="1.4" />
                <circle cx="6" cy="13" r="1.4" /><circle cx="10" cy="13" r="1.4" />
              </svg>
            </button>
            <button
              type="button"
              class="overview__module-btn"
              aria-label={`Hide ${overviewModuleLabel(id)}`}
              onclick={() => overviewLayout.hide(id)}
            >
              {@render eyeIcon(false)}
            </button>
          </div>
        {/if}
        {@render moduleCard(id)}
      </div>
    {/each}

    {#if editing && ids.length === 0}
      <p class="microlabel overview__lane-empty">Empty lane &mdash; drop a module here</p>
    {/if}
    {#if dragId !== null && dropTarget?.column === column}
      <div class="overview__drop-indicator" style={`top:${dropIndicatorTop}px`} aria-hidden="true"></div>
    {/if}
  </div>
{/snippet}

<!-- moduleCard is the one place each rearrangeable module's actual
  content lives. Everything a module reads is ordinary component scope,
  so moving a card between lanes changes nothing about what it renders --
  only where. -->
{#snippet moduleCard(id)}
  {#if id === 'top-consumers'}
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
  {:else if id === 'events'}
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
  {:else if id === 'metrics-rail'}
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
  {/if}
{/snippet}

<div class="overview">
  <h1 class="page-title">Overview</h1>
  <SourcesBanner sources={live.frame?.sources ?? {}} />

  <div class="card overview__headline-zone" class:overview__headline-zone--clear={overviewStatus.ok}>
    <div class="overview__headline-row">
      <span
        class="overview__headline-dot"
        class:overview__headline-dot--pulse={!overviewStatus.ok}
        style={`background:${statusColor}; color:${statusColor}`}
        aria-hidden="true"
      ></span>
      <h2 class="overview__headline-text">{overviewStatus.headline}</h2>
    </div>
    {#if !overviewStatus.ok}
      <div class="overview__status-band">
        <div class="overview__status-facts">
          <section class="overview__attention">
            <span class="microlabel">Needs a look</span>
            {#each overviewStatus.anomalies as anomaly, i (i)}
              <CalloutRow {anomaly} />
            {/each}
          </section>
        </div>
        <div class="overview__status-visuals">
          {@render statusVisuals()}
        </div>
      </div>
    {/if}
  </div>

  {#if overviewStatus.ok}
    <div class="overview__clear-band">
      {@render statusVisuals()}
    </div>
  {/if}

  <div class="overview__modules-band" class:overview__modules-band--editing={editing}>
    {#if canCustomize}
      <div class="overview__modules-bar">
        {#if editing}
          <span class="microlabel overview__customize-hint">Drag a handle to move a module · the eye hides one</span>
          <button
            type="button"
            class="overview__customize-btn"
            disabled={layoutIsDefault}
            onclick={() => overviewLayout.reset()}
          >
            Reset layout
          </button>
        {/if}
        <button
          type="button"
          class="overview__customize-btn"
          class:overview__customize-btn--active={editing}
          aria-pressed={editing}
          onclick={toggleEditing}
        >
          {editing ? 'Done' : 'Customize'}
        </button>
      </div>
    {/if}

    <div class="overview__modules-lanes" bind:this={lanesEl}>
      {#if showWideLane}
        {@render moduleLane('wide', wideIds)}
      {/if}
      {#if showNarrowLane}
        {@render moduleLane('narrow', narrowIds)}
      {/if}
    </div>

    {#if editing}
      <div class="overview__hidden-tray">
        <span class="microlabel">Hidden</span>
        {#if hiddenIds.length === 0}
          <p class="microlabel overview__hidden-empty">Nothing hidden.</p>
        {:else}
          <div class="overview__hidden-list">
            {#each hiddenIds as id (id)}
              <div class="overview__ghost" data-hidden-module={id}>
                <span class="overview__ghost-name">{overviewModuleLabel(id)}</span>
                <button
                  type="button"
                  class="overview__module-btn"
                  aria-label={`Show ${overviewModuleLabel(id)}`}
                  onclick={() => overviewLayout.show(id)}
                >
                  {@render eyeIcon(true)}
                </button>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
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
  /* The band itself is now a vertical stack -- the Customize bar, the
     lanes row, and (in edit mode) the hidden tray -- so the two-column
     split moved down one level onto __modules-lanes, which is otherwise
     the exact flex row the band used to be. */
  .overview__modules-band {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
  }
  .overview__modules-lanes {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }
  .overview__modules-lane {
    position: relative; /* the drop indicator is absolutely placed in here */
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }
  .overview__modules-wide {
    flex: 1.6 1 0;
  }
  .overview__modules-narrow {
    flex: 1 1 0;
  }
  /* One module per lane row: the wrapper carries the edit-mode chrome
     and the drag transform, and stretches so the card inside keeps the
     full lane width it had when it was the lane's direct child. */
  .overview__module {
    position: relative;
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  @media (max-width: 47.9375rem) {
    .overview__modules-lanes {
      flex-direction: column;
      gap: 1.5rem;
    }
    /* Mobile stacks the lanes, so the saved arrangement still applies
       here -- wide lane first, then narrow, each in its own saved order.
       Only EDITING is desktop-only (see CUSTOMIZE_MEDIA). */
    .overview__modules-lane {
      width: 100%;
      flex: none;
    }
  }

  /* --- Customize bar: quiet by default, in the same mono/uppercase
     register as the Top Consumers switcher right below it, and pushed
     to the band's top-right corner where it stays out of the way until
     it's wanted. ------------------------------------------------- */
  .overview__modules-bar {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
    min-height: 26px;
  }
  .overview__customize-hint {
    margin-right: auto;
    text-transform: none;
    letter-spacing: 0.01em;
  }
  .overview__customize-btn {
    min-height: 26px;
    padding: 0 0.6rem;
    border: 1px solid transparent;
    border-radius: 7px;
    background: transparent;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    cursor: pointer;
  }
  .overview__customize-btn:hover:not(:disabled) {
    border-color: var(--border);
    color: var(--ink);
  }
  .overview__customize-btn:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .overview__customize-btn--active {
    border-color: color-mix(in oklab, var(--accent) 45%, var(--border));
    background: var(--surface-soft);
    color: var(--accent-strong);
    font-weight: 600;
  }

  /* --- Edit mode: enough decoration to say "these move", not enough to
     restyle the page. The dashed ring is drawn OUTSIDE each card
     (outline, not border) so no module changes size on entering edit
     mode and nothing below it shifts. ---------------------------- */
  .overview__modules-band--editing {
    user-select: none;
  }
  .overview__module--editing {
    outline: 1px dashed color-mix(in oklab, var(--accent) 42%, transparent);
    outline-offset: 3px;
    border-radius: 14px;
  }
  .overview__module--dragging {
    position: relative;
    z-index: 5;
    opacity: 0.9;
    outline-color: var(--accent);
    filter: drop-shadow(0 10px 22px color-mix(in oklab, var(--ink) 26%, transparent));
    cursor: grabbing;
  }
  /* Only the CARD goes inert while lifted -- no stray hover states or
     accidental "View all" clicks under a dragging pointer. The tools bar
     above it deliberately stays live: the grip is the pointer-capture
     target, and taking its events away mid-gesture would cut the drag
     off at the knees. */
  .overview__module--dragging .card {
    pointer-events: none;
  }

  /* The one card with its own top-right affordance is Top Consumers,
     and the tools bar lands exactly on it. Hidden (not removed) while
     editing, so the head row doesn't reflow and the link is back the
     instant Done is pressed -- a deliberate hand-off of that corner,
     rather than a toolbar that looks like it's covering something by
     accident. Nothing else on this band has anything up there. */
  .overview__modules-band--editing .overview__top-link {
    visibility: hidden;
  }
  .overview__module-tools {
    position: absolute;
    top: 6px;
    right: 8px;
    z-index: 2;
    display: flex;
    gap: 2px;
    padding: 2px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface-raised);
    box-shadow: var(--shadow-sm);
  }
  .overview__module-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--ink-2);
    cursor: pointer;
  }
  .overview__module-btn:hover {
    background: var(--surface-soft);
    color: var(--ink);
  }
  .overview__module-grip {
    cursor: grab;
    /* Without this a touch drag scrolls the page instead of moving the
       card -- pointer capture alone doesn't stop the browser's own
       panning gesture. */
    touch-action: none;
  }
  .overview__module-grip:active {
    cursor: grabbing;
  }

  /* The drop indicator: a slot-shaped marker in the gap the card would
     land in. Centred on its boundary, so it reads as "between these
     two" rather than "on top of this one". */
  .overview__drop-indicator {
    position: absolute;
    left: 0;
    right: 0;
    height: 3.1rem;
    transform: translateY(-50%);
    border: 1px dashed var(--accent);
    border-radius: 12px;
    background: color-mix(in oklab, var(--accent) 12%, transparent);
    pointer-events: none;
  }
  .overview__lane-empty {
    margin: 0;
    padding: 1.6rem 1rem;
    border: 1px dashed var(--border);
    border-radius: 12px;
    text-align: center;
    text-transform: none;
    letter-spacing: 0.01em;
  }

  /* --- Hidden tray: hidden modules keep no position (the saved
     document lists an id in exactly one place), so their ghosts live
     here rather than pretending to hold a slot they no longer have.
     Bringing one back puts it at the end of its own default lane --
     the same rule a module added by a future release gets. --------- */
  .overview__hidden-tray {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.5rem 0.75rem;
    padding: 0.6rem 0.85rem;
    border: 1px dashed var(--border);
    border-radius: 12px;
    background: var(--surface-muted);
  }
  .overview__hidden-empty,
  .overview__hidden-list {
    margin: 0;
  }
  .overview__hidden-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .overview__ghost {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.2rem 0.3rem 0.2rem 0.65rem;
    border: 1px dashed color-mix(in oklab, var(--ink-2) 45%, transparent);
    border-radius: 9px;
    background: color-mix(in oklab, var(--surface) 60%, transparent);
    color: var(--ink-2);
  }
  .overview__ghost-name {
    font-family: var(--font-mono);
    font-size: 0.72rem;
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
  /* All-clear: the card holds only the headline row (the adaptive
     all-clear pass, top-of-file doc), so it slims to a strip -- the
     vertical padding drops while the horizontal stays aligned with the
     attention state's own. */
  .overview__headline-zone--clear {
    padding-top: 1rem;
    padding-bottom: 1rem;
  }

  /* --- All-clear band: the fleet strip + bay schematic at full page
     width, side by side once there's room for two real modules
     (>=64rem -- at the app's usual 48rem split the sidebar is already
     eating ~15rem, which would squeeze each module under ~24rem),
     stacked below that. The slots flex equally; each component already
     fills its slot (width: 100%). ---- */
  .overview__clear-band {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }
  .overview__visual-slot {
    min-width: 0;
  }
  .overview__clear-band .overview__visual-slot {
    flex: 1 1 0;
  }
  @media (max-width: 63.9375rem) {
    .overview__clear-band {
      flex-direction: column;
    }
    .overview__clear-band .overview__visual-slot {
      flex: none;
      width: 100%;
    }
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

  /* --- Status band: attention (left) + fleet strip/schematic (right)
     at >=768px (unified with every other view's own mobile breakpoint,
     dropped from the header-compaction pass's one-off 64rem), one
     vertical stack below it. The array/warmest fact lines that used to
     lead the left column live inside BaySchematic's own head now (the
     facts-relocation pass -- see arrayStateSentence's doc), so the
     column is just the "Needs a look" section, top-aligned with the
     visuals beside it. ---- */
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

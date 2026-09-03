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
  exist -- so the whole two-column band was conditional on
  !overviewStatus.ok. All-clear instead renders the headline card as a
  compact strip (overview__headline-zone--clear) and promotes the fleet
  strip + bay schematic into overview__clear-band, a full-width row of
  their own (side by side at >=64rem, stacked below), pulling the
  modules band and GPU strip up the page by roughly the dead column's
  height. (Superseded on the attention side by the counts-and-fleet
  pass below, which gives BOTH states that same full-width row.)

  Counts-and-fleet pass -- three asks, one shape:

  1. "It doesn't create a list there, but instead has a count of items
     that need you. The user can click on the number and then be brought
     to a list of items that need attention. Alerts will go to the
     events page, and any container contentions will go to the insights
     page." The attention section no longer renders one CalloutRow per
     anomaly; it renders at most two count chips over the SAME anomaly
     list (lib/attentionCounts.ts owns the bucketing, the routing and
     the wording). The headline's own count is still anomalies.length,
     so the chips always sum to it, and an acknowledged concern is
     missing from the count exactly the way it used to be missing from
     the list -- deriveOverviewStatus drops it before either sees it.

     What that costs, and the one open question this pass leaves: the
     per-row Ack control went with the rows. Acknowledgement itself is
     untouched -- the store, the API, the derivation filter and
     CalloutRow (the component that owned the control) are all still
     here and still tested -- but nothing on THIS page renders it any
     more, and re-inventing one beside a count would just be a second,
     quieter list. Its new home is a product call: the natural one is
     the destination pages the chips already point at.

  2. "Container fleet section should be sized to take up the available
     screen space beneath it, and objects (containers) inside the
     section should be auto-resized depending on quantity of
     containers." That lives inside FleetStrip (see its own doc for the
     measurement and the fit), but it is the reason for the layout
     change here: the status band's two-column split existed to put a
     TALL list of callouts beside the visuals, and two chips are not a
     column. So the band collapsed -- the chips moved inline into the
     headline card, and the fleet + schematic row that all-clear
     already had became the layout for both states. The fleet is
     therefore BESIDE the schematic rather than stacked above it, which
     is what makes "the space beneath it" a real, empty thing to grow
     into; the state the page is in is still legible in the DOM
     (overview__status-band vs overview__clear-band, one shared rule
     set). Where the two do stack -- below 64rem -- the fleet is
     ordered last, for the same reason.

  3. "Glowing Container activity should be triggered by any metric that
     is above a threshold, not just CPU" -- entirely inside FleetStrip
     and lib/fleetActivity.ts. The one thing it changed out here: the
     fleet strip is handed each container's whole metrics bag plus the
     host memory total, rather than a hand-picked cpu/mem pair.

  Customize pass (the ask: let a user rearrange the Overview): the
  modules band -- and ONLY the modules band -- became rearrangeable. Its
  cards are no longer written out in a fixed order; each is one entry in
  a keyed {#each} over lib/overviewLayout.ts's ordered id lists, which
  are persisted server-side (GET/PUT /api/layout/overview, the
  /api/groups whole-document precedent) so an arrangement follows the
  owner across browsers rather than living in one browser's localStorage
  the way topResource/theme/motion do.

  What is deliberately NOT rearrangeable: the status band (headline,
  attention chips, fleet strip, bay schematic) and the all-clear band
  above it, plus the GPU strip below. The "needs you" surface must not be
  buryable -- a layout gesture that can hide the reason you opened the
  page is a bug with a UI, not a feature.

  Constrained-resize pass (the follow-up ask: let a user size what they
  rearranged): two controls, both deliberately CONSTRAINED rather than
  freeform -- this is still a designed page with two lanes, not a canvas.

  1. The COLUMN SPLIT. A hairline divider on the lanes' own boundary,
     drawn only in edit mode, sets how the wide and narrow lanes share
     the band. One saved number, clamped 0.60-0.75, defaulting to the
     1.6:1 the band shipped with. Same hand-rolled pointer shape as the
     module drag (capture, snapshot at pointerdown, Escape cancels), plus
     arrow keys, because a drag-only control would have been this page's
     first pointer-only affordance. The drag previews locally and commits
     ONCE on release, so crossing the band costs one PUT, not sixty.
     Mobile ignores the ratio outright -- stacked lanes have no split.

  2. HEIGHT STEPS. compact/normal/tall on the two modules with a
     genuinely elastic body: Top Consumers (3/5/8 leaderboard rows) and
     Recent events (4/8/14 feed rows). A step is a ROW BUDGET, not a
     pixel box -- both modules render a list at a fixed row pitch, so a
     step lands on the page's existing rhythm by construction and `tall`
     buys real content rather than padding. The rail gets no control at
     all: four fixed label+value+sparkline tiles have no list to lengthen
     and nothing that reads better taller, so a taller rail would be the
     same four tiles with more air between them -- the dead space this
     file's own layout passes have spent three rounds deleting.

  Interplay with everything ADAPTIVE on this page (rule 2 below, and the
  all-clear band reclaiming the status band's vertical space): a user-set
  size WINS. A module left at 'normal' renders exactly the budget this
  page shipped with and keeps its content-driven height -- it is the only
  thing the surrounding layout can grow into; a module set to compact or
  tall renders the owner's budget and no adaptive rule overrules it.
  Every module at 'normal' -- the default document -- is today's page
  exactly, in the all-clear state and out of it. The same priority holds
  on the width side: a ratio only ever divides a band that HAS two lanes,
  and a lone lane still spans the whole band (flex-basis 0 -- see
  overviewLaneFlex), so hiding a lane's last module still expands the
  survivor across the full width whatever ratio is saved.

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
  import { attentionChips } from '../lib/attentionCounts';
  import { band } from '../lib/thresholds';
  import {
    OVERVIEW_RATIO_MAX,
    OVERVIEW_RATIO_MIN,
    OVERVIEW_SIZES,
    isAdaptivelySized,
    isDefaultOverviewLayout,
    isResizableOverviewModule,
    nudgeOverviewRatio,
    overviewLaneFlex,
    overviewModuleLabel,
    overviewModuleMaxRows,
    overviewModuleRows,
    overviewModuleSize,
    overviewRatioFromDrag,
  } from '../lib/overviewLayout';
  import { overviewLayout } from '../lib/overviewLayout.svelte';
  import { dropTargetAt } from '../lib/dragReorder';

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

  // --- Saved layout ------------------------------------------------------
  //
  // The saved document drives three things that live far apart in this
  // file: the modules band's arrangement (the Customize section near the
  // bottom, where the rest of its deriveds are) and the two ROW BUDGETS
  // just below, which the leaderboard and the events feed need long
  // before that section is reached. It is read once, here, above its
  // first use.
  //
  // A row budget IS the height step: every resizable module here renders
  // a list at a fixed row pitch, so "compact/normal/tall" is 3/5/8 or
  // 4/8/14 rows rather than a pixel box -- integer multiples of the
  // page's existing rhythm, and a `tall` card that bought actual content
  // (more of the leaderboard, more events to read) instead of padding.
  // See OVERVIEW_MODULES' own doc for the numbers and why.
  //
  // The interplay with everything adaptive on this page (the all-clear
  // band reclaiming the status band's vertical space, an emptied lane
  // handing its width to the survivor): a user-set step WINS. A module
  // left at 'normal' renders exactly the budget this page shipped with
  // and keeps its content-driven height, which is what the surrounding
  // layout is free to grow into; a module set to compact or tall renders
  // the owner's budget and no adaptive rule overrules it. With nothing
  // resized -- the default document -- every budget below resolves to
  // today's number, so the band renders exactly as it always has.
  let layoutDoc = $derived(overviewLayout.doc);

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
  // The whole metrics bag rides through rather than a hand-picked pair:
  // the strip's glow now ranks five metrics (lib/fleetActivity.ts), and
  // a prop per metric would mean editing this map every time that set
  // changes. hostMemBytes is the one thing the bag can't answer for
  // itself -- the frame carries no host total directly, only
  // resourceScaleMax's back-calculation from the used bytes/pct pair.
  let fleetContainers = $derived(
    containerEntries
      .filter(([, c]) => containerRunState(c.state) !== 'created')
      .map(([name, c]) => ({
        name,
        state: c.state,
        health: c.health,
        icon: c.icon,
        metrics: c.metrics,
      })),
  );
  let hostMemBytes = $derived(resourceScaleMax('mem', live.frame));

  // TOP_MODULE_LIMIT: this module's own top-N cut, per the D2 compact-
  // module brief -- now the 'normal' step's own budget rather than a
  // fixed number (see topRowLimit below), and the fallback for a module
  // the size table somehow can't answer for. ALL_PRESENT_LIMIT feeds
  // topFromFrame instead -- rank stability (rankStability.ts) needs every
  // present container's own instant value to compute a correct rolling
  // average and to let a real challenger be seen climbing BEFORE it's
  // already inside the naive top-5, not just TOP_MODULE_LIMIT's own cut
  // re-applied one metric late; stableTopN does the actual top-N cut
  // itself, after averaging.
  const TOP_MODULE_LIMIT = 5;
  const ALL_PRESENT_LIMIT = 500;
  const topRankState = createRankStabilityState();
  // The cut is handed to stableTopN rather than applied to its result:
  // the hysteresis and re-sort cadence that keep this leaderboard from
  // flickering are defined AT a membership size, so they have to be told
  // the real one. stableTopN obeys a shrinking limit on its very next
  // call (see its own doc) so a step change reads as instant.
  let topRowLimit = $derived(overviewModuleRows('top-consumers', overviewModuleSize(layoutDoc, 'top-consumers')) ?? TOP_MODULE_LIMIT);
  let topRows = $derived(
    stableTopN(
      topFromFrame(live.frame, topResource, ALL_PRESENT_LIMIT),
      topRankState,
      topResource,
      topRowLimit,
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
  // The attention section is COUNTS now, not a list (Scott: "it doesn't
  // create a list there, but instead has a count of items that need
  // you"). Same anomalies, same ack filter, bucketed two ways and
  // rendered as at most two chips -- see lib/attentionCounts.ts for the
  // split and why each chip lands where it does.
  let chips = $derived(attentionChips(overviewStatus.anomalies));

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

  // EVENTS_FETCH_LIMIT is the WIDEST step's budget, not the current
  // one: the feed is fetched on a 30s poll and on focus, and a height
  // step is a rendering choice, so switching one has to re-render out of
  // what is already held rather than wait on a request. The cost is a
  // handful of extra rows on a fetch that was already tiny.
  const EVENTS_FETCH_LIMIT = overviewModuleMaxRows('events') ?? 8;
  let eventsRowLimit = $derived(overviewModuleRows('events', overviewModuleSize(layoutDoc, 'events')) ?? 8);
  let visibleEvents = $derived(events.slice(0, eventsRowLimit));

  async function loadEvents() {
    try {
      events = await fetchEvents({ limit: EVENTS_FETCH_LIMIT });
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

  // SIZE_GLYPHS: the height switcher's own labels. One letter each, in
  // the same tiny mono/uppercase register as the Top Consumers switcher
  // this control is modeled on -- the toolbar floats over the gap above
  // a card and has no room for three spelled-out words, and every button
  // carries the real word in its accessible name and its tooltip.
  const SIZE_GLYPHS = { compact: 'S', normal: 'M', tall: 'L' };

  let editing = $state(false);
  // canCustomize starts true so a server-rendered/first paint doesn't
  // flash the affordance in and out; onMount replaces it with the real
  // match immediately.
  let canCustomize = $state(true);

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

  // --- Column split -------------------------------------------------------
  //
  // ONE saved number: the wide lane's share of the two lanes' combined
  // flex space. dividerRatio is the LIVE PREVIEW while a divider drag is
  // in flight and null the rest of the time -- the drag never touches the
  // store, so a drag across the whole band costs exactly one PUT (on
  // release) rather than relying on the store's debounce to swallow
  // sixty.
  //
  // laneFlex feeds two custom properties on the lanes row rather than a
  // width per lane: `flex-basis: 0` plus a grow factor is what makes a
  // LONE lane still take the whole band (the visibility-driven expansion,
  // rule 2 in the top-of-file doc) without a special case -- see
  // overviewLaneFlex's own doc. Mobile ignores both properties outright
  // (the lanes stack, `flex: none`).
  let dividerRatio = $state(null);
  let laneRatio = $derived(dividerRatio ?? layoutDoc.ratio);
  let laneFlex = $derived(overviewLaneFlex(laneRatio));
  let laneRatioPct = $derived(Math.round(laneRatio * 100));
  // The divider only exists while BOTH lanes do: there is no split to
  // adjust when one lane has the band to itself, and leaving a handle
  // floating over the middle of a single-lane band would suggest
  // otherwise.
  let showDivider = $derived(editing && showWideLane && showNarrowLane);

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
  // commit or discard. Every gesture (drop, hide, show, resize, reset)
  // already persisted itself through the store's own debounced PUT; an
  // in-flight divider drag is the one exception, and abandoning it is
  // the same "never mind" Escape gives.
  function exitEditing() {
    endDrag();
    endDividerDrag();
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

  // --- Divider drag -------------------------------------------------------
  //
  // The same hand-rolled pointer shape as the module drag above -- pointer
  // capture, a context snapshot taken once at pointerdown, Escape cancels
  // -- with one difference: this gesture's live feedback is a REAL layout
  // change (the two lanes actually resize as you drag), not a transform on
  // a lifted card. That is the point of it, and it is cheap: the ratio
  // lands on two custom properties, so a move re-runs flex, not Svelte's
  // renderer, and the rail's four uPlot sparklines take their existing
  // ResizeObserver -> setSize path -- a resize, never a rebuild (their
  // needsRebuild shape only turns on colors and theme).
  //
  // dividerSpan is snapshotted rather than re-measured per move for the
  // same reason captureGeometry is: the very thing being measured is what
  // the gesture is changing, so a live measurement would feed the
  // divider's own effect back into its input.
  let dividerOrigin = null;
  let dividerPointerId = null;
  let dividerHandle = null;

  // laneSpanPx is the width the two lanes actually divide -- their own
  // two rects added, so the flex gap and the divider sitting between them
  // (which belong to neither) are excluded. See overviewRatioFromDrag.
  function laneSpanPx() {
    if (!lanesEl) return 0;
    let span = 0;
    for (const lane of lanesEl.querySelectorAll('[data-lane]')) span += lane.getBoundingClientRect().width;
    return span;
  }

  function startDividerDrag(event) {
    if (!editing || dividerOrigin !== null || event.button !== 0) return;
    event.preventDefault(); // no text selection, no native drag
    dividerOrigin = { x: event.clientX, ratio: laneRatio, span: laneSpanPx() };
    dividerPointerId = event.pointerId;
    dividerHandle = event.currentTarget;
    dividerHandle.setPointerCapture(event.pointerId);
    dividerRatio = dividerOrigin.ratio;
  }

  function dividerMove(event) {
    if (dividerOrigin === null || event.pointerId !== dividerPointerId) return;
    dividerRatio = overviewRatioFromDrag(dividerOrigin.ratio, event.clientX - dividerOrigin.x, dividerOrigin.span);
  }

  function dividerUp(event) {
    if (dividerOrigin === null || event.pointerId !== dividerPointerId) return;
    const ratio = dividerRatio;
    endDividerDrag();
    overviewLayout.setRatio(ratio);
  }

  // endDividerDrag drops the preview without committing -- the shared
  // path for a cancelled pointer, an Escape, leaving edit mode, and (once
  // it has read the ratio) a completed drag. Clearing dividerRatio hands
  // the lanes straight back to the saved value, so a cancel snaps to
  // where the split actually is rather than leaving the preview showing.
  function endDividerDrag() {
    if (dividerHandle && dividerPointerId !== null && dividerHandle.hasPointerCapture?.(dividerPointerId)) {
      dividerHandle.releasePointerCapture(dividerPointerId);
    }
    dividerRatio = null;
    dividerOrigin = null;
    dividerPointerId = null;
    dividerHandle = null;
  }

  // Keyboard: the divider is the page's only pointer-only affordance if
  // it isn't given one, so it takes the standard window-splitter keys --
  // arrows nudge a point at a time (Shift for five), Home/End go straight
  // to the clamps. Each press commits immediately; the store's own
  // debounce is what coalesces a held key into a single PUT.
  function dividerKeydown(event) {
    if (event.key === 'Escape') {
      endDividerDrag();
      return;
    }
    let next = null;
    if (event.key === 'ArrowLeft') next = nudgeOverviewRatio(laneRatio, event.shiftKey ? -5 : -1);
    else if (event.key === 'ArrowRight') next = nudgeOverviewRatio(laneRatio, event.shiftKey ? 5 : 1);
    else if (event.key === 'Home') next = OVERVIEW_RATIO_MIN;
    else if (event.key === 'End') next = OVERVIEW_RATIO_MAX;
    if (next === null) return;
    event.preventDefault();
    overviewLayout.setRatio(next);
  }

  // Escape cancels an in-flight gesture, the universal "never mind" for
  // one already under way -- a lifted module or a divider being dragged.
  // Bound only while one is actually running so the page has no stray
  // global key handler the rest of the time. It has to be a WINDOW
  // listener for the divider too: a pointerdown that preventDefault()s
  // never moves focus, so there is no focused element for the key to
  // reach (dividerKeydown above serves the keyboard-only path instead).
  $effect(() => {
    if (dragId === null && dividerRatio === null) return;
    const onKeydown = (e) => {
      if (e.key !== 'Escape') return;
      endDrag();
      endDividerDrag();
    };
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });
</script>

<!-- statusVisuals: the fleet strip + bay schematic pair, now the same
  full-width row in BOTH page states (see the counts-and-fleet pass in
  the top-of-file doc) -- only its wrapper's class differs, so which
  state the page is in stays readable in the DOM. Each sits in its own
  __visual-slot so the schematic's slot disappears with it (BaySchematic
  renders nothing for zero entries) rather than holding an empty half
  open, and so the stacked breakpoint can reorder them without either
  component knowing. -->
{#snippet statusVisuals()}
  <div class="overview__visual-slot overview__visual-slot--fleet">
    <FleetStrip containers={fleetContainers} {hostMemBytes} />
  </div>
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
        data-size={overviewModuleSize(layoutDoc, id)}
        data-adaptive={isAdaptivelySized(layoutDoc, id)}
        style={dragId === id ? `transform: translate3d(${dragDelta.x}px, ${dragDelta.y}px, 0)` : ''}
        animate:flip={{ duration: editing ? dragMotionMs : 0 }}
      >
        {#if editing}
          <div class="overview__module-tools">
            {#if isResizableOverviewModule(id)}
              <div class="overview__size-switcher" role="group" aria-label={`${overviewModuleLabel(id)} height`}>
                {#each OVERVIEW_SIZES as size (size)}
                  <button
                    type="button"
                    class="overview__size-btn"
                    class:overview__size-btn--active={overviewModuleSize(layoutDoc, id) === size}
                    aria-pressed={overviewModuleSize(layoutDoc, id) === size}
                    aria-label={`Set ${overviewModuleLabel(id)} to ${size}`}
                    title={`${size} — ${overviewModuleRows(id, size)} rows`}
                    onclick={() => overviewLayout.setSize(id, size)}
                  >
                    {SIZE_GLYPHS[size]}
                  </button>
                {/each}
              </div>
            {/if}
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
          {#each visibleEvents as event (event.ID)}
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
  {:else if id === 'storage'}
    <!-- The bay schematic, a rearrangeable module since the rail took
      its pinned place at the top of the page. BaySchematic renders
      nothing at all for an array with no filesystem-bearing members,
      which in edit mode would leave a draggable card with no body -- so
      the wrapper says what it is either way. -->
    <div class="card overview__storage">
      {#if baySchematicEntries.length > 0}
        <BaySchematic
          entries={baySchematicEntries}
          summary={closingLine}
          stateLine={arrayStateSentence}
          warmestLine={hottestSentence}
        />
      {:else}
        <span class="microlabel">Storage array</span>
        <p class="microlabel overview__storage-empty">No array members reporting yet.</p>
      {/if}
    </div>
  {/if}
{/snippet}

<!-- metricsRail: the four host tiles, PINNED at the very top of the page
  (Scott: "Move the disk storage section down so that the CPU/Mem/Net/IO
  metrics are at the top of the overview page"). It is deliberately no
  longer a Customize module -- see OVERVIEW_MODULES' own doc for the swap
  and what it costs a saved layout. As a full-width row its four tiles
  sit side by side rather than stacked, which is the only change to the
  rail itself. -->
{#snippet metricsRail()}
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
{/snippet}

<div class="overview">
  <h1 class="page-title">Overview</h1>
  <SourcesBanner sources={live.frame?.sources ?? {}} />

  {@render metricsRail()}

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
      <section class="overview__attention">
        <span class="microlabel">Needs a look</span>
        <div class="overview__chips">
          {#each chips as chip (chip.bucket)}
            <a class="overview__chip" href={chip.href} aria-label={chip.ariaLabel} data-chip={chip.bucket}>
              <span class="overview__chip-count tabular-nums" aria-hidden="true">{chip.count}</span>
              <span class="overview__chip-noun" aria-hidden="true">{chip.noun}</span>
            </a>
          {/each}
        </div>
      </section>
    {/if}
  </div>

  {#if overviewStatus.ok}
    <div class="overview__clear-band">
      {@render statusVisuals()}
    </div>
  {:else}
    <div class="overview__status-band">
      {@render statusVisuals()}
    </div>
  {/if}

  <div class="overview__modules-band" class:overview__modules-band--editing={editing}>
    {#if canCustomize}
      <div class="overview__modules-bar">
        {#if editing}
          <span class="microlabel overview__customize-hint">
            Drag a handle to move a module · S/M/L sets its height · the eye hides one · drag the divider to split the
            columns
          </span>
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

    <!-- The two lane flex factors ride as custom properties so a divider
      drag changes one attribute rather than re-rendering the band. The
      divider itself is absolutely positioned ON the boundary rather than
      being a third flex child: a real child would add a second flex gap
      that only existed in edit mode, so entering edit mode would shift
      both lanes sideways. -->
    <div
      class="overview__modules-lanes"
      style={`--lane-ratio:${laneRatio}; --lane-flex-wide:${laneFlex.wide}; --lane-flex-narrow:${laneFlex.narrow}`}
      bind:this={lanesEl}
    >
      {#if showWideLane}
        {@render moduleLane('wide', wideIds)}
      {/if}
      {#if showDivider}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -- a
          FOCUSABLE separator is the WAI-ARIA window-splitter widget
          (role=separator + tabindex=0 + aria-value*), which is exactly
          what this is; Svelte's rule only models the decorative,
          non-focusable separator and has no way to tell the two apart.
          Dropping to a plain interactive role would lose a real screen
          reader the one thing it most needs to hear about this control. -->
        <div
          class="overview__lane-divider"
          class:overview__lane-divider--active={dividerRatio !== null}
          role="separator"
          tabindex="0"
          aria-orientation="vertical"
          aria-label="Column split"
          aria-valuemin={Math.round(OVERVIEW_RATIO_MIN * 100)}
          aria-valuemax={Math.round(OVERVIEW_RATIO_MAX * 100)}
          aria-valuenow={laneRatioPct}
          aria-valuetext={`${laneRatioPct}% to the wide column`}
          data-ratio={laneRatio}
          onpointerdown={startDividerDrag}
          onpointermove={dividerMove}
          onpointerup={dividerUp}
          onpointercancel={endDividerDrag}
          onkeydown={dividerKeydown}
        >
          <span class="overview__lane-divider-grip" aria-hidden="true"></span>
        </div>
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
    position: relative; /* the divider is absolutely placed on the boundary */
    display: flex;
    align-items: flex-start;
    gap: var(--lane-gap);
  }
  .overview__modules-lane {
    position: relative; /* the drop indicator is absolutely placed in here */
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }
  /* The split: two grow factors against flex-basis 0, in the band's own
     original 1.6 : 1 notation (see overviewLaneFlex, which owns the
     arithmetic and the reason the narrow lane is pinned at 1 rather than
     the two summing to 1). The fallbacks ARE that original pair -- what
     renders for the instant before the saved document lands, and if the
     inline properties ever go missing.
     --lane-ratio is the same split as a plain fraction, carried
     separately because the divider is positioned by it rather than by a
     grow factor; --lane-gap is named because that offset has to know it
     too. */
  .overview__modules-lanes {
    --lane-gap: 1rem;
  }
  .overview__modules-wide {
    flex: var(--lane-flex-wide, 1.6) 1 0;
  }
  .overview__modules-narrow {
    flex: var(--lane-flex-narrow, 1) 1 0;
  }

  /* --- Column divider: invisible outside edit mode (it isn't rendered
     at all), and quiet inside it -- a hairline with a short grip, the
     same restraint the dashed module outlines take. Centred ON the
     boundary: the wide lane occupies its factor's share of everything
     but the gap, so the boundary's own centre is that plus half a gap.
     ------------------------------------------------------------- */
  .overview__lane-divider {
    position: absolute;
    top: 0;
    bottom: 0;
    left: calc((100% - var(--lane-gap)) * var(--lane-ratio, 0.615) + var(--lane-gap) / 2);
    z-index: 4;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    margin-left: -9px;
    border: none;
    background: transparent;
    cursor: col-resize;
    /* Without this a touch drag pans the page instead of moving the
       divider -- pointer capture alone doesn't stop the browser's own
       gesture (the module grip carries the same rule for the same
       reason). */
    touch-action: none;
  }
  .overview__lane-divider-grip {
    width: 3px;
    height: 100%;
    max-height: 6rem;
    border-radius: 3px;
    background: color-mix(in oklab, var(--accent) 35%, transparent);
    transition: background 120ms ease;
  }
  .overview__lane-divider:hover .overview__lane-divider-grip,
  .overview__lane-divider:focus-visible .overview__lane-divider-grip,
  .overview__lane-divider--active .overview__lane-divider-grip {
    background: var(--accent);
  }
  .overview__lane-divider:focus-visible {
    outline: 1px solid var(--accent);
    outline-offset: 1px;
    border-radius: 4px;
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
       The saved SPLIT deliberately does not: two full-width stacked
       lanes have no ratio to honour, so `flex: none` simply ignores the
       custom properties above. Only EDITING is desktop-only (see
       CUSTOMIZE_MEDIA), which is also why the divider can never appear
       down here -- but it is belt-and-braces'd off anyway, since a
       stacked band's "boundary" would be meaningless. */
    .overview__modules-lane {
      width: 100%;
      flex: none;
    }
    .overview__lane-divider {
      display: none;
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

  /* The tools STRADDLE each card's top edge rather than sitting inside
     its top-right corner, because every card in this band already uses
     that corner for something live: Top Consumers' "View all" link, and
     -- the one that forced this -- the rail's first tile, whose CPU
     percentage a toolbar parked there covers outright. Floating them
     into the gap above costs only that gap getting a little taller in
     edit mode (the two rules below), and covers nothing at all. */
  .overview__modules-band--editing {
    gap: 1.5rem;
  }
  .overview__modules-band--editing .overview__modules-lane {
    gap: 1.6rem;
  }
  .overview__module-tools {
    position: absolute;
    top: -14px;
    right: 10px;
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
  /* The height switcher: the Top Consumers metric switcher's own idiom
     (a segmented strip of tiny mono buttons, the selected one raised out
     of the trough) shrunk to fit the tools bar beside the grip and the
     eye. Only the two modules with an elastic body get one -- the rail's
     four fixed tiles have no height to choose (see OVERVIEW_MODULES). */
  .overview__size-switcher {
    display: inline-flex;
    gap: 2px;
    padding: 2px;
    margin-right: 2px;
    border-radius: 6px;
    background: var(--surface-soft);
  }
  .overview__size-btn {
    width: 20px;
    height: 20px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.62rem;
    line-height: 1;
    text-transform: uppercase;
    cursor: pointer;
  }
  .overview__size-btn:hover {
    color: var(--ink);
  }
  .overview__size-btn--active {
    background: var(--surface);
    color: var(--accent-strong);
    font-weight: 600;
    box-shadow: 0 1px 2px color-mix(in oklab, var(--ink) 12%, transparent);
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

  /* --- Visuals band: the fleet strip, at full page width. It was a
     two-slot row (fleet beside the bay schematic) until the schematic
     became a Customize module; the slot machinery stays because the
     band is still the thing that owns the fleet's own width, and one
     flex child at `flex: 1 1 0` spans it exactly.

     Two class names, one rule set: the band is the same row in both
     page states, and which state it is rendered in stays legible in the
     DOM rather than being erased into a single neutral name -- the
     all-clear specs read exactly that difference. ---- */
  .overview__clear-band,
  .overview__status-band {
    display: flex;
    align-items: flex-start;
    gap: 1rem;
  }
  .overview__visual-slot {
    min-width: 0;
    flex: 1 1 0;
  }
  @media (max-width: 63.9375rem) {
    .overview__clear-band,
    .overview__status-band {
      flex-direction: column;
    }
    .overview__visual-slot {
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

  /* --- Metrics rail: pinned at the top of the page now, so a ROW of
     four equal tiles rather than the narrow lane's stack. Each tile
     keeps its own label + value + sparkline; they just sit beside each
     other. Below the app's usual mobile split the row would give each
     tile ~90px, which its paired value+sparkline cannot use, so it
     folds to two-up and then to the original stack. ------------- */
  .overview__metrics-rail {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 0.6rem 1.6rem;
    min-width: 0;
    padding: 1rem 1.2rem;
  }
  /* StatTile's bare mode draws a hairline UNDER each tile -- the seam
     between rows of a stack. Side by side that reads as four
     underlines, so the row takes the seam off and lets the column gap
     do the separating. The tiles' own sparkline height is untouched:
     96px is a deliberate number ("not tall enough for the graphs to
     look good" at the previous 52), and a wider tile is no reason to
     take it back. */
  .overview__metrics-rail :global(.stat-tile--bare) {
    padding: 0;
    border-bottom: none;
  }
  /* And its label/value row stops pushing the two to opposite ends.
     Across a full-width rail that put each tile's VALUE hard against
     the next tile's LABEL -- "15.9%  MEMORY" reads as one pair and the
     column it belongs to is anyone's guess. Grouped at the left, each
     tile is unambiguously its own. */
  .overview__metrics-rail :global(.stat-tile__row) {
    justify-content: flex-start;
    gap: 0.6rem;
  }
  @media (max-width: 63.9375rem) {
    .overview__metrics-rail {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
  @media (max-width: 30rem) {
    .overview__metrics-rail {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  /* --- Storage: the bay schematic's own module wrapper. The schematic
     brings its whole card body (head, facts, device grid, hover label);
     this only exists to give the module a card to be dragged by, and to
     say what it is on an array that has nothing to draw yet. ------ */
  .overview__storage {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    min-width: 0;
    padding: 1.2rem;
  }
  .overview__storage-empty {
    margin: 0;
  }

  /* --- Attention: plain content, connected to the headline by
     PROXIMITY alone -- it's just the next thing in the headline-zone's
     own flex column, spaced by that column's own gap, same as every
     other subline above it. No frame, no brackets, no leader line: the
     one rule surviving the corrective pass is that a line either
     separates two real regions or encodes real data.

     It is a ROW now, not a column of rows: the section holds at most
     two count chips (the counts pass -- see lib/attentionCounts.ts), so
     stacking them would spend a whole card's height on two numbers. The
     label sits inline at the front, and the chips wrap under it on a
     narrow screen. ------------------------------------------------- */

  .overview__attention {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.55rem 0.9rem;
    padding: 0.7rem 1rem;
    border-radius: 11px;
    background: color-mix(in oklab, var(--status-warning) 8%, var(--surface-muted));
    border: 1px solid color-mix(in oklab, var(--status-warning) 20%, var(--border));
  }
  .overview__chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  /* One chip = one number you can press. The COUNT is the affordance --
     "click on the number and then be brought to a list of items that
     need attention" -- so it carries the weight and the noun beside it
     stays a quiet gloss; the whole chip is the hit target and the whole
     sentence (including where it goes) is in its aria-label, because
     "3 alerts" alone tells a screen reader nothing about activating it. */
  .overview__chip {
    display: inline-flex;
    align-items: baseline;
    gap: 0.4rem;
    min-height: 34px;
    padding: 0.15rem 0.75rem;
    border: 1px solid color-mix(in oklab, var(--status-warning) 32%, var(--border));
    border-radius: 999px;
    background: var(--surface);
    color: var(--ink);
    text-decoration: none;
    transition:
      border-color 120ms ease,
      background 120ms ease;
  }
  .overview__chip:hover,
  .overview__chip:focus-visible {
    border-color: var(--accent);
    background: var(--surface-soft);
  }
  .overview__chip-count {
    font-family: var(--font-display);
    font-size: 1.35rem;
    font-weight: 700;
    line-height: 1;
    letter-spacing: -0.03em;
  }
  .overview__chip-noun {
    color: var(--ink-2);
    font-size: 0.82rem;
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

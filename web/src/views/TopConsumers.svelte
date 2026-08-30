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
  import { appendAfterSeed, mergeSeed, pushRing, seriesPointsToRing } from '../lib/livering';
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
    resourceMetricKeys,
    resourceScaleMax,
    TOP_RESOURCES,
    topFromFrame,
    unattributedValue,
  } from '../lib/topFromFrame';
  import { createRankStabilityState, stableTopN } from '../lib/rankStability';
  import TopBarList from '../components/TopBarList.svelte';
  import TimeChart from '../components/TimeChart.svelte';
  import CoreBudgetRibbon from '../components/CoreBudgetRibbon.svelte';
  import ContainerIcon from '../components/ContainerIcon.svelte';

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

  // MAX_HERO_LINES: the hero chart's own top-N cut ("Instead of showing
  // the containers all together in a horizontal bar... show them as
  // lines on the main line graph with different colors"), independent of
  // COMPLETE_LIST_LIMIT above -- the ranked list below stays complete,
  // only the CHART bounds itself, both because a legend/line count much
  // past this stops being readable and because Now mode backs every
  // line with its own live ring (heroSlots below) and a fetched window
  // backs every line with its own /api/series request -- 10 is exactly
  // the categorical --series-1..10 palette's own size (tokens.css), so
  // every line gets a real, distinct hue with nothing left to bucket
  // into an "Other."
  const MAX_HERO_LINES = 10;

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
  // sees this. This is the raw, per-tick instant ranking -- listRankState/
  // heroRankState below each stabilize their OWN top-N cut of it
  // (rankStability.ts): the complete list's own membership never
  // actually excludes anyone (its limit is COMPLETE_LIST_LIMIT), but its
  // ORDER still needs the same rolling-average/cadence treatment the
  // hero selection's membership does, so both read off this one shared,
  // unlimited computation.
  let nowRows = $derived(topFromFrame(live.frame, resource, COMPLETE_LIST_LIMIT, { direction: isDirectional }));
  const listRankState = createRankStabilityState();
  const heroRankState = createRankStabilityState();
  let stableNowRows = $derived(
    stableTopN(nowRows, listRankState, resource, COMPLETE_LIST_LIMIT, live.frame?.ts ?? 0),
  );

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

  // hostReferencePoints is the hero chart's own muted reference line --
  // the SAME host-level rings/fetched totals the old single-host-line
  // chart used, just read as one combined [ts,val][] series regardless
  // of window: net/io sum their own down+up (or read+write) rings
  // point-by-point (sumSeriesPoints, the same helper the fetched-window
  // host total already summed its two component fetches with above).
  // gpu is deliberately absent (falls through to []) -- see this file's
  // own module doc for why gpu never gets a host total at all.
  function hostReferencePoints() {
    if (windowKey === 'now') {
      switch (resource) {
        case 'cpu':
          return cpuTotalRing.points;
        case 'mem':
          return memUsedRing.points;
        case 'net':
          return sumSeriesPoints([netRxRing.points, netTxRing.points]);
        case 'io':
          return sumSeriesPoints([ioReadRing.points, ioWriteRing.points]);
        default:
          return [];
      }
    }
    if (!fetchedHostTotal) return [];
    return fetchedHostTotal.points2 ? sumSeriesPoints([fetchedHostTotal.points, fetchedHostTotal.points2]) : fetchedHostTotal.points;
  }

  // --- Hero chart: top containers as lines, not bars ----------------------
  //
  // "Instead of showing the containers all together in a horizontal
  // bar for their consumption, show them as lines on the main line
  // graph with different colors. This same behavior should be done for
  // each metric type." The ranked bars below (TopBarList) stay exactly
  // as they were -- they're this chart's own legend data, not replaced
  // by it.
  //
  // heroSlots: MAX_HERO_LINES fixed ring buffers for Now mode, one PER
  // RANK SLOT rather than per container -- rings can't be created
  // dynamically for an arbitrary, changing set of container names (see
  // liveRing's own "call once per metric from a component's own
  // top-level script" contract); a fixed pool sized to the chart's own
  // cap, read by rank each tick, is the same shape the header rings
  // above already use ("created unconditionally... which ones actually
  // get READ is a display-time decision"). A slot's points reset the
  // instant its assigned entity changes -- otherwise a reassignment
  // (one container falling out of the top-N as another climbs in) would
  // paste one container's history directly onto another's, reading as
  // an impossible instant jump.
  function makeHeroSlot() {
    let sum = $state([]);
    let dirA = $state([]);
    let dirB = $state([]);
    let assigned = null;
    // seeded gates tick()'s own append rule exactly like liveRing's own
    // field of the same name (livering.svelte.ts) -- false until seed()
    // below actually applies real history for whichever entity is
    // assigned RIGHT NOW; a plain closure variable, not $state, for the
    // identical reason theirs is (nothing needs to react to it changing
    // on its own, only the ring writes that accompany it).
    let seeded = false;
    return {
      get points() {
        return sum;
      },
      get directionPoints() {
        return [dirA, dirB];
      },
      // resetAssignment blanks this slot back to its pre-mount state --
      // called on a resource/window switch (see that effect's own doc,
      // below) even when the entity NAME happens not to change, because
      // tick()'s own equality check alone can't tell "still the same
      // container" apart from "same name, completely different resource"
      // -- e.g. one container topping both CPU and Memory would otherwise
      // carry cpu.pct values straight over as if they were mem.bytes the
      // instant the tab switched.
      resetAssignment() {
        assigned = null;
        sum = [];
        dirA = [];
        dirB = [];
        seeded = false;
      },
      // seed folds a /api/series ring-tier fetch in as this slot's own
      // initial contents, for whichever entity is assigned right now --
      // mergeSeed's usual base-plus-already-live-held merge (see its own
      // doc), applied to sum and, when given, each direction component. A
      // stale response for an entity this slot has since moved on from is
      // seedHeroSlot's own job to have already aborted; this method just
      // trusts whatever it's handed.
      seed(sumPoints, dirAPoints = [], dirBPoints = []) {
        const heldSum = untrack(() => sum);
        const mergedSum = mergeSeed(heldSum, sumPoints, LIVE_WINDOW_SEC);
        if (mergedSum === heldSum) return; // empty/no-op seed -- see mergeSeed's own doc
        seeded = true;
        sum = mergedSum;
        if (dirAPoints.length > 0) dirA = mergeSeed(untrack(() => dirA), dirAPoints, LIVE_WINDOW_SEC);
        if (dirBPoints.length > 0) dirB = mergeSeed(untrack(() => dirB), dirBPoints, LIVE_WINDOW_SEC);
      },
      // tick's own reassignment reset also clears `seeded` (a fresh
      // entity has no seed yet, no matter what the previous one had) and
      // reports whether THIS call is the one that assigned a real
      // (non-null) entity -- the driving $effect below uses that to fire
      // this slot's own seed fetch exactly once per assignment, covering
      // a brand new mount (every slot starts unassigned) and a
      // rank-stability entry mid-view alike, with no separate "on mount"
      // path needed.
      tick(ts, entity, value, a, b) {
        let justAssigned = false;
        if (entity !== assigned) {
          assigned = entity;
          sum = [];
          dirA = [];
          dirB = [];
          seeded = false;
          justAssigned = entity !== null;
        }
        if (entity === null || value === undefined) return justAssigned;
        // untrack: tick() runs from inside the driving $effect below, which
        // must depend on live.frame/heroTopNow ONLY -- reading sum/dirA/dirB
        // here (as pushRing's own first argument) would otherwise ALSO
        // register them as that effect's dependencies, and the write right
        // back to them on the next line would then re-dirty it, forever
        // (effect_update_depth_exceeded, reproduced live) -- the exact
        // self-referential loop livering.svelte.ts's own liveRing() already
        // documents and untracks for the identical reason.
        sum = untrack(() =>
          seeded ? appendAfterSeed(sum, ts, value, LIVE_WINDOW_SEC) : pushRing(sum, ts, value, LIVE_WINDOW_SEC),
        );
        if (a !== undefined) {
          dirA = untrack(() =>
            seeded ? appendAfterSeed(dirA, ts, a, LIVE_WINDOW_SEC) : pushRing(dirA, ts, a, LIVE_WINDOW_SEC),
          );
        }
        if (b !== undefined) {
          dirB = untrack(() =>
            seeded ? appendAfterSeed(dirB, ts, b, LIVE_WINDOW_SEC) : pushRing(dirB, ts, b, LIVE_WINDOW_SEC),
          );
        }
        return justAssigned;
      },
    };
  }
  const heroSlots = Array.from({ length: MAX_HERO_LINES }, () => makeHeroSlot());

  // heroSeedControllers: one AbortController per hero slot's own
  // in-flight seed fetch (Map, slot index -> controller) -- ad hoc rather
  // than one controller per effect, because a slot's own seed fires from
  // inside the driving tick effect below whenever THAT slot's entity is
  // (re)assigned, not from a single per-resource effect the way the
  // header rings' own seeding above is. abortHeroSeed cancels slot i's
  // own pending fetch, if any -- called before starting a fresh one for
  // the same slot (a second reassignment landing before the first fetch
  // resolved must not let the stale one win) and by the reset effect
  // below (a resource/window switch invalidates every slot's fetch at
  // once).
  const heroSeedControllers = new Map();
  function abortHeroSeed(i) {
    heroSeedControllers.get(i)?.abort();
    heroSeedControllers.delete(i);
  }
  onMount(() => {
    return () => {
      for (let i = 0; i < MAX_HERO_LINES; i++) abortHeroSeed(i);
    };
  });

  // seedHeroSlot fetches hero slot i's newly-assigned entity's own
  // ring-tier history (last LIVE_WINDOW_SEC seconds -- the same window
  // every other live ring on this page bounds itself to) and folds it in
  // as that slot's own seed. Same kind/metrics shape as fetchedHeroSeries
  // below, just against the ring tier instead of a fetched range, and
  // sums resourceMetricKeys(res) the SAME way tick()'s own live math
  // already does (topFromFrame's sumPresentMetrics) -- for a directional
  // resource, each component is seeded on its own alongside their sum, so
  // the seed matches exactly what heroSeries plots for both the combined
  // line and its direction breakdown.
  function seedHeroSlot(i, entity, res) {
    abortHeroSeed(i);
    const controller = new AbortController();
    heroSeedControllers.set(i, controller);
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    const metrics = resourceMetricKeys(res);
    fetchSeries({ kind: 'container', entity, metrics, from, to, signal: controller.signal })
      .then((results) => {
        heroSeedControllers.delete(i);
        const byMetric = {};
        for (const r of results) byMetric[r.metric] = r.points;
        const dirKeys = resourceDirectionKeys(res);
        if (dirKeys) {
          const ptsA = byMetric[dirKeys[0]] ?? [];
          const ptsB = byMetric[dirKeys[1]] ?? [];
          heroSlots[i].seed(sumSeriesPoints([ptsA, ptsB]), seriesPointsToRing(ptsA), seriesPointsToRing(ptsB));
        } else {
          heroSlots[i].seed(sumSeriesPoints(metrics.map((m) => byMetric[m] ?? [])));
        }
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded -- a reset or a fresh reassignment beat this fetch back
        heroSeedControllers.delete(i);
      });
  }

  // heroTopNow: the stable top-MAX_HERO_LINES entries of nowRows -- its
  // own rankStability selection, not merely the ranked list's own naive
  // slice: each hero SLOT (heroSlots above) resets its whole ring the
  // instant its assigned entity changes, so a chart line churning at the
  // same rate the leaderboard USED to would repeatedly blank a line back
  // to empty instead of drawing a real trend, an even worse symptom than
  // the leaderboard's own hard-swap.
  let heroTopNow = $derived(stableTopN(nowRows, heroRankState, resource, MAX_HERO_LINES, live.frame?.ts ?? 0));

  $effect(() => {
    if (windowKey !== 'now') return;
    const frame = live.frame;
    if (!frame) return;
    const top = heroTopNow;
    const r = resource;
    for (let i = 0; i < MAX_HERO_LINES; i++) {
      const row = top[i];
      const entity = row?.entity ?? null;
      const justAssigned = heroSlots[i].tick(frame.ts, entity, row?.value, row?.direction?.[0], row?.direction?.[1]);
      if (justAssigned) seedHeroSlot(i, entity, r);
    }
  });

  // fetchedHeroSeries (history windows only): one /api/series call per
  // top-MAX_HERO_LINES entity, chained off fetchedRows (so it always
  // fetches for the SAME ranking the list below just resolved, never a
  // stale or a not-yet-ranked set) rather than duplicating fetchTop's
  // own ranking logic. Aborts and re-fires on every fetchedRows change,
  // same stale-response guard as the rows/host-total effect above.
  let fetchedHeroSeries = $state([]);

  $effect(() => {
    if (windowKey === 'now') {
      fetchedHeroSeries = [];
      return;
    }
    const entities = fetchedRows.slice(0, MAX_HERO_LINES).map((r) => r.entity);
    if (entities.length === 0) {
      fetchedHeroSeries = [];
      return;
    }
    const seconds = { '1h': 3600, '24h': 86400, '7d': 7 * 86400 }[windowKey];
    const to = Math.floor(Date.now() / 1000);
    const from = to - seconds;
    const metrics = resourceMetricKeys(resource);
    const controller = new AbortController();
    Promise.all(
      entities.map((entity) =>
        fetchSeries({ kind: 'container', entity, metrics, from, to, signal: controller.signal }).then((results) => ({ entity, results })),
      ),
    )
      .then((perEntity) => {
        fetchedHeroSeries = perEntity;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer fetchedRows
        fetchedHeroSeries = [];
      });
    return () => controller.abort();
  });

  // heroSeries unifies both windows into the one shape TimeChart/the
  // legend chips read: {entity, label, points, colorVar, width?, dash?,
  // directionPoints?, directionLabels?}. entity is null only for the
  // trailing host-reference line (its own chip renders "Host total"
  // with no ContainerIcon). Colors are assigned by CHART POSITION
  // (index), not by entity identity -- same "color follows the
  // leaderboard's own rank, not a stable per-entity hash" rule
  // TopBarList's bars already use, since a categorical hue is only
  // meaningful for as long as an entity holds a top-N slot at all.
  let heroSeries = $derived.by(() => {
    const rowDirLabels = ROW_DIRECTION_LABELS[resource];
    let lines;
    if (windowKey === 'now') {
      lines = heroTopNow.map((row, i) => ({
        entity: row.entity,
        label: row.entity,
        points: heroSlots[i].points,
        colorVar: `--series-${i + 1}`,
        ...(isDirectional ? { directionPoints: heroSlots[i].directionPoints, directionLabels: rowDirLabels } : {}),
      }));
    } else {
      const [keyA, keyB] = isDirectional ? resourceMetricKeys(resource) : [];
      lines = fetchedHeroSeries.map((entry, i) => {
        const byMetric = {};
        for (const r of entry.results) byMetric[r.metric] = r.points;
        if (isDirectional) {
          const ptsA = byMetric[keyA] ?? [];
          const ptsB = byMetric[keyB] ?? [];
          return {
            entity: entry.entity,
            label: entry.entity,
            points: sumSeriesPoints([ptsA, ptsB]),
            colorVar: `--series-${i + 1}`,
            directionPoints: [ptsA, ptsB],
            directionLabels: rowDirLabels,
          };
        }
        return { entity: entry.entity, label: entry.entity, points: byMetric[resourceMetricKeys(resource)[0]] ?? [], colorVar: `--series-${i + 1}` };
      });
    }
    if (hasHostTotal) {
      const hostPoints = hostReferencePoints();
      if (hostPoints.length > 0) {
        lines = [...lines, { entity: null, label: 'Host total', points: hostPoints, colorVar: '--ink-2', width: 1, dash: [4, 4] }];
      }
    }
    return lines;
  });

  let heroChart = $state(undefined);
  let hiddenHeroIdx = $state(new Set());

  // A resource/window switch is a new chart context -- any per-line
  // hide from the OLD one shouldn't silently carry over onto whatever
  // now occupies that same chip position.
  $effect(() => {
    resource;
    windowKey;
    hiddenHeroIdx = new Set();
  });

  // Same "new chart context" trigger, for the hero rings themselves:
  // every slot forgets its current assignment so the very next tick()
  // call (above) always sees a "new" entity -- even one it already had
  // under the OLD resource -- and reseeds it fresh via seedHeroSlot,
  // rather than risking the cross-resource collision resetAssignment's
  // own doc describes. Also cancels whatever seed fetch was still
  // in-flight for the resource/window we just left.
  $effect(() => {
    resource;
    windowKey;
    for (let i = 0; i < MAX_HERO_LINES; i++) {
      abortHeroSeed(i);
      heroSlots[i].resetAssignment();
    }
  });

  function toggleHeroLine(i) {
    const next = new Set(hiddenHeroIdx);
    if (next.has(i)) next.delete(i);
    else next.add(i);
    hiddenHeroIdx = next;
    heroChart?.toggleSeries(i + 1);
  }

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

  let containerRows = $derived(windowKey === 'now' ? stableNowRows : fetchedRows);

  // heroCapped: whether the ranked list actually has MORE entities than
  // the chart is showing -- drives the quiet "showing the top N" note,
  // never shown for a small fleet where the cap never actually bound
  // anything.
  let heroCapped = $derived(containerRows.length > MAX_HERO_LINES);

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
  <h1 class="page-title">Metrics</h1>

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

  {#if (hasHostTotal && hostTotal) || heroSeries.length > 0}
    <div class="card top-consumers__header">
      {#if hasHostTotal && hostTotal}
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
      {:else}
        <span class="microlabel">{TOP_RESOURCES.find((r) => r.key === resource)?.label} &middot; per container</span>
      {/if}
      {#if scaleCeilingLabel}
        <p class="microlabel top-consumers__scale">{scaleCeilingLabel}</p>
      {/if}
      {#if showRibbon}
        <CoreBudgetRibbon {hostCores} segments={coreBudget.segments} freeCores={coreBudget.freeCores} icons={coreBudgetIcons} />
      {/if}
      {#if heroSeries.length > 0}
        <TimeChart
          bind:this={heroChart}
          series={heroSeries}
          formatValue={FORMATTERS[resource]}
          live={windowKey === 'now'}
          height={260}
          showLegend={false}
        />
        <div class="top-consumers__legend" role="group" aria-label="Chart lines">
          {#each heroSeries as s, i (s.entity ?? '__host')}
            <button
              type="button"
              class="top-consumers__chip"
              class:top-consumers__chip--off={hiddenHeroIdx.has(i)}
              style="--chip-color: var({s.colorVar})"
              aria-pressed={!hiddenHeroIdx.has(i)}
              aria-label={`${s.entity ?? 'Host total'} line, click to ${hiddenHeroIdx.has(i) ? 'show' : 'hide'}`}
              onmouseenter={() => heroChart?.focusSeries(i + 1)}
              onmouseleave={() => heroChart?.focusSeries(null)}
              onclick={() => toggleHeroLine(i)}
            >
              {#if s.entity}
                <ContainerIcon name={s.entity} icon={coreBudgetIcons[s.entity]} size={14} />
              {:else}
                <span class="top-consumers__chip-swatch"></span>
              {/if}
              <span>{s.entity ?? 'Host total'}</span>
            </button>
          {/each}
        </div>
        {#if heroCapped}
          <p class="microlabel top-consumers__cap-note">Chart shows the top {MAX_HERO_LINES} by usage &mdash; the full list continues below.</p>
        {/if}
      {/if}
    </div>
  {/if}

  <div class="card top-consumers__panel">
    <span class="microlabel top-consumers__panel-label">Top Consumers</span>
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
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .top-consumers__panel-label {
    margin: 0;
  }
  .top-consumers__error {
    margin: 0;
    color: var(--status-warning);
  }
  .top-consumers__loading {
    margin: 0;
  }
  /* Legend chips: the hero chart's own external legend (uPlot's built-in
     one is off, showLegend={false} -- see TimeChart's own doc), tied to
     the SAME top-N list the ranked bars below read. Chip order matches
     the chart's own series order (rank, then the host-total line last),
     not a name sort -- rank IS the chip row's own meaning here. */
  .top-consumers__legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .top-consumers__chip {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.25rem 0.55rem;
    border: 1px solid color-mix(in oklab, var(--chip-color) 45%, transparent);
    border-radius: 999px;
    background: color-mix(in oklab, var(--chip-color) 12%, transparent);
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 0.72rem;
    cursor: pointer;
    transition:
      opacity 150ms ease,
      background-color 150ms ease;
  }
  .top-consumers__chip:hover {
    background: color-mix(in oklab, var(--chip-color) 22%, transparent);
  }
  /* A toggled-off line's chip stays legible (never the dashed-out,
     near-invisible treatment a disabled control would get) -- it's a
     live, click-to-restore choice, not an unavailable option. */
  .top-consumers__chip--off {
    opacity: 0.45;
  }
  .top-consumers__chip-swatch {
    display: inline-block;
    width: 10px;
    height: 2px;
    border-top: 2px dashed var(--chip-color);
    flex-shrink: 0;
  }
  .top-consumers__cap-note {
    margin: 0;
  }
</style>

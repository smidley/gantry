<!--
  Storage: the array/disk detail view. A header chart (IO/Used/Temp per
  drive, see its own doc below), disk grid (one card per disk entity,
  grouped parity -> data -> cache/pools -> flash), a parity card (state
  + progress/speed/ETA + a short start/finish history), a mover chip, a
  shares table, and a docker storage card -- all read straight off the
  live SSE frame except the parity history, which isn't in the frame at
  all (events never are -- see sse.svelte.ts's own doc) and is polled
  the same way Overview polls its events feed.
-->
<script>
  import { onMount, untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { motion } from '../lib/motion.svelte';
  import { live } from '../lib/sse.svelte';
  import { fetchEvents, fetchSeries } from '../lib/api';
  import { appendAfterSeed, mergeSeed, pushRing, seriesPointsToRing } from '../lib/livering';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate, fmtRelTime } from '../lib/format';
  import { etaFromProgress, parityIsRunning, seqStep, sharesFromMetrics, sumSeriesPoints } from '../lib/metrics';
  import { seriesColorVar } from '../lib/compareColors';
  import {
    diskChartDash,
    diskKind,
    diskRole,
    diskTempState,
    diskUsagePct,
    diskUsagePctSeries,
    sortDiskEntities,
  } from '../lib/disks';
  import { band, bandToken } from '../lib/thresholds';
  import HealthDot from '../components/HealthDot.svelte';
  import TimeChart from '../components/TimeChart.svelte';

  const EVENTS_POLL_MS = 30_000;
  const ROLE_LABEL = { parity: 'Parity', data: 'Data disk', pool: 'Cache / pool', flash: 'Boot (flash)' };
  // Four-way type badge (Scott's own report: a real box's boot flash
  // device was misread as HDD and its NVMe pools as generic SSD --
  // rotational alone can't tell either apart, see disks.ts's diskKind
  // doc). Color identity lives in storage-disk__media--<kind> below, one
  // per kind except hdd (deliberately left the plain neutral chip
  // color -- the ordinary/majority case, not one that needs to stand out).
  const MEDIA_LABEL = { hdd: 'HDD', ssd: 'SSD', nvme: 'NVMe', usb: 'USB' };
  const MEDIA_TITLE = { hdd: 'Spinning disk', ssd: 'Solid state', nvme: 'NVMe solid state', usb: 'USB flash drive' };

  let disks = $derived(live.frame?.disks ?? {});
  let diskMeta = $derived(live.frame?.disk_meta ?? {});
  let diskNames = $derived(sortDiskEntities(Object.keys(disks)));
  let array = $derived(live.frame?.unraid?.array ?? {});
  let dockerStorage = $derived(live.frame?.unraid?.docker ?? {});
  let sources = $derived(live.frame?.sources ?? {});
  let ts = $derived(live.frame?.ts ?? 0);

  // --- Header chart: IO/Used/Temp per drive --------------------------
  //
  // "Have a graph that can switch between disk IO, storage used, and
  // temperature. Each line on the chart should be a separate drive."
  // Reuses the Metrics page's own multi-line hero pattern (TopConsumers.
  // svelte: legend chips, focus-on-hover, click-to-toggle) plus its own
  // live-seed history fix (heroLines' seed/prune, mirrored below by
  // makeDiskSlot) -- every drive is visible from mount (Scott's own
  // follow-up ask: no default-hidden set, "one /api/series per drive
  // per metric on the ring tier is cheap"), each with its own categorical
  // line color by SLOT POSITION (seriesColorVar) -- unlike the Metrics
  // hero chart and Compare (containerColor's own per-name hash, since a
  // container can leave and re-enter a ranking), a disk BAY'S slot IS
  // the stable identity here: it keeps the same position for the life of
  // the array regardless of which physical drive currently occupies it,
  // so position-based color stays correct. Not the kind tint the legend
  // used to draw its whole chip in -- the kind tint is demoted to a
  // small accent dot on the chip instead (diskKind's own ssd/nvme/usb/
  // hdd read), since a categorical hue is the thing that actually tells
  // two same-kind drives (disk1 vs disk2) apart, while kind is still
  // worth a quiet secondary glance.
  //
  // diskio has no per-disk-ENTITY series of its own (host.go's real
  // per-device counters, and fake.go's own mirror of that shape, are
  // both host-scoped and keyed by raw device name -- see fake.go's own
  // emitDisks doc) -- ioForSlot/seedDiskSlot below join back to a slot via
  // disk_meta[slot].device, the same join any other real-mode consumer
  // of this data would need.
  const CHART_METRICS = [
    { key: 'io', label: 'IO' },
    { key: 'used', label: 'Used' },
    { key: 'temp', label: 'Temp' },
  ];
  const CHART_WINDOWS = [
    { key: 'now', label: 'Now' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
  ];
  const CHART_WINDOW_SECONDS = { '1h': 3600, '24h': 86400, '7d': 7 * 86400 };
  const CHART_FORMATTERS = { io: fmtRate, used: fmtPct, temp: (v) => `${v.toFixed(1)}°C` };
  const CHART_LIVE_WINDOW_SEC = 900;
  // KIND_COLOR_VAR mirrors BaySchematic/Storage's own per-kind mapping
  // exactly (ssd/nvme/usb get an accent color, hdd -- the ordinary/
  // majority case -- gets no override there). No longer the LINE's own
  // color (see the header chart doc above) -- kept as the legend chip's
  // small kind-accent dot instead, hdd's plain --ink-2 reading as "no
  // accent" there too, same "calm, not a rainbow" precedent.
  const KIND_COLOR_VAR = { ssd: '--series-3', nvme: '--series-1', usb: '--series-4' };
  function kindColorVar(kind) {
    return KIND_COLOR_VAR[kind] ?? '--ink-2';
  }

  let chartMetric = $state('io');
  let chartWindow = $state('now');
  let chartInstance = $state(undefined);

  // hiddenSlots: every drive starts visible (Scott's own follow-up ask --
  // no default-hidden set at all, pools/parity/"has recent IO" special-
  // casing dropped entirely). A plain empty Set, not the null-sentinel
  // this used to need to distinguish "haven't decided defaults yet" from
  // "decided, nothing's hidden" -- there's no decision left to defer.
  let hiddenSlots = $state(new Set());

  // MAX_CHART_DISKS: a fixed ring pool, sized well above any array this
  // app is likely to see (Scott's own "12 members" plus headroom) --
  // rings can't be created dynamically per disk slot (same "call
  // $state/$effect from a component's own synchronous setup" contract
  // livering.svelte.ts's liveRing() documents); a fixed pool assigned by
  // SORTED POSITION, reset on reassignment, is the same shape
  // TopConsumers' own heroSlots use for its (far more volatile) top-N
  // ranking.
  const MAX_CHART_DISKS = 32;
  function makeDiskSlot() {
    let ioSum = $state([]);
    let ioA = $state([]);
    let ioB = $state([]);
    let used = $state([]);
    let temp = $state([]);
    let assigned = null;
    // Three independent seeded flags, not heroSlot's single one -- io/
    // used/temp are three separate TAB VIEWS a caller switches between
    // (this chart's own CHART_METRICS switcher), not one resource's own
    // sum-plus-direction-breakdown the way heroSlot's sum/dirA/dirB
    // always travel together. Each gates only its own group's tick()
    // append rule, exactly like heroSlot's `seeded` gates its one group.
    let ioSeeded = false;
    let usedSeeded = false;
    let tempSeeded = false;
    return {
      get ioPoints() {
        return ioSum;
      },
      get ioDirection() {
        return [ioA, ioB];
      },
      get usedPoints() {
        return used;
      },
      get tempPoints() {
        return temp;
      },
      // seededFor reports whether THIS slot has already been seeded for
      // `metric` -- the driving seed effect below reads this to skip a
      // drive/metric combo that's already covered, so a metric-tab
      // switch back to one already seeded earlier doesn't refire a
      // redundant fetch, and a toggle-OFF/toggle-ON of an UNRELATED
      // drive doesn't either.
      seededFor(metric) {
        if (metric === 'io') return ioSeeded;
        if (metric === 'used') return usedSeeded;
        return tempSeeded;
      },
      // resetAssignment blanks this slot back to its pre-mount state --
      // mirrors heroSlot's own (TopConsumers.svelte), for the same
      // reason: nothing here actually calls it today (disk-slot
      // reassignment is array-topology-change-rare, not the constant
      // top-N churn heroSlot resets for), but tick()'s own reassignment
      // branch below needs the identical "blank everything, including
      // every seeded flag" logic, so it's factored out once rather than
      // duplicated between the two call sites.
      resetAssignment() {
        assigned = null;
        ioSum = [];
        ioA = [];
        ioB = [];
        used = [];
        temp = [];
        ioSeeded = false;
        usedSeeded = false;
        tempSeeded = false;
      },
      // seedIO/seedUsed/seedTemp fold one /api/series ring-tier fetch in
      // as this slot's own initial contents for that one group --
      // mergeSeed's usual base-plus-already-live-held merge (livering.ts's
      // own doc), same shape as heroSlot's single seed() method, just
      // one per independent group instead of one covering all of them.
      seedIO(sumPoints, aPoints, bPoints) {
        const heldSum = untrack(() => ioSum);
        const merged = mergeSeed(heldSum, sumPoints, CHART_LIVE_WINDOW_SEC);
        if (merged === heldSum) return; // empty/no-op seed -- see mergeSeed's own doc
        ioSeeded = true;
        ioSum = merged;
        ioA = mergeSeed(untrack(() => ioA), aPoints, CHART_LIVE_WINDOW_SEC);
        ioB = mergeSeed(untrack(() => ioB), bPoints, CHART_LIVE_WINDOW_SEC);
      },
      seedUsed(points) {
        const held = untrack(() => used);
        const merged = mergeSeed(held, points, CHART_LIVE_WINDOW_SEC);
        if (merged === held) return;
        usedSeeded = true;
        used = merged;
      },
      seedTemp(points) {
        const held = untrack(() => temp);
        const merged = mergeSeed(held, points, CHART_LIVE_WINDOW_SEC);
        if (merged === held) return;
        tempSeeded = true;
        temp = merged;
      },
      tick(tickTs, slot, ioRead, ioWrite, usedPct, tempC) {
        if (slot !== assigned) {
          assigned = slot;
          ioSum = [];
          ioA = [];
          ioB = [];
          used = [];
          temp = [];
          ioSeeded = false;
          usedSeeded = false;
          tempSeeded = false;
        }
        if (slot === null) return;
        if (ioRead !== undefined || ioWrite !== undefined) {
          const r = ioRead ?? 0;
          const w = ioWrite ?? 0;
          ioSum = untrack(() =>
            ioSeeded ? appendAfterSeed(ioSum, tickTs, r + w, CHART_LIVE_WINDOW_SEC) : pushRing(ioSum, tickTs, r + w, CHART_LIVE_WINDOW_SEC),
          );
          ioA = untrack(() =>
            ioSeeded ? appendAfterSeed(ioA, tickTs, r, CHART_LIVE_WINDOW_SEC) : pushRing(ioA, tickTs, r, CHART_LIVE_WINDOW_SEC),
          );
          ioB = untrack(() =>
            ioSeeded ? appendAfterSeed(ioB, tickTs, w, CHART_LIVE_WINDOW_SEC) : pushRing(ioB, tickTs, w, CHART_LIVE_WINDOW_SEC),
          );
        }
        if (usedPct !== undefined) {
          used = untrack(() =>
            usedSeeded ? appendAfterSeed(used, tickTs, usedPct, CHART_LIVE_WINDOW_SEC) : pushRing(used, tickTs, usedPct, CHART_LIVE_WINDOW_SEC),
          );
        }
        if (tempC !== undefined) {
          temp = untrack(() =>
            tempSeeded ? appendAfterSeed(temp, tickTs, tempC, CHART_LIVE_WINDOW_SEC) : pushRing(temp, tickTs, tempC, CHART_LIVE_WINDOW_SEC),
          );
        }
      },
    };
  }
  const diskSlotRings = Array.from({ length: MAX_CHART_DISKS }, () => makeDiskSlot());

  // ioForSlot: this slot's current host-scoped diskio read/write, via
  // disk_meta's own device join -- undefined (not 0) when the slot's
  // device isn't known yet, so a genuinely-idle drive (0 B/s) is never
  // confused with "no reading at all".
  function ioForSlot(frame, slot) {
    const device = diskMeta[slot]?.device;
    if (device === undefined) return [undefined, undefined];
    return [frame.host?.[`diskio.${device}.read_bps`], frame.host?.[`diskio.${device}.write_bps`]];
  }

  // Drives every slot's own ring pool from the live frame, one tick at a
  // time -- assignment is by SORTED POSITION (diskNames' own fixed
  // order), so a slot only ever resets when the array's actual member
  // set changes, never on an ordinary tick.
  $effect(() => {
    const frame = live.frame;
    if (!frame) return;
    for (let i = 0; i < MAX_CHART_DISKS; i++) {
      const slot = diskNames[i] ?? null;
      if (slot === null) {
        diskSlotRings[i].tick(frame.ts, null);
        continue;
      }
      const [ioRead, ioWrite] = ioForSlot(frame, slot);
      diskSlotRings[i].tick(frame.ts, slot, ioRead, ioWrite, diskUsagePct(disks[slot]) ?? undefined, disks[slot]?.['temp.c']);
    }
  });

  // diskSeedControllers/abortDiskSeed: one AbortController per DRIVE's own
  // in-flight seed fetch (Map, slot name -> controller) -- same ad hoc
  // shape as TopConsumers' heroSeedControllers, for the identical reason:
  // a seed fires per-drive from inside the effect below rather than from
  // one single per-resource effect, so there's no one shared controller
  // to reuse. abortDiskSeed cancels slot `slot`'s own pending fetch, if
  // any, before a fresh one for the same slot supersedes it.
  const diskSeedControllers = new Map();
  function abortDiskSeed(slot) {
    diskSeedControllers.get(slot)?.abort();
    diskSeedControllers.delete(slot);
  }
  onMount(() => {
    return () => {
      for (const slot of diskSeedControllers.keys()) abortDiskSeed(slot);
    };
  });

  // seedDiskSlot fetches ring `i` (drive `slot`)'s own ring-tier history
  // for JUST the currently-active chart metric -- "seed only the
  // CURRENTLY selected [metric]'s own ring(s), cheap, re-seed on switch"
  // is the same rule the Metrics page's own header rings use for their
  // resource switch (TopConsumers.svelte) -- io/used/temp here are three
  // independent tabs, not one resource's own always-together direction
  // breakdown the way heroSlot's single per-assignment seed covers, so
  // there's no reason to fetch all three up front when only one is ever
  // on screen at a time.
  function seedDiskSlot(i, slot, metric) {
    abortDiskSeed(slot);
    const controller = new AbortController();
    diskSeedControllers.set(slot, controller);
    const to = Math.floor(Date.now() / 1000);
    const from = to - CHART_LIVE_WINDOW_SEC;
    if (metric === 'io') {
      const device = diskMeta[slot]?.device;
      if (device === undefined) {
        // No device join yet for this slot -- nothing to ask /api/series
        // for; tick() above will pick it up live once disk_meta catches
        // up, same as it always has.
        diskSeedControllers.delete(slot);
        return;
      }
      const readKey = `diskio.${device}.read_bps`;
      const writeKey = `diskio.${device}.write_bps`;
      fetchSeries({ kind: 'host', entity: '', metrics: [readKey, writeKey], from, to, signal: controller.signal })
        .then((results) => {
          diskSeedControllers.delete(slot);
          const byMetric = {};
          for (const r of results) byMetric[r.metric] = r.points;
          const ptsA = byMetric[readKey] ?? [];
          const ptsB = byMetric[writeKey] ?? [];
          diskSlotRings[i].seedIO(sumSeriesPoints([ptsA, ptsB]), seriesPointsToRing(ptsA), seriesPointsToRing(ptsB));
        })
        .catch((err) => {
          if (err?.name === 'AbortError') return; // superseded -- a fresh reassignment or metric switch beat this back
          diskSeedControllers.delete(slot);
        });
      return;
    }
    if (metric === 'used') {
      fetchSeries({ kind: 'disk', entity: slot, metrics: ['fs.used_bytes', 'fs.free_bytes'], from, to, signal: controller.signal })
        .then((results) => {
          diskSeedControllers.delete(slot);
          const byMetric = {};
          for (const r of results) byMetric[r.metric] = r.points;
          diskSlotRings[i].seedUsed(diskUsagePctSeries(byMetric['fs.used_bytes'] ?? [], byMetric['fs.free_bytes'] ?? []));
        })
        .catch((err) => {
          if (err?.name === 'AbortError') return;
          diskSeedControllers.delete(slot);
        });
      return;
    }
    fetchSeries({ kind: 'disk', entity: slot, metrics: ['temp.c'], from, to, signal: controller.signal })
      .then((results) => {
        diskSeedControllers.delete(slot);
        diskSlotRings[i].seedTemp(seriesPointsToRing(results[0]?.points ?? []));
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return;
        diskSeedControllers.delete(slot);
      });
  }

  // Drives seedDiskSlot: fires it for every VISIBLE drive that hasn't
  // already been seeded for the CURRENTLY active metric -- covers a
  // fresh mount (nothing's seeded yet), a metric-tab switch (the newly-
  // active metric's ring starts cold for every drive until this runs),
  // and a legend toggle-ON (a drive that was hidden becomes visible, and
  // -- being new to this metric -- hasn't been seeded either). A
  // toggle-OFF changes hiddenSlots too (same Set-swap convention as
  // every other toggle in this app) but touches nothing here: every
  // OTHER drive's own seededFor(metric) is already true, so the loop is
  // a no-op for all of them.
  //
  // diskNames is read TRACKED here, deliberately NOT untrack()'d the way
  // fetchedDiskSeries' own effect below reads it -- that effect's own
  // trigger (chartWindow) only ever changes from an explicit click, well
  // after the live frame has already arrived, so untracking diskNames
  // there costs nothing. This effect's own trigger needs to cover the
  // page's very FIRST mount too, which can land before live.frame has
  // delivered anything at all (diskNames still []): an untracked read
  // would run this loop once against that empty list and then never get
  // a reason to run again once real disks show up, since none of
  // chartMetric/chartWindow/hiddenSlots change on their own just because
  // a frame arrived. Tracking diskNames means this reruns every ~2s tick
  // for as long as the page is open (same as TopConsumers' own hero-tick
  // effect always has) -- harmless: seededFor(metric)'s own cheap
  // boolean check turns every rerun after the real seed into a no-op
  // loop over up to MAX_CHART_DISKS flags, no fetch involved.
  $effect(() => {
    if (chartWindow !== 'now') return;
    const metric = chartMetric;
    const hidden = hiddenSlots;
    const names = diskNames;
    for (let i = 0; i < names.length && i < MAX_CHART_DISKS; i++) {
      const slot = names[i];
      if (hidden.has(slot)) continue;
      if (diskSlotRings[i].seededFor(metric)) continue;
      seedDiskSlot(i, slot, metric);
    }
  });

  function toggleChartSlot(slot) {
    const next = new Set(hiddenSlots);
    if (next.has(slot)) next.delete(slot);
    else next.add(slot);
    hiddenSlots = next;
  }
  function focusChartSlot(slot) {
    const idx = visibleChartSeries.findIndex((s) => s.slot === slot);
    chartInstance?.focusSeries(idx === -1 ? null : idx + 1);
  }

  // fetchedDiskSeries (history windows only): {slot -> {used, temp,
  // ioSum, ioA, ioB}}, refetched only on a window switch -- diskNames/
  // diskMeta are read via untrack (both re-derive off the live frame
  // every ~2s tick; tracking them here would refetch history on every
  // single tick for no reason). A disk added or removed while a
  // history window is showing picks up on the next window toggle, not
  // instantly.
  let fetchedDiskSeries = $state({});
  let chartLoading = $state(false);
  let chartFailed = $state(false);
  // fetchedChartRange: the [from, to] this effect actually asked
  // /api/series for -- handed to the header chart as xDomain (D2
  // chart-integrity pass) so the axis shows the FULL requested window
  // even when a drive's own real history covers only a sliver of it. See
  // lib/chartRange.ts's own doc for the sparse-data bug this fixes.
  let fetchedChartRange = $state(undefined);

  $effect(() => {
    const w = chartWindow;
    if (w === 'now') {
      fetchedDiskSeries = {};
      fetchedChartRange = undefined;
      chartFailed = false;
      chartLoading = false;
      return;
    }
    const seconds = CHART_WINDOW_SECONDS[w];
    const to = Math.floor(Date.now() / 1000);
    const from = to - seconds;
    fetchedChartRange = [from, to];

    const names = untrack(() => diskNames);
    const dm = untrack(() => diskMeta);
    if (names.length === 0) {
      fetchedDiskSeries = {};
      return;
    }
    const controller = new AbortController();
    chartLoading = true;
    chartFailed = false;
    Promise.all(
      names.map((slot) => {
        const device = dm[slot]?.device;
        const hostMetrics = device !== undefined ? [`diskio.${device}.read_bps`, `diskio.${device}.write_bps`] : [];
        return Promise.all([
          fetchSeries({
            kind: 'disk',
            entity: slot,
            metrics: ['fs.used_bytes', 'fs.free_bytes', 'temp.c'],
            from,
            to,
            signal: controller.signal,
          }),
          hostMetrics.length > 0
            ? fetchSeries({ kind: 'host', entity: '', metrics: hostMetrics, from, to, signal: controller.signal })
            : Promise.resolve([]),
        ]).then(([diskResults, hostResults]) => ({ slot, diskResults, hostResults, device }));
      }),
    )
      .then((perSlot) => {
        const out = {};
        for (const { slot, diskResults, hostResults, device } of perSlot) {
          const byMetric = {};
          for (const r of diskResults) byMetric[r.metric] = r.points;
          for (const r of hostResults) byMetric[r.metric] = r.points;
          const readPts = device !== undefined ? (byMetric[`diskio.${device}.read_bps`] ?? []) : [];
          const writePts = device !== undefined ? (byMetric[`diskio.${device}.write_bps`] ?? []) : [];
          out[slot] = {
            used: diskUsagePctSeries(byMetric['fs.used_bytes'] ?? [], byMetric['fs.free_bytes'] ?? []),
            temp: byMetric['temp.c'] ?? [],
            ioA: readPts,
            ioB: writePts,
            ioSum: sumSeriesPoints([readPts, writePts]),
          };
        }
        fetchedDiskSeries = out;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer window switch
        fetchedDiskSeries = {};
        chartFailed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) chartLoading = false;
      });
    return () => controller.abort();
  });

  // chartSeriesAll: one entry per drive, EVERY drive (visibleChartSeries
  // below is what actually reaches TimeChart) -- the legend renders off
  // this full list so a toggled-off chip stays visible to toggle back
  // on. entity/kind never change with chartMetric/chartWindow; points/
  // directionPoints do. colorVar/dash/kindColor are all position-only
  // (seriesColorVar/diskChartDash(i), kindColorVar(kind)) -- see the
  // header chart's own doc above for why the LINE's identity moved from
  // kind to slot position, with kind demoted to kindColor's small legend
  // accent.
  let chartSeriesAll = $derived.by(() => {
    const metric = chartMetric;
    const w = chartWindow;
    return diskNames.map((slot, i) => {
      const kind = diskKind(diskMeta[slot], disks[slot]);
      const colorVar = seriesColorVar(i);
      const dash = diskChartDash(i);
      const kindColor = kindColorVar(kind);
      // label: TimeChart's own tooltip keys each row by `label` (see its
      // {#each tooltip.rows as row (row.label)}) -- every series here
      // must carry one, or every row shares the same undefined key the
      // instant a tooltip actually renders (each_key_duplicate,
      // reproduced live on first hover).
      if (w === 'now') {
        const ring = diskSlotRings[i];
        if (metric === 'io') {
          return { slot, label: slot, colorVar, dash, kindColor, points: ring.ioPoints, directionPoints: ring.ioDirection, directionLabels: ['r', 'w'] };
        }
        if (metric === 'used') return { slot, label: slot, colorVar, dash, kindColor, points: ring.usedPoints };
        return { slot, label: slot, colorVar, dash, kindColor, points: ring.tempPoints };
      }
      const fetched = fetchedDiskSeries[slot];
      if (!fetched) return { slot, label: slot, colorVar, dash, kindColor, points: [] };
      if (metric === 'io') {
        return { slot, label: slot, colorVar, dash, kindColor, points: fetched.ioSum, directionPoints: [fetched.ioA, fetched.ioB], directionLabels: ['r', 'w'] };
      }
      if (metric === 'used') return { slot, label: slot, colorVar, dash, kindColor, points: fetched.used };
      return { slot, label: slot, colorVar, dash, kindColor, points: fetched.temp };
    });
  });

  // visibleChartSeries: chartSeriesAll minus every currently-hidden
  // slot -- filtered OUT of the array TimeChart receives entirely
  // (rather than kept in and uPlot-hidden, TopConsumers' own toggle
  // mechanism) so a hidden line is also absent from the hover tooltip,
  // not just undrawn -- "scrub pins all VISIBLE lines," never a toggled-
  // off one.
  let visibleChartSeries = $derived(chartSeriesAll.filter((s) => !hiddenSlots.has(s.slot)));
  let chartLabel = $derived(`${CHART_METRICS.find((m) => m.key === chartMetric)?.label} by drive`);

  // glideMs: the perpetual-glide motion pass's own shared duration
  // (live.glideMs, or 0 under reduced motion -- see streamdriver.ts's
  // "Cadence-driven glide" doc) -- fed to the CSS-transition-duration
  // bars below (parity progress, per-disk usage) and to parityPctTween.
  let glideMs = $derived(motion.reduced ? 0 : live.glideMs);

  let started = $derived(array['array.started']);
  let parityPct = $derived(array['parity.progress_pct']);
  // parityPctTween glides the percentage the progress bar/text below
  // display -- previously a bare fmtPct(parityPct)/width binding,
  // snapping every ~2s tick with no easing at all (this view's own
  // instance of the same gap Overview's arrayStateSentence had -- see
  // its doc). No scrub mechanism exists for either to mirror.
  let parityPctTween = new Tween(untrack(() => parityPct ?? 0), { duration: untrack(() => glideMs), easing: linear });
  $effect(() => {
    parityPctTween.set(parityPct ?? 0, { duration: glideMs, easing: linear });
  });
  let paritySpeed = $derived(array['parity.speed_bps']);
  // parityIsRunning treats an explicit 0 (the wire value var.go/fake.go
  // now both write on finish -- see its own doc) as idle, not merely
  // "key present" -- a bare `!== undefined` check would read that
  // finish-zero as still running forever, right back into the bug this
  // is fixing.
  let parityRunning = $derived(parityIsRunning(parityPct));
  let moverRunning = $derived(array['mover.running'] === 1);
  let shares = $derived(sharesFromMetrics(array));

  // eta: identical shape to ArrayCard's own effect (see its doc for why
  // this is derived from parity.progress_pct's own rate of change,
  // never from speed_bps). prevSample is plain instance state -- it only
  // needs to survive between effect runs, never to trigger one itself.
  let prevSample = null;
  let eta = $state(null);
  $effect(() => {
    if (!parityRunning || parityPct === undefined) {
      prevSample = null;
      eta = null;
      return;
    }
    if (prevSample) {
      eta = etaFromProgress(prevSample.ts, prevSample.pct, ts, parityPct);
    }
    prevSample = { ts, pct: parityPct };
  });

  // parityHistory: not in the live frame (events never are) -- fetched
  // once on mount, then re-polled every 30s and on window focus, the
  // same low-urgency background-refresh pattern Overview's own events
  // feed uses (a parity run lasts many minutes to hours, so 30s latency
  // on "did it just start/finish" is harmless; no AbortController is
  // needed here for the same reason it isn't in Overview's loadEvents --
  // this isn't a rapid, user-selector-driven fetch that can race itself).
  let parityHistory = $state([]);

  // parityHistorySeedPending gates the "No parity check history yet."
  // message below the same way ContainerDetail/GPUEntityCard's own
  // liveSeedPending gates their chart cards: while true, a truly-empty
  // parityHistory stays silent instead of flashing that message the
  // instant this view mounts, before the very first loadParityHistory()
  // below has had a chance to resolve. Only ever flips false once, on
  // that first resolution -- a later poll/focus refresh finding zero
  // history is a real "No parity check history yet.", not a pending one.
  let parityHistorySeedPending = $state(true);

  async function loadParityHistory() {
    try {
      parityHistory = await fetchEvents({ kinds: ['parity.start', 'parity.finish'], limit: 5 });
    } catch {
      // A transient fetch failure leaves the last-good history showing
      // rather than blanking it -- the next poll or focus tries again.
    } finally {
      parityHistorySeedPending = false;
    }
  }
  onMount(() => {
    loadParityHistory();
    const interval = setInterval(loadParityHistory, EVENTS_POLL_MS);
    window.addEventListener('focus', loadParityHistory);
    return () => {
      clearInterval(interval);
      window.removeEventListener('focus', loadParityHistory);
    };
  });

  function historyLabel(event) {
    if (event.Kind === 'parity.start') return 'Started';
    return event.Detail ? `Finished · ${event.Detail}` : 'Finished';
  }
</script>

<!-- Type-at-a-glance glyphs (ask: "different types of things in the same
     category should stand out -- nvme storage vs spinning disk", later
     extended to all four kinds: "the boot flash drive is not HDD, it's
     USB... the cache drives are NVMe... color coded or highlighted
     differently"): a platter+spindle for HDD, a chip-with-pins for SSD,
     a slim ticked stick for NVMe (an M.2 module's own silhouette, wider
     and shorter than SSD's square chip), a body+connector for USB
     (echoing router.ts's own GPU nav icon's "chip" visual language for
     the solid-state pair) -- every glyph always paired with its own text
     label AND a tinted badge background (storage-disk__media--<kind>
     below), never color alone. -->
{#snippet diskMediaGlyph(type)}
  {#if type === 'ssd'}
    <svg class="storage-disk__media-icon" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="3" y="3" width="10" height="10" rx="1.5" />
      <path
        d="M5.5 3v2M8 3v2M10.5 3v2M5.5 11v2M8 11v2M10.5 11v2M3 5.5h2M3 8h2M3 10.5h2M11 5.5h2M11 8h2M11 10.5h2"
      />
    </svg>
  {:else if type === 'nvme'}
    <svg class="storage-disk__media-icon" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="1.5" y="6.25" width="13" height="3.5" rx="1" />
      <path d="M4.5 6.25v3.5M7.5 6.25v3.5M10.5 6.25v3.5" />
    </svg>
  {:else if type === 'usb'}
    <svg class="storage-disk__media-icon" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="3.5" y="6" width="9" height="8" rx="1.5" />
      <rect x="6.5" y="2" width="3" height="4" />
      <path d="M6.5 3.6h3" />
    </svg>
  {:else}
    <svg class="storage-disk__media-icon" viewBox="0 0 16 16" aria-hidden="true">
      <circle cx="8" cy="8" r="5.25" />
      <circle class="storage-disk__media-icon-hub" cx="8" cy="8" r="1.3" />
    </svg>
  {/if}
{/snippet}

<div class="storage-view">
  <h1 class="page-title">Storage</h1>

  <div class="card storage-chart">
    <div class="storage-chart__head">
      <span class="microlabel">{chartLabel}</span>
      <div class="segmented" role="tablist" aria-label="Chart metric">
        {#each CHART_METRICS as m (m.key)}
          <button
            type="button"
            role="tab"
            aria-selected={chartMetric === m.key}
            class="segmented__btn"
            class:segmented__btn--active={chartMetric === m.key}
            onclick={() => (chartMetric = m.key)}
          >
            {m.label}
          </button>
        {/each}
      </div>
    </div>
    <div class="segmented" role="group" aria-label="Chart window">
      {#each CHART_WINDOWS as w (w.key)}
        <button
          type="button"
          class="segmented__btn"
          class:segmented__btn--active={chartWindow === w.key}
          onclick={() => (chartWindow = w.key)}
        >
          {w.label}
        </button>
      {/each}
    </div>

    {#if diskNames.length === 0}
      <p class="microlabel storage-chart__empty">No disk data yet.</p>
    {:else if chartFailed}
      <p class="microlabel storage-chart__error">Couldn't load this window. Try again shortly.</p>
    {:else}
      {#if chartLoading}
        <p class="microlabel storage-chart__loading">Loading…</p>
      {/if}
      <TimeChart
        bind:this={chartInstance}
        series={visibleChartSeries}
        formatValue={CHART_FORMATTERS[chartMetric]}
        live={chartWindow === 'now'}
        xDomain={chartWindow === 'now' ? undefined : fetchedChartRange}
        height={240}
        showLegend={false}
      />
      <div class="storage-chart__legend" role="group" aria-label="Chart drives">
        {#each chartSeriesAll as s (s.slot)}
          {@const hidden = hiddenSlots.has(s.slot)}
          <button
            type="button"
            class="storage-chart__chip"
            class:storage-chart__chip--off={hidden}
            style={`--chip-color: var(${s.colorVar}); --chip-kind-color: var(${s.kindColor})`}
            aria-pressed={!hidden}
            aria-label={`${s.slot} line, click to ${hidden ? 'show' : 'hide'}`}
            onmouseenter={() => focusChartSlot(s.slot)}
            onmouseleave={() => focusChartSlot(null)}
            onclick={() => toggleChartSlot(s.slot)}
          >
            <span class="storage-chart__chip-kind" aria-hidden="true"></span>
            {s.slot}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <div class="card storage-parity">
    <div class="storage-parity__head">
      <span class="microlabel">Array</span>
      {#if started === 1}
        <HealthDot status="good" label="Started" />
      {:else if started === 0}
        <HealthDot status="serious" label="Stopped" />
      {:else}
        <span class="microlabel storage-parity__unknown">Unknown</span>
      {/if}
    </div>

    <div class="storage-parity__section">
      <span class="microlabel">Parity check</span>
      {#if parityRunning}
        <div class="storage-parity__progress">
          <div class="storage-parity__progress-track">
            <div
              class="storage-parity__progress-fill"
              style="width: {Math.min(100, Math.max(0, parityPctTween.current))}%; transition-duration: {glideMs}ms"
            ></div>
          </div>
          <span class="tabular-nums storage-parity__progress-pct">{fmtPct(parityPctTween.current)}</span>
          <span class="storage-parity__progress-detail tabular-nums">
            {fmtRate(paritySpeed ?? 0)} &middot; ETA {eta === null ? 'calculating…' : fmtDuration(eta)}
          </span>
        </div>
      {:else}
        <span class="storage-parity__idle">No check running</span>
      {/if}
    </div>

    <div class="storage-parity__chips">
      <span class="storage-parity__chip" class:storage-parity__chip--active={moverRunning}>
        Mover {moverRunning ? 'running' : 'idle'}
      </span>
    </div>

    <div class="storage-parity__section">
      <span class="microlabel">Recent checks</span>
      {#if parityHistorySeedPending}
        <!-- first loadParityHistory() call hasn't settled yet -- see parityHistorySeedPending's own doc -->
      {:else if parityHistory.length === 0}
        <p class="microlabel storage-parity__empty">No parity check history yet.</p>
      {:else}
        <ul class="storage-parity__history">
          {#each parityHistory as event (event.ID)}
            <li>
              <span>{historyLabel(event)}</span>
              <span class="microlabel storage-parity__history-time">{fmtRelTime(event.TS)}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>

  {#if diskNames.length === 0}
    <p class="microlabel storage-view__empty">
      No disk data yet.{sources.unraid && sources.unraid !== 'ok' ? ` ${sources.unraid}` : ''}
    </p>
  {:else}
    <div class="card storage-disks">
      <span class="microlabel">Disks &middot; {diskNames.length}</span>
      <div class="storage-disks__list">
        {#each diskNames as name (name)}
          {@const metrics = disks[name]}
          {@const role = diskRole(name)}
          {@const mediaType = diskKind(diskMeta[name], metrics)}
          {@const temp = diskTempState(metrics)}
          {@const usagePct = diskUsagePct(metrics)}
          {@const errors = metrics['errors'] ?? 0}
          {@const tempTint = temp.kind === 'reading' ? bandToken(band(mediaType === 'nvme' ? 'disk.temp.nvme' : 'disk.temp', temp.celsius)) : undefined}
          {@const usageTint = usagePct !== null ? bandToken(band('disk.capacity', usagePct)) : undefined}
          <div class="storage-disk">
            <div class="storage-disk__head">
              <span class="microlabel storage-disk__eyebrow">
                <span>{ROLE_LABEL[role]}</span>
                {#if mediaType}
                  <span class="storage-disk__media storage-disk__media--{mediaType}" title={MEDIA_TITLE[mediaType]}>
                    {@render diskMediaGlyph(mediaType)}<span class="storage-disk__media-label">{MEDIA_LABEL[mediaType]}</span>
                  </span>
                {/if}
              </span>
              {#if temp.kind === 'reading'}
                <span class="tabular-nums storage-disk__temp" style={tempTint ? `color: ${tempTint}` : undefined}
                  >{temp.celsius.toFixed(1)}&deg;C</span
                >
              {:else}
                <span class="storage-disk__chip">{temp.kind === 'spun-down' ? 'Spun down' : 'No sensor'}</span>
              {/if}
            </div>
            <div class="storage-disk__name">{name}</div>

            {#if usagePct !== null}
              <div class="storage-disk__usage">
                <div class="storage-disk__usage-track">
                  <div
                    class="storage-disk__usage-fill"
                    style="width: {usagePct}%; background: {seqStep(usagePct)}; transition-duration: 150ms, {glideMs}ms"
                  ></div>
                </div>
                <span class="tabular-nums storage-disk__usage-pct" style={usageTint ? `color: ${usageTint}` : undefined}
                  >{fmtPct(usagePct)}</span
                >
                <span class="tabular-nums storage-disk__bytes">
                  {fmtBytes(metrics['fs.used_bytes'])} / {fmtBytes(metrics['fs.used_bytes'] + metrics['fs.free_bytes'])}
                </span>
                {#if usagePct > 90}
                  <HealthDot status="warning" label="High usage" />
                {/if}
              </div>
            {/if}

            {#if errors > 0}
              <HealthDot status="serious" label={`${errors} error${errors === 1 ? '' : 's'}`} />
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <div class="storage-view__row">
    <div class="card storage-shares">
      <span class="microlabel">Shares</span>
      {#if shares.length === 0}
        <p class="microlabel storage-shares__empty">
          No share data yet.{sources.unraid && sources.unraid !== 'ok' ? ` ${sources.unraid}` : ''}
        </p>
      {:else}
        <p class="microlabel storage-shares__caption">
          Share sizes are the backing array or pool total — Unraid doesn't track true per-share usage.
        </p>
        <div class="storage-shares__table-wrap">
          <table class="storage-shares__table">
            <thead>
              <tr>
                <th class="microlabel">Share</th>
                <th class="microlabel">Used</th>
              </tr>
            </thead>
            <tbody>
              {#each shares as share (share.name)}
                <tr>
                  <td>{share.name}</td>
                  <td class="tabular-nums">{fmtBytes(share.usedBytes)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>

    <div class="card storage-docker">
      <span class="microlabel">Docker storage</span>
      {#if dockerStorage['docker.images_bytes'] === undefined && dockerStorage['docker.containers_bytes'] === undefined && dockerStorage['docker.volumes_bytes'] === undefined}
        <p class="microlabel storage-docker__empty">
          No docker storage data yet.{sources['docker-disk'] && sources['docker-disk'] !== 'ok'
            ? ` ${sources['docker-disk']}`
            : ''}
        </p>
      {:else}
        <dl class="storage-docker__list">
          <dt>Images</dt>
          <dd class="tabular-nums">{fmtBytes(dockerStorage['docker.images_bytes'] ?? 0)}</dd>
          <dt>Containers</dt>
          <dd class="tabular-nums">{fmtBytes(dockerStorage['docker.containers_bytes'] ?? 0)}</dd>
          <dt>Volumes</dt>
          <dd class="tabular-nums">{fmtBytes(dockerStorage['docker.volumes_bytes'] ?? 0)}</dd>
        </dl>
      {/if}
    </div>
  </div>
</div>

<style>
  .storage-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .storage-view__empty {
    margin: 0;
  }

  .storage-chart {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .storage-chart__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .storage-chart__empty,
  .storage-chart__loading {
    margin: 0;
  }
  .storage-chart__error {
    margin: 0;
    color: var(--status-warning);
  }
  /* Legend chips: same shape/interaction as the Metrics page's own hero
     chart (top-consumers__chip) -- --chip-color is the LINE's own
     per-position categorical hue now (colorVar, chartSeriesAll), not a
     kind tint; kind (--chip-kind-color) is demoted to the small dot
     below, since a drive's identity is its slot name plus its line
     color, not a container icon. */
  .storage-chart__legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .storage-chart__chip {
    display: inline-flex;
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
  .storage-chart__chip:hover {
    background: color-mix(in oklab, var(--chip-color) 22%, transparent);
  }
  /* The kind-accent dot: a quiet secondary read (ssd/nvme/usb tinted,
     hdd's plain --ink-2 reading as "no accent" -- same "calm, not a
     rainbow" precedent storage-disk__media--hdd already sets) alongside
     the chip's own per-position line color. */
  .storage-chart__chip-kind {
    display: inline-block;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--chip-kind-color);
    flex-shrink: 0;
  }
  /* A toggled-off line's chip stays legible (never the near-invisible
     disabled treatment) -- it's a live, click-to-restore choice. */
  .storage-chart__chip--off {
    opacity: 0.45;
  }

  .storage-parity {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .storage-parity__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .storage-parity__unknown {
    color: var(--ink-2);
  }
  .storage-parity__section {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .storage-parity__progress {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .storage-parity__progress-track {
    flex: 1;
    min-width: 6rem;
    height: 10px;
    border-radius: 5px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .storage-parity__progress-fill {
    height: 100%;
    background: var(--series-1);
    /* duration is inline (transition-duration, above) -- see
       BaySchematic's matching fill for why a plain CSS transition
       (rather than a Tween/headState) is enough for one interpolated
       property. */
    transition-property: width;
    transition-timing-function: linear;
  }
  .storage-parity__progress-pct {
    font-family: var(--font-mono);
    font-size: 0.85rem;
    min-width: 3.2em;
  }
  .storage-parity__progress-detail {
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--ink-2);
  }
  .storage-parity__idle {
    color: var(--ink-2);
    font-size: 0.85rem;
  }
  .storage-parity__chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .storage-parity__chip {
    font-family: var(--font-mono);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.3rem 0.55rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
  }
  .storage-parity__chip--active {
    background: color-mix(in oklab, var(--status-good) 18%, transparent);
    color: var(--status-good);
  }
  .storage-parity__empty {
    margin: 0;
  }
  .storage-parity__history {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .storage-parity__history li {
    display: flex;
    justify-content: space-between;
    gap: 0.75rem;
    font-size: 0.85rem;
  }
  .storage-parity__history-time {
    white-space: nowrap;
  }

  .storage-disks {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .storage-disks__list {
    display: flex;
    flex-direction: column;
  }
  /* Rail row, not a card -- was one .card per disk (parity/data/cache
     alike, 5-20+ of them depending on the array), the exact "same
     module at every scale" pattern this rollout replaces elsewhere: a
     hairline between rows instead, same convention as StatTile's own
     bare rail and EventFeedItem. */
  .storage-disk {
    padding: 0.65rem 0;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .storage-disk:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
  .storage-disk:first-child {
    padding-top: 0;
  }
  .storage-disk__head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.3rem 0.5rem;
  }
  .storage-disk__eyebrow {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
    white-space: nowrap;
  }
  /* Type badge: a tinted pill (Scott's own ask -- "these should be color
     coded or highlighted differently"), matching storage-disk__chip's
     own pill shape below. color set here is the KIND's accent, picked up
     by the glyph's stroke via currentColor -- but never by the text,
     which storage-disk__media-label pins back to ink-2 explicitly, so
     color is reinforcement on top of shape+text, never the only channel.
     hdd (the ordinary/majority case, not one that needs to stand out)
     deliberately gets no override here -- the plain neutral chip tint
     below is its own "color". */
  .storage-disk__media {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.15rem 0.5rem 0.15rem 0.4rem;
    border-radius: 999px;
    color: var(--ink-2);
    background: color-mix(in oklab, var(--ink) 7%, transparent);
  }
  .storage-disk__media-label {
    color: var(--ink-2);
  }
  .storage-disk__media--ssd {
    color: var(--series-3);
    background: color-mix(in oklab, var(--series-3) 12%, transparent);
  }
  .storage-disk__media--nvme {
    color: var(--series-1);
    background: color-mix(in oklab, var(--series-1) 12%, transparent);
  }
  .storage-disk__media--usb {
    color: var(--series-4);
    background: color-mix(in oklab, var(--series-4) 14%, transparent);
  }
  .storage-disk__media-icon {
    width: 11px;
    height: 11px;
    flex-shrink: 0;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.2;
  }
  .storage-disk__media-icon-hub {
    fill: currentColor;
    stroke: none;
  }
  .storage-disk__temp {
    font-size: 0.85rem;
  }
  .storage-disk__chip {
    font-family: var(--font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.2rem 0.5rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
    white-space: nowrap;
  }
  .storage-disk__name {
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 1rem;
    color: var(--ink);
  }
  .storage-disk__usage {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .storage-disk__usage-track {
    flex: 1 1 6rem;
    height: 8px;
    border-radius: 4px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .storage-disk__usage-fill {
    height: 100%;
    /* Two independently-timed transitions on the same element: filter's
       own fixed 150ms hover feedback (unrelated to live data), and
       width's live glide duration -- positionally matched to this
       property order by the inline transition-duration list (above,
       template). */
    transition-property: filter, width;
    transition-timing-function: ease, linear;
  }
  .storage-disk__usage-pct {
    font-size: 0.78rem;
    min-width: 3em;
    text-align: right;
  }
  /* Bars are current-state, not time series -- no scrubbing, just
     animated emphasis on hover; pct+bytes are already shown adjacent,
     so this is emphasis only, no new value display. */
  .storage-disk__usage:hover .storage-disk__usage-fill {
    filter: brightness(1.15);
  }
  .storage-disk__usage:hover .storage-disk__usage-pct {
    font-weight: 700;
  }
  .storage-disk__bytes {
    font-size: 0.75rem;
    color: var(--ink-2);
    white-space: nowrap;
  }

  .storage-view__row {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
    align-items: start;
  }
  @media (max-width: 47.9375rem) {
    .storage-view__row {
      grid-template-columns: 1fr;
    }
  }
  .storage-shares,
  .storage-docker {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .storage-shares__caption {
    margin: 0;
    text-transform: none;
    letter-spacing: normal;
  }
  .storage-shares__empty,
  .storage-docker__empty {
    margin: 0;
  }
  .storage-shares__table-wrap {
    overflow-x: auto;
  }
  .storage-shares__table {
    width: 100%;
    border-collapse: collapse;
    min-width: 16rem;
  }
  .storage-shares__table th {
    text-align: left;
    padding: 0.35rem 0.5rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
  }
  .storage-shares__table td {
    padding: 0.35rem 0.5rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.85rem;
  }
  .storage-shares__table th:last-child,
  .storage-shares__table td:last-child {
    text-align: right;
  }
  .storage-docker__list {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  .storage-docker__list dt {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .storage-docker__list dd {
    margin: 0;
    font-size: 0.85rem;
    text-align: right;
  }
</style>

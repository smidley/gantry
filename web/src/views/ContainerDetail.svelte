<!--
  ContainerDetail: one container's header, range-scoped charts (CPU,
  memory, network, IO, GPU per-engine, PSI -- the last three only when
  present), event markers, a metadata card, and its log viewer.

  "Unknown container" (isGone, below) replaces the whole charts grid
  with one plain "no longer present" line instead of composing it from
  five separate per-chart empty states -- clickable events (eventHref)
  made a name that's since been fully removed a real, no-longer-rare way
  to land here, and five stacked "No CPU/memory/network/... data" lines
  for something that fundamentally isn't here read as broken rather than
  graceful. The header's own muted "not currently running" chip and the
  metadata card's em-dash fallbacks are untouched -- still compositional,
  and still correct for the item-1 "stopped but known" case, where c is
  defined (just Metrics-empty), so isGone never fires for it.
-->
<script>
  import { untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fetchContainerStorage, fetchEvents, fetchSeries } from '../lib/api';
  import { fmtBytes, fmtCores, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { containerHealthStatus } from '../lib/containerStatus';
  import { deriveContainerAnomaly, containerAnomalyEvidence } from '../lib/containerAnomaly';
  import { limitsFactsParts } from '../lib/containerLimits';
  import { band, bandToken } from '../lib/thresholds';
  import { GPU_ENGINE_ORDER } from '../lib/metrics';
  import { eventsToMarkers } from '../lib/eventMarkers';
  import {
    mountCapacitySlot,
    normalizeStorageKind,
    recentlyActiveDevices,
    recordDeviceActivity,
    sharePlacementText,
    sortMounts,
  } from '../lib/containerStorage';
  import { diskKind, diskUsagePct } from '../lib/disks';
  import { deviceSharePct } from '../lib/insights';

  import ContainerIcon from '../components/ContainerIcon.svelte';
  import HealthDot from '../components/HealthDot.svelte';
  import TimeChart from '../components/TimeChart.svelte';
  import LogViewer from '../components/LogViewer.svelte';
  import StorageDeviceRow from '../components/StorageDeviceRow.svelte';
  import StorageTotalRow from '../components/StorageTotalRow.svelte';
  import AnomalyBanner from '../components/AnomalyBanner.svelte';
  import ImpactPanel from '../components/ImpactPanel.svelte';

  let { name } = $props();

  const SYNC_KEY = 'container-detail';
  const LIVE_WINDOW_SEC = 900;
  const STORAGE_POLL_MS = 2000;

  // Storage-system badge vocabulary (StorageRefDTO's own kinds: share,
  // pool, disk, flash, other). Tints skip --series-1/4 -- this card's
  // own device rows use those two for read/write, see StorageDeviceRow --
  // and --series-2/8, both red/orange enough to risk a plain kind badge
  // misreading as an alarm (Scott's own read on --series-2, extended
  // here to its close sibling).
  const STORAGE_KIND_LABEL = { share: 'Share', pool: 'Pool', disk: 'Disk', flash: 'Flash', other: 'Other' };
  const STORAGE_KIND_TITLE = {
    share: 'Unraid user share',
    pool: 'Cache/pool device',
    disk: 'Array disk',
    flash: 'Boot (flash) device',
    other: 'Unresolved host path',
  };
  const RANGES = [
    { key: 'live', label: 'Live · 15m' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
  ];
  const RANGE_SECONDS = { '1h': 3600, '24h': 86400, '7d': 7 * 86400, '30d': 30 * 86400 };

  // Every metric this page might ever chart, fetched together in one
  // /api/series call per range switch (see the $effect below) -- gpu/psi
  // entries are dynamic-presence ones: a chart section only renders when
  // at least one of its metrics actually has points.
  const ALL_METRICS = [
    'cpu.pct',
    'cpu.throttled_pct',
    // cpu.alloc_pct (additive -- allocation display): only actually
    // plotted as a third CPU-chart series once hasPoints('cpu.alloc_pct')
    // says a limited container has real data for it, same conditional-
    // series shape cpu.throttled_pct already uses just below.
    'cpu.alloc_pct',
    'mem.bytes',
    'net.rx_bps',
    'net.tx_bps',
    'io.read_bps',
    'io.write_bps',
    'gpu.render.busy_pct',
    'gpu.video.busy_pct',
    'gpu.video-enhance.busy_pct',
    'gpu.copy.busy_pct',
    'psi.cpu.some_pct',
    'psi.io.some_pct',
  ];

  let activeRange = $state('live');

  // Live rings: one per metric, unconditionally -- which ones actually
  // end up with any points (and therefore get charted) is a display-time
  // decision, not a ring-creation-time one (creating $state/$effect
  // pairs conditionally, after the fact, isn't how runes work).
  const liveRings = {};
  for (const metric of ALL_METRICS) {
    liveRings[metric] = liveRing((f) => f.containers?.[name]?.metrics?.[metric], LIVE_WINDOW_SEC);
  }

  // liveSeedPending gates the four live-mode chart cards' own empty-state
  // messages below, same as GPUEntityCard's own field of the same name:
  // while true, a truly-empty live ring stays silent instead of flashing
  // "No CPU/memory/network/disk IO data for this range" the instant this
  // view mounts (or remounts on a container-to-container navigation, per
  // the {#key} wrapper's own doc), before the seed fetch just below has
  // even had a chance to say whether there's real history or not --
  // reproduced live (a ~100-150ms flash on every route swap into this
  // page). Flips false once the seed settles either way (found data,
  // found none, or failed); `name` is stable for this component's whole
  // lifetime, so this only ever needs to run once.
  let liveSeedPending = $state(true);

  // Seed every live ring from server history on mount, once: `name` is
  // stable for this component's whole lifetime (App.svelte's own {#key}
  // wrapper fully remounts ContainerDetail on a container-name change --
  // see its doc), so this effect's only real dependency never varies,
  // same as the events effect below. Runs regardless of which range tab
  // is active so a later switch BACK to Live finds it already filled,
  // not empty-then-refetching. A failed/empty seed leaves every ring
  // exactly as unseeded as it is today (see mergeSeed's own doc) -- no
  // error banner, no new skeleton state, just today's cold start.
  $effect(() => {
    const containerName = name;
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    const controller = new AbortController();
    fetchSeries({ kind: 'container', entity: containerName, metrics: ALL_METRICS, from, to, signal: controller.signal })
      .then((results) => {
        for (const r of results) liveRings[r.metric]?.seed(seriesPointsToRing(r.points));
        liveSeedPending = false;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // unmounted (or -- can't actually happen, name is stable -- superseded) before the seed resolved
        liveSeedPending = false;
      });
    return () => controller.abort();
  });

  // fetchedSeries holds the non-live ranges' /api/series result, keyed by
  // metric -- refetched ONLY when activeRange (or name) changes, never
  // live-appended, per the range picker's own contract.
  let fetchedSeries = $state({});
  let fetchInFlight = $state(false);
  let fetchFailed = $state(false);
  // fetchedRange: the [from, to] this effect actually asked /api/series
  // for -- handed to every chart below as xDomain (D2 chart-integrity
  // pass) so each axis shows the FULL requested window even when this
  // container's own real history covers only a sliver of it. See
  // lib/chartRange.ts's own doc for the sparse-data bug this fixes.
  let fetchedRange = $state(undefined);

  // Stale-response race: switching range (or container name) fast enough
  // that an earlier /api/series call is still in flight when a newer one
  // starts must not let the earlier response win just because it happens
  // to resolve last -- e.g. Live->1h->24h in quick succession must not
  // let 1h's response land after 24h's and silently show 1h's data under
  // 24h's active button. The effect's own cleanup (the returned function)
  // runs BEFORE the next call to this same effect, so aborting there
  // guarantees every superseded request is cancelled before its
  // replacement's fetch even starts. The aborted request's own .then is
  // never reached (fetch rejects instead); its .catch recognizes the
  // abort via err?.name and ignores it rather than clearing already-newer
  // state, and its .finally only clears fetchInFlight when IT wasn't the
  // one aborted (a stale request's finally firing after its replacement
  // has already set fetchInFlight=true must not clobber that back to
  // false).
  $effect(() => {
    const range = activeRange;
    const containerName = name;
    if (range === 'live') {
      fetchedSeries = {};
      fetchedRange = undefined;
      fetchFailed = false;
      fetchInFlight = false;
      return;
    }
    const seconds = RANGE_SECONDS[range];
    const to = Math.floor(Date.now() / 1000);
    const from = to - seconds;
    fetchedRange = [from, to];
    const controller = new AbortController();
    fetchInFlight = true;
    fetchFailed = false;
    fetchSeries({ kind: 'container', entity: containerName, metrics: ALL_METRICS, from, to, signal: controller.signal })
      .then((results) => {
        const byMetric = {};
        for (const r of results) byMetric[r.metric] = r.points;
        fetchedSeries = byMetric;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer range/name switch
        fetchedSeries = {};
        fetchFailed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) fetchInFlight = false;
      });
    return () => controller.abort();
  });

  // pointsFor reads either shape (live ring 2-tuples [ts,val], or fetched
  // 3-tuples [ts,avg,max]) -- TimeChart only ever destructures the first
  // two elements of each point, so both shapes work unchanged.
  function pointsFor(metric) {
    return activeRange === 'live' ? liveRings[metric].points : (fetchedSeries[metric] ?? []);
  }
  // hasPoints requires >1 point, not merely >0: a single point can't
  // show a trend anyway (there's no line to draw, just one dot), and --
  // reproduced live while building the GPU view -- uPlot's own x-axis
  // tick-picker mishandles a genuinely single-point (zero-width) time
  // domain, rendering nonsensical year-granularity gridlines with no
  // visible data rather than the correct few-second range. TimeChart's
  // own xRange padding (see its doc) handles every OTHER too-narrow
  // case (2+ close-together points); this is the one gap that needs
  // fixing at the call site instead, since there is no meaningful
  // "point 2" to pad around yet.
  function hasPoints(metric) {
    return pointsFor(metric).length > 1;
  }
  function hasNonzero(metric) {
    return pointsFor(metric).some(([, v]) => v > 0);
  }

  let cpuSeries = $derived.by(() => {
    const s = [{ label: 'CPU', points: pointsFor('cpu.pct'), colorVar: '--series-1' }];
    if (hasNonzero('cpu.throttled_pct')) s.push({ label: 'Throttled', points: pointsFor('cpu.throttled_pct'), colorVar: '--series-2' });
    // Allocation (additive -- dual-perspective usage): a limited
    // container's % of its OWN ceiling, plotted alongside host-share so
    // scrubbing/hovering the chart shows both at once via TimeChart's
    // existing per-series tooltip -- no new chart plumbing needed, same
    // "related percentage, same chart" pattern Throttled already uses.
    if (hasPoints('cpu.alloc_pct')) s.push({ label: 'Allocation', points: pointsFor('cpu.alloc_pct'), colorVar: '--series-3' });
    return s;
  });
  let memSeries = $derived([{ label: 'Memory', points: pointsFor('mem.bytes'), colorVar: '--series-1' }]);
  let netSeries = $derived([
    { label: 'Down', points: pointsFor('net.rx_bps'), colorVar: '--series-1' },
    { label: 'Up', points: pointsFor('net.tx_bps'), colorVar: '--series-4' },
  ]);
  let ioSeries = $derived([
    { label: 'Read', points: pointsFor('io.read_bps'), colorVar: '--series-1' },
    { label: 'Write', points: pointsFor('io.write_bps'), colorVar: '--series-4' },
  ]);
  const GPU_SERIES_VAR = { render: '--series-1', video: '--series-2', 'video-enhance': '--series-3', copy: '--series-4' };
  let gpuSeries = $derived.by(() =>
    GPU_ENGINE_ORDER.filter((engine) => hasPoints(`gpu.${engine}.busy_pct`)).map((engine) => ({
      label: engine,
      points: pointsFor(`gpu.${engine}.busy_pct`),
      colorVar: GPU_SERIES_VAR[engine],
    })),
  );
  let psiSeries = $derived.by(() => {
    const s = [];
    if (hasPoints('psi.cpu.some_pct')) s.push({ label: 'CPU', points: pointsFor('psi.cpu.some_pct'), colorVar: '--series-1' });
    if (hasPoints('psi.io.some_pct')) s.push({ label: 'IO', points: pointsFor('psi.io.some_pct'), colorVar: '--series-2' });
    return s;
  });

  // events/markers: fetched once per container name (not per range switch
  // -- a generous recency-ordered limit comfortably covers every range
  // this page offers, and TimeChart's own draw hook already clips
  // markers outside the visible x-domain, so over-fetching is harmless).
  let events = $state([]);
  $effect(() => {
    const containerName = name;
    fetchEvents({ entity: containerName, limit: 200 })
      .then((result) => {
        events = result;
      })
      .catch(() => {
        events = [];
      });
  });
  let markers = $derived(eventsToMarkers(events));

  // Storage section: mounts + per-backing-device live IO, from
  // /api/containers/{name}/storage. Polled every 2s while this view is
  // mounted rather than read off the live frame -- per-device rates are
  // ring-only ("live:"-prefixed samples, see store/query.go's own doc),
  // so buildSnapshot never puts them in the SSE frame at all. storageData
  // is the last successful response ever (null before the first one
  // resolves); a LATER poll failing leaves it showing rather than
  // blanking it, same resilience Storage.svelte's own parity-history
  // poll already has. A container this 404s for (or any other fetch
  // failure before ever succeeding once) simply never gets a panel --
  // storageData stays null -- rather than an error card.
  let storageData = $state(null);

  // deviceLastActiveAt backs the Live IO noise rule (Scott: containers
  // touch Unraid's own bzmodules boot-image loop device once via module
  // autoload, then it sits at 0 forever -- a device row that never has
  // anything to say shouldn't stay in the list saying it, forever). A
  // plain, non-reactive Map -- recordDeviceActivity is the only writer,
  // called from the poll below, same "plain Map, not $state" convention
  // Containers.svelte's own seedTargets uses for the identical reason
  // (nothing here needs to be watched directly; only visibleDevices'
  // own read of it, further down, needs to be reactive).
  const deviceLastActiveAt = new Map();

  $effect(() => {
    const containerName = name;
    async function poll() {
      try {
        const data = await fetchContainerStorage(containerName);
        recordDeviceActivity(data.devices, deviceLastActiveAt, Date.now());
        storageData = data;
      } catch {
        // leave storageData as it is -- see the doc above.
      }
    }
    poll();
    const interval = setInterval(poll, STORAGE_POLL_MS);
    return () => clearInterval(interval);
  });
  let sortedMounts = $derived(storageData ? sortMounts(storageData.mounts) : []);
  // visibleDevices is the Live IO noise rule's own output: only devices
  // active this poll or within RECENT_IO_WINDOW_MS's trailing memory --
  // recomputed whenever storageData changes (a new poll), which is
  // exactly the granularity this needs (STORAGE_POLL_MS is 2s, the
  // window is 60s -- no reason to re-derive between polls). The Total
  // row below deliberately sums storageData.devices, not this -- see its
  // own doc.
  let visibleDevices = $derived(storageData ? recentlyActiveDevices(storageData.devices, deviceLastActiveAt, Date.now()) : []);
  // diskFrame backs each pool/disk/flash mount's own capacity line
  // ("N% full · X free") -- the live frame's per-slot fs.used_bytes/
  // fs.free_bytes (unraid's disks.go, same map Storage.svelte's own
  // disk cards read), joined client-side via mountCapacitySlot rather
  // than fetched separately: this data is already flowing on every SSE
  // tick, no new endpoint needed. A share mount (spans disks, no single
  // slot) or a slot the frame hasn't reported on yet both fall out as
  // "no capacity line" for free -- mountCapacitySlot/diskUsagePct both
  // return null rather than a wrong number, see their own docs.
  let diskFrame = $derived(live.frame?.disks ?? {});
  // diskMetaFrame backs the share-placement note's own pool-kind tint
  // (diskKind, below) -- same live-frame-not-fetched reasoning as
  // diskFrame just above, and correctly reads as "kind unknown" (not a
  // wrong guess) for a pool this frame hasn't reported disk_meta for yet.
  let diskMetaFrame = $derived(live.frame?.disk_meta ?? {});

  let c = $derived(live.frame?.containers?.[name]);
  // isGone: a confirmed "the registry has never heard of this name"
  // reading (not just "the very first frame hasn't landed yet" -- see
  // live.frameCount's own gate on the header's "Not currently running"
  // chip above) -- eventHref is a NEW way to land on a name that's since
  // been fully removed (an old event linking to a container long gone),
  // where the composed per-chart "No X data" empty states below would
  // otherwise stack five deep for something that fundamentally isn't
  // here, rather than saying so once.
  let isGone = $derived(!c && live.frameCount > 0);
  let frameTs = $derived(live.frame?.ts ?? 0);
  let startedAt = $derived(c?.metrics?.['meta.started_at']);
  let restarts = $derived(c?.metrics?.['meta.restart_count']);
  // cpuCoresNow: a quiet "right now" annotation next to the CPU chart's
  // own label -- the chart below plots cpu.pct's host-share history, but
  // leaves its docker-stats-style core count implicit; this reads that
  // straight off the live frame the same way the header facts above do.
  let cpuCoresNow = $derived(fmtCores(c?.metrics?.['cpu.cores'] ?? 0));

  // Dual-perspective usage (Scott: "I want to know how much of the
  // system resources it's consuming, AND how much of it's own allocated
  // resources it's consuming"): host-share stats stay live-frame "now"
  // annotations, same convention as cpuCoresNow above -- NOT tied to
  // activeRange, which only governs the charts' own history. The
  // allocation-relative half is undefined (blank) for an unlimited
  // container, same "absence means unlimited" contract the metrics
  // themselves carry.
  let cpuPctNow = $derived(c?.metrics?.['cpu.pct']);
  let cpuAllocPctNow = $derived(c?.metrics?.['cpu.alloc_pct']);
  let cpuNowText = $derived.by(() => {
    const parts = [];
    if (cpuPctNow !== undefined) parts.push(fmtPct(cpuPctNow));
    if (cpuCoresNow) parts.push(cpuCoresNow);
    return parts.join(' · ');
  });
  let cpuAllocText = $derived(cpuAllocPctNow !== undefined ? `${fmtPct(cpuAllocPctNow)} of its allocation` : '');

  let memBytesNow = $derived(c?.metrics?.['mem.bytes']);
  let memPctNow = $derived(c?.metrics?.['mem.pct']);
  let memLimitPctNow = $derived(c?.metrics?.['mem.limit_pct']);
  let memNowText = $derived.by(() => {
    const parts = [];
    if (memBytesNow !== undefined) parts.push(fmtBytes(memBytesNow));
    if (memPctNow !== undefined) parts.push(`${fmtPct(memPctNow)} of host`);
    return parts.join(' · ');
  });
  let memLimitText = $derived(memLimitPctNow !== undefined ? `${fmtPct(memLimitPctNow)} of its limit` : '');
  // memLimitColor: the ONE number in this whole dual-perspective display
  // that threshold-colors (Scott's own ask), via thresholds.ts' existing
  // container.mem_limit_pct band -- undefined (plain ink) below its own
  // "warn" floor, same as every other banded number in this app.
  let memLimitColor = $derived(memLimitPctNow !== undefined ? bandToken(band('container.mem_limit_pct', memLimitPctNow)) : undefined);

  // --- Impact panel (Phase 5 Task 12) ---------------------------------

  // activeInsights: the live frame's own insights.active block -- no
  // separate fetch, the same "reads straight off the live frame"
  // contract the rest of this page follows.
  let activeInsights = $derived(live.frame?.insights?.active ?? []);
  // hostMetrics backs the share strip's own per-device denominator
  // (deviceSharePct's own doc) -- host/diskio.<dev>.read_bps/.write_bps,
  // already flowing on every SSE tick.
  let hostMetrics = $derived(live.frame?.host ?? {});
  // deviceShares reuses visibleDevices (the Live IO section's own noise-
  // filtered list, above) rather than storageData.devices directly: a
  // device idle long enough to be hidden from Live IO has ~0% share
  // anyway, and showing it here too would just be the same clutter
  // twice.
  let deviceShares = $derived(
    visibleDevices.map((d) => ({
      device: d.device,
      label: d.label,
      sharePct: deviceSharePct(
        d.read_bps,
        d.write_bps,
        hostMetrics[`diskio.${d.device}.read_bps`] ?? 0,
        hostMetrics[`diskio.${d.device}.write_bps`] ?? 0,
      ),
    })),
  );
  // gpuEngineShares: container/<name>/gpu.<eng>.busy_pct is ALREADY a
  // share of that engine (Task 0's own signal inventory), not a raw
  // number needing further division -- only engines this container has
  // actually reported a value for are shown, same conditional-presence
  // rule gpuSeries above already follows.
  let gpuEngineShares = $derived(
    GPU_ENGINE_ORDER.filter((engine) => c?.metrics?.[`gpu.${engine}.busy_pct`] !== undefined).map((engine) => ({
      engine,
      busyPct: c.metrics[`gpu.${engine}.busy_pct`],
    })),
  );

  // pidsNow/pidsLimitNow back the Metadata card's own quiet "142 / 2048"
  // row -- shown only when pidsLimitNow is defined (limited), per the
  // "nothing shown when fully unlimited" rule the whole limits feature
  // follows; see limitsParts below for the same rule applied to memory/
  // CPU/cpuset.
  let pidsNow = $derived(c?.metrics?.['pids']);
  let pidsLimitNow = $derived(c?.metrics?.['pids.limit']);

  // limitsParts backs the header's own "Limits" facts line -- empty when
  // every resource is unlimited, in which case the caller renders no
  // line at all (Scott: "containers that have limits... should list
  // them", implicitly: containers that don't, shouldn't grow any new
  // chrome at all).
  let limitsParts = $derived(
    c
      ? limitsFactsParts({
          memLimitBytes: c.metrics?.['mem.limit_bytes'],
          cpuAllocCores: c.metrics?.['cpu.alloc_cores'],
          pidsLimit: c.metrics?.['pids.limit'],
          cpuset: c.cpuset,
        })
      : [],
  );

  // anomaly/evidence back the "why does this need me" banner -- null
  // (no banner) for a healthy running container or one whose frame
  // hasn't arrived yet; isGone (a confirmed-removed container) also
  // naturally yields null here since c itself is undefined by then.
  let anomaly = $derived(c ? deriveContainerAnomaly(c.state, c.health, c.exit_code) : null);
  let evidence = $derived(anomaly ? containerAnomalyEvidence(events) : []);

  // logsSectionEl backs the banner's own "jump to logs" control --
  // scrollIntoView + a manual focus (tabindex="-1" on the target, see
  // the template below) rather than a plain href="#..." anchor: this
  // app's whole router treats location.hash as A ROUTE (router.ts parses
  // the ENTIRE hash on every change), so a bare in-page named anchor
  // would be read as an unknown route and land on Not Found instead of
  // scrolling anywhere.
  let logsSectionEl = $state();
  function scrollToLogs() {
    logsSectionEl?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    logsSectionEl?.focus?.();
  }
</script>

<div class="container-detail">
  <div class="container-detail__header">
    <div class="container-detail__identity">
      <ContainerIcon {name} icon={c?.icon} size={28} />
      <h1 class="page-title container-detail__title">{name}</h1>
      {#if c}
        <HealthDot status={containerHealthStatus(c.state, c.health)} label={c.state} />
      {:else if live.frameCount > 0}
        <span class="microlabel container-detail__not-live">Not currently running</span>
      {/if}
    </div>
    {#if c}
      <div class="container-detail__facts">
        <span class="tabular-nums">{c.image}</span>
        <span class="tabular-nums">
          Uptime {startedAt !== undefined ? fmtDuration(frameTs - startedAt) : '—'}
        </span>
        <span class="tabular-nums">Restarts {restarts ?? '—'}</span>
      </div>
      {#if limitsParts.length > 0}
        <div class="container-detail__facts container-detail__limits tabular-nums">
          <span>Limits: {limitsParts.join(' · ')}</span>
        </div>
      {/if}
    {/if}
  </div>

  {#if anomaly}
    <AnomalyBanner severity={anomaly.severity} headline={anomaly.headline} {evidence} onJumpToLogs={scrollToLogs} />
  {/if}

  <div class="segmented" role="group" aria-label="Time range">
    {#each RANGES as r (r.key)}
      <button
        type="button"
        class="segmented__btn"
        class:segmented__btn--active={activeRange === r.key}
        onclick={() => (activeRange = r.key)}
      >
        {r.label}
      </button>
    {/each}
  </div>

  {#if fetchFailed}
    <p class="microlabel container-detail__fetch-error">Couldn't load history for this range. Try again shortly.</p>
  {:else if fetchInFlight}
    <p class="microlabel container-detail__loading">Loading…</p>
  {/if}

  {#if isGone}
    <p class="microlabel container-detail__gone">This container is no longer present.</p>
  {:else}
  <div class="container-detail__charts">
    <div class="card container-detail__chart-card">
      <div class="container-detail__chart-head">
        <span class="microlabel">CPU</span>
        <div class="container-detail__chart-stats">
          {#if cpuNowText}<span class="container-detail__chart-now">{cpuNowText}</span>{/if}
          {#if cpuAllocText}<span class="container-detail__chart-now">{cpuAllocText}</span>{/if}
        </div>
      </div>
      {#if hasPoints('cpu.pct')}
        <TimeChart series={cpuSeries} formatValue={fmtPct} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} xDomain={activeRange === 'live' ? undefined : fetchedRange} />
      {:else if activeRange === 'live' && liveSeedPending}
        <!-- Live ring is still cold AND we don't yet know whether the seed
             found real history -- rendering nothing here (rather than the
             empty message below) avoids a false "no CPU data" flash before
             the seed has actually settled. Same gate as GPUEntityCard's own
             template, repeated per chart card below. -->
      {:else}
        <p class="microlabel container-detail__empty">No CPU data for this range.</p>
      {/if}
    </div>
    <div class="card container-detail__chart-card">
      <div class="container-detail__chart-head">
        <span class="microlabel">Memory</span>
        <div class="container-detail__chart-stats">
          {#if memNowText}<span class="container-detail__chart-now">{memNowText}</span>{/if}
          {#if memLimitText}
            <span class="container-detail__chart-now" style={memLimitColor ? `color: ${memLimitColor}` : undefined}>
              {memLimitText}
            </span>
          {/if}
        </div>
      </div>
      {#if hasPoints('mem.bytes')}
        <TimeChart series={memSeries} formatValue={fmtBytes} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} xDomain={activeRange === 'live' ? undefined : fetchedRange} />
      {:else if activeRange === 'live' && liveSeedPending}
        <!-- see the CPU card's own doc above -->
      {:else}
        <p class="microlabel container-detail__empty">No memory data for this range.</p>
      {/if}
    </div>
    <div class="card container-detail__chart-card">
      <span class="microlabel">Network</span>
      {#if hasPoints('net.rx_bps') || hasPoints('net.tx_bps')}
        <TimeChart series={netSeries} formatValue={fmtRate} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} xDomain={activeRange === 'live' ? undefined : fetchedRange} />
      {:else if activeRange === 'live' && liveSeedPending}
        <!-- see the CPU card's own doc above -->
      {:else}
        <p class="microlabel container-detail__empty">No network data for this range.</p>
      {/if}
    </div>
    <div class="card container-detail__chart-card">
      <span class="microlabel">Disk IO</span>
      {#if hasPoints('io.read_bps') || hasPoints('io.write_bps')}
        <TimeChart series={ioSeries} formatValue={fmtRate} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} xDomain={activeRange === 'live' ? undefined : fetchedRange} />
      {:else if activeRange === 'live' && liveSeedPending}
        <!-- see the CPU card's own doc above -->
      {:else}
        <p class="microlabel container-detail__empty">No disk IO data for this range.</p>
      {/if}
    </div>
    {#if gpuSeries.length > 0}
      <div class="card container-detail__chart-card">
        <span class="microlabel">GPU</span>
        <TimeChart series={gpuSeries} formatValue={fmtPct} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} xDomain={activeRange === 'live' ? undefined : fetchedRange} />
      </div>
    {/if}
    {#if psiSeries.length > 0}
      <div class="card container-detail__chart-card">
        <span class="microlabel">Pressure (PSI)</span>
        <TimeChart series={psiSeries} formatValue={fmtPct} {markers} syncKey={SYNC_KEY} live={activeRange === 'live'} xDomain={activeRange === 'live' ? undefined : fetchedRange} />
      </div>
    {/if}
  </div>
  {/if}

  {#if storageData}
    <div class="card container-detail__storage">
      <span class="microlabel">Storage</span>

      <div class="container-detail__storage-section">
        <span class="microlabel">Mounts</span>
        {#if sortedMounts.length === 0}
          <p class="microlabel container-detail__storage-empty">No mounts for this container.</p>
        {:else}
          <!-- One grid, 4 fixed-width tracks (repeated at >=1200px -- see
               this class's own style doc): a mount's paths/badge/capacity
               used to be two flex rows that only ever aligned with
               THEMSELVES, never with the mount above or below it, let
               alone the other CSS column at wide widths. display:contents
               on .storage-mount lifts its own children straight into this
               grid so every field lands in the same column, every row. -->
          <div class="container-detail__storage-mounts">
            {#each sortedMounts as mount (mount.destination)}
              {@const kind = normalizeStorageKind(mount.storage.kind)}
              {@const capSlot = mountCapacitySlot(mount)}
              {@const usagePct = capSlot ? diskUsagePct(diskFrame[capSlot]) : null}
              {@const placementPool = mount.storage.placement?.pool}
              {@const placementKind = placementPool ? diskKind(diskMetaFrame[placementPool], diskFrame[placementPool]) : null}
              {@const placementText = sharePlacementText(mount.storage.placement, placementKind)}
              <div class="storage-mount">
                <span class="storage-mount__dest" title={mount.destination}>{mount.destination}</span>
                <span class="storage-mount__source-cell">
                  <span class="storage-mount__arrow" aria-hidden="true">&larr;</span>
                  <span class="storage-mount__source" title={mount.source}>{mount.source}</span>
                </span>
                <span class="storage-mount__badge-cell">
                  <span class="storage-mount__badge-row">
                    <span class="storage-mount__badge storage-mount__badge--{kind}" title={STORAGE_KIND_TITLE[kind]}>
                      {STORAGE_KIND_LABEL[kind]}{mount.storage.name ? ` · ${mount.storage.name}` : ''}
                    </span>
                    {#if !mount.rw}<span class="storage-mount__ro">ro</span>{/if}
                  </span>
                  {#if placementText}
                    <span class="storage-mount__placement {placementKind ? `storage-mount__placement--${placementKind}` : ''}">
                      {placementText}
                    </span>
                  {/if}
                </span>
                <span class="storage-mount__capacity-cell tabular-nums">
                  {#if usagePct !== null}
                    {fmtPct(usagePct)} full &middot; {fmtBytes(diskFrame[capSlot]['fs.free_bytes'])} free
                  {/if}
                </span>
              </div>
            {/each}
          </div>
        {/if}
      </div>

      {#if storageData.devices.length > 0}
        <div class="container-detail__storage-section">
          <span class="microlabel">Live IO</span>
          {#if visibleDevices.length === 0}
            <!-- Every device this container touches has been idle for a
                 full RECENT_IO_WINDOW_MS -- e.g. bzmodules, autoloaded
                 once at container start then untouched forever (Scott:
                 "what is bzmodules?"). The Total row would just read a
                 permanent 0 B/s here, so it's skipped too rather than
                 shown alongside a message that already says as much. -->
            <p class="microlabel container-detail__storage-empty">No recent disk IO.</p>
          {:else}
            <!-- Same display:contents grid technique as the mounts list
                 above, minus the 2-CSS-column split (a container rarely
                 has more than a handful of backing devices, so there's
                 no wide-viewport case to fill). The header row's own 5
                 cells are the grid's first row, sharing its column
                 tracks -- Read/Write name the two right-aligned value
                 columns once instead of repeating the word on every row. -->
            <div class="container-detail__storage-devices">
              <span class="storage-device-header" aria-hidden="true"></span>
              <span class="storage-device-header" aria-hidden="true"></span>
              <span class="storage-device-header" aria-hidden="true"></span>
              <span class="storage-device-header storage-device-header--value" aria-hidden="true">Read</span>
              <span class="storage-device-header storage-device-header--value" aria-hidden="true">Write</span>
              {#each visibleDevices as d (d.device)}
                <StorageDeviceRow entry={d} />
              {/each}
              <!-- Sums ALL of storageData.devices, not just visibleDevices
                   -- an idle-but-hidden device still contributed 0 to this
                   container's real total, and staying truthful about that
                   matters more here than staying visually in sync with
                   whichever rows happen to be shown above it. -->
              <StorageTotalRow devices={storageData.devices} />
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}

  {#if c}
    <ImpactPanel
      containerName={name}
      insights={activeInsights}
      cpuPct={cpuPctNow}
      memPct={memPctNow}
      devices={deviceShares}
      gpuEngines={gpuEngineShares}
    />
  {/if}

  <div class="card container-detail__metadata">
    <span class="microlabel">Metadata</span>
    <dl class="container-detail__meta-list">
      <dt>Image</dt>
      <dd>{c?.image ?? '—'}</dd>
      <dt>State</dt>
      <dd>{c?.state ?? 'not currently running'}</dd>
      <dt>Started</dt>
      <dd>{startedAt !== undefined ? new Date(startedAt * 1000).toLocaleString() : '—'}</dd>
      <dt>Restarts</dt>
      <dd>{restarts ?? '—'}</dd>
      {#if pidsLimitNow !== undefined}
        <dt>Pids</dt>
        <dd>{pidsNow !== undefined ? Math.round(pidsNow) : '—'} / {Math.round(pidsLimitNow)}</dd>
      {/if}
    </dl>
  </div>

  <div class="card container-detail__logs" bind:this={logsSectionEl} tabindex="-1">
    <span class="microlabel">Logs</span>
    <LogViewer {name} />
  </div>
</div>

<style>
  .container-detail {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .container-detail__header {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .container-detail__identity {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .container-detail__title {
    margin: 0;
  }
  .container-detail__not-live {
    color: var(--status-warning);
  }
  .container-detail__facts {
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--ink-2);
  }
  /* Limits reuses .container-detail__facts' own typography (same row
     visually continues the identity summary just above it) -- this
     class only adds the small gap that separates it as its own line. */
  .container-detail__limits {
    margin-top: -0.1rem;
  }
  .container-detail__fetch-error {
    color: var(--status-warning);
  }
  .container-detail__loading {
    margin: 0;
  }
  .container-detail__charts {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
  }
  /* GPU/PSI only render their own card when that data exists (see the
     template above), so the grid can land at an ODD total (4 fixed +
     0-2 conditional) -- an unpaired last card would otherwise leave its
     row's second cell dead. Span it full-width instead, matching the
     wasted-space rule everywhere else in this rollout: reproduced with
     GPU present, PSI absent (5 cards). */
  .container-detail__chart-card:last-child:nth-child(odd) {
    grid-column: 1 / -1;
  }
  @media (max-width: 47.9375rem) {
    .container-detail__charts {
      grid-template-columns: 1fr;
    }
  }
  /* min-width:0 -- a grid item's default min-width:auto lets the uPlot
     canvas inside (a replaced element whose width is baked in literal
     pixels at build/setSize time) act as its track's minimum, so once
     the content box narrows (window resize, a vertical scrollbar
     appearing) the 1fr tracks above physically can't shrink and the
     cards overrun the page sideways. Worse, that state is permanent:
     TimeChart's own ResizeObserver never gets to re-fit the chart,
     because the .time-chart it watches is held at its stale width by
     the very canvas it would resize. Reproduced live at 1440 -> 1200
     (cards stuck ~210px past the viewport, whole page scrolling
     horizontally). With the minimum released the cards shrink with
     their tracks, the observer fires, and the canvas follows within a
     frame -- same idiom as .stat-tile, the other fixed-pixel-canvas
     host. */
  .container-detail__chart-card {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0;
  }
  .container-detail__chart-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
  }
  .container-detail__chart-now {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  /* Dual-perspective usage: up to two stat lines (host-relative, then
     allocation-relative) stacked right-aligned under the chart-head's
     own baseline row, rather than crammed onto one line -- reproduced at
     375px, where both figures inline together ran past the card edge. */
  .container-detail__chart-stats {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.1rem;
  }
  .container-detail__empty {
    margin: 2rem 0;
    text-align: center;
  }
  .container-detail__gone {
    margin: 2rem 0;
    text-align: center;
  }
  .container-detail__storage,
  .container-detail__metadata,
  .container-detail__logs {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .container-detail__storage-section {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .container-detail__storage-empty {
    margin: 0;
  }
  /* A real grid, 4 fixed-width tracks -- doubled at >=1200px (one shared
     template, not two independent ones), so destination/source/badge/
     capacity land in the same track for every mount, in EITHER CSS
     column, not just within one. The previous `columns: 2` masonry
     layout could only ever align a mount with itself: each visual
     column there is its own independent flow with no shared row grid,
     so nothing guaranteed the left and right groups' fields ever lined
     up (Scott: "try to line things up a little better here"). Fixed
     tracks beat `auto`/content-sized ones for the same reason -- a
     content-sized column's width comes from ITS OWN group's widest
     value, which can differ left vs. right. */
  .container-detail__storage-mounts {
    display: grid;
    grid-template-columns: minmax(4.5rem, 7rem) minmax(5rem, 1fr) minmax(6rem, 8.5rem) minmax(8rem, 11rem);
    column-gap: 1.25rem;
    align-items: baseline;
  }
  /* Doubled at >=1200px -- see this class's own doc for why one shared
     template, not two independent ones. Source's own minimum can't be 0
     here the way a plain `1fr` would default it: reproduced at exactly
     1200-1440px (this card's real available width once the sidebar and
     card padding are subtracted), where dest+badge+capacity's OWN
     minimums for both groups already consumed every pixel this card
     had, leaving 1fr nothing to distribute and the whole source column
     -- and every mount's own host path -- invisible. A real (if narrow)
     minimum floor for source, and smaller minimums for the other three
     (their own vocabulary is short and bounded -- "Share \xb7 appdata"-
     length text -- unlike an arbitrary host path), leaves this workable
     at the ask's own 1200px rather than needing a much wider one. */
  @media (min-width: 75rem) {
    .container-detail__storage-mounts {
      grid-template-columns: repeat(2, minmax(4.5rem, 7rem) minmax(5rem, 1fr) minmax(6rem, 8.5rem) minmax(8rem, 11rem));
    }
  }
  /* Narrow phones: the 4-track row above needs ~27rem just for its 3
     fixed-minimum columns (dest+badge+capacity) before source gets
     anything at all -- reproduced at 375px, where source's own
     minmax(0, ...) floor let it shrink straight to 0 and its whole path
     silently vanished rather than visibly overflowing. Two tracks
     instead, source paired under dest and capacity under badge (the
     same two-line grouping this section used before the grid rewrite),
     still column-aligned across mounts, just narrower. */
  @media (max-width: 36rem) {
    .container-detail__storage-mounts {
      grid-template-columns: minmax(5rem, auto) minmax(0, 1fr);
      row-gap: 0.15rem;
    }
    .storage-mount > :nth-child(1),
    .storage-mount > :nth-child(2) {
      padding-bottom: 0.15rem;
      border-bottom: none;
    }
    .storage-mount > :nth-child(3),
    .storage-mount > :nth-child(4) {
      padding-top: 0.15rem;
    }
    .storage-mount__capacity-cell {
      text-align: left;
    }
  }
  .container-detail__storage-devices {
    display: grid;
    grid-template-columns: auto minmax(3rem, 1fr) auto auto auto;
    column-gap: 0.75rem;
    align-items: center;
  }
  /* Narrow phones: 5 real columns of content (label/device/kind/read/
     write) don't fit this card's own width even at auto sizing --
     reproduced at 375px, where Write's own column ran past the card's
     right edge. StorageDeviceRow/StorageTotalRow fall back to a plain
     wrapping flex row at this same breakpoint (see their own docs); the
     header row assumed 5 shared grid columns, so it's dropped rather
     than shown misaligned -- each row still names Read/Write inline via
     its own aria-label, same as always. */
  @media (max-width: 36rem) {
    .container-detail__storage-devices {
      display: flex;
      flex-direction: column;
    }
    .storage-device-header {
      display: none;
    }
  }
  /* display:contents lifts a mount's own 4 cells straight into the grid
     above as its next row -- see that class's own doc for why this
     replaced the old flex-row-per-mount layout. Every cell below (not
     just .storage-mount itself) carries the row's padding/hairline,
     since display:contents leaves .storage-mount generating no box of
     its own to put either on. */
  .storage-mount {
    display: contents;
  }
  .storage-mount > * {
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    min-width: 0;
  }
  .storage-mount__dest {
    font-size: 0.85rem;
    color: var(--ink);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .storage-mount__source-cell {
    display: flex;
    align-items: baseline;
    gap: 0.4rem;
    overflow: hidden;
  }
  .storage-mount__arrow {
    color: var(--ink-2);
    flex-shrink: 0;
  }
  .storage-mount__source {
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--ink-2);
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Column, not the single row this used to be -- share placement
     (storage-mount__placement, below) needs its own line under the
     badge+ro row rather than crowding into it as a third wrapped chip;
     every OTHER kind's badge-cell is unaffected, it just has nothing to
     stack under it. */
  .storage-mount__badge-cell {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.2rem;
    overflow: hidden;
  }
  .storage-mount__badge-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  /* Share placement (Scott: "you can see that the downloads share is
     used, but you don't know that the drive it's stored on is the nvme
     cache drive... we need to connect the dots") -- plain muted text by
     default (mode "yes"/"no", or "only"/"prefer" onto a pool whose kind
     isn't known yet), tinted to match the pool's own kind once diskKind
     resolves it, same three accent colors storage-mount__badge--pool's
     sibling badges and Storage.svelte's own disk-media badges already
     use for ssd/nvme/usb -- hdd deliberately stays untinted, same "the
     ordinary case doesn't need to stand out" rule as everywhere else
     this vocabulary appears. */
  .storage-mount__placement {
    font-size: 0.68rem;
    color: var(--ink-2);
  }
  .storage-mount__placement--ssd {
    color: var(--series-3);
  }
  .storage-mount__placement--nvme {
    color: var(--series-1);
  }
  .storage-mount__placement--usb {
    color: var(--series-4);
  }
  /* Storage-system badge: same tinted-pill recipe as Storage.svelte's
     own disk media badges (storage-disk__media) -- a neutral chip for
     "share" (the ordinary/majority case, same role hdd plays there),
     one accent color per other kind. See the STORAGE_KIND_LABEL doc
     above for why these four particular tokens. */
  .storage-mount__badge {
    display: inline-flex;
    align-items: center;
    padding: 0.15rem 0.5rem;
    border-radius: 999px;
    font-size: 0.72rem;
    color: var(--ink-2);
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    white-space: nowrap;
  }
  .storage-mount__badge--pool {
    color: var(--series-3);
    background: color-mix(in oklab, var(--series-3) 12%, transparent);
  }
  .storage-mount__badge--disk {
    color: var(--series-5);
    background: color-mix(in oklab, var(--series-5) 12%, transparent);
  }
  .storage-mount__badge--flash {
    color: var(--series-6);
    background: color-mix(in oklab, var(--series-6) 12%, transparent);
  }
  .storage-mount__badge--other {
    color: var(--series-7);
    background: color-mix(in oklab, var(--series-7) 12%, transparent);
  }
  .storage-mount__capacity-cell {
    color: var(--ink-2);
    font-size: 0.72rem;
    text-align: right;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .storage-mount__ro {
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.15rem 0.45rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
    white-space: nowrap;
  }
  /* Live IO's own header row -- its 5 cells are this grid's first row
     (see the template above), naming the two right-aligned value
     columns once instead of every row repeating "Read"/"Write" as
     inline text the way this section used to. */
  .storage-device-header {
    padding: 0 0 0.4rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--ink-2);
  }
  .storage-device-header--value {
    text-align: right;
  }
  .container-detail__meta-list {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  .container-detail__meta-list dt {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .container-detail__meta-list dd {
    margin: 0;
    font-size: 0.85rem;
  }
</style>

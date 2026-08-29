<!--
  Compare: multi-container detail view -- "I have multiple containers
  that work together as a team for an app, and I would want to see how
  they're all working together" (Scott's own ask). Driven entirely by the
  route's own comma-joined name list (router.ts's 'compare' route,
  lib/compareRoute.ts's parse/build helpers): bookmarkable/shareable, no
  server-side "comparison" concept at all -- every number here is derived
  client-side from the same live frame and /api/series calls every other
  view already uses.

  Layout: header (member chips + group totals, both always LIVE/current,
  independent of the range picker below) -> one chart per metric (CPU/
  Memory/Net/IO, GPU only when at least one member has GPU data) -> a
  compact per-member detail table. Charts share one uPlot syncKey (the
  same "multiple TimeCharts on one page, one synced crosshair" contract
  ContainerDetail's own CPU/Mem/Net/IO/GPU/PSI set already uses) rather
  than the separate lib/scrubbus.svelte.ts module: that bus is Sparkline/
  ContainerRow's own hover-driven mechanism for a different chart
  primitive (plain inline SVG, no crosshair of its own) -- TimeChart
  already has a proven, simpler answer to "keep several charts scrubbing
  together" for its own uPlot canvases.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { motion } from '../lib/motion.svelte';
  import { live } from '../lib/sse.svelte';
  import { pushRing } from '../lib/livering';
  import { sumSeriesPoints } from '../lib/metrics';
  import { resourceMetricKeys, resourceScaleMax } from '../lib/topFromFrame';
  import { fetchSeries } from '../lib/api';
  import { fmtBytes, fmtCores, fmtPct, fmtRate } from '../lib/format';
  import { buildCompareHash, knownCompareNames, MAX_COMPARE_MEMBERS, parseCompareNames } from '../lib/compareRoute';
  import { seriesColorVar } from '../lib/compareColors';
  import { computeGroupTotals } from '../lib/compareTotals';
  import { groups } from '../lib/groups.svelte';
  import ContainerIcon from '../components/ContainerIcon.svelte';
  import TimeChart from '../components/TimeChart.svelte';
  import CompareMemberRow from '../components/CompareMemberRow.svelte';

  // names: the route's raw, already-single-decoded ":names" param
  // (App.svelte passes $route.params.names straight through -- see
  // router.ts's own doc on why a second per-name decode isn't needed).
  let { names = undefined } = $props();

  const SYNC_KEY = 'compare';
  const LIVE_WINDOW_SEC = 900;
  const GPU_METRIC_KEYS = resourceMetricKeys('gpu');
  const ALL_METRICS = ['cpu.pct', 'mem.bytes', 'net.rx_bps', 'net.tx_bps', 'io.read_bps', 'io.write_bps', ...GPU_METRIC_KEYS];
  const RANGES = [
    { key: 'live', label: 'Live · 15m' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
  ];
  const RANGE_SECONDS = { '1h': 3600, '24h': 86400, '7d': 7 * 86400, '30d': 30 * 86400 };

  let activeRange = $state('live');

  // requestedNames: every name the URL asks for, in URL order (parsed,
  // deduped) -- the header/totals/table read this list UNCAPPED, so a
  // stale or removed member still shows up (as a "no longer present"
  // row/chip the user can remove) rather than silently vanishing.
  let requestedNames = $derived(parseCompareNames(names));

  // knownForCharts/chartMembers: the chart/legend-color-eligible subset --
  // filtered to names the live frame actually knows about FIRST, then
  // capped to MAX_COMPARE_MEMBERS, so a stale bookmarked name never
  // occupies one of the 10 precious chart slots ahead of a real member.
  // Before the first frame has ever landed (live.frameCount === 0) every
  // requested name is provisionally treated as known -- same "don't
  // trust a not-yet-populated frame's absence as a real absence" gate
  // ContainerDetail's own isGone uses -- so a fresh page load doesn't
  // flash "showing 0 of N" before the frame has had a chance to answer.
  let knownForCharts = $derived(
    live.frameCount > 0 ? knownCompareNames(requestedNames, live.frame?.containers ?? {}) : requestedNames,
  );
  let chartMembers = $derived(knownForCharts.slice(0, MAX_COMPARE_MEMBERS));
  let chartsCapped = $derived(knownForCharts.length > MAX_COMPARE_MEMBERS);

  // memberColor: chart-eligible members get their assigned series color
  // (position in chartMembers, matching every chart's own series order);
  // anything past the 10-member cap (or not currently known) gets no
  // assigned color at all -- it isn't drawn on any chart, so a categorical
  // hue there would falsely imply otherwise.
  let memberColor = $derived(new Map(chartMembers.map((name, i) => [name, seriesColorVar(i)])));

  function removeHref(name) {
    return buildCompareHash(requestedNames.filter((n) => n !== name));
  }

  // --- Save as group: persists the currently-requested member set
  // (requestedNames -- every member in the URL, uncapped by
  // MAX_COMPARE_MEMBERS, the same set the header's own chips/totals
  // read) as a named group via groups.svelte's shared store, so it
  // shows up as a chip in the Containers view's own Groups row. -------
  let saveGroupOpen = $state(false);
  let saveGroupName = $state('');
  let saveGroupSaved = $state(false);

  function openSaveGroup() {
    saveGroupName = '';
    saveGroupSaved = false;
    saveGroupOpen = true;
  }
  function cancelSaveGroup() {
    saveGroupOpen = false;
  }
  async function submitSaveGroup() {
    const name = saveGroupName.trim();
    if (!name) return;
    const ok = await groups.saveAsGroup(name, requestedNames);
    if (ok) {
      saveGroupOpen = false;
      saveGroupSaved = true;
    }
  }

  // --- Live ring slots: a fixed pool of MAX_COMPARE_MEMBERS, same "can't
  // create $state dynamically for an arbitrary changing set" constraint
  // (and same fix) as the Metrics page's own heroSlots -- see
  // TopConsumers.svelte's makeHeroSlot doc. Generalized to every metric a
  // chart below might need, in one bundle per slot, rather than one pool
  // per metric: a slot's identity (which member currently occupies it)
  // is shared across all of that member's own metrics.
  function makeCompareSlot() {
    let cpu = $state([]);
    let mem = $state([]);
    let netRx = $state([]);
    let netTx = $state([]);
    let ioRead = $state([]);
    let ioWrite = $state([]);
    let gpu = $state([]);
    let assigned = null;

    return {
      get cpu() {
        return cpu;
      },
      get mem() {
        return mem;
      },
      get netRx() {
        return netRx;
      },
      get netTx() {
        return netTx;
      },
      get ioRead() {
        return ioRead;
      },
      get ioWrite() {
        return ioWrite;
      },
      get gpu() {
        return gpu;
      },
      // tick resets every one of this slot's rings the instant its
      // assigned member changes (add/remove/reorder) -- otherwise a
      // reassignment would paste one container's history directly onto
      // another's, reading as an impossible instant jump (same reasoning
      // as heroSlots' own doc). untrack wraps the reads+writes below for
      // the identical reason theirs does: this runs from inside the
      // driving $effect further down, which must depend on live.frame/
      // chartMembers ONLY, not on these rings' own current values.
      tick(ts, name, c) {
        if (name !== assigned) {
          assigned = name;
          untrack(() => {
            cpu = [];
            mem = [];
            netRx = [];
            netTx = [];
            ioRead = [];
            ioWrite = [];
            gpu = [];
          });
        }
        if (!name || !c) return;
        const m = c.metrics ?? {};
        untrack(() => {
          if (m['cpu.pct'] !== undefined) cpu = pushRing(cpu, ts, m['cpu.pct'], LIVE_WINDOW_SEC);
          if (m['mem.bytes'] !== undefined) mem = pushRing(mem, ts, m['mem.bytes'], LIVE_WINDOW_SEC);
          if (m['net.rx_bps'] !== undefined) netRx = pushRing(netRx, ts, m['net.rx_bps'], LIVE_WINDOW_SEC);
          if (m['net.tx_bps'] !== undefined) netTx = pushRing(netTx, ts, m['net.tx_bps'], LIVE_WINDOW_SEC);
          if (m['io.read_bps'] !== undefined) ioRead = pushRing(ioRead, ts, m['io.read_bps'], LIVE_WINDOW_SEC);
          if (m['io.write_bps'] !== undefined) ioWrite = pushRing(ioWrite, ts, m['io.write_bps'], LIVE_WINDOW_SEC);
          let gpuSum;
          for (const key of GPU_METRIC_KEYS) {
            if (m[key] === undefined) continue;
            gpuSum = (gpuSum ?? 0) + m[key];
          }
          if (gpuSum !== undefined) gpu = pushRing(gpu, ts, gpuSum, LIVE_WINDOW_SEC);
        });
      },
    };
  }
  const compareSlots = Array.from({ length: MAX_COMPARE_MEMBERS }, () => makeCompareSlot());

  $effect(() => {
    if (activeRange !== 'live') return;
    const frame = live.frame;
    if (!frame) return;
    const members = chartMembers;
    for (let i = 0; i < MAX_COMPARE_MEMBERS; i++) {
      const name = members[i] ?? null;
      compareSlots[i].tick(frame.ts, name, name ? frame.containers?.[name] : undefined);
    }
  });

  // --- Fetched (non-live) series: one /api/series call per chart member,
  // same top-10-style request bounding the Metrics page's own hero chart
  // uses (fetchedHeroSeries) -- fired together, keyed by member name so
  // pointsFor below can look a member up regardless of which slot it
  // currently occupies.
  let fetchedByMember = $state({});
  let fetchInFlight = $state(false);
  let fetchFailed = $state(false);

  $effect(() => {
    const range = activeRange;
    const members = chartMembers;
    if (range === 'live') {
      fetchedByMember = {};
      fetchFailed = false;
      fetchInFlight = false;
      return;
    }
    if (members.length === 0) {
      fetchedByMember = {};
      return;
    }
    const seconds = RANGE_SECONDS[range];
    const to = Math.floor(Date.now() / 1000);
    const from = to - seconds;
    const controller = new AbortController();
    fetchInFlight = true;
    fetchFailed = false;
    Promise.all(
      members.map((name) =>
        fetchSeries({ kind: 'container', entity: name, metrics: ALL_METRICS, from, to, signal: controller.signal }).then(
          (results) => ({ name, results }),
        ),
      ),
    )
      .then((perMember) => {
        const byMember = {};
        for (const { name, results } of perMember) {
          const byMetric = {};
          for (const r of results) byMetric[r.metric] = r.points;
          byMember[name] = byMetric;
        }
        fetchedByMember = byMember;
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // superseded by a newer range/member-set switch
        fetchedByMember = {};
        fetchFailed = true;
      })
      .finally(() => {
        if (!controller.signal.aborted) fetchInFlight = false;
      });
    return () => controller.abort();
  });

  function fetchedPoints(name, metric) {
    return fetchedByMember[name]?.[metric] ?? [];
  }

  // --- Per-metric series, one colored line per chart member -----------
  let cpuSeries = $derived(
    chartMembers.map((name, i) => ({
      label: name,
      points: activeRange === 'live' ? compareSlots[i].cpu : fetchedPoints(name, 'cpu.pct'),
      colorVar: seriesColorVar(i),
    })),
  );
  let memSeries = $derived(
    chartMembers.map((name, i) => ({
      label: name,
      points: activeRange === 'live' ? compareSlots[i].mem : fetchedPoints(name, 'mem.bytes'),
      colorVar: seriesColorVar(i),
    })),
  );
  let netSeries = $derived(
    chartMembers.map((name, i) => {
      const rx = activeRange === 'live' ? compareSlots[i].netRx : fetchedPoints(name, 'net.rx_bps');
      const tx = activeRange === 'live' ? compareSlots[i].netTx : fetchedPoints(name, 'net.tx_bps');
      return {
        label: name,
        points: sumSeriesPoints([rx, tx]),
        colorVar: seriesColorVar(i),
        directionPoints: [rx, tx],
        directionLabels: ['↓', '↑'],
      };
    }),
  );
  let ioSeries = $derived(
    chartMembers.map((name, i) => {
      const read = activeRange === 'live' ? compareSlots[i].ioRead : fetchedPoints(name, 'io.read_bps');
      const write = activeRange === 'live' ? compareSlots[i].ioWrite : fetchedPoints(name, 'io.write_bps');
      return {
        label: name,
        points: sumSeriesPoints([read, write]),
        colorVar: seriesColorVar(i),
        directionPoints: [read, write],
        directionLabels: ['r', 'w'],
      };
    }),
  );
  let gpuSeries = $derived(
    chartMembers.map((name, i) => ({
      label: name,
      points:
        activeRange === 'live' ? compareSlots[i].gpu : sumSeriesPoints(GPU_METRIC_KEYS.map((k) => fetchedPoints(name, k))),
      colorVar: seriesColorVar(i),
    })),
  );
  // hasGpu gates the whole GPU chart card: "GPU only if any member has
  // gpu metrics" -- a member with none simply draws an empty line
  // alongside everyone else's, same as ContainerDetail's own per-engine
  // gpuSeries.length>0 gate, just checked across members instead of
  // engines.
  let hasGpu = $derived(gpuSeries.some((s) => s.points.length > 0));

  // --- Charts: bind:this refs so a chip hover can focus the SAME member
  // across every chart at once (TimeChart's own exported focusSeries,
  // the Metrics hero's own hook -- just fanned out to several chart
  // instances instead of one). Series order is IDENTICAL across every
  // chart (chartMembers, unchanged), so index i always means the same
  // member in every one of them.
  let cpuChart = $state(undefined);
  let memChart = $state(undefined);
  let netChart = $state(undefined);
  let ioChart = $state(undefined);
  let gpuChart = $state(undefined);

  function focusAll(idx) {
    cpuChart?.focusSeries(idx);
    memChart?.focusSeries(idx);
    netChart?.focusSeries(idx);
    ioChart?.focusSeries(idx);
    gpuChart?.focusSeries(idx);
  }

  // --- Group totals: always live/current, independent of activeRange
  // (see this file's own module doc) -- a plain sum over EVERY requested
  // member (uncapped: cheap, no network request, just a live.frame read),
  // so the totals row stays truthful even past the charts' own 10-member
  // cap.
  let groupTotals = $derived(
    computeGroupTotals(requestedNames, live.frame?.containers ?? {}, resourceScaleMax('mem', live.frame)),
  );

  function tweenTo(tween, value) {
    tween.set(value, { duration: motion.reduced ? 0 : live.glideMs, easing: linear });
  }
  let totalCpuPctTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalCpuCoresTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalMemBytesTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalMemHostPctTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalNetRxTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalNetTxTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalIoReadTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let totalIoWriteTween = new Tween(0, { duration: live.glideMs, easing: linear });

  $effect(() => tweenTo(totalCpuPctTween, groupTotals.cpuPct));
  $effect(() => tweenTo(totalCpuCoresTween, groupTotals.cpuCores));
  $effect(() => tweenTo(totalMemBytesTween, groupTotals.memBytes));
  $effect(() => tweenTo(totalMemHostPctTween, groupTotals.memHostPct ?? 0));
  $effect(() => tweenTo(totalNetRxTween, groupTotals.netRxBps));
  $effect(() => tweenTo(totalNetTxTween, groupTotals.netTxBps));
  $effect(() => tweenTo(totalIoReadTween, groupTotals.ioReadBps));
  $effect(() => tweenTo(totalIoWriteTween, groupTotals.ioWriteBps));

  let totalCoresLabel = $derived(fmtCores(totalCpuCoresTween.current));
</script>

{#if requestedNames.length < 2}
  <div class="compare compare--hint">
    <h1 class="page-title">Compare</h1>
    <p class="microlabel">
      {requestedNames.length === 0 ? 'No containers selected.' : 'Select at least one more container to compare.'}
    </p>
    <a class="compare__hint-link" href="#/containers">&larr; Back to Containers</a>
  </div>
{:else}
  <div class="compare">
    <h1 class="page-title">Compare</h1>

    <div class="card compare__header">
      <div class="compare__members" role="group" aria-label="Compared containers">
        {#each requestedNames as name (name)}
          {@const color = memberColor.get(name)}
          {@const c = live.frame?.containers?.[name]}
          <span
            class="compare__chip"
            class:compare__chip--uncharted={!color}
            style={color ? `--chip-color: var(${color})` : undefined}
            role="group"
            aria-label={`${name}, chart line`}
            onmouseenter={() => color && focusAll(chartMembers.indexOf(name) + 1)}
            onmouseleave={() => color && focusAll(null)}
          >
            <ContainerIcon {name} icon={c?.icon} size={16} />
            <span>{name}</span>
            <a class="compare__chip-remove" href={removeHref(name)} aria-label={`Remove ${name} from comparison`}>✕</a>
          </span>
        {/each}
      </div>

      <div class="compare__save-group">
        {#if saveGroupOpen}
          <form
            class="compare__save-group-form"
            onsubmit={(e) => {
              e.preventDefault();
              submitSaveGroup();
            }}
          >
            <input
              type="text"
              class="compare__save-group-input"
              placeholder="Group name…"
              bind:value={saveGroupName}
              aria-label="Group name"
              onkeydown={(e) => {
                if (e.key === 'Escape') cancelSaveGroup();
              }}
            />
            <button type="submit" class="compare__save-group-btn" disabled={groups.saving || !saveGroupName.trim()}>
              {groups.saving ? 'Saving…' : 'Save'}
            </button>
            <button type="button" class="compare__save-group-cancel" onclick={cancelSaveGroup}>Cancel</button>
          </form>
        {:else}
          <button type="button" class="compare__save-group-open" onclick={openSaveGroup}>Save as group&hellip;</button>
          {#if saveGroupSaved}<span class="microlabel compare__save-group-success">Saved.</span>{/if}
        {/if}
        {#if groups.saveError}<span class="microlabel compare__save-group-error">{groups.saveError}</span>{/if}
      </div>

      <div class="compare__totals" role="group" aria-label="Group totals">
        <span class="microlabel compare__totals-label">Group totals</span>
        <div class="compare__totals-grid">
          <div class="compare__total">
            <span class="microlabel">CPU</span>
            <span class="compare__total-value tabular-nums">{fmtPct(totalCpuPctTween.current)}</span>
            {#if totalCoresLabel}<span class="compare__total-secondary">{totalCoresLabel}</span>{/if}
          </div>
          <div class="compare__total">
            <span class="microlabel">Memory</span>
            <span class="compare__total-value tabular-nums">{fmtBytes(totalMemBytesTween.current)}</span>
            {#if groupTotals.memHostPct !== undefined}
              <span class="compare__total-secondary">{fmtPct(totalMemHostPctTween.current)} of host</span>
            {/if}
          </div>
          <div class="compare__total">
            <span class="microlabel">Network</span>
            <span class="compare__total-value compare__total-value--split tabular-nums">
              <span>&darr; {fmtRate(totalNetRxTween.current)}</span>
              <span>&uarr; {fmtRate(totalNetTxTween.current)}</span>
            </span>
          </div>
          <div class="compare__total">
            <span class="microlabel">Disk IO</span>
            <span class="compare__total-value compare__total-value--split tabular-nums">
              <span>r {fmtRate(totalIoReadTween.current)}</span>
              <span>w {fmtRate(totalIoWriteTween.current)}</span>
            </span>
          </div>
        </div>
      </div>
    </div>

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
      <p class="microlabel compare__fetch-error">Couldn't load history for this range. Try again shortly.</p>
    {:else if fetchInFlight}
      <p class="microlabel">Loading…</p>
    {/if}

    {#if chartsCapped}
      <p class="microlabel compare__cap-note">
        Charting the first {MAX_COMPARE_MEMBERS} of {knownForCharts.length} members -- the totals and table below still
        cover everyone.
      </p>
    {/if}

    {#if chartMembers.length === 0}
      <p class="microlabel">{live.frameCount === 0 ? 'Loading…' : 'None of these containers are currently known.'}</p>
    {:else}
      <div class="compare__charts">
        <div class="card compare__chart-card">
          <span class="microlabel">CPU</span>
          <TimeChart bind:this={cpuChart} series={cpuSeries} formatValue={fmtPct} syncKey={SYNC_KEY} live={activeRange === 'live'} showLegend={false} />
        </div>
        <div class="card compare__chart-card">
          <span class="microlabel">Memory</span>
          <TimeChart bind:this={memChart} series={memSeries} formatValue={fmtBytes} syncKey={SYNC_KEY} live={activeRange === 'live'} showLegend={false} />
        </div>
        <div class="card compare__chart-card">
          <span class="microlabel">Network</span>
          <TimeChart bind:this={netChart} series={netSeries} formatValue={fmtRate} syncKey={SYNC_KEY} live={activeRange === 'live'} showLegend={false} />
        </div>
        <div class="card compare__chart-card">
          <span class="microlabel">Disk IO</span>
          <TimeChart bind:this={ioChart} series={ioSeries} formatValue={fmtRate} syncKey={SYNC_KEY} live={activeRange === 'live'} showLegend={false} />
        </div>
        {#if hasGpu}
          <div class="card compare__chart-card">
            <span class="microlabel">GPU</span>
            <TimeChart bind:this={gpuChart} series={gpuSeries} formatValue={fmtPct} syncKey={SYNC_KEY} live={activeRange === 'live'} showLegend={false} />
          </div>
        {/if}
      </div>
    {/if}

    <div class="card compare__table-wrap">
      <span class="microlabel compare__table-label">Detail</span>
      <table class="compare-table">
        <thead>
          <tr>
            <th class="microlabel">Name</th>
            <th class="microlabel">Health</th>
            <th class="microlabel compare-table__th--numeric">CPU</th>
            <th class="microlabel compare-table__th--numeric">Mem</th>
            <th class="microlabel compare-table__th--numeric">Net</th>
            <th class="microlabel compare-table__th--numeric">IO</th>
            <th class="microlabel compare-table__th--numeric">PIDs</th>
            <th class="microlabel compare-table__th--numeric">Uptime</th>
          </tr>
        </thead>
        <tbody>
          {#each requestedNames as name (name)}
            <CompareMemberRow {name} colorVar={memberColor.get(name) ?? '--ink-2'} />
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}

<style>
  .compare {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .compare--hint {
    gap: 0.5rem;
  }
  .compare__hint-link {
    color: var(--series-1);
  }
  .compare__header {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .compare__members {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .compare__chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.25rem 0.4rem 0.25rem 0.55rem;
    border: 1px solid color-mix(in oklab, var(--chip-color, var(--ink)) 45%, transparent);
    border-radius: 999px;
    background: color-mix(in oklab, var(--chip-color, var(--ink)) 12%, transparent);
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .compare__chip--uncharted {
    opacity: 0.6;
  }
  .compare__chip-remove {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    color: var(--ink-2);
    text-decoration: none;
    font-size: 0.7rem;
    flex-shrink: 0;
  }
  .compare__chip-remove:hover {
    background: color-mix(in oklab, var(--ink) 12%, transparent);
    color: var(--ink);
  }
  .compare__save-group {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    min-height: 28px;
  }
  /* Plain understated text button (matches overview__top-link/
     compare__hint-link's own treatment), not a bordered pill -- this is
     a secondary action, not a peer of the member chips above it. */
  .compare__save-group-open {
    padding: 0;
    border: none;
    background: transparent;
    color: var(--series-1);
    font-size: 0.78rem;
    cursor: pointer;
  }
  .compare__save-group-open:hover {
    text-decoration: underline;
  }
  .compare__save-group-form {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .compare__save-group-input {
    min-height: 28px;
    padding: 0 0.6rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.82rem;
    width: 12rem;
  }
  .compare__save-group-btn {
    min-height: 28px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-size: 0.78rem;
    cursor: pointer;
  }
  .compare__save-group-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  .compare__save-group-cancel {
    padding: 0;
    border: none;
    background: transparent;
    color: var(--ink-2);
    font-size: 0.78rem;
    cursor: pointer;
  }
  .compare__save-group-cancel:hover {
    color: var(--ink);
  }
  .compare__save-group-success {
    color: var(--status-good);
  }
  .compare__save-group-error {
    color: var(--status-warning);
  }
  .compare__totals {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding-top: 0.5rem;
    border-top: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .compare__totals-label {
    margin: 0;
  }
  .compare__totals-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 0.75rem;
  }
  @media (max-width: 47.9375rem) {
    .compare__totals-grid {
      grid-template-columns: repeat(2, 1fr);
    }
  }
  .compare__total {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .compare__total-value {
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 1.3rem;
    color: var(--ink);
  }
  .compare__total-value--split {
    display: flex;
    gap: 0.75rem;
    font-size: 1rem;
  }
  .compare__total-secondary {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.72rem;
  }
  .compare__fetch-error {
    color: var(--status-warning);
  }
  .compare__cap-note {
    margin: 0;
  }
  .compare__charts {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
  }
  .compare__charts > :last-child:nth-child(odd) {
    grid-column: 1 / -1;
  }
  @media (max-width: 47.9375rem) {
    .compare__charts {
      grid-template-columns: 1fr;
    }
  }
  .compare__chart-card {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0; /* releases this grid track on narrow viewports -- see ContainerDetail's own identical rule for why */
  }
  .compare__table-wrap {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    overflow-x: auto;
  }
  .compare__table-label {
    margin: 0;
  }
  .compare-table {
    border-collapse: collapse;
    min-width: 42rem;
  }
  .compare-table th {
    padding: 0.4rem 0.6rem;
    text-align: left;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    white-space: nowrap;
  }
  .compare-table__th--numeric {
    text-align: right;
  }
</style>

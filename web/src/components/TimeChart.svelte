<!--
  TimeChart: the uPlot wrapper every metric chart (container detail,
  GPU, storage, ...) builds on. One y-axis always (series never get a
  second scale); a legend when >=2 series; a crosshair-driven tooltip;
  vertical event-marker lines with a hover label; ResizeObserver-driven
  width; re-creates on theme change (colors are baked into the canvas
  as literal values, not live var() references).

  `syncKey` is not in the plan brief's literal props list, but IS
  required by its own "synced crosshair group (uPlot sync key per
  page)" contract line -- a page with multiple TimeCharts (e.g.
  Container detail's CPU/mem/net/IO/GPU set) passes the same syncKey to
  each instance to link their crosshairs; omitted, a chart is
  independent.
-->
<script>
  import uPlot from 'uplot';
  import { onDestroy, onMount } from 'svelte';
  import { theme, resolveToken } from '../lib/theme.svelte';
  import { fmtRelTime } from '../lib/format';
  import { needsRebuild } from '../lib/chartRebuild';
  import { advanceHeadState, headValue, liveWindowRange, LIVE_WINDOW_SEC, HEAD_EASE_MS } from '../lib/streamdriver';
  import { subscribeWhileVisible } from '../lib/streamdriver.svelte';
  import { prefersReducedMotion } from 'svelte/motion';

  // formatValue (additive, optional -- Task 14-17's own fold-in note:
  // "views pre-format tooltip values (or formatter callback) -- TimeChart
  // stays generic") lets a caller render its y-axis ticks and tooltip
  // values through format.ts (fmtBytes/fmtRate/fmtPct/...) instead of a
  // bare number -- a memory chart's tooltip otherwise reads "900000000 B"
  // rather than "900.0 MB". Undefined preserves the exact original
  // behavior (raw number + a plain unit suffix) for any caller that
  // hasn't been updated to pass one.
  //
  // live (additive, optional, default false -- smooth-streaming) opts a
  // caller into mechanisms 1+2: a continuously-sliding 15-minute x-window
  // and newest-sample head easing, both driven by the shared animation
  // driver (lib/streamdriver.svelte.ts) instead of the plain setData-per-
  // SSE-tick this chart has always done. A caller passes true only for
  // its OWN live range (e.g. `live={activeRange === 'live'}`) -- a
  // fetched/historical range must stay exactly as static as it is today.
  let { series = [], unit = '', height = 220, markers = [], syncKey = undefined, formatValue = undefined, live = false } = $props();

  const SEVERITY_VAR = {
    info: '--status-good',
    warning: '--status-warning',
    serious: '--status-serious',
    critical: '--status-critical',
  };

  // MIN_X_SPAN_SEC floors the x-axis's own domain width. Reproduced live
  // while building the GPU/Events views: a chart fed by liveRing (any
  // live-mode TimeChart -- Container Detail, GPU) starts with just one
  // or two points in the first couple of seconds after mounting, and
  // uPlot's own auto tick-picker mishandles that near-zero-width domain
  // -- it renders YEAR-granularity gridlines (seen: "2027", "2028",
  // "2029") with no visible data at all, rather than the correct
  // few-second range, for as long as the real span stays under this
  // floor. Once enough points arrive the natural span always exceeds
  // 10s within a couple more ticks (2s cadence), so this only smooths
  // over that brief startup window -- it never affects a real range
  // (every history-fetched range is hours-to-months wide).
  const MIN_X_SPAN_SEC = 10;

  // xRange is uPlot's Range.Function for the x scale: pads a too-narrow
  // [initMin, initMax] out symmetrically to MIN_X_SPAN_SEC, and passes
  // a genuinely empty domain (no points at all yet) through unchanged
  // rather than inventing a fake one.
  function xRange(_u, initMin, initMax) {
    if (initMin == null || initMax == null) return [initMin, initMax];
    const span = initMax - initMin;
    if (span >= MIN_X_SPAN_SEC) return [initMin, initMax];
    const pad = (MIN_X_SPAN_SEC - span) / 2;
    return [initMin - pad, initMax + pad];
  }

  let container;
  let plotEl;
  let chart = null;
  let ro = null;

  // prevShape is the last-built chart's structural shape (see
  // lib/chartRebuild.ts) -- compared against the current one on every
  // effect run to decide destroy+rebuild vs. the cheap setData path below.
  let prevShape = null;

  // tooltip/hoverMarker are plain $state (not derived from uPlot's own
  // data) because they're driven imperatively from uPlot's cursor
  // hook, not from a Svelte-tracked reactive computation.
  let tooltip = $state(null); // {x, y, ts, rows: [{label, color, value}]}
  let hoverMarker = $state(null); // {x, marker}

  // buildAlignedData unions every series' own timestamps into one
  // shared x-axis (uPlot requires a single aligned x per plot), filling
  // any series' missing timestamps with null -- a real gap, not a
  // false zero.
  function buildAlignedData(seriesList) {
    const tsSet = new Set();
    for (const s of seriesList) for (const [ts] of s.points) tsSet.add(ts);
    const xs = Array.from(tsSet).sort((a, b) => a - b);
    const idx = new Map(xs.map((ts, i) => [ts, i]));
    const ys = seriesList.map(() => new Array(xs.length).fill(null));
    seriesList.forEach((s, si) => {
      for (const [ts, val] of s.points) {
        const i = idx.get(ts);
        if (i !== undefined) ys[si][i] = val;
      }
    });
    return [xs, ...ys];
  }

  // alignedData is the last REAL buildAlignedData(series) result -- the
  // pristine target values every series is currently easing TOWARD in
  // live mode. Only ever replaced wholesale, by the data-effect below,
  // on a genuine data change; the animation-tick handler (also below)
  // only ever READS it, so headState's own next-tick comparisons can
  // never drift from what an actual SSE frame reported.
  let alignedData = null;

  // headState[i] is series i's own in-flight head ease -- null until
  // that series has reported at least one real (non-gap) value. Reset
  // in build() below: a structural rebuild can reorder or replace
  // series entirely, and stale state keyed by the OLD index would ease
  // the WRONG series toward the wrong target.
  let headState = [];

  // tailValue reads a series' own last non-null y in aligned data --
  // walking backward rather than assuming index length-1 is non-null,
  // since the newest timestamp in the shared x-axis can belong to a
  // DIFFERENT series (a real gap for this one, per buildAlignedData's
  // own doc), which must stay a gap here too rather than easing toward
  // a stale earlier value.
  function tailValue(ys) {
    for (let i = ys.length - 1; i >= 0; i--) {
      if (ys[i] !== null) return ys[i];
    }
    return null;
  }

  // Per-series advanceHeadState folding lives in lib/streamdriver.ts now
  // (pure, unit-tested there) -- this is just the aligned-data-shape glue:
  // extract series i's own raw tail value (or null for a real gap, which
  // must stay a gap here rather than easing toward a stale earlier one)
  // and fold it via the shared function, one series at a time.
  function advanceAll(alignedYs, nowMs, durationMs) {
    return alignedYs.map((ys, i) => {
      const raw = tailValue(ys);
      return raw === null ? null : advanceHeadState(headState[i], raw, nowMs, durationMs);
    });
  }

  // applyHeadState patches only the LAST index of each series to its
  // current eased value, MUTATING `aligned` in place rather than
  // returning a copy -- safe because `alignedData` is only ever the
  // pristine target array immediately after the data effect rebuilds it
  // wholesale via buildAlignedData(); every read of a series' true raw
  // tail (tailValue, above) happens before this runs again for that
  // series, so patching the disposable last slot here can never leak a
  // stale eased value back out as if it were real data. Skips the
  // per-tick array-clone this would otherwise cost at up to 30fps across
  // every series of every live chart on screen. durationMs defaults to
  // the real ease (HEAD_EASE_MS); callers under reduced motion pass 0 so
  // headValue snaps straight to targetValue -- the driver's own ticks
  // never run in that state (see streamdriver.svelte.ts), so without this
  // the tail would otherwise freeze one arrival stale forever instead of
  // tracking each new value immediately.
  function applyHeadState(aligned, state, nowMs, durationMs = HEAD_EASE_MS) {
    for (let i = 0; i < state.length; i++) {
      const s = state[i];
      if (!s) continue;
      const oneSeries = aligned[i + 1];
      oneSeries[oneSeries.length - 1] = headValue(s.prevValue, s.targetValue, s.arrivalMs, nowMs, durationMs);
    }
    return aligned;
  }

  function markerColor(severity) {
    return resolveToken(`var(${SEVERITY_VAR[severity] ?? SEVERITY_VAR.info})`);
  }

  // drawMarkers is a uPlot "draw" hook: dashed vertical lines at each
  // marker's timestamp, drawn straight onto the plot canvas. Positions
  // use valToPos(..., true) (canvas-pixel space) to match ctx's own
  // coordinate space -- see handleCursor below for why hit-testing
  // uses the CSS-pixel variant instead.
  function drawMarkers(u) {
    if (!markers.length) return;
    const { ctx } = u;
    ctx.save();
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 3]);
    for (const m of markers) {
      const x = u.valToPos(m.ts, 'x', true);
      if (x < u.bbox.left - 1 || x > u.bbox.left + u.bbox.width + 1) continue;
      ctx.strokeStyle = markerColor(m.severity);
      ctx.beginPath();
      ctx.moveTo(x, u.bbox.top);
      ctx.lineTo(x, u.bbox.top + u.bbox.height);
      ctx.stroke();
    }
    ctx.restore();
  }

  // handleCursor is a uPlot "setCursor" hook: recomputes the tooltip
  // (crosshair values for every series at the hovered index) and
  // which marker, if any, the cursor is close enough to for a hover
  // label. u.cursor.left is CSS-pixel space, so marker hit-testing
  // uses valToPos's CSS-pixel variant (canvasPixels omitted) to match.
  function handleCursor(u) {
    if (u.cursor.left == null || u.cursor.left < 0) {
      tooltip = null;
      hoverMarker = null;
      return;
    }

    const idx = u.cursor.idx;
    tooltip =
      idx == null
        ? null
        : {
            x: u.cursor.left,
            y: u.cursor.top ?? 0,
            ts: u.data[0][idx],
            rows: series.map((s, i) => {
              const raw = u.data[i + 1][idx];
              // A real gap (null -- see buildAlignedData) stays unformatted
              // ('—' is rendered by the template below) rather than being
              // handed to formatValue, which would otherwise turn a
              // missing sample into a misleadingly concrete "0.0 B".
              return {
                label: s.label,
                color: resolveToken(s.colorVar),
                value: raw == null ? null : formatValue ? formatValue(raw) : raw,
              };
            }),
          };

    const HOVER_PX = 6;
    hoverMarker = null;
    for (const m of markers) {
      const x = u.valToPos(m.ts, 'x');
      if (Math.abs(x - u.cursor.left) <= HOVER_PX) {
        hoverMarker = { x, marker: m };
        break;
      }
    }
  }

  function build() {
    if (!plotEl || !container) return;
    chart?.destroy();
    chart = null;
    // A structural rebuild can reorder/replace series entirely -- reset
    // headState so index i can't end up easing a NEW series toward a
    // STALE target left over from whatever used to be at that index.
    headState = [];

    const width = container.clientWidth || 320;
    const ink = resolveToken('var(--ink)');
    const gridColor = resolveToken('var(--ink-2)');

    chart = new uPlot(
      {
        width,
        height,
        padding: [8, 8, 8, 8],
        scales: { x: { time: true, range: xRange } },
        axes: [
          { stroke: ink, grid: { stroke: gridColor, width: 1 } },
          {
            stroke: ink,
            grid: { stroke: gridColor, width: 1 },
            values: (_u, vals) => vals.map((v) => (formatValue ? formatValue(v) : unit ? `${v} ${unit}` : `${v}`)),
            // uPlot's own default y-axis gutter width is sized for short
            // bare numbers; a formatValue label ("858.3 MiB", "12.4 MB/s")
            // is wider and was observed getting left-clipped (text-align
            // right against too narrow a box runs the label's leading
            // characters off the canvas's left edge) -- size the gutter
            // off the actual longest rendered label instead of trusting
            // uPlot's un-aware default. The 7px/char + 14px estimate is
            // deliberately generous (default font is small/proportional,
            // not truly 7px-per-char monospace) since a slightly wide
            // gutter costs a little plot area, while a narrow one clips.
            size: (_u, values) => {
              // uPlot calls this during layout passes where `values` can
              // be null (e.g. before any ticks are computed yet) --
              // guard defensively rather than let a null.reduce blank
              // the whole chart (reproduced live while building this).
              const longest = (values ?? []).reduce((max, v) => Math.max(max, String(v ?? '').length), 0);
              return Math.max(40, longest * 7 + 14);
            },
          },
        ],
        series: [
          {},
          ...series.map((s) => ({
            label: s.label,
            stroke: resolveToken(s.colorVar),
            width: 2,
            points: { show: false },
          })),
        ],
        cursor: {
          points: { show: true },
          ...(syncKey ? { sync: { key: syncKey } } : {}),
        },
        legend: { show: series.length >= 2 },
        hooks: {
          draw: [drawMarkers],
          setCursor: [handleCursor],
        },
      },
      buildAlignedData(series),
      plotEl,
    );
  }

  // currentShape reads exactly the inputs that change what build() bakes
  // into the uPlot config -- see lib/chartRebuild.ts's own doc for why
  // markers and each series' points are excluded (a marker or data-only
  // change never needs more than the setData path below).
  function currentShape() {
    return {
      series: series.map((s) => ({ label: s.label, colorVar: s.colorVar })),
      theme: theme.resolved,
      unit,
      hasFormatValue: !!formatValue,
    };
  }

  $effect(() => {
    // Track every input that can affect either path below: a structural
    // one (series shape, unit, formatValue, a theme flip) goes through
    // build(), same as before; markers is tracked here too even though
    // it's absent from currentShape()'s shape, purely so a marker-only
    // change still re-runs this effect at all -- drawMarkers/handleCursor
    // already read the live `markers`/`series` bindings fresh on every
    // uPlot redraw, so the setData call below is all a marker change
    // needs to actually show up.
    series;
    unit;
    markers;
    formatValue;
    theme.resolved;

    const shape = currentShape();
    const rebuilding = !chart || needsRebuild(prevShape, shape);
    if (rebuilding) {
      build();
    }
    if (live) {
      // Live mode always recomputes headState off the fresh aligned
      // data and re-applies it, even right after a rebuild (which
      // already rendered the raw target once via build()'s own
      // buildAlignedData call): a rebuild resets headState above to
      // prevValue === targetValue for every series, so this second
      // setData is a visual no-op in that case -- keeping ONE code path
      // rather than skipping it post-rebuild is what keeps this branch
      // simple.
      const nowMs = Date.now();
      const durationMs = prefersReducedMotion.current ? 0 : HEAD_EASE_MS;
      // The shared driver's own ticks also step this chart's x-window
      // (see the animation-tick effect below), far more often than this
      // effect re-runs -- but that subscription is gated behind
      // IntersectionObserver (subscribeWhileVisible), and under reduced
      // motion the driver never ticks at all regardless of visibility.
      // Either way, nothing else would ever set a first x-range for a
      // chart that hasn't been on-screen yet when real data arrives (a
      // live-seed history fetch landing while its chart is still below
      // the fold, reproduced live on the Containers view's lower rows,
      // whose per-row Sparkline shares this same gap -- see its own
      // doc): setData is always called with resetScales=false in live
      // mode, so without this, that data would sit there with no
      // x-range that ever includes it. Stepping the window here too,
      // unconditionally, from the data-arrival path itself, fixes both
      // that and reduced motion's own already-documented freeze -- a
      // discrete step per arrival under reduced motion, exactly the
      // pre-feature behavior; a harmless redundant assignment (same
      // formula, same values) once the driver's own more frequent tick
      // is already running for a chart that IS visible.
      const [min, max] = liveWindowRange(nowMs, LIVE_WINDOW_SEC);
      chart.setScale('x', { min, max });
      alignedData = buildAlignedData(series);
      headState = advanceAll(alignedData.slice(1), nowMs, durationMs);
      chart.setData(applyHeadState(alignedData, headState, nowMs, durationMs), false);
    } else if (!rebuilding) {
      chart.setData(buildAlignedData(series));
    }
    prevShape = shape;
  });

  // The live-mode animation subscription is its own effect (rather than
  // folded into the data effect above) so it can react to `live` itself
  // flipping -- a range-picker switch away from Live must unsubscribe
  // immediately, handing the shared driver back if this was its last
  // active subscriber, not merely stop mattering. container is already
  // bound by the time any $effect in this component first runs (see
  // streamdriver.svelte.ts's own doc), so subscribeWhileVisible always
  // has a real element to observe.
  $effect(() => {
    if (!live) return;
    const unsubscribe = subscribeWhileVisible(
      () => container,
      (nowMs) => {
        if (!chart || !alignedData) return;
        const [min, max] = liveWindowRange(nowMs, LIVE_WINDOW_SEC);
        chart.setScale('x', { min, max });
        if (headState.length > 0) {
          const durationMs = prefersReducedMotion.current ? 0 : HEAD_EASE_MS;
          chart.setData(applyHeadState(alignedData, headState, nowMs, durationMs), false);
        }
      },
    );
    return unsubscribe;
  });

  onMount(() => {
    ro = new ResizeObserver(() => {
      if (chart && container) chart.setSize({ width: container.clientWidth || 320, height });
    });
    if (container) ro.observe(container);
  });

  onDestroy(() => {
    ro?.disconnect();
    chart?.destroy();
  });
</script>

<div class="time-chart" bind:this={container}>
  <div bind:this={plotEl}></div>
  {#if tooltip}
    <div class="time-chart__tooltip" style="left: {tooltip.x}px; top: {tooltip.y}px">
      <div class="microlabel">{fmtRelTime(tooltip.ts, Date.now())}</div>
      {#each tooltip.rows as row (row.label)}
        <div class="time-chart__tooltip-row">
          <span class="time-chart__swatch" style="background: {row.color}"></span>
          <span>{row.label}</span>
          <span class="tabular-nums">
            {row.value ?? '—'}{!formatValue && unit && row.value !== null ? ` ${unit}` : ''}
          </span>
        </div>
      {/each}
    </div>
  {/if}
  {#if hoverMarker}
    <div class="time-chart__marker-label microlabel" style="left: {hoverMarker.x}px">
      {hoverMarker.marker.label}
    </div>
  {/if}
</div>

<style>
  .time-chart {
    position: relative;
    width: 100%;
  }
  .time-chart :global(.u-legend) {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 0.75rem;
  }
  .time-chart__tooltip {
    position: absolute;
    transform: translate(8px, 8px);
    background: var(--surface);
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 6px;
    padding: 0.4rem 0.6rem;
    color: var(--ink);
    font-size: 0.75rem;
    white-space: nowrap;
    pointer-events: none;
    z-index: 5;
  }
  .time-chart__tooltip-row {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }
  .time-chart__swatch {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 2px;
    flex-shrink: 0;
  }
  .time-chart__marker-label {
    position: absolute;
    top: 2px;
    transform: translateX(-50%);
    background: var(--surface);
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 4px;
    padding: 0.1rem 0.35rem;
    pointer-events: none;
    z-index: 4;
    white-space: nowrap;
  }
</style>

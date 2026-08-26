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

  let { series = [], unit = '', height = 220, markers = [], syncKey = undefined } = $props();

  const SEVERITY_VAR = {
    info: '--status-good',
    warning: '--status-warning',
    serious: '--status-serious',
    critical: '--status-critical',
  };

  let container;
  let plotEl;
  let chart = null;
  let ro = null;

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
            rows: series.map((s, i) => ({
              label: s.label,
              color: resolveToken(s.colorVar),
              value: u.data[i + 1][idx],
            })),
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

    const width = container.clientWidth || 320;
    const ink = resolveToken('var(--ink)');
    const gridColor = resolveToken('var(--ink-2)');

    chart = new uPlot(
      {
        width,
        height,
        padding: [8, 8, 8, 8],
        scales: { x: { time: true } },
        axes: [
          { stroke: ink, grid: { stroke: gridColor, width: 1 } },
          {
            stroke: ink,
            grid: { stroke: gridColor, width: 1 },
            values: (_u, vals) => vals.map((v) => (unit ? `${v} ${unit}` : `${v}`)),
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

  $effect(() => {
    // Re-create on new series/unit/markers, or on a theme flip -- every
    // color above is resolved to a literal at build() time, so a theme
    // change leaves stale colors baked into the canvas until this
    // re-runs.
    series;
    unit;
    markers;
    theme.resolved;
    build();
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
          <span class="tabular-nums">{row.value ?? '—'}{unit ? ` ${unit}` : ''}</span>
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

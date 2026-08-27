<!--
  Sparkline: a minimal uPlot line, 1 series, no axes, fixed 28px height --
  used inline in StatTile/table cells, never as a standalone chart.
-->
<script>
  import uPlot from 'uplot';
  import { onDestroy, onMount } from 'svelte';
  import { theme, resolveToken, withAlpha } from '../lib/theme.svelte';
  import { needsRebuild } from '../lib/chartRebuild';

  let { points = [], color = 'var(--series-1)' } = $props();

  let el;
  let chart = null;
  let ro = null;

  // prevShape is the last-built chart's structural shape -- see
  // lib/chartRebuild.ts and TimeChart's matching field for the full
  // rationale. Sparkline's only structural input (besides theme) is its
  // color prop, modeled as a single-series shape with a constant label so
  // the same shared helper applies unchanged.
  let prevShape = null;

  function toData(pts) {
    return [pts.map((p) => p[0]), pts.map((p) => p[1])];
  }

  function build() {
    if (!el) return;
    chart?.destroy();
    const resolved = resolveToken(color);
    chart = new uPlot(
      {
        width: el.clientWidth || 120,
        height: 28,
        padding: [1, 1, 1, 1],
        cursor: { show: false },
        legend: { show: false },
        scales: { x: { time: false } },
        axes: [{ show: false }, { show: false }],
        series: [
          {},
          {
            stroke: resolved,
            width: 2,
            fill: withAlpha(resolved, 12),
            points: { show: false },
          },
        ],
      },
      toData(points),
      el,
    );
  }

  $effect(() => {
    // Rebuild only on a color-prop change or a theme flip -- a
    // var(--series-N) reference resolves to a different literal color per
    // theme, and re-reading tokens.css off the document is the only way a
    // canvas-drawn line notices that. A points-only change (every SSE
    // frame, in Live mode -- this is what the Containers table's per-row
    // sparklines get) takes the cheap setData path instead, the same
    // destroy+recreate-avoidance fix as TimeChart's matching effect.
    points;
    color;
    theme.resolved;

    const shape = { series: [{ label: '', colorVar: color }], theme: theme.resolved, hasFormatValue: false };
    if (!chart || needsRebuild(prevShape, shape)) {
      build();
    } else {
      chart.setData(toData(points));
    }
    prevShape = shape;
  });

  onMount(() => {
    // Previously, resize was handled for free: a full rebuild every
    // frame re-read el.clientWidth from scratch. Now that data-only
    // changes take the setData path above instead, an actual resize
    // (sidebar collapse, viewport change) needs its own explicit
    // setSize -- the same ResizeObserver->setSize shape TimeChart already
    // uses.
    ro = new ResizeObserver(() => {
      if (chart && el) chart.setSize({ width: el.clientWidth || 120, height: 28 });
    });
    if (el) ro.observe(el);
  });

  onDestroy(() => {
    ro?.disconnect();
    chart?.destroy();
  });
</script>

<div bind:this={el} class="sparkline"></div>

<style>
  .sparkline {
    height: 28px;
    width: 100%;
  }
</style>

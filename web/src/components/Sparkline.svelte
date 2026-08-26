<!--
  Sparkline: a minimal uPlot line, 1 series, no axes, fixed 28px height --
  used inline in StatTile/table cells, never as a standalone chart.
-->
<script>
  import uPlot from 'uplot';
  import { onDestroy } from 'svelte';
  import { theme, resolveToken, withAlpha } from '../lib/theme.svelte';

  let { points = [], color = 'var(--series-1)' } = $props();

  let el;
  let chart = null;

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
    // Re-build on new data, a color-prop change, or a theme flip -- a
    // var(--series-N) reference resolves to a different literal color
    // per theme, and re-reading tokens.css off the document is the
    // only way a canvas-drawn line notices that.
    points;
    color;
    theme.resolved;
    build();
  });

  onDestroy(() => chart?.destroy());
</script>

<div bind:this={el} class="sparkline"></div>

<style>
  .sparkline {
    height: 28px;
    width: 100%;
  }
</style>

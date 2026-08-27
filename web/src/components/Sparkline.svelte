<!--
  Sparkline: a minimal uPlot line, 1 series, no axes, fixed 28px height --
  used inline in StatTile/table cells, never as a standalone chart.
-->
<script>
  import uPlot from 'uplot';
  import { onDestroy, onMount } from 'svelte';
  import { theme, resolveToken, withAlpha } from '../lib/theme.svelte';
  import { needsRebuild } from '../lib/chartRebuild';
  import { headValue, liveWindowRange, LIVE_WINDOW_SEC, HEAD_EASE_MS } from '../lib/streamdriver';
  import { subscribeWhileVisible } from '../lib/streamdriver.svelte';
  import { prefersReducedMotion } from 'svelte/motion';

  // live defaults true: every real Sparkline in this app charts a
  // liveRing (StatTile, ContainerRow's per-row CPU column) -- there is
  // no historical/fetched Sparkline usage today, unlike TimeChart, which
  // is why that component requires an explicit opt-in instead. See its
  // own doc for the full mechanism 1+2 rationale this mirrors.
  let { points = [], color = 'var(--series-1)', live = true } = $props();

  let el;
  let chart = null;
  let ro = null;

  // prevShape is the last-built chart's structural shape -- see
  // lib/chartRebuild.ts and TimeChart's matching field for the full
  // rationale. Sparkline's only structural input (besides theme) is its
  // color prop, modeled as a single-series shape with a constant label so
  // the same shared helper applies unchanged.
  let prevShape = null;

  // alignedData/headState: Sparkline's single-series analogue of
  // TimeChart's own pair (see that file's doc for the full rationale).
  // alignedData stays the pristine last-real-points snapshot; headState
  // is one {prevValue, targetValue, arrivalMs} record (or null before
  // the first real point) rather than an array, since Sparkline only
  // ever charts one series.
  let alignedData = null;
  let headState = null;

  function toData(pts) {
    return [pts.map((p) => p[0]), pts.map((p) => p[1])];
  }

  function advanceHeadState(pts, nowMs) {
    if (pts.length === 0) return null;
    const raw = pts[pts.length - 1][1];
    if (!headState) return { prevValue: raw, targetValue: raw, arrivalMs: nowMs };
    if (headState.targetValue === raw) return headState;
    return { prevValue: headState.targetValue, targetValue: raw, arrivalMs: nowMs };
  }

  // applyHeadState returns a COPY of [xs, ys] with only the tail y
  // patched to its current eased value -- same "copy the tail, never
  // mutate the source" contract as TimeChart's own version. Same
  // reduced-motion durationMs override too -- see its doc.
  function applyHeadState(data, state, nowMs, durationMs = HEAD_EASE_MS) {
    if (!state) return data;
    const [xs, ys] = data;
    const patched = ys.slice();
    patched[patched.length - 1] = headValue(state.prevValue, state.targetValue, state.arrivalMs, nowMs, durationMs);
    return [xs, patched];
  }

  function build() {
    if (!el) return;
    chart?.destroy();
    // A rebuild (color/theme change) means the chart itself is brand
    // new -- headState resetting to null makes the next tick treat the
    // first post-rebuild value as a fresh, unanimated starting point.
    headState = null;
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
    const rebuilding = !chart || needsRebuild(prevShape, shape);
    if (rebuilding) {
      build();
    }
    if (live) {
      // See TimeChart's matching branch: always re-derive headState off
      // the fresh points and re-apply it, even right after a rebuild --
      // a rebuild resets headState to prevValue === targetValue, so the
      // extra setData is a visual no-op there, and one code path stays
      // simpler than skipping it conditionally.
      const nowMs = Date.now();
      const durationMs = prefersReducedMotion.current ? 0 : HEAD_EASE_MS;
      alignedData = toData(points);
      headState = advanceHeadState(points, nowMs);
      chart.setData(applyHeadState(alignedData, headState, nowMs, durationMs), false);
    } else if (!rebuilding) {
      chart.setData(toData(points));
    }
    prevShape = shape;
  });

  // See TimeChart's matching effect for the full rationale: its own
  // effect so a `live` flip un/resubscribes immediately rather than
  // just changing what an existing subscription's tick does.
  $effect(() => {
    if (!live) return;
    const unsubscribe = subscribeWhileVisible(
      () => el,
      (nowMs) => {
        if (!chart || !alignedData) return;
        const [min, max] = liveWindowRange(nowMs, LIVE_WINDOW_SEC);
        chart.setScale('x', { min, max });
        if (headState) {
          const durationMs = prefersReducedMotion.current ? 0 : HEAD_EASE_MS;
          chart.setData(applyHeadState(alignedData, headState, nowMs, durationMs), false);
        }
      },
    );
    return unsubscribe;
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

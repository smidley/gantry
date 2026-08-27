<!--
  Sparkline: a minimal uPlot line, 1 series, no axes, fixed 28px height --
  used inline in StatTile/table cells, never as a standalone chart.
-->
<script>
  import uPlot from 'uplot';
  import { onDestroy, onMount } from 'svelte';
  import { theme, resolveToken, withAlpha } from '../lib/theme.svelte';
  import { needsRebuild } from '../lib/chartRebuild';
  import { advanceHeadState, headValue, liveWindowRange, LIVE_WINDOW_SEC, HEAD_EASE_MS } from '../lib/streamdriver';
  import { subscribeWhileVisible } from '../lib/streamdriver.svelte';
  import { nearestPointAt, tsAtFraction } from '../lib/scrub';
  import { prefersReducedMotion } from 'svelte/motion';

  // live defaults true: every real Sparkline in this app charts a
  // liveRing (StatTile, ContainerRow's per-row CPU column) -- there is
  // no historical/fetched Sparkline usage today, unlike TimeChart, which
  // is why that component requires an explicit opt-in instead. See its
  // own doc for the full mechanism 1+2 rationale this mirrors.
  //
  // onScrub (additive, optional -- hover-scrub): called with
  // {ts, value} while the pointer hovers a real point, or null on
  // leave/cancel/no-target. Sparkline stays presentation-only -- it
  // renders its OWN hover dot/hairline from this same computation, but
  // hands the hit to its caller (StatTile/ContainerRow) to own whatever
  // number that hit actually updates.
  let { points = [], color = 'var(--series-1)', live = true, onScrub = undefined } = $props();

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

  // applyHeadState patches the tail of `data`'s y-array to its current
  // eased value, MUTATING in place rather than returning a copy -- same
  // "safe because alignedData is rebuilt wholesale before its next real
  // read" contract as TimeChart's own version (see its doc), and the same
  // per-tick array-clone this skips.
  function applyHeadState(data, state, nowMs, durationMs = HEAD_EASE_MS) {
    if (!state) return data;
    const ys = data[1];
    ys[ys.length - 1] = headValue(state.prevValue, state.targetValue, state.arrivalMs, nowMs, durationMs);
    return data;
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
      // Always step the window here, not only under reduced motion: the
      // shared driver's own (far more frequent) tick is the ONLY other
      // place this gets set, and it's gated behind IntersectionObserver
      // (subscribeWhileVisible) -- a chart that hasn't been on-screen
      // yet when real data arrives (a live-seed history fetch landing
      // on one of the Containers view's below-the-fold rows, reproduced
      // live building this) would otherwise hold that data with no
      // x-range that ever includes it: setData is always called with
      // resetScales=false in live mode, so nothing else would ever
      // range the axis onto it. Redundant with the tick's own
      // more-frequent update once a chart IS visible -- same formula,
      // just also invoked here -- so this changes nothing for that case
      // beyond one extra identical assignment.
      const [min, max] = liveWindowRange(nowMs, LIVE_WINDOW_SEC);
      chart.setScale('x', { min, max });
      alignedData = toData(points);
      headState = points.length === 0 ? null : advanceHeadState(headState, points[points.length - 1][1], nowMs, durationMs);
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

  // --- Hover-scrub (additive, optional -- onScrub only) -----------------
  //
  // scrubActive/markerPos are Sparkline's OWN presentation state for the
  // dot+hairline overlay; markerPos deliberately keeps its last real
  // position when scrubActive goes false rather than resetting, so the
  // fade-out below (an always-mounted element, opacity toggled by class)
  // fades out IN PLACE instead of jumping to a stale 0,0 first.
  let scrubActive = $state(false);
  let markerPos = $state({ left: 0, top: 0 });

  // computeScrub maps a client-space x back to a ring value via the same
  // [now-15m, now] window the chart's own x-scale is set to (liveWindowRange
  // -- see the module doc), NOT the points' own span: an early/sparse ring
  // still scrubs anywhere across the tile to its nearest real sample
  // instead of only across whatever narrow span already has data.
  function computeScrub(clientX) {
    if (!chart || !el) return;
    const rect = el.getBoundingClientRect();
    const fraction = (clientX - rect.left) / (rect.width || 1);
    const [min, max] = liveWindowRange(Date.now(), LIVE_WINDOW_SEC);
    const hit = nearestPointAt(points, tsAtFraction(fraction, min, max));
    if (!hit) {
      clearScrub();
      return;
    }
    markerPos = { left: chart.valToPos(hit.ts, 'x', false), top: chart.valToPos(hit.value, 'y', false) };
    scrubActive = true;
    onScrub?.({ ts: hit.ts, value: hit.value });
  }

  function clearScrub() {
    if (scrubActive) onScrub?.(null);
    scrubActive = false;
  }

  function handlePointerMove(e) {
    if (!onScrub || !live) return;
    computeScrub(e.clientX);
  }
</script>

<div
  bind:this={el}
  class="sparkline"
  role="presentation"
  onpointermove={handlePointerMove}
  onpointerleave={clearScrub}
  onpointercancel={clearScrub}
>
  <div class="sparkline__hairline" class:sparkline__hairline--visible={scrubActive} style="left: {markerPos.left}px"></div>
  <div
    class="sparkline__dot"
    class:sparkline__dot--visible={scrubActive}
    style="left: {markerPos.left}px; top: {markerPos.top}px; background: {color}"
  ></div>
</div>

<style>
  .sparkline {
    height: 28px;
    width: 100%;
    position: relative;
  }
  .sparkline__hairline {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 1px;
    background: color-mix(in oklab, var(--ink) 35%, transparent);
    pointer-events: none;
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .sparkline__hairline--visible {
    opacity: 1;
  }
  .sparkline__dot {
    position: absolute;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    transform: translate(-3px, -3px);
    pointer-events: none;
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .sparkline__dot--visible {
    opacity: 1;
  }
</style>

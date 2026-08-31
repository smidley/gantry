<!--
  Sparkline: a minimal uPlot line, 1-2 series, no axes, fixed 28px height --
  used inline in StatTile/table cells, never as a standalone chart.

  points2/color2 (additive, optional -- dual-line directional charts):
  a second series for a directional pair (down/up, read/write) sharing
  this one small chart, per dataviz's own "color follows entity" rule
  generalized to a metric's own fixed direction -- down/read always
  --series-1, up/write always --series-4, app-wide (Scott: "we shouldn't
  use red since that usually means something bad" -- --series-2 skews
  orange/red-adjacent). Kept genuinely lightweight: the single-series
  path (points2 absent, by far the common case -- every table-cell/
  leaderboard sparkline in this app) pays none of the second series' own
  alignment cost.
-->
<script>
  import uPlot from 'uplot';
  import { onDestroy, onMount } from 'svelte';
  import { theme, resolveToken, withAlpha } from '../lib/theme.svelte';
  import { needsRebuild } from '../lib/chartRebuild';
  import { advanceHeadState, headValue, liveWindowRange, LIVE_WINDOW_SEC } from '../lib/streamdriver';
  import { subscribeWhileVisible } from '../lib/streamdriver.svelte';
  // Renamed to liveStore on import only because this component's own
  // `live` prop (the animate-or-not flag) already owns that name -- same
  // aliasing TopBarRow.svelte uses for the identical reason.
  import { live as liveStore } from '../lib/sse.svelte';
  import { nearestPointAt, tsAtFraction } from '../lib/scrub';
  import { scrubBus } from '../lib/scrubbus.svelte';
  import { motion } from '../lib/motion.svelte';

  // live defaults true: every real Sparkline in this app charts a
  // liveRing (StatTile, ContainerRow's per-row CPU column) -- there is
  // no historical/fetched Sparkline usage today, unlike TimeChart, which
  // is why that component requires an explicit opt-in instead. See its
  // own doc for the full mechanism 1+2 rationale this mirrors.
  let { points = [], color = 'var(--series-1)', points2 = undefined, color2 = 'var(--series-4)', live = true, height = 28 } =
    $props();

  let el;
  let chart = null;
  let ro = null;

  // prevShape is the last-built chart's structural shape -- see
  // lib/chartRebuild.ts and TimeChart's matching field for the full
  // rationale. Sparkline's own structural inputs (besides theme) are its
  // color props and whether a second series is present at all -- a
  // caller that starts passing/stops passing points2 (shouldn't happen
  // in practice; every real call site's shape is fixed) still rebuilds
  // correctly rather than silently keeping the old 1-series chart.
  let prevShape = null;

  // alignedData/headState(2): Sparkline's own analogue of TimeChart's
  // per-series array (see that file's doc for the full rationale), just
  // two named slots instead of an array -- Sparkline never charts more
  // than a directional pair. headState2 stays null whenever points2 is
  // absent, the whole feature's normal (and by far most common) case.
  let alignedData = null;
  let headState = null;
  let headState2 = null;

  // toData builds uPlot's [xs, ys] (or [xs, ys, ys2]) shape. The 1-series
  // path (points2 absent -- every table-cell/leaderboard sparkline in
  // this app) stays the cheap direct map it always was; only a genuine
  // second series pays for aligning two independently-pushed rings onto
  // one shared x-axis (uPlot requires it), the same union-of-timestamps,
  // null-fill-the-gaps approach TimeChart's own buildAlignedData uses --
  // in practice the two rings tick in lockstep (same SSE frame, same ts,
  // every push), so this almost always degenerates to a plain zip, but
  // reads correctly even if one ring seeds with a slightly different
  // span than the other during the brief startup window.
  function toData(pts, pts2) {
    if (!pts2) return [pts.map((p) => p[0]), pts.map((p) => p[1])];
    const tsSet = new Set();
    for (const [ts] of pts) tsSet.add(ts);
    for (const [ts] of pts2) tsSet.add(ts);
    const xs = Array.from(tsSet).sort((a, b) => a - b);
    const idx = new Map(xs.map((ts, i) => [ts, i]));
    const ys = new Array(xs.length).fill(null);
    const ys2 = new Array(xs.length).fill(null);
    for (const [ts, v] of pts) ys[idx.get(ts)] = v;
    for (const [ts, v] of pts2) ys2[idx.get(ts)] = v;
    return [xs, ys, ys2];
  }

  // applyHeadState patches the tail of `data`'s y-array (both series,
  // when a second is present) to its current glided value, MUTATING in
  // place rather than returning a copy -- same "safe because alignedData
  // is rebuilt wholesale before its next real read" contract as
  // TimeChart's own version (see its doc), and the same per-tick
  // array-clone this skips. No durationMs parameter: each state's own
  // durationMs (fixed at the instant advanceHeadState created that leg --
  // see its doc) is what headValue must read, not whatever the driver's
  // cadence estimate happens to be RIGHT NOW.
  function applyHeadState(data, state, state2, nowMs) {
    if (state) {
      const ys = data[1];
      ys[ys.length - 1] = headValue(state.prevValue, state.targetValue, state.arrivalMs, nowMs, state.durationMs);
    }
    if (state2 && data[2]) {
      const ys2 = data[2];
      ys2[ys2.length - 1] = headValue(state2.prevValue, state2.targetValue, state2.arrivalMs, nowMs, state2.durationMs);
    }
    return data;
  }

  function build() {
    if (!el) return;
    chart?.destroy();
    // A rebuild (color/theme change) means the chart itself is brand
    // new -- headState(2) resetting to null makes the next tick treat
    // the first post-rebuild value as a fresh, unanimated starting point.
    headState = null;
    headState2 = null;
    const resolved = resolveToken(color);
    const series = [
      {},
      {
        stroke: resolved,
        width: 2,
        cap: 'round', // D2 pass: "rounded joins/caps" (joins already default to round in this uPlot version)
        // A vertical fade (color -> transparent), not the old flat 12%
        // wash -- D2 pass: "single-series charts... get a vertical fade
        // fill (series color -> transparent, ~18%->0)". A plain function
        // (not a static color) so it reads the CURRENT plot bbox on
        // every draw, same as TimeChart's own seriesFill.
        fill: (u) => {
          const top = u.bbox.top;
          const bottom = Math.max(top + 1, u.bbox.top + u.bbox.height);
          const grad = u.ctx.createLinearGradient(0, top, 0, bottom);
          grad.addColorStop(0, withAlpha(resolved, 18));
          grad.addColorStop(1, withAlpha(resolved, 0));
          return grad;
        },
        points: { show: false },
      },
    ];
    if (points2) {
      const resolved2 = resolveToken(color2);
      // no fill -- two overlapping fills read as noise, not signal
      series.push({ stroke: resolved2, width: 2, cap: 'round', points: { show: false } });
    }
    chart = new uPlot(
      {
        width: el.clientWidth || 120,
        height,
        padding: [1, 1, 1, 1],
        cursor: { show: false },
        legend: { show: false },
        scales: { x: { time: false } },
        axes: [{ show: false }, { show: false }],
        series,
      },
      toData(points, points2),
      el,
    );
    // A rebuild's fresh uPlot instance has no idea where the bus's own
    // marker (if a scrub is already active) belongs in ITS new pixel
    // space -- resync immediately rather than waiting for the next bus
    // publish (which may not come for a while, e.g. an already-scrubbed
    // page sitting still under a theme flip).
    syncMarkerFromBus();
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
    points2;
    color2;
    theme.resolved;

    const shapeSeries = [{ label: '', colorVar: color }];
    if (points2) shapeSeries.push({ label: '', colorVar: color2 });
    const shape = { series: shapeSeries, theme: theme.resolved, hasFormatValue: false };
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
      // durationMs is THIS arrival's own leg duration: the shared driver's
      // freshly-measured cadence EMA (liveStore.glideMs), or 0 to snap
      // under reduced motion -- see streamdriver.ts's "Cadence-driven
      // glide" doc for why this varies per arrival instead of a fixed
      // guess.
      const durationMs = motion.reduced ? 0 : liveStore.glideMs;
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
      alignedData = toData(points, points2);
      headState = points.length === 0 ? null : advanceHeadState(headState, points[points.length - 1][1], nowMs, durationMs);
      headState2 =
        !points2 || points2.length === 0 ? null : advanceHeadState(headState2, points2[points2.length - 1][1], nowMs, durationMs);
      chart.setData(applyHeadState(alignedData, headState, headState2, nowMs), false);
    } else if (!rebuilding) {
      chart.setData(toData(points, points2));
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
        if (headState || headState2) {
          chart.setData(applyHeadState(alignedData, headState, headState2, nowMs), false);
        }
      },
    );
    return unsubscribe;
  });

  // --- Hover-scrub, synced across every mounted scrub-aware surface -----
  //
  // sourceId is this instance's own opaque ownership token for the
  // shared bus (see lib/scrubbus.svelte's doc) -- stable for the whole
  // mounted lifetime, never compared to anything but itself.
  const sourceId = Symbol('sparkline');

  // lastClientX is the FIX for a real bug the pixel-anchored version of
  // this had: recomputing ts from Date.now() on every single pointermove
  // means a pointer that's genuinely stationary but still firing repeat
  // move events (real trackpad/mouse sensor jitter -- reproduced: the
  // SAME pixel, re-hovered 49s later, resolved to a different value)
  // creeps the published ts steadily forward through history even
  // though the pointer never actually moved. Skipping any event whose
  // clientX matches the last one we actually acted on makes this
  // properly timestamp-anchored: ts only ever changes on a genuine
  // pointer move, never merely because time itself passed.
  let lastClientX = null;

  // scrubActive/markerPos are this Sparkline's OWN presentation state
  // for its dot+hairline -- driven by the BUS below, not by whether
  // THIS instance is the one currently tracking the pointer, so a
  // follower (any other mounted sparkline while one of them is being
  // scrubbed) renders identically to the initiator. markerPos
  // deliberately keeps its last real position when scrubActive goes
  // false rather than resetting, so the fade-out (an always-mounted
  // element, opacity toggled by class) fades out IN PLACE instead of
  // jumping to a stale 0,0 first.
  let scrubActive = $state(false);
  let markerPos = $state({ left: 0, top: 0 });

  // syncMarkerFromBus positions THIS sparkline's own dot/hairline at
  // wherever ITS OWN points land nearest the bus's shared ts -- the
  // follower half of the sync (every mounted sparkline runs this off
  // the SAME bus.ts), and also what the initiator's own dot uses, via
  // the reactive effect below: nothing here cares who published.
  function syncMarkerFromBus() {
    if (!chart) return;
    const ts = scrubBus.ts;
    const hit = ts === null ? null : nearestPointAt(points, ts);
    scrubActive = hit !== null;
    if (hit) markerPos = { left: chart.valToPos(hit.ts, 'x', false), top: chart.valToPos(hit.value, 'y', false) };
  }

  $effect(() => {
    scrubBus.ts;
    points;
    syncMarkerFromBus();
  });

  // publishFromPointer is the INITIATOR half: maps a client-space x to a
  // timestamp across the same [now-15m, now] window the chart's own
  // x-scale is set to (liveWindowRange -- see the module doc), NOT the
  // points' own span, so an early/sparse ring still scrubs anywhere
  // across the tile rather than only across whatever narrow span
  // already has data -- and publishes that ts to the shared bus rather
  // than computing a "hit" locally, since every surface (including this
  // one, via the effect above) derives its own hit from the bus's ts
  // independently.
  function publishFromPointer(clientX) {
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const fraction = (clientX - rect.left) / (rect.width || 1);
    const [min, max] = liveWindowRange(Date.now(), LIVE_WINDOW_SEC);
    scrubBus.publish(tsAtFraction(fraction, min, max), sourceId);
  }

  function clearScrub() {
    lastClientX = null;
    scrubBus.clear(sourceId);
  }

  function handlePointerMove(e) {
    if (!live) return;
    if (e.clientX === lastClientX) return;
    lastClientX = e.clientX;
    publishFromPointer(e.clientX);
  }

  onMount(() => {
    // Previously, resize was handled for free: a full rebuild every
    // frame re-read el.clientWidth from scratch. Now that data-only
    // changes take the setData path above instead, an actual resize
    // (sidebar collapse, viewport change) needs its own explicit
    // setSize -- the same ResizeObserver->setSize shape TimeChart already
    // uses.
    ro = new ResizeObserver(() => {
      if (chart && el) chart.setSize({ width: el.clientWidth || 120, height });
    });
    if (el) ro.observe(el);
  });

  onDestroy(() => {
    ro?.disconnect();
    chart?.destroy();
    // An unmounting owner (navigating away mid-scrub, or a Containers
    // row whose container just disappeared from the live frame) must
    // release the bus itself -- nothing else ever will, since no more
    // pointer events can ever come from a destroyed component. Without
    // this, every OTHER mounted surface would stay permanently pinned to
    // whatever this one last published, with no way back to live.
    // clearScrubIfOwner's own guard (see lib/scrub.ts) makes this a
    // harmless no-op when this instance isn't the current owner anyway.
    scrubBus.clear(sourceId);
  });
</script>

<div
  bind:this={el}
  class="sparkline"
  role="presentation"
  style="height: {height}px"
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
    background: color-mix(in oklab, var(--ink) 24%, transparent);
    pointer-events: none;
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .sparkline__hairline--visible {
    opacity: 1;
  }
  .sparkline__dot {
    position: absolute;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    border: 2px solid var(--surface);
    transform: translate(-4px, -4px);
    box-shadow: 0 0 0 1px color-mix(in oklab, var(--ink) 14%, transparent), 0 2px 6px color-mix(in oklab, var(--ink) 16%, transparent);
    pointer-events: none;
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .sparkline__dot--visible {
    opacity: 1;
  }
</style>

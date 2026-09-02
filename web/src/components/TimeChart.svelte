<!--
  TimeChart: the uPlot wrapper every metric chart (container detail,
  GPU, storage, ...) builds on. One y-axis always (series never get a
  second scale); a legend when >=2 series; a crosshair-driven tooltip;
  vertical event-marker lines with a hover label; an optional shaded
  band behind the plot (`band` prop, see its own doc below); ResizeObserver-
  driven width; re-creates on theme change (colors are baked into the
  canvas as literal values, not live var() references).

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
  import { theme, resolveToken, withAlpha } from '../lib/theme.svelte';
  import { fmtRelTime } from '../lib/format';
  import { needsRebuild } from '../lib/chartRebuild';
  import { buildAlignedData } from '../lib/seriesAlign';
  import { xRange as resolveXRange } from '../lib/chartRange';
  import { advanceHeadState, headValue, liveWindowRange, LIVE_WINDOW_SEC } from '../lib/streamdriver';
  import { subscribeWhileVisible } from '../lib/streamdriver.svelte';
  import { live as liveStore } from '../lib/sse.svelte';
  import { motion } from '../lib/motion.svelte';
  import { scrubBus } from '../lib/scrubbus.svelte';

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
  // showLegend (additive, optional, default true): the Metrics page's own
  // multi-line hero (up to 9 series -- top-8 containers + a muted host
  // total) drives an external legend of its own (icon+name chips tied to
  // the ranked list below, see TopConsumers.svelte), so uPlot's plain
  // text-table legend would just be a second, redundant one underneath
  // it. Every other caller (ContainerDetail, GPU, ...) has no chip row
  // of its own and keeps relying on this one exactly as before.
  //
  // focusSeries/toggleSeries (exported, additive) are the hero's own
  // hook for driving THAT external legend's hover/click straight into
  // uPlot's already-built-in per-series dimming/hide, rather than
  // reimplementing either: uPlot indexes series from 1 (0 is the shared
  // x-axis), matching `series`'s own array position + 1 -- callers pass
  // that same 1-based index back in.

  // xDomain (additive, optional -- D2 chart-integrity pass): [from, to]
  // unix seconds, the REQUESTED window a fetched (non-live) caller asked
  // /api/series for. When given, the x-axis shows exactly this span
  // regardless of how much of it actually has data -- see
  // lib/chartRange.ts's own doc for the sparse-data bug this fixes.
  // Never set by a live caller (the sliding window below already owns
  // live mode's own x-range every tick).

  // band (additive, optional -- the evidence drawer's own incident
  // chart): [from, to] unix seconds, a shaded rect drawn behind the plot
  // marking a span the caller wants visually set apart from its own
  // padding context (e.g. an insight's own active window, distinct from
  // the wider fetched xDomain around it) -- see drawBand below. A plain
  // whisper-weight ink fill, the exact GRID_ALPHA_PCT tier this file's
  // own gridlines already use, so it reads as "this range matters" in
  // both themes without competing with any series' own color.
  let {
    series = [],
    unit = '',
    height = 220,
    markers = [],
    band = undefined,
    syncKey = undefined,
    formatValue = undefined,
    live = false,
    showLegend = true,
    xDomain = undefined,
  } = $props();

  export function focusSeries(idx) {
    chart?.setSeries(idx, { focus: idx !== null });
  }

  export function toggleSeries(idx) {
    if (!chart) return;
    chart.setSeries(idx, { show: !chart.series[idx]?.show });
  }

  // FOCUS_DIM_ALPHA/FOCUS_PROX_PX (hover-scrub design's "per-series
  // focus"): uPlot's own built-in cursor.focus mechanism handles this
  // entirely -- no custom code needed. Enabling it dims every series
  // (canvas stroke/fill, its own DOM cursor-point marker, AND its own
  // legend row -- all three, automatically) except whichever one is
  // nearest the cursor, which stays full-alpha; that relative contrast
  // IS the "nearest series emphasized" the design asks for. FOCUS_PROX_PX
  // is deliberately huge (the type's own doc caps meaningful values at
  // 1e6) so a series is always considered "close enough" to focus
  // regardless of the cursor's actual y-distance from it -- this always
  // resolves to nearest-by-y rather than only within some pixel radius.
  const FOCUS_DIM_ALPHA = 0.28;
  const FOCUS_PROX_PX = 1e6;

  const SEVERITY_VAR = {
    info: '--status-good',
    warning: '--status-warning',
    serious: '--status-serious',
    critical: '--status-critical',
  };

  // --- D2 visual modernization: quieter grid/axes, matching the
  // microlabel type ramp instead of uPlot's own defaults --------------
  const GRID_ALPHA_PCT = 12; // whisper-weight horizontal gridlines only -- see build()'s axes config
  const AXIS_FONT = '11px "Inter Variable", ui-sans-serif, system-ui, sans-serif'; // canvas fonts can't read var(--font-sans); literal stack
  const MIN_TICK_SPACE_PX = 72; // calmer tick density on both axes vs. uPlot's own tighter default

  // xScaleRange is uPlot's Range.Function for the x scale -- the actual
  // padding-vs-explicit-domain decision is lib/chartRange.ts's pure
  // xRange (unit-tested there); this is just the glue that hands it the
  // CURRENT xDomain prop, which a plain top-level function closing over
  // $props() already reads live (Svelte 5 props are signals).
  function xScaleRange(_u, initMin, initMax) {
    return resolveXRange(initMin, initMax, xDomain);
  }

  let container;
  let plotEl;
  let chart = null;
  let ro = null;
  const scrubSourceId = {};
  let applyingBusCursor = false;
  let pointerInside = false;

  // prevShape is the last-built chart's structural shape (see
  // lib/chartRebuild.ts) -- compared against the current one on every
  // effect run to decide destroy+rebuild vs. the cheap setData path below.
  let prevShape = null;

  // tooltip/hoverMarker are plain $state (not derived from uPlot's own
  // data) because they're driven imperatively from uPlot's cursor
  // hook, not from a Svelte-tracked reactive computation.
  let tooltip = $state(null); // {x, y, ts, rows: [{label, color, value}]}
  let hoverMarker = $state(null); // {x, marker}

  // buildAlignedData: see lib/seriesAlign.ts (moved there so it's
  // independently unit-testable, matching this file's own established
  // split for streamdriver/livering/theme -- see their own docs). Also
  // now bridges an isolated small per-series gap via interpolation (that
  // module's own doc has the full root-cause story for why this, not
  // cross-series timestamp realignment, turned out to be the fix the
  // "disconnected patches" bug actually needed).

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
  // current glided value, MUTATING `aligned` in place rather than
  // returning a copy -- safe because `alignedData` is only ever the
  // pristine target array immediately after the data effect rebuilds it
  // wholesale via buildAlignedData(); every read of a series' true raw
  // tail (tailValue, above) happens before this runs again for that
  // series, so patching the disposable last slot here can never leak a
  // stale glided value back out as if it were real data. Skips the
  // per-tick array-clone this would otherwise cost at up to 30fps across
  // every series of every live chart on screen. No durationMs parameter:
  // each state's own durationMs (fixed at the instant advanceHeadState
  // created that leg -- see its doc) is what headValue must read: under
  // reduced motion that's always 0, so headValue snaps straight to
  // targetValue -- the driver's own ticks never run in that state (see
  // streamdriver.svelte.ts), so without this the tail would otherwise
  // freeze one arrival stale forever instead of tracking each new value
  // immediately.
  function applyHeadState(aligned, state, nowMs) {
    for (let i = 0; i < state.length; i++) {
      const s = state[i];
      if (!s) continue;
      const oneSeries = aligned[i + 1];
      oneSeries[oneSeries.length - 1] = headValue(s.prevValue, s.targetValue, s.arrivalMs, nowMs, s.durationMs);
    }
    return aligned;
  }

  function markerColor(severity) {
    return resolveToken(`var(${SEVERITY_VAR[severity] ?? SEVERITY_VAR.info})`);
  }

  // drawBand is a uPlot "draw" hook, run BEFORE drawMarkers (registered
  // first in hooks.draw below) so a marker's own dashed line always
  // paints on TOP of the shaded rect rather than under it. Clips to the
  // plot's own bbox exactly like drawMarkers' own visibility check, so a
  // band that only partly overlaps the visible x-range (the common case:
  // the incident's own unpadded span sitting inside the wider padded
  // xDomain around it) never draws past the axes.
  function drawBand(u) {
    if (!band) return;
    const { ctx } = u;
    const x1 = Math.max(u.bbox.left, u.valToPos(band[0], 'x', true));
    const x2 = Math.min(u.bbox.left + u.bbox.width, u.valToPos(band[1], 'x', true));
    if (x2 <= x1) return;
    ctx.save();
    ctx.fillStyle = withAlpha(resolveToken('var(--ink)'), GRID_ALPHA_PCT);
    ctx.fillRect(x1, u.bbox.top, x2 - x1, u.bbox.height);
    ctx.restore();
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

  // directionDetail (additive, optional -- the Metrics hero's own
  // directional resources): a series can carry directionPoints ([RingPoint[],
  // RingPoint[]], its down/up or read/write pair -- the SAME two values
  // the drawn line itself sums together) plus directionLabels for the
  // short prefix each side renders with (['↓','↑'], ['r','w'], matching
  // TopBarList's own ROW_DIRECTION_LABELS convention). Rendering only the
  // sum on the line but the full split in the tooltip keeps the chart
  // itself readable at up to 9 lines while a hover still answers "how
  // much of this was download vs. upload" -- exactly topFromFrame's own
  // row.direction, just looked up at the hovered instant instead of now.
  // Looked up by exact timestamp match (points are ring buffers, already
  // sorted ascending) rather than aligned into uPlot's own data arrays --
  // it's tooltip-only, never drawn, so it doesn't need to live on the
  // same shared x-axis buildAlignedData computes for the real series.
  function directionDetail(s, ts) {
    if (!s.directionPoints || ts == null) return undefined;
    const [ptsA, ptsB] = s.directionPoints;
    const valueAt = (pts) => pts.find((p) => p[0] === ts)?.[1];
    const a = valueAt(ptsA);
    const b = valueAt(ptsB);
    if (a === undefined && b === undefined) return undefined;
    const fmt = (v) => (v === undefined ? '—' : formatValue ? formatValue(v) : v);
    const [labelA, labelB] = s.directionLabels ?? ['A', 'B'];
    return `${labelA} ${fmt(a)} · ${labelB} ${fmt(b)}`;
  }

  // --- Hover fade-fill (D2 visual pass: "multi-line charts fill ONLY
  // the focused/hovered line... hover focuses a line (existing legend
  // hover) and its fill fades in") -------------------------------------
  //
  // fillFocusIdx mirrors whichever series uPlot's own built-in
  // cursor.focus mechanism has ALREADY decided is nearest the cursor
  // (series[i]._focus, set before every setCursor hook fires) -- not a
  // second focus computation of our own, so the fill always agrees with
  // whichever line's stroke/legend row is simultaneously undimmed.
  // seriesFill (used in build()) reads fillFocusIdx/fillFocusChangedAtMs
  // on every uPlot draw to decide whether a given series gets a gradient
  // at all, and how far into its own fade-in it currently is.
  const FILL_FADE_MS = 240;
  const FILL_TOP_ALPHA_PCT = 15; // enough depth to locate the line without turning the plot into a solid area chart
  let fillFocusIdx = null;
  let fillFocusChangedAtMs = 0;
  let fillFadeRaf = null;

  function fillFadeProgress(nowMs) {
    if (motion.reduced) return 1; // no glide under reduced motion -- snaps straight to full strength
    return Math.min(1, Math.max(0, (nowMs - fillFocusChangedAtMs) / FILL_FADE_MS));
  }

  function runFillFade() {
    fillFadeRaf = null;
    chart?.redraw(false, false); // cheap repaint -- no path rebuild, no axis recalc
    if (fillFadeProgress(Date.now()) < 1) fillFadeRaf = requestAnimationFrame(runFillFade);
  }

  // trackFillFocus reads the CURRENT focus off uPlot's own per-series
  // state (u===null on cursor-leave, matching handleCursor's own early
  // return below) and, only on an actual change, (re)starts the short
  // rAF loop above -- never a second overlapping one: a focus change
  // mid-fade just retargets the already-running loop, since seriesFill
  // reads fillFocusChangedAtMs fresh on every draw regardless of which
  // invocation of this function last moved it.
  function trackFillFocus(u) {
    const foundIdx = u ? u.series.findIndex((s, i) => i > 0 && s._focus) : -1;
    const nextIdx = foundIdx > 0 ? foundIdx : null;
    if (nextIdx === fillFocusIdx) return;
    fillFocusIdx = nextIdx;
    fillFocusChangedAtMs = Date.now();
    if (!motion.reduced && fillFadeRaf === null) fillFadeRaf = requestAnimationFrame(runFillFade);
  }

  // seriesFill builds series `seriesIdx`'s own uPlot `fill` option, as a
  // FUNCTION (not a static color) so it can read u.bbox fresh on every
  // draw -- a resize must not leave a stale gradient rect behind. A
  // single-series chart (a tile-scale metric, ContainerDetail's own
  // per-metric cards, any chart that happens to have exactly one line)
  // always fades from FILL_TOP_ALPHA_PCT at the plot's top to fully
  // transparent at its bottom baseline -- there's no ambiguity about
  // which line "has focus" when there's only one. A multi-series chart
  // (the Metrics hero, Storage's per-drive lines -- 10 simultaneous
  // fills would read as mud, per the design brief) only ever fills the
  // ONE series uPlot itself currently has focused, ramping in via
  // fillFadeProgress rather than snapping; every other series renders no
  // fill at all until IT becomes the focused one.
  function seriesFill(colorHex, seriesIdx, isMulti) {
    return (u) => {
      const alphaMul = isMulti ? (seriesIdx === fillFocusIdx ? fillFadeProgress(Date.now()) : 0) : 1;
      if (alphaMul <= 0) return 'transparent';
      const top = u.bbox.top;
      const bottom = Math.max(top + 1, u.bbox.top + u.bbox.height);
      const grad = u.ctx.createLinearGradient(0, top, 0, bottom);
      grad.addColorStop(0, withAlpha(colorHex, FILL_TOP_ALPHA_PCT * alphaMul));
      grad.addColorStop(1, withAlpha(colorHex, 0));
      return grad;
    };
  }

  // handleCursor is a uPlot "setCursor" hook: recomputes the tooltip
  // (crosshair values for every series at the hovered index), the
  // hover-fill focus tracking above, and which marker, if any, the
  // cursor is close enough to for a hover label. u.cursor.left is
  // CSS-pixel space, so marker hit-testing uses valToPos's CSS-pixel
  // variant (canvasPixels omitted) to match.
  function handleCursor(u) {
    const eventTarget = u.cursor.event?.target;
    const localPointer = !!eventTarget && !!container?.contains(eventTarget);
    if (u.cursor.left == null || u.cursor.left < 0) {
      if (localPointer && !applyingBusCursor) scrubBus.clear(scrubSourceId);
      tooltip = null;
      hoverMarker = null;
      trackFillFocus(null);
      return;
    }
    trackFillFocus(u);

    const idx = u.cursor.idx;
    tooltip =
      idx == null
        ? null
        : {
            x: u.cursor.left,
            y: u.cursor.top ?? 0,
            ts: u.data[0][idx],
            // Sorted desc by raw value (nulls last): "one clean floating
            // panel" reads top-to-bottom as a mini leaderboard of
            // whatever's hovered, rather than whichever order the
            // caller's own series array happens to be in (rank order for
            // the hero chart, but URL/request order for Compare,
            // declaration order for ContainerDetail's per-engine/
            // direction charts).
            rows: series
              .map((s, i) => {
                const raw = u.data[i + 1][idx];
                // A real gap (null -- see buildAlignedData) stays unformatted
                // ('—' is rendered by the template below) rather than being
                // handed to formatValue, which would otherwise turn a
                // missing sample into a misleadingly concrete "0.0 B".
                return {
                  label: s.label,
                  color: resolveToken(s.colorVar),
                  raw,
                  value: raw == null ? null : formatValue ? formatValue(raw) : raw,
                  detail: directionDetail(s, u.data[0][idx]),
                };
              })
              .sort((a, b) => (b.raw ?? -Infinity) - (a.raw ?? -Infinity)),
          };

    if (pointerInside && !applyingBusCursor && tooltip?.ts != null) {
      scrubBus.publish(tooltip.ts, scrubSourceId);
    }

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

  // A uPlot TimeChart can't release the scrub from handleCursor's own
  // cursor-leave alone (see scrubBus.enter/leave): its crosshair is
  // syncKey-synced and re-fired by a live setData every frame, so the
  // owner's clear can lose that race. Bracket real pointer occupancy on
  // the container instead. enter marks this chart as the genuine pointer
  // source, so a live-tick re-fire of handleCursor can't re-publish a
  // stale cursor once the pointer is gone; leave hands the slot back and
  // the bus forces itself to live when the last surface leaves. The
  // guards keep enter/leave balanced against repeat or crossed events.
  function handleEnter() {
    if (pointerInside) return;
    pointerInside = true;
    scrubBus.enter();
  }

  function handleLeave() {
    if (!pointerInside) return;
    pointerInside = false;
    scrubBus.leave();
    tooltip = null;
    hoverMarker = null;
  }

  function build() {
    if (!plotEl || !container) return;
    chart?.destroy();
    chart = null;
    // A structural rebuild can reorder/replace series entirely -- reset
    // headState so index i can't end up easing a NEW series toward a
    // STALE target left over from whatever used to be at that index.
    headState = [];
    // A fresh uPlot instance has no series identity for fillFocusIdx to
    // keep meaning the same line -- start every rebuild with no fill
    // focus and no fade in flight, same as a genuine cursor-leave.
    fillFocusIdx = null;
    fillFocusChangedAtMs = 0;

    const width = container.clientWidth || 320;
    const inkMuted = resolveToken('var(--ink-2)');
    // gridColor: the y-axis's own whisper-weight horizontal lines (D2
    // pass: "kill the full gridline cage... horizontal gridlines only,
    // whisper-weight"). The x-axis gets none at all (grid.show:false
    // below) -- its own tick LABELS are the only thing marking time now.
    const gridColor = withAlpha(inkMuted, GRID_ALPHA_PCT);
    const isMulti = series.length > 1;

    chart = new uPlot(
      {
        width,
        height,
        padding: [12, 12, 10, 8],
        scales: { x: { time: true, range: xScaleRange } },
        axes: [
          {
            stroke: inkMuted,
            font: AXIS_FONT,
            grid: { show: false },
            ticks: { show: false },
            space: MIN_TICK_SPACE_PX,
          },
          {
            stroke: inkMuted,
            font: AXIS_FONT,
            grid: { stroke: gridColor, width: 1 },
            ticks: { show: false },
            space: MIN_TICK_SPACE_PX,
            values: (_u, vals) => vals.map((v) => (formatValue ? formatValue(v) : unit ? `${v} ${unit}` : `${v}`)),
            // uPlot's own default y-axis gutter width is sized for short
            // bare numbers; a formatValue label ("858.3 MiB", "12.4 MB/s")
            // is wider and was observed getting left-clipped (text-align
            // right against too narrow a box runs the label's leading
            // characters off the canvas's left edge) -- size the gutter
            // off the actual longest rendered label instead of trusting
            // uPlot's un-aware default. The 6.5px/char + 12px estimate
            // (tightened for AXIS_FONT's smaller size, down from the
            // pre-modernization 7px/14px) is deliberately generous
            // (default font is small/proportional, not truly monospace
            // at this rendering size) since a slightly wide gutter costs
            // a little plot area, while a narrow one clips.
            size: (_u, values) => {
              // uPlot calls this during layout passes where `values` can
              // be null (e.g. before any ticks are computed yet) --
              // guard defensively rather than let a null.reduce blank
              // the whole chart (reproduced live while building this).
              const longest = (values ?? []).reduce((max, v) => Math.max(max, String(v ?? '').length), 0);
              return Math.max(36, longest * 6.5 + 12);
            },
          },
        ],
        series: [
          {},
          ...series.map((s, i) => {
            const colorHex = resolveToken(s.colorVar);
            return {
              label: s.label,
              // strokeAlphaPct (optional -- the hero chart's own muted
              // "Host total" reference line): mutes the resolved color
              // itself rather than drawing a dotted line, per the D2
              // pass's "drop the dotted-noise look" -- a cleaner, calmer
              // way to stay visually distinct from the solid container
              // lines around it.
              stroke: s.strokeAlphaPct != null ? withAlpha(colorHex, s.strokeAlphaPct) : colorHex,
              width: s.width ?? 2.25,
              dash: s.dash,
              cap: 'round', // D2 pass: "rounded joins/caps" (joins already default to round in this uPlot version)
              points: { show: false },
              fill: seriesFill(colorHex, i + 1, isMulti),
            };
          }),
        ],
        cursor: {
          points: { show: true, size: 8, width: 2 },
          focus: { prox: FOCUS_PROX_PX },
          ...(syncKey ? { sync: { key: syncKey } } : {}),
        },
        focus: { alpha: FOCUS_DIM_ALPHA },
        legend: { show: showLegend && series.length >= 2 },
        hooks: {
          draw: [drawBand, drawMarkers],
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
      series: series.map((s) => ({ label: s.label, colorVar: s.colorVar, width: s.width, dash: s.dash, strokeAlphaPct: s.strokeAlphaPct })),
      theme: theme.resolved,
      unit,
      hasFormatValue: !!formatValue,
      showLegend,
    };
  }

  $effect(() => {
    // Track every input that can affect either path below: a structural
    // one (series shape, unit, formatValue, a theme flip) goes through
    // build(), same as before; markers/band/xDomain are tracked here too
    // even though they're absent from currentShape()'s shape, purely so a
    // marker-, band-, or domain-only change still re-runs this effect at
    // all -- drawBand/drawMarkers/handleCursor/xScaleRange already read
    // the live `band`/`markers`/`series`/`xDomain` bindings fresh on every
    // uPlot redraw, so the setData call below (which re-triggers uPlot's
    // own x-scale auto-ranging for a non-live chart) is all either needs
    // to actually show up.
    series;
    unit;
    markers;
    band;
    xDomain;
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
      // durationMs is THIS arrival's own leg duration: the shared
      // driver's freshly-measured cadence EMA (liveStore.glideMs), or 0
      // to snap under reduced motion -- see streamdriver.ts's
      // "Cadence-driven glide" doc for why this varies per arrival
      // instead of a fixed guess.
      const durationMs = motion.reduced ? 0 : liveStore.glideMs;
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
      chart.setData(applyHeadState(alignedData, headState, nowMs), false);
    } else if (!rebuilding) {
      chart.setData(buildAlignedData(series));
    }
    prevShape = shape;
  });

  // Bridge TimeChart's uPlot cursor group to the same page-global scrub
  // instant used by sparklines and stat tiles. Existing syncKey groups
  // still handle TimeChart-to-TimeChart movement; this adds the missing
  // TimeChart-to-Sparkline direction without creating a second clock.
  $effect(() => {
    const ts = scrubBus.ts;
    if (!chart || scrubBus.isOwner(scrubSourceId)) return;
    applyingBusCursor = true;
    if (ts === null) {
      chart.setCursor({ left: -10, top: -10 });
    } else {
      const left = chart.valToPos(ts, 'x');
      if (Number.isFinite(left)) chart.setCursor({ left, top: chart.cursor.top ?? chart.bbox.height / 2 });
    }
    applyingBusCursor = false;
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
          chart.setData(applyHeadState(alignedData, headState, nowMs), false);
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
    if (pointerInside) scrubBus.leave();
    scrubBus.clear(scrubSourceId);
    ro?.disconnect();
    if (fillFadeRaf !== null) cancelAnimationFrame(fillFadeRaf);
    chart?.destroy();
  });
</script>

<div
  class="time-chart"
  bind:this={container}
  onpointerenter={handleEnter}
  onpointerleave={handleLeave}
  onpointercancel={handleLeave}
>
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
        {#if row.detail}
          <div class="time-chart__tooltip-detail tabular-nums">{row.detail}</div>
        {/if}
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
    border-radius: 11px;
    background: linear-gradient(180deg, color-mix(in oklab, var(--accent) 3%, var(--surface-muted)), transparent 72%);
  }
  .time-chart :global(.uplot) {
    border-radius: inherit;
  }
  .time-chart :global(.u-legend) {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 0.75rem;
    padding-top: 0.35rem;
  }
  .time-chart :global(.u-legend .u-marker) {
    border-radius: 999px !important;
  }
  /* Per-series focus dimming (see FOCUS_DIM_ALPHA above) sets opacity as
     a plain inline style/class straight from uPlot's own JS -- these
     transitions are the only thing that makes that step change read as
     an eased fade rather than a snap; the DIMMING decision itself stays
     entirely uPlot's, this is CSS-only polish on top of it. */
  .time-chart :global(.u-legend tr) {
    transition: opacity 150ms ease;
  }
  .time-chart :global(.u-cursor-pt) {
    border: 2px solid var(--surface) !important;
    border-radius: 50%;
    box-shadow: 0 0 0 1px color-mix(in oklab, var(--ink) 14%, transparent), 0 2px 7px color-mix(in oklab, var(--ink) 18%, transparent);
    transition: opacity 150ms ease, box-shadow 150ms ease;
  }
  .time-chart :global(.u-cursor-x) {
    border-right: 1px solid color-mix(in oklab, var(--ink) 24%, transparent) !important;
  }
  .time-chart :global(.u-cursor-y) {
    border-bottom-color: transparent !important;
  }
  /* Unified tooltip (D2 pass): one floating panel, elevated off the
     chart with the same card-shadow FORMULA app.css's own .card uses
     (color-mix against --ink, not a new token), just carried further
     (wider blur, no border-driven light-mode look) since this needs to
     read as floating ABOVE plotted lines, not as a static surface. */
  .time-chart__tooltip {
    position: absolute;
    transform: translate(12px, 12px);
    min-width: 11.5rem;
    background: color-mix(in oklab, var(--surface) 96%, transparent);
    border: 1px solid var(--border);
    border-radius: 11px;
    padding: 0.62rem 0.72rem;
    color: var(--ink);
    font-size: 0.75rem;
    white-space: nowrap;
    pointer-events: none;
    z-index: 5;
    box-shadow: var(--shadow-md);
    backdrop-filter: blur(12px) saturate(125%);
  }
  .time-chart__tooltip > .microlabel {
    display: block;
    margin-bottom: 0.35rem;
  }
  .time-chart__tooltip-row {
    display: grid;
    grid-template-columns: 8px minmax(0, 1fr) auto;
    align-items: center;
    gap: 0.5rem;
    padding: 0.16rem 0;
  }
  .time-chart__tooltip-detail {
    margin: 0 0 0.1rem 1.2rem; /* aligns under the row's own label, past the swatch */
    color: var(--ink-2);
    font-size: 0.68rem;
  }
  .time-chart__swatch {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .time-chart__marker-label {
    position: absolute;
    top: 2px;
    transform: translateX(-50%);
    background: var(--surface);
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 7px;
    padding: 0.18rem 0.42rem;
    pointer-events: none;
    z-index: 4;
    white-space: nowrap;
  }
</style>

// Pure x-scale range decision for TimeChart's uPlot config, pulled out
// into its own testable module (same split every other TimeChart/
// Sparkline pure-logic file in this app uses). Two problems, one
// function:
//
// 1. MIN_X_SPAN_SEC (pre-existing): a live chart fed by a fresh,
//    near-empty ring -- or any chart briefly down to 0-1 real points --
//    hands uPlot a near-zero real domain. uPlot's own auto tick-picker
//    mishandles that: YEAR-granularity gridlines with no visible data,
//    reproduced live while building the GPU/Events views (seen: "2027",
//    "2028", "2029"). Padding a too-narrow [initMin, initMax] out to a
//    small floor keeps every chart in the few-seconds-to-months range
//    where uPlot's own picker behaves; the natural span always clears
//    this floor within a couple more live ticks, so it only ever smooths
//    over that brief startup window.
//
// 2. xDomain (D2 chart-integrity pass): the SAME pathology, triggered a
//    different way -- a FETCHED (non-live) window's REQUESTED span (the
//    [from, to] a caller actually asked /api/series for) can be far
//    wider than however much of it has real data. A container with two
//    minutes of real history inside a requested 7-day window still has
//    initMin/initMax only ~120 seconds apart once uPlot auto-ranges off
//    the DATA's own extent -- narrow enough, in practice, to occasionally
//    still misbehave the same way (1) does, and it reads wrong
//    regardless of whether it does: a "7d" chart should show a 7-day
//    axis, not silently shrink to whatever slice happens to have points.
//    An explicit xDomain bypasses BOTH the data-extent guess and the
//    padding below, honoring the caller's own requested window exactly --
//    even one narrower than minSpanSec, since that's a deliberate,
//    explicit choice rather than an auto-derived guess that needs
//    smoothing.
export const MIN_X_SPAN_SEC = 10;

export function xRange(
  initMin: number | null,
  initMax: number | null,
  xDomain?: readonly [number, number],
  minSpanSec: number = MIN_X_SPAN_SEC,
): [number | null, number | null] {
  if (xDomain) return [xDomain[0], xDomain[1]];
  if (initMin == null || initMax == null) return [initMin, initMax];
  const span = initMax - initMin;
  if (span >= minSpanSec) return [initMin, initMax];
  const pad = (minSpanSec - span) / 2;
  return [initMin - pad, initMax + pad];
}

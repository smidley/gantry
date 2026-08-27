// Pure math + state-machine logic behind "smooth streaming" (see
// docs/superpowers/sdd/smooth-streaming): the shared animation driver's
// throttle decision and subscription gating, plus the two per-tick
// formulas (the live sliding x-window, the newest-sample head ease)
// TimeChart/Sparkline apply while charting a live series. Deliberately
// plain (no runes, no svelte/motion import, no DOM) so it's trivially
// unit-testable and carries no import-time browser dependency -- same
// split as lib/livering.ts/lib/theme.ts vs. their own ".svelte.ts"
// wrappers. Critically, svelte/motion's prefersReducedMotion touches
// window.matchMedia at MODULE LOAD (not just on use), so anything a
// vitest ".test.ts" imports transitively must never pull that module in;
// see lib/streamdriver.svelte.ts's own doc for where that actually lives.

// LIVE_WINDOW_SEC is the sliding strip-chart window's fixed span (15
// minutes) -- matches lib/livering.ts's own pushRing default and every
// real liveRing() call in this app, so the visible window and the ring's
// own data horizon stay in lockstep long-term; only the window's EDGE
// moves continuously now; instead of jumping in step with the 2s tick
// cadence.
export const LIVE_WINDOW_SEC = 900;

// HEAD_EASE_MS is how long the newest sample takes to ease from its
// previous displayed value to the freshly-arrived one (mechanism 2).
export const HEAD_EASE_MS = 1500;

// MAX_FPS caps the shared driver's broadcast rate -- a display's own
// refresh rate (60Hz, 120Hz, ...) is far more often than a strip-chart
// window or an ease curve needs to visibly update; capping the work
// every subscriber does per second is what keeps a page full of live
// charts cheap.
export const MAX_FPS = 30;

// lerp: the one interpolation primitive every "ease toward a target"
// computation in this feature (head values, and indirectly svelte/
// motion's own Tween for mechanism 3) reduces to.
export function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

// easeOutCubic: fast start, slow finish -- the only curve mechanism 2
// uses (see the design's own "no springs/bounces" restraint). Clamps t
// to [0,1] itself so a caller never has to remember to before calling
// it; t<=0 -> 0 (unstarted), t>=1 -> 1 (settled).
export function easeOutCubic(t: number): number {
  const clamped = Math.min(1, Math.max(0, t));
  return 1 - Math.pow(1 - clamped, 3);
}

// headValue is mechanism 2 end to end: the displayed value for a
// series' newest point, easing from prevValue (what was displayed right
// before this sample arrived) to targetValue (the sample itself) over
// durationMs, timed off nowMs - arrivalMs (both wall-clock ms -- see
// streamdriver.svelte.ts's own doc for why the driver broadcasts
// Date.now() rather than requestAnimationFrame's own performance.now()-
// relative timestamp). A non-finite prev/target (defensive: a caller
// should never actually pass one) or a non-positive duration both skip
// straight to the settled target rather than propagating NaN into a
// chart.
export function headValue(
  prevValue: number,
  targetValue: number,
  arrivalMs: number,
  nowMs: number,
  durationMs: number = HEAD_EASE_MS,
): number {
  if (!Number.isFinite(prevValue) || !Number.isFinite(targetValue)) return targetValue;
  if (durationMs <= 0) return targetValue;
  const t = (nowMs - arrivalMs) / durationMs;
  return lerp(prevValue, targetValue, easeOutCubic(t));
}

// HeadState is one series' own in-flight head ease: prevValue/targetValue
// bracket the ease, arrivalMs anchors it in wall-clock time -- see
// headValue's own doc for how the three combine into a displayed value.
export interface HeadState {
  prevValue: number;
  targetValue: number;
  arrivalMs: number;
}

// advanceHeadState folds one freshly-arrived raw value into a series' own
// HeadState (mechanism 2, TimeChart/Sparkline's shared per-series
// bookkeeping -- both components call this once per real data arrival,
// TimeChart once per series, Sparkline once for its own single series).
// null prevState means "no ease yet" (the series' first real value,
// or fresh after a structural rebuild): snaps in unanimated, prevValue
// === targetValue. An unchanged raw value (an animation tick landing
// between two identical SSE frames, or a data-shape change with no new
// sample) keeps the EXISTING state so an in-flight ease never restarts.
//
// The one case that actually matters: raw has genuinely changed while a
// previous ease might still be mid-flight. The new ease's prevValue seeds
// from headValue(...) -- the value CURRENTLY ON SCREEN at nowMs, per the
// prior state -- never from prevState.targetValue (the old ease's
// destination). Seeding from the old target is the bug this function
// exists to not have: a re-arrival before the previous ease finishes
// (ordinary jitter, or a buffered catch-up burst) would otherwise jump
// the display straight to wherever the OLD ease was heading before
// continuing on to the new one -- a visible one-frame discontinuity,
// exactly what this whole feature exists to remove. svelte/motion's own
// Tween.set has the same contract: re-calling it captures the tween's
// live interpolated .current as the new start, never the previous
// target.
export function advanceHeadState(
  prevState: HeadState | null,
  raw: number,
  nowMs: number,
  durationMs: number = HEAD_EASE_MS,
): HeadState {
  if (!prevState) return { prevValue: raw, targetValue: raw, arrivalMs: nowMs };
  if (prevState.targetValue === raw) return prevState;
  const displayed = headValue(prevState.prevValue, prevState.targetValue, prevState.arrivalMs, nowMs, durationMs);
  return { prevValue: displayed, targetValue: raw, arrivalMs: nowMs };
}

// shouldBroadcast is the driver's own 30fps throttle decision: true once
// at least 1000/maxFps ms have elapsed since the last broadcast (or
// immediately, the very first time -- lastMs === null). A plain
// timestamp-delta check, not a setTimeout/setInterval, so the driver
// stays exactly one requestAnimationFrame loop that simply skips
// forwarding most of its ticks to subscribers, per the design's own
// "(timestamp delta check)" framing.
export function shouldBroadcast(lastMs: number | null, nowMs: number, maxFps: number = MAX_FPS): boolean {
  if (lastMs === null) return true;
  return nowMs - lastMs >= 1000 / maxFps;
}

// liveWindowRange is mechanism 1's own formula: the sliding [min, max]
// x-domain (in uPlot's own x units -- unix SECONDS, matching every ring
// timestamp and TimeChart's `time: true` scale) for "the last windowSec
// seconds, ending at nowMs." Called once per animation tick with the
// driver's own wall-clock nowMs, this is what makes the window glide
// continuously between the ring's discrete 2s arrivals.
export function liveWindowRange(nowMs: number, windowSec: number = LIVE_WINDOW_SEC): [number, number] {
  const nowSec = nowMs / 1000;
  return [nowSec - windowSec, nowSec];
}

// --- Subscription gating state machine --------------------------------
//
// The shared driver's rAF loop must run exactly while (subscribers > 0
// AND !reducedMotion) -- zero subscribers (every live chart currently
// off-screen, or none mounted) or reduced-motion both mean "stopped
// entirely," never merely "ticking with no listeners." Modeled as a
// tiny reducer over the driver's own bookkeeping (subscriber count +
// the current reduced-motion flag) so streamdriver.svelte.ts's actual
// rAF/IntersectionObserver wiring can stay a thin, untested shell around
// logic that's fully exercised here instead.

export interface GateState {
  subscribers: number;
  reducedMotion: boolean;
}

export const initialGateState: GateState = { subscribers: 0, reducedMotion: false };

export type GateEvent = { type: 'subscribe' } | { type: 'unsubscribe' } | { type: 'reduced-motion'; value: boolean };

// gateReducer never lets subscribers go negative -- an unsubscribe that
// somehow outnumbers subscribes (shouldn't happen; defensive) floors at
// 0 rather than going negative and making isDriverActive misread a
// negative count as "no subscribers" for the wrong reason.
export function gateReducer(state: GateState, event: GateEvent): GateState {
  switch (event.type) {
    case 'subscribe':
      return { ...state, subscribers: state.subscribers + 1 };
    case 'unsubscribe':
      return { ...state, subscribers: Math.max(0, state.subscribers - 1) };
    case 'reduced-motion':
      return { ...state, reducedMotion: event.value };
    default:
      return state;
  }
}

export function isDriverActive(state: GateState): boolean {
  return state.subscribers > 0 && !state.reducedMotion;
}

// Pure math + state-machine logic behind "smooth streaming" (see
// docs/superpowers/sdd/smooth-streaming): the shared animation driver's
// throttle decision and subscription gating, plus the two per-tick
// formulas (the live sliding x-window, the newest-sample head glide)
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

// --- Cadence-driven glide (perpetual-motion pass) ----------------------
//
// The original mechanism eased every newly-arrived sample over a FIXED
// 1500ms, well short of the ~2s real SSE cadence: every surface pulsed
// into place and then sat frozen for the remainder of each tick --
// Scott's own complaint ("things look very choppy... I want it to feel
// like it's flowing in real time"). The fix retimes each glide to span
// the MEASURED cadence instead of a guessed constant, so the displayed
// value is still visibly in motion the instant the next real sample
// lands, never idle between frames.
//
// MIN/MAX_CADENCE_MS bound the EMA against real-world jitter (a
// backgrounded-tab resume, GC pause, or a frame that's merely a little
// early/late) without letting one wild outlier become the next several
// legs' own duration -- also doubles as the "this was a genuine
// gap/reconnect, snap instead of gliding across it" threshold: a caller
// measuring a delta beyond MAX_CADENCE_MS (isReconnectGap, below) should
// pass durationMs=0 for that one leg, matching how reduced motion already
// skips the ease entirely, rather than crawling across a multi-second
// stall.
export const MIN_CADENCE_MS = 1000;
export const MAX_CADENCE_MS = 4000;

// CADENCE_EMA_ALPHA: how much weight each freshly-measured inter-arrival
// delta gets against the running estimate -- high enough to track a real
// cadence change (e.g. a server restart at a different tick rate) within
// a handful of frames, low enough that one jittery delta doesn't yank
// every surface's glide duration around on its own.
export const CADENCE_EMA_ALPHA = 0.3;

// DEFAULT_GLIDE_MS is the fallback duration for a caller (or test) that
// hasn't measured a real cadence yet -- production call sites always
// pass the shared driver's own live EMA estimate (see sse.svelte.ts's
// cadenceMs) instead of relying on this default.
export const DEFAULT_GLIDE_MS = 1500;

// updateCadenceEma folds one freshly-measured inter-arrival delta into a
// running exponential-moving-average estimate of the real cadence.
// deltaMs is clamped into [MIN_CADENCE_MS, MAX_CADENCE_MS] BEFORE
// blending -- a single huge outlier (a backgrounded tab, a GC pause)
// then nudges the estimate by at most alpha's own share of the clamped
// range, rather than swinging it wildly for several frames afterward; a
// weighted blend of always-in-range values stays in range too, so the
// result never needs a second clamp. null prevEma (no estimate yet) seeds
// straight from the first clamped delta, unaveraged.
export function updateCadenceEma(prevEma: number | null, deltaMs: number, alpha: number = CADENCE_EMA_ALPHA): number {
  const clamped = Math.min(MAX_CADENCE_MS, Math.max(MIN_CADENCE_MS, deltaMs));
  if (prevEma === null) return clamped;
  return prevEma + alpha * (clamped - prevEma);
}

// isReconnectGap reports whether a just-measured inter-arrival delta is
// large enough that the surfaces riding it should SNAP to the new value
// rather than glide across the gap (a stalled tab resuming, an SSE
// reconnect after the stream dropped) -- see MAX_CADENCE_MS's own doc for
// why this reuses the clamp ceiling as the same threshold.
export function isReconnectGap(deltaMs: number, maxCadenceMs: number = MAX_CADENCE_MS): boolean {
  return deltaMs > maxCadenceMs;
}

// lerp: the one interpolation primitive every "ease toward a target"
// computation in this feature (head values, and indirectly svelte/
// motion's own Tween for mechanism 3) reduces to.
export function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

// linear: constant velocity for the whole glide -- the curve mechanism 2
// uses now. A front-loaded curve (the previous easeOutCubic) visibly
// SETTLES well before the next real sample arrives at the ~2s real
// cadence, which is what reads as a pulse-then-freeze; a constant
// velocity keeps the displayed value moving for the glide's entire
// span, right up to the next retiming. Clamps t to [0,1] itself, same
// contract as the curve it replaced: t<=0 -> 0 (unstarted), t>=1 -> 1
// (settled).
export function linear(t: number): number {
  return Math.min(1, Math.max(0, t));
}

// headValue is mechanism 2 end to end: the displayed value for a
// series' newest point, gliding from prevValue (what was displayed right
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
  durationMs: number = DEFAULT_GLIDE_MS,
): number {
  if (!Number.isFinite(prevValue) || !Number.isFinite(targetValue)) return targetValue;
  if (durationMs <= 0) return targetValue;
  const t = (nowMs - arrivalMs) / durationMs;
  return lerp(prevValue, targetValue, linear(t));
}

// HeadState is one series' own in-flight head glide: prevValue/targetValue
// bracket the glide, arrivalMs anchors it in wall-clock time, and
// durationMs is THIS LEG's own span -- fixed at the instant it was
// created (from whatever cadence was measured then) and never
// retroactively changed, so a later shift in the driver's own EMA can't
// make an already-in-flight glide's math inconsistent with itself. See
// headValue's own doc for how the four combine into a displayed value.
export interface HeadState {
  prevValue: number;
  targetValue: number;
  arrivalMs: number;
  durationMs: number;
}

// advanceHeadState folds one freshly-arrived raw value into a series' own
// HeadState (mechanism 2, TimeChart/Sparkline's shared per-series
// bookkeeping -- both components call this once per real data arrival,
// TimeChart once per series, Sparkline once for its own single series).
// null prevState means "no glide yet" (the series' first real value,
// or fresh after a structural rebuild): snaps in unanimated, prevValue
// === targetValue. An unchanged raw value (an animation tick landing
// between two identical SSE frames, or a data-shape change with no new
// sample) keeps the EXISTING state so an in-flight glide never restarts.
//
// durationMs is THIS call's own leg duration -- the caller's freshly
// measured cadence (or 0, to snap: reduced motion, or a reconnect gap
// per isReconnectGap) -- stored on the returned state for every future
// headValue read of it (see HeadState's own doc for why a leg's duration
// never changes after creation). The SEED for that new leg, however, is
// computed off the OLD leg's own duration (prevState.durationMs), not
// this call's: the seed asks "where is the display RIGHT NOW, per the
// glide already in flight," which only that older duration answers
// correctly.
//
// The one case that actually matters: raw has genuinely changed while a
// previous glide might still be mid-flight. The new glide's prevValue
// seeds from headValue(...) -- the value CURRENTLY ON SCREEN at nowMs,
// per the prior state -- never from prevState.targetValue (the old
// glide's destination). Seeding from the old target is the bug this
// function exists to not have: a re-arrival before the previous glide
// finishes (ordinary jitter, or a buffered catch-up burst) would
// otherwise jump the display straight to wherever the OLD glide was
// heading before continuing on to the new one -- a visible one-frame
// discontinuity, exactly what this whole feature exists to remove.
// svelte/motion's own Tween.set has the same contract: re-calling it
// captures the tween's live interpolated .current as the new start,
// never the previous target.
export function advanceHeadState(
  prevState: HeadState | null,
  raw: number,
  nowMs: number,
  durationMs: number = DEFAULT_GLIDE_MS,
): HeadState {
  if (!prevState) return { prevValue: raw, targetValue: raw, arrivalMs: nowMs, durationMs };
  if (prevState.targetValue === raw) return prevState;
  const displayed = headValue(prevState.prevValue, prevState.targetValue, prevState.arrivalMs, nowMs, prevState.durationMs);
  return { prevValue: displayed, targetValue: raw, arrivalMs: nowMs, durationMs };
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

// MAX_FPS caps the shared driver's broadcast rate -- a display's own
// refresh rate (60Hz, 120Hz, ...) is far more often than a strip-chart
// window or a glide computation needs to visibly update; capping the
// work every subscriber does per second is what keeps a page full of
// live charts cheap.
export const MAX_FPS = 30;

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

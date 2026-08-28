import { describe, expect, it } from 'vitest';
import {
  advanceHeadState,
  gateReducer,
  headValue,
  initialGateState,
  isDriverActive,
  isReconnectGap,
  lerp,
  linear,
  liveWindowRange,
  MAX_CADENCE_MS,
  MIN_CADENCE_MS,
  shouldBroadcast,
  updateCadenceEma,
  type GateState,
  type HeadState,
} from './streamdriver';

describe('lerp', () => {
  it('returns a at t=0 and b at t=1', () => {
    expect(lerp(10, 20, 0)).toBe(10);
    expect(lerp(10, 20, 1)).toBe(20);
  });

  it('interpolates linearly at a midpoint', () => {
    expect(lerp(0, 100, 0.5)).toBe(50);
    expect(lerp(10, 20, 0.25)).toBe(12.5);
  });

  it('extrapolates for t outside [0,1] -- callers that need clamping do it themselves', () => {
    expect(lerp(0, 10, 1.5)).toBe(15);
    expect(lerp(0, 10, -0.5)).toBe(-5);
  });
});

describe('linear', () => {
  it('starts at 0 and ends at 1', () => {
    expect(linear(0)).toBe(0);
    expect(linear(1)).toBe(1);
  });

  it('clamps t below 0 to the start and above 1 to the end', () => {
    expect(linear(-1)).toBe(0);
    expect(linear(2)).toBe(1);
  });

  it('progresses at constant velocity -- exactly half done at the midpoint, unlike a front-loaded curve', () => {
    expect(linear(0.5)).toBe(0.5);
    expect(linear(0.25)).toBe(0.25);
  });
});

describe('headValue', () => {
  it('reads as prevValue the instant the sample arrives (t=0)', () => {
    expect(headValue(10, 50, 1000, 1000)).toBe(10);
  });

  it('reads as targetValue once the glide duration has fully elapsed', () => {
    expect(headValue(10, 50, 1000, 1000 + 1500)).toBe(50);
    expect(headValue(10, 50, 1000, 1000 + 5000)).toBe(50); // well past duration, still settled
  });

  it('follows a linear curve mid-flight, not a front-loaded one', () => {
    // t = 0.5 of the default 1500ms duration -> linear(0.5) = 0.5
    const v = headValue(0, 100, 1000, 1000 + 750);
    expect(v).toBeCloseTo(50, 10);
  });

  it('honors a caller-supplied duration instead of the 1500ms default', () => {
    expect(headValue(0, 100, 1000, 1200, 400)).toBe(headValue(0, 100, 0, 200, 400));
    expect(headValue(0, 100, 1000, 1400, 400)).toBe(100); // fully elapsed at 400ms
  });

  it('snaps straight to target when durationMs is 0 (reduced motion, or a reconnect gap)', () => {
    expect(headValue(0, 100, 1000, 1000, 0)).toBe(100);
  });

  it('falls back to targetValue for a non-finite prev or target rather than propagating NaN', () => {
    expect(headValue(NaN, 50, 1000, 1000)).toBe(50);
    expect(headValue(10, NaN, 1000, 1000)).toBe(NaN); // targetValue itself is NaN -- nothing sensible to fall back to
    expect(headValue(Infinity, 50, 1000, 1000)).toBe(50);
  });
});

describe('advanceHeadState', () => {
  it('starts an unanimated glide (prevValue === targetValue) for the first real value', () => {
    const s = advanceHeadState(null, 42, 1000);
    expect(s).toEqual({ prevValue: 42, targetValue: 42, arrivalMs: 1000, durationMs: 1500 });
  });

  it('keeps the existing state when the raw value has not actually changed', () => {
    const prev: HeadState = { prevValue: 10, targetValue: 50, arrivalMs: 1000, durationMs: 1500 };
    expect(advanceHeadState(prev, 50, 1200)).toBe(prev); // same reference, not just equal
  });

  // Regression: re-arrival mid-glide must continue from the value CURRENTLY
  // ON SCREEN, not restart from the old target -- getting this wrong is
  // exactly the one-frame jump smooth-streaming exists to remove. Trace:
  // a glide from 10 toward 50 starts at t=0 (arrivalMs=0, durationMs=1500);
  // a new frame reports 80 at nowMs=300, 1/5 of the way through that leg.
  // At that instant the display reads headValue(10,50,0,300,1500) --
  // linear(0.2) = 0.2, so 10 + 0.2*40 = 18 -- and that, not the old target
  // 50, must become the new glide's prevValue.
  it('seeds the new glide from the currently-displayed value, not the old target', () => {
    const prev: HeadState = { prevValue: 10, targetValue: 50, arrivalMs: 0, durationMs: 1500 };
    const next = advanceHeadState(prev, 80, 300);
    expect(next.prevValue).toBeCloseTo(18, 10);
    expect(next.targetValue).toBe(80);
    expect(next.arrivalMs).toBe(300);
  });

  it('renders the seeded value immediately, proving no jump at the instant of re-arrival', () => {
    const prev: HeadState = { prevValue: 10, targetValue: 50, arrivalMs: 0, durationMs: 1500 };
    const next = advanceHeadState(prev, 80, 300);
    // At the exact moment `next` takes effect (nowMs === next.arrivalMs),
    // the displayed value must equal what was ALREADY on screen the
    // instant before -- headValue(...) at t=0 returns prevValue verbatim,
    // so this only holds if seeding used the live 18, not the buggy 50.
    expect(headValue(next.prevValue, next.targetValue, next.arrivalMs, 300, next.durationMs)).toBeCloseTo(18, 10);
    expect(headValue(next.prevValue, next.targetValue, next.arrivalMs, 300, next.durationMs)).not.toBe(50);
  });

  it("seeds off the OLD leg's own duration even when a new (different) duration is supplied for the new leg", () => {
    // The old leg was a fast 400ms scrub-style glide, already 3/4 settled
    // by nowMs=300 (t=0.75 of 400ms) -- linear(0.75) = 0.75, so
    // 10 + 0.75*40 = 40. A newly-measured 2500ms cadence for the NEW leg
    // must not retroactively change how far along the OLD one had gotten.
    const prev: HeadState = { prevValue: 10, targetValue: 50, arrivalMs: 0, durationMs: 400 };
    const next = advanceHeadState(prev, 80, 300, 2500);
    expect(next.prevValue).toBeCloseTo(40, 10);
    expect(next.durationMs).toBe(2500); // the NEW leg carries the newly-measured duration
  });

  it('honors a caller-supplied durationMs of 0 for the OLD leg when computing the seed (reduced motion already settled)', () => {
    // durationMs=0 means the PRIOR leg had already snapped straight to
    // its target, so re-arriving mid-"glide" (there is none) must seed
    // from that settled target.
    const prev: HeadState = { prevValue: 10, targetValue: 50, arrivalMs: 0, durationMs: 0 };
    const next = advanceHeadState(prev, 80, 300, 0);
    expect(next.prevValue).toBe(50);
  });
});

describe('updateCadenceEma', () => {
  it('seeds straight from the first clamped delta when there is no prior estimate', () => {
    expect(updateCadenceEma(null, 2000)).toBe(2000);
  });

  it('clamps a seed delta below MIN_CADENCE_MS up to the floor', () => {
    expect(updateCadenceEma(null, 200)).toBe(MIN_CADENCE_MS);
  });

  it('clamps a seed delta above MAX_CADENCE_MS down to the ceiling', () => {
    expect(updateCadenceEma(null, 30_000)).toBe(MAX_CADENCE_MS);
  });

  it('blends a subsequent delta by alpha toward the new (clamped) value', () => {
    // prevEma=2000, delta=3000, alpha=0.3 -> 2000 + 0.3*(3000-2000) = 2300
    expect(updateCadenceEma(2000, 3000, 0.3)).toBeCloseTo(2300, 10);
  });

  it('a single wild outlier nudges the estimate by at most alpha of the clamp range, not the raw delta', () => {
    // delta=60s clamps to MAX_CADENCE_MS (4000) before blending: 2000 +
    // 0.3*(4000-2000) = 2600 -- nowhere near what blending the raw 60000
    // unclamped would produce.
    expect(updateCadenceEma(2000, 60_000, 0.3)).toBeCloseTo(2600, 10);
  });

  it('stays within [MIN_CADENCE_MS, MAX_CADENCE_MS] no matter how many extreme deltas accumulate', () => {
    let ema: number | null = 2000;
    for (let i = 0; i < 50; i++) ema = updateCadenceEma(ema, 999_999);
    expect(ema).toBeLessThanOrEqual(MAX_CADENCE_MS);
    ema = 2000;
    for (let i = 0; i < 50; i++) ema = updateCadenceEma(ema, 1);
    expect(ema).toBeGreaterThanOrEqual(MIN_CADENCE_MS);
  });
});

describe('isReconnectGap', () => {
  it('is false for an ordinary, even slightly jittery cadence', () => {
    expect(isReconnectGap(1900)).toBe(false);
    expect(isReconnectGap(MAX_CADENCE_MS)).toBe(false); // inclusive at the boundary
  });

  it('is true once the delta exceeds the clamp ceiling -- a stalled tab or an SSE reconnect', () => {
    expect(isReconnectGap(MAX_CADENCE_MS + 1)).toBe(true);
    expect(isReconnectGap(60_000)).toBe(true);
  });

  it('honors a caller-supplied threshold instead of the default MAX_CADENCE_MS', () => {
    expect(isReconnectGap(5000, 10_000)).toBe(false);
    expect(isReconnectGap(15_000, 10_000)).toBe(true);
  });
});

describe('shouldBroadcast', () => {
  it('always broadcasts the first time (lastMs is null)', () => {
    expect(shouldBroadcast(null, 0)).toBe(true);
    expect(shouldBroadcast(null, 123456)).toBe(true);
  });

  it('holds back until the 30fps frame interval has elapsed', () => {
    expect(shouldBroadcast(1000, 1000 + 33)).toBe(false); // 1000/30 = 33.33...ms
    expect(shouldBroadcast(1000, 1000 + 34)).toBe(true);
  });

  it('is inclusive at the exact interval boundary for a clean-dividing fps', () => {
    expect(shouldBroadcast(0, 99, 10)).toBe(false); // 1000/10 = 100ms exactly
    expect(shouldBroadcast(0, 100, 10)).toBe(true);
  });

  it('still broadcasts after an arbitrarily long gap (e.g. a backgrounded tab resuming)', () => {
    expect(shouldBroadcast(1000, 60_000)).toBe(true);
  });
});

describe('liveWindowRange', () => {
  it('spans exactly windowSec seconds, ending at nowMs converted to seconds', () => {
    const [min, max] = liveWindowRange(1_000_000_000, 900);
    expect(max).toBe(1_000_000);
    expect(min).toBe(1_000_000 - 900);
    expect(max - min).toBe(900);
  });

  it('defaults to the 15-minute LIVE_WINDOW_SEC span', () => {
    const [min, max] = liveWindowRange(900_000);
    expect(max - min).toBe(900);
  });
});

describe('gate state machine (gateReducer / isDriverActive)', () => {
  it('starts inactive: no subscribers, motion not reduced', () => {
    expect(isDriverActive(initialGateState)).toBe(false);
  });

  it('becomes active on the first subscribe', () => {
    const s = gateReducer(initialGateState, { type: 'subscribe' });
    expect(s.subscribers).toBe(1);
    expect(isDriverActive(s)).toBe(true);
  });

  it('tracks concurrent subscribers and only goes inactive once the last one leaves', () => {
    let s: GateState = initialGateState;
    s = gateReducer(s, { type: 'subscribe' });
    s = gateReducer(s, { type: 'subscribe' });
    s = gateReducer(s, { type: 'subscribe' });
    expect(s.subscribers).toBe(3);
    s = gateReducer(s, { type: 'unsubscribe' });
    expect(s.subscribers).toBe(2);
    expect(isDriverActive(s)).toBe(true);
    s = gateReducer(s, { type: 'unsubscribe' });
    s = gateReducer(s, { type: 'unsubscribe' });
    expect(s.subscribers).toBe(0);
    expect(isDriverActive(s)).toBe(false);
  });

  it('floors at 0 rather than going negative on an unbalanced unsubscribe', () => {
    const s = gateReducer(initialGateState, { type: 'unsubscribe' });
    expect(s.subscribers).toBe(0);
    expect(isDriverActive(s)).toBe(false);
  });

  it('reduced motion forces inactive even with subscribers present', () => {
    let s: GateState = initialGateState;
    s = gateReducer(s, { type: 'subscribe' });
    s = gateReducer(s, { type: 'reduced-motion', value: true });
    expect(s.subscribers).toBe(1);
    expect(isDriverActive(s)).toBe(false);
  });

  it('resumes once reduced motion clears again, with no need to re-subscribe', () => {
    let s: GateState = initialGateState;
    s = gateReducer(s, { type: 'subscribe' });
    s = gateReducer(s, { type: 'reduced-motion', value: true });
    s = gateReducer(s, { type: 'reduced-motion', value: false });
    expect(isDriverActive(s)).toBe(true);
  });

  it('stays inactive when reduced motion is on and there are no subscribers either', () => {
    const s = gateReducer(initialGateState, { type: 'reduced-motion', value: true });
    expect(isDriverActive(s)).toBe(false);
  });
});

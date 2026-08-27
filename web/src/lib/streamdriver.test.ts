import { describe, expect, it } from 'vitest';
import {
  easeOutCubic,
  gateReducer,
  headValue,
  initialGateState,
  isDriverActive,
  lerp,
  liveWindowRange,
  shouldBroadcast,
  type GateState,
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

describe('easeOutCubic', () => {
  it('starts at 0 and ends at 1', () => {
    expect(easeOutCubic(0)).toBe(0);
    expect(easeOutCubic(1)).toBe(1);
  });

  it('clamps t below 0 to the start and above 1 to the end', () => {
    expect(easeOutCubic(-1)).toBe(0);
    expect(easeOutCubic(2)).toBe(1);
  });

  it('front-loads progress (fast start, slow finish): more than half done by the midpoint', () => {
    expect(easeOutCubic(0.5)).toBeCloseTo(0.875, 10);
  });
});

describe('headValue', () => {
  it('reads as prevValue the instant the sample arrives (t=0)', () => {
    expect(headValue(10, 50, 1000, 1000)).toBe(10);
  });

  it('reads as targetValue once the ease duration has fully elapsed', () => {
    expect(headValue(10, 50, 1000, 1000 + 1500)).toBe(50);
    expect(headValue(10, 50, 1000, 1000 + 5000)).toBe(50); // well past duration, still settled
  });

  it('follows the easeOutCubic curve mid-flight, not a linear one', () => {
    // t = 0.5 of the default 1500ms duration -> easeOutCubic(0.5) = 0.875
    const v = headValue(0, 100, 1000, 1000 + 750);
    expect(v).toBeCloseTo(87.5, 10);
  });

  it('honors a caller-supplied duration instead of the 1500ms default', () => {
    expect(headValue(0, 100, 1000, 1200, 400)).toBe(headValue(0, 100, 0, 200, 400));
    expect(headValue(0, 100, 1000, 1400, 400)).toBe(100); // fully elapsed at 400ms
  });

  it('snaps straight to target when durationMs is 0 (reduced motion)', () => {
    expect(headValue(0, 100, 1000, 1000, 0)).toBe(100);
  });

  it('falls back to targetValue for a non-finite prev or target rather than propagating NaN', () => {
    expect(headValue(NaN, 50, 1000, 1000)).toBe(50);
    expect(headValue(10, NaN, 1000, 1000)).toBe(NaN); // targetValue itself is NaN -- nothing sensible to fall back to
    expect(headValue(Infinity, 50, 1000, 1000)).toBe(50);
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

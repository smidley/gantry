import { describe, expect, it } from 'vitest';
import { MIN_X_SPAN_SEC, xRange } from './chartRange';

describe('xRange', () => {
  it('passes a genuinely empty domain through unchanged', () => {
    expect(xRange(null, null)).toEqual([null, null]);
  });

  it('leaves a span at or above the floor untouched', () => {
    expect(xRange(0, 10)).toEqual([0, 10]);
    expect(xRange(1000, 4000)).toEqual([1000, 4000]);
  });

  it('symmetrically pads a too-narrow span up to the floor', () => {
    const [min, max] = xRange(100, 102); // span=2, floor=10 -> pad 4 each side
    expect(min).toBeCloseTo(96, 5);
    expect(max).toBeCloseTo(106, 5);
    expect((max as number) - (min as number)).toBeCloseTo(MIN_X_SPAN_SEC, 5);
  });

  it('pads a single-point (zero-width) domain into a centered floor-width window', () => {
    const [min, max] = xRange(100, 100);
    expect(min).toBeCloseTo(95, 5);
    expect(max).toBeCloseTo(105, 5);
  });

  it('respects a custom minSpanSec floor', () => {
    expect(xRange(0, 2, undefined, 4)).toEqual([-1, 3]);
  });

  it('an explicit xDomain wins outright, ignoring the data-derived initMin/initMax entirely', () => {
    // The exact bug this exists to fix: a 7-day REQUESTED window whose
    // real data only spans ~2 minutes must still show the full 7 days,
    // not the narrow real extent uPlot would otherwise auto-range to.
    const sevenDays = 7 * 86400;
    const to = 2_000_000;
    const from = to - sevenDays;
    expect(xRange(to - 120, to, [from, to])).toEqual([from, to]);
  });

  it('an explicit xDomain wins even over a genuinely empty data domain (no points fell in the window at all)', () => {
    const from = 1_000_000;
    const to = 1_000_000 + 3600;
    expect(xRange(null, null, [from, to])).toEqual([from, to]);
  });

  it('an explicit xDomain is honored exactly even when it is itself narrower than minSpanSec -- a deliberate caller choice, not an auto-guess to smooth', () => {
    expect(xRange(0, 0, [5, 7])).toEqual([5, 7]);
  });
});

import { describe, expect, it } from 'vitest';
import { nearestPointAt, tsAtFraction } from './scrub';

describe('tsAtFraction', () => {
  it('returns min at fraction 0 and max at fraction 1', () => {
    expect(tsAtFraction(0, 100, 200)).toBe(100);
    expect(tsAtFraction(1, 100, 200)).toBe(200);
  });

  it('interpolates linearly at a midpoint', () => {
    expect(tsAtFraction(0.5, 100, 200)).toBe(150);
    expect(tsAtFraction(0.25, 0, 1000)).toBe(250);
  });

  it('clamps a fraction outside [0,1] rather than extrapolating', () => {
    expect(tsAtFraction(-0.2, 100, 200)).toBe(100);
    expect(tsAtFraction(1.2, 100, 200)).toBe(200);
  });
});

describe('nearestPointAt', () => {
  it('returns null for an empty ring', () => {
    expect(nearestPointAt([], 100)).toBeNull();
  });

  it('returns the only point regardless of ts, for a single-point ring', () => {
    const points: [number, number][] = [[100, 5]];
    expect(nearestPointAt(points, 0)).toEqual({ ts: 100, value: 5, index: 0 });
    expect(nearestPointAt(points, 100)).toEqual({ ts: 100, value: 5, index: 0 });
    expect(nearestPointAt(points, 9999)).toEqual({ ts: 100, value: 5, index: 0 });
  });

  it('hits a point exactly when ts matches it precisely', () => {
    const points: [number, number][] = [
      [10, 1],
      [20, 2],
      [30, 3],
    ];
    expect(nearestPointAt(points, 20)).toEqual({ ts: 20, value: 2, index: 1 });
  });

  it('picks the nearer of two straddling points', () => {
    const points: [number, number][] = [
      [10, 1],
      [20, 2],
    ];
    expect(nearestPointAt(points, 13)).toEqual({ ts: 10, value: 1, index: 0 });
    expect(nearestPointAt(points, 17)).toEqual({ ts: 20, value: 2, index: 1 });
  });

  it('breaks an exact tie toward the earlier point', () => {
    const points: [number, number][] = [
      [10, 1],
      [20, 2],
    ];
    expect(nearestPointAt(points, 15)).toEqual({ ts: 10, value: 1, index: 0 });
  });

  it('clamps to the first point when ts falls before the ring entirely', () => {
    const points: [number, number][] = [
      [100, 1],
      [110, 2],
    ];
    expect(nearestPointAt(points, 0)).toEqual({ ts: 100, value: 1, index: 0 });
  });

  it('clamps to the last point when ts falls after the ring entirely', () => {
    const points: [number, number][] = [
      [100, 1],
      [110, 2],
    ];
    expect(nearestPointAt(points, 9999)).toEqual({ ts: 110, value: 2, index: 1 });
  });

  it('scrubs an empty region between real points to the nearest real sample', () => {
    // Simulates a ring that only has a couple of recent ticks so far --
    // a target ts far from either (well inside the live window, but
    // nowhere near either real sample) still resolves to whichever real
    // point is closer, never null.
    const points: [number, number][] = [
      [890, 40],
      [900, 42],
    ];
    expect(nearestPointAt(points, 100)).toEqual({ ts: 890, value: 40, index: 0 });
  });

  it('returns null for a non-finite ts', () => {
    const points: [number, number][] = [[100, 1]];
    expect(nearestPointAt(points, NaN)).toBeNull();
    expect(nearestPointAt(points, Infinity)).toBeNull();
  });

  it('finds the nearest point over a larger ring without a linear scan bias', () => {
    const points: [number, number][] = Array.from({ length: 50 }, (_, i) => [i * 10, i]);
    expect(nearestPointAt(points, 234)).toEqual({ ts: 230, value: 23, index: 23 });
    expect(nearestPointAt(points, 236)).toEqual({ ts: 240, value: 24, index: 24 });
  });
});

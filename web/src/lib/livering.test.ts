import { describe, expect, it } from 'vitest';
import { pushRing } from './livering';

describe('pushRing', () => {
  it('appends a point to an empty ring', () => {
    expect(pushRing([], 100, 5)).toEqual([[100, 5]]);
  });

  it('appends in order across multiple pushes', () => {
    let points = pushRing([], 100, 1);
    points = pushRing(points, 110, 2);
    points = pushRing(points, 120, 3);
    expect(points).toEqual([
      [100, 1],
      [110, 2],
      [120, 3],
    ]);
  });

  it('drops points older than windowSec relative to the newest ts', () => {
    let points = pushRing([], 0, 1, 100);
    points = pushRing(points, 50, 2, 100);
    points = pushRing(points, 150, 3, 100); // cutoff is now 50 -- ts=0 must fall off
    expect(points).toEqual([
      [50, 2],
      [150, 3],
    ]);
  });

  it('always keeps at least the newest point even with windowSec 0', () => {
    let points = pushRing([], 0, 1, 0);
    points = pushRing(points, 10, 2, 0);
    expect(points).toEqual([[10, 2]]);
  });

  it('replaces a same-instant point rather than appending a duplicate', () => {
    let points = pushRing([], 100, 1);
    points = pushRing(points, 100, 2);
    expect(points).toEqual([[100, 2]]);
  });

  it('does not mutate the array it was given', () => {
    const original = pushRing([], 100, 1);
    const next = pushRing(original, 110, 2);
    expect(original).toEqual([[100, 1]]);
    expect(next).toEqual([
      [100, 1],
      [110, 2],
    ]);
  });

  it('drops non-finite ts or value, returning the input unchanged', () => {
    const points = pushRing([[100, 1]], 110, 2);
    expect(pushRing(points, NaN, 3)).toBe(points);
    expect(pushRing(points, 120, NaN)).toBe(points);
    expect(pushRing(points, Infinity, 3)).toBe(points);
  });

  it('caps ring length at the hard cap regardless of window', () => {
    let points: [number, number][] = [];
    for (let i = 0; i < 1500; i++) {
      points = pushRing(points, i, i, 1_000_000); // huge window so only the hard cap can prune
    }
    expect(points.length).toBeLessThanOrEqual(1200);
    expect(points[points.length - 1]).toEqual([1499, 1499]);
  });
});

import { describe, expect, it } from 'vitest';
import { buildAlignedData } from './seriesAlign';

describe('buildAlignedData', () => {
  it('unions perfectly-aligned series onto one shared x-axis with no nulls', () => {
    const a = { points: [[0, 1], [2, 2], [4, 3]] as const };
    const b = { points: [[0, 10], [2, 20], [4, 30]] as const };
    expect(buildAlignedData([a, b])).toEqual([
      [0, 2, 4],
      [1, 2, 3],
      [10, 20, 30],
    ]);
  });

  it('bridges an isolated single-tick miss via linear interpolation between its real neighbors', () => {
    const a = { points: [[0, 1], [2, 2], [4, 3], [6, 4], [8, 5]] as const };
    const b = { points: [[0, 10], [2, 20], [6, 40], [8, 50]] as const }; // missing ts=4
    const [xs, , by] = buildAlignedData([a, b]);
    expect(xs).toEqual([0, 2, 4, 6, 8]);
    // ts=4 sits exactly halfway between b's real neighbors (20 at ts=2,
    // 40 at ts=6) -- the bridged value must be their midpoint, not left
    // null and not snapped to either side.
    expect(by).toEqual([10, 20, 30, 40, 50]);
  });

  it('never bridges the leading edge of a series that has not started yet -- a real "not tracked before this" gap', () => {
    const a = { points: [[0, 1], [2, 2], [4, 3], [6, 4]] as const };
    const b = { points: [[4, 30], [6, 40]] as const }; // starts partway through
    const [, , by] = buildAlignedData([a, b]);
    expect(by).toEqual([null, null, 30, 40]);
  });

  it('never bridges the trailing edge of a series whose newest sample has not arrived yet', () => {
    const a = { points: [[0, 1], [2, 2], [4, 3], [6, 4]] as const };
    const b = { points: [[0, 10], [2, 20]] as const }; // ends partway through
    const [, , by] = buildAlignedData([a, b]);
    expect(by).toEqual([10, 20, null, null]);
  });

  it('does not bridge a genuinely wide gap -- a real absence must stay visible, not get papered over', () => {
    // Ten evenly-spaced ticks (typical step = 2) establish a dense
    // cadence; b then goes silent for a long stretch relative to that
    // cadence before returning -- must NOT be interpolated across, or a
    // container that was truly stopped/out of the top-10 for minutes
    // would read as a smooth, fabricated trend line through its absence.
    const a = {
      points: Array.from({ length: 10 }, (_, i) => [i * 2, i] as const),
    };
    const b = { points: [[0, 100], [18, 200]] as const }; // present at the ends only, silent 2..16
    const [, , by] = buildAlignedData([a, b]);
    expect(by[0]).toBe(100);
    expect(by[by.length - 1]).toBe(200);
    // Every point strictly between the two real b samples stayed null --
    // none of them got fabricated.
    expect(by.slice(1, -1).every((v) => v === null)).toBe(true);
  });

  it('handles two single-point series without fabricating a value at either edge', () => {
    // Only two distinct timestamps total -- both series' own single
    // sample sits at an edge relative to the other, so neither null is
    // interior (there's nothing to bridge yet either way).
    const a = { points: [[0, 1]] as const };
    const b = { points: [[5, 2]] as const };
    const [xs, ay, by] = buildAlignedData([a, b]);
    expect(xs).toEqual([0, 5]);
    expect(ay).toEqual([1, null]);
    expect(by).toEqual([null, 2]);
  });

  it('never mutates a real (non-bridged) value', () => {
    const a = { points: [[0, 1], [2, 2], [4, 3]] as const };
    const b = { points: [[0, 10], [2, 20], [4, 30]] as const };
    const [, ay, by] = buildAlignedData([a, b]);
    expect(ay).toEqual([1, 2, 3]);
    expect(by).toEqual([10, 20, 30]);
  });

  it('handles an empty series list without throwing', () => {
    expect(buildAlignedData([])).toEqual([[]]);
  });
});

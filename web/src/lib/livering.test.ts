import { describe, expect, it } from 'vitest';
import { pushRing, appendAfterSeed, mergeSeed, seriesPointsToRing } from './livering';

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

describe('mergeSeed + appendAfterSeed', () => {
  it('seeds an empty ring, sorting out-of-order input and dropping non-finite entries', () => {
    const seeded = mergeSeed(
      [],
      [
        [110, 2],
        [100, 1],
        [NaN, 9],
        [120, Infinity],
      ],
      900,
    );
    expect(seeded).toEqual([
      [100, 1],
      [110, 2],
    ]);
  });

  it('seed then append newer: merges the live append onto the seeded history', () => {
    let ring = mergeSeed(
      [],
      [
        [100, 1],
        [110, 2],
      ],
      900,
    );
    ring = appendAfterSeed(ring, 120, 3, 900);
    expect(ring).toEqual([
      [100, 1],
      [110, 2],
      [120, 3],
    ]);
  });

  it('append duplicate-ts: ignored outright, not replaced', () => {
    const ring = mergeSeed(
      [],
      [
        [100, 1],
        [110, 2],
      ],
      900,
    );
    // Unlike pushRing's own same-instant rule (replace), a seeded ring's
    // appendAfterSeed must leave the ring byte-for-byte unchanged -- same
    // reference back -- for a duplicate of its own newest point.
    expect(appendAfterSeed(ring, 110, 99, 900)).toBe(ring);
  });

  it('append older: ignored, never inserted out of order', () => {
    const ring = mergeSeed(
      [],
      [
        [100, 1],
        [110, 2],
      ],
      900,
    );
    expect(appendAfterSeed(ring, 90, 99, 900)).toBe(ring);
  });

  it('empty seed behaves as today: an already-pushed ring is returned unchanged, by reference', () => {
    let existing = pushRing([], 100, 1);
    existing = pushRing(existing, 110, 2);
    expect(mergeSeed(existing, [], 900)).toBe(existing);
  });

  it('folds a live point that arrived before the seed resolved in on top, deduped', () => {
    // Simulates the seed->stream race: one live frame already pushed
    // (ts=120) before the seed fetch (history ending at ts=110) lands.
    const held = pushRing([], 120, 5);
    const merged = mergeSeed(held, [
      [100, 1],
      [110, 2],
    ]);
    expect(merged).toEqual([
      [100, 1],
      [110, 2],
      [120, 5],
    ]);
  });

  it('trims seeded history to the window, relative to its own newest point', () => {
    const seed: [number, number][] = [
      [0, 1],
      [50, 2],
      [150, 3],
    ];
    expect(mergeSeed([], seed, 100)).toEqual([
      [50, 2],
      [150, 3],
    ]);
  });
});

describe('seriesPointsToRing', () => {
  it('keeps ts+avg, dropping max', () => {
    expect(
      seriesPointsToRing([
        [100, 1.5, 3],
        [110, 2.5, 4],
      ]),
    ).toEqual([
      [100, 1.5],
      [110, 2.5],
    ]);
  });

  it('maps an empty series to an empty ring', () => {
    expect(seriesPointsToRing([])).toEqual([]);
  });
});

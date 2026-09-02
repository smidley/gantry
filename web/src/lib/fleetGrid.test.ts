import { describe, expect, it } from 'vitest';
import { fitFleetCells, fleetGridHeight } from './fleetGrid';

// The sizing contract the fleet's auto-resize rests on: the largest
// square that still fits every block, a hard floor that scrolls rather
// than shrinking further, and a ceiling so a three-container fleet
// doesn't render three billboards.
const GAP = 3;
const MIN = 10;
const MAX = 96;

function fit(count: number, width: number, height: number, over: Partial<{ gap: number; min: number; max: number }> = {}) {
  return fitFleetCells({ count, width, height, gap: GAP, min: MIN, max: MAX, ...over });
}

// fits re-derives the geometry the result claims, straight from its own
// numbers -- every case below leans on this rather than restating the
// arithmetic per assertion.
function fits(count: number, width: number, height: number, over: Partial<{ gap: number; min: number; max: number }> = {}) {
  const gap = over.gap ?? GAP;
  const r = fit(count, width, height, over);
  const usedWidth = r.cols * r.cell + (r.cols - 1) * gap;
  const usedHeight = r.rows * r.cell + (r.rows - 1) * gap;
  return { ...r, usedWidth, usedHeight };
}

describe('fitFleetCells', () => {
  it('gives a tiny fleet big blocks and a large fleet small ones, in the same box', () => {
    const three = fit(3, 640, 420);
    const thirty = fit(30, 640, 420);
    const sixty = fit(60, 640, 420);
    expect(three.cell).toBeGreaterThan(thirty.cell);
    expect(thirty.cell).toBeGreaterThan(sixty.cell);
    // The ask's own worked example: 3 containers read as LARGE.
    expect(three.cell).toBe(MAX);
  });

  it('never overflows its box while a bigger-than-floor cell still fits', () => {
    for (const count of [1, 3, 10, 30, 60]) {
      for (const [w, h] of [
        [640, 420],
        [420, 300],
        [320, 180],
        [900, 640],
        [240, 520],
      ]) {
        const r = fits(count, w, h);
        expect(r.usedWidth, `${count} @ ${w}x${h}`).toBeLessThanOrEqual(w);
        expect(r.cols * r.rows, `${count} @ ${w}x${h}`).toBeGreaterThanOrEqual(count);
        if (!r.overflow) expect(r.usedHeight, `${count} @ ${w}x${h}`).toBeLessThanOrEqual(h);
      }
    }
  });

  // Leaves no free size on the table AT THE SAME ROW COST: any bigger
  // cell that still fits must need more rows than the one chosen (which
  // is the only reason the fit would ever have passed it over).
  it('returns the largest cell that fits without costing an extra row', () => {
    for (const count of [1, 3, 10, 30, 60]) {
      for (const [w, h] of [
        [640, 420],
        [420, 300],
        [520, 210],
        [490, 560],
      ]) {
        const r = fit(count, w, h);
        if (r.overflow) continue;
        for (let bigger = r.cell + 1; bigger <= MAX; bigger++) {
          const cols = Math.floor((w + GAP) / (bigger + GAP));
          if (cols < 1) continue;
          const rows = Math.ceil(count / cols);
          const needed = rows * bigger + (rows - 1) * GAP;
          if (needed > h) continue;
          expect(rows, `${count} @ ${w}x${h}: ${bigger}px fits in ${rows} rows, ${r.cell}px was chosen`).toBeGreaterThan(
            r.rows,
          );
        }
      }
    }
  });

  // The row-saving rule, on the case that forced it: four blocks in a
  // 490px-wide field. The plain largest-cell answer is three-and-one
  // over two rows; giving back a couple of px puts all four on one.
  it('gives back a sliver of cell size to save a whole row', () => {
    const r = fitFleetCells({ count: 4, width: 490, height: 560, gap: 6, min: 12, max: 120 });
    expect(r.rows).toBe(1);
    expect(r.cols).toBeGreaterThanOrEqual(4);
    expect(r.cell).toBeGreaterThan(120 * 0.85);
    expect(r.cell).toBeLessThan(120);
    expect(fleetGridHeight(r, 6)).toBe(r.cell);
  });

  // ...but never at the cost of real size on a fleet where the area is
  // what binds: a row saved there would mean tiny blocks in a mostly
  // empty field.
  it('does not trade real size for a row on a fleet the area actually binds', () => {
    const r = fitFleetCells({ count: 40, width: 490, height: 560, gap: 6, min: 12, max: 120 });
    const naive = (() => {
      for (let s = 120; s >= 12; s--) {
        const cols = Math.floor((490 + 6) / (s + 6));
        if (cols < 1) continue;
        const rows = Math.ceil(40 / cols);
        if (rows * s + (rows - 1) * 6 <= 560) return { cell: s, rows };
      }
      return null;
    })()!;
    expect(r.cell).toBeGreaterThanOrEqual(naive.cell * (1 - 0.15));
    expect(r.rows).toBeLessThanOrEqual(naive.rows);
  });

  it('clamps to the max ceiling even in a huge box', () => {
    expect(fit(1, 2000, 2000).cell).toBe(MAX);
    expect(fit(3, 2000, 2000).cell).toBe(MAX);
    expect(fit(1, 2000, 2000, { max: 40 }).cell).toBe(40);
  });

  it('sits on the min floor and reports overflow only when the floor itself does not fit', () => {
    // 60 blocks at the 10px floor in a 100px-wide, 20px-tall box: 7
    // columns, 9 rows, far past 20px of height.
    const tight = fit(60, 100, 20);
    expect(tight.cell).toBe(MIN);
    expect(tight.overflow).toBe(true);

    // The same 60 blocks with room to breathe never reach the floor and
    // never scroll.
    const roomy = fit(60, 640, 420);
    expect(roomy.cell).toBeGreaterThan(MIN);
    expect(roomy.overflow).toBe(false);
  });

  it('honours the gap on both axes', () => {
    // 4 cells of 20px across a 92px box: with no gap 4 fit exactly; with
    // a 4px gap they need 92px too (4*20 + 3*4), and a 5px gap is one
    // column too many.
    expect(fitFleetCells({ count: 4, width: 92, height: 20, gap: 0, min: 20, max: 20 }).cols).toBe(4);
    expect(fitFleetCells({ count: 4, width: 92, height: 20, gap: 4, min: 20, max: 20 }).cols).toBe(4);
    expect(fitFleetCells({ count: 4, width: 92, height: 20, gap: 5, min: 20, max: 20 }).cols).toBe(3);

    // A wider gap costs real cell size in a fixed box.
    expect(fit(30, 420, 300, { gap: 16 }).cell).toBeLessThan(fit(30, 420, 300, { gap: 0 }).cell);
  });

  it('handles an empty fleet and an unmeasured box without throwing', () => {
    expect(fit(0, 640, 420)).toEqual({ cell: MIN, cols: 0, rows: 0, overflow: false });
    // Before the first ResizeObserver reading: one column at the floor,
    // honestly reported as overflowing.
    const unmeasured = fit(12, 0, 0);
    expect(unmeasured.cell).toBe(MIN);
    expect(unmeasured.cols).toBe(1);
    expect(unmeasured.overflow).toBe(true);
    expect(fit(12, NaN, NaN).cell).toBe(MIN);
  });

  it('keeps rows x cols able to hold every block', () => {
    for (let count = 1; count <= 60; count++) {
      const r = fit(count, 500, 360);
      expect(r.cols * r.rows, `count ${count}`).toBeGreaterThanOrEqual(count);
      expect(r.rows, `count ${count}`).toBe(Math.ceil(count / r.cols));
      expect(r.cell).toBeGreaterThanOrEqual(MIN);
      expect(r.cell).toBeLessThanOrEqual(MAX);
    }
  });

  // The trend is what the ask is about: more containers, smaller
  // blocks. It is not strictly monotonic step by step, and deliberately
  // so -- the row-saving rule can hand one count a slightly smaller
  // cell than the next one up, when that count is the one a row was
  // available to save on. What must hold is that a single step can
  // never gain MORE than the slack, and that the trend across the range
  // is firmly downward.
  it('shrinks the cell as the fleet grows, never gaining more than the row slack in one step', () => {
    const cells: number[] = [];
    for (let count = 1; count <= 60; count++) cells.push(fit(count, 560, 340).cell);

    for (let i = 1; i < cells.length; i++) {
      expect(cells[i], `count ${i + 1} after ${cells[i - 1]}px`).toBeLessThanOrEqual(Math.ceil(cells[i - 1] / 0.85));
    }
    expect(cells[2]).toBeGreaterThan(cells[29]); // 3 vs 30
    expect(cells[29]).toBeGreaterThan(cells[59]); // 30 vs 60
    expect(cells[0]).toBeGreaterThan(cells[59]); // 1 vs 60
  });
});

describe('fleetGridHeight', () => {
  it('is zero for an empty fleet -- nothing to hold space open for', () => {
    expect(fleetGridHeight(fit(0, 640, 420), GAP)).toBe(0);
  });

  it('is one row of max cells for a small fleet that hit the ceiling', () => {
    const r = fit(3, 640, 420);
    expect(r.cell).toBe(MAX);
    expect(r.rows).toBe(1);
    expect(fleetGridHeight(r, GAP)).toBe(MAX);
  });

  it('never exceeds the offered height unless the fit overflowed', () => {
    for (const count of [1, 3, 10, 30, 60]) {
      for (const [w, h] of [
        [640, 420],
        [420, 300],
        [1400, 560],
      ]) {
        const r = fit(count, w, h);
        if (!r.overflow) expect(fleetGridHeight(r, GAP), `${count} @ ${w}x${h}`).toBeLessThanOrEqual(h);
      }
    }
  });

  // The reason this is a second pass and not a cap on the input: a small
  // fleet shrinks the FIELD instead of inflating the BLOCKS, while a
  // fleet big enough for the area to bind still gets count-driven sizing.
  it('shrinks the field for a small fleet without pinning a big one to the ceiling', () => {
    // FleetStrip's own clamps against a full-width band on a laptop.
    const clamps = { min: 12, max: 176, gap: 6 };
    const small = fit(3, 1400, 560, clamps);
    const big = fit(40, 1400, 560, clamps);
    expect(small.cell).toBe(clamps.max);
    expect(fleetGridHeight(small, clamps.gap)).toBeLessThan(560);
    expect(big.cell).toBeLessThan(clamps.max);
    expect(big.cell).toBeLessThan(small.cell);
  });
});

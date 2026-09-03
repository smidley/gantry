import { describe, expect, it } from 'vitest';
import { fitFleetCells, fleetGridHeight } from './fleetGrid';

// The sizing contract the fleet's auto-resize rests on: the largest
// square that still fits every block, one shared cell across the
// running and stopped groups, a hard floor that scrolls rather than
// shrinking further, and a ceiling so a three-container fleet doesn't
// render three billboards.
//
// MAX mirrors FleetStrip's own CELL_MAX (Scott: "I liked the smaller
// sized container blocks better. Let's not allow them to get quite so
// big.") -- these assert against the real clamp rather than a convenient
// one, so a change to it has to be made deliberately here too.
const GAP = 6;
const MIN = 12;
const MAX = 64;
const GROUP_GAP = 22;

type Over = Partial<{ gap: number; min: number; max: number; groupGap: number }>;

function fit(counts: number[] | number, width: number, height: number, over: Over = {}) {
  return fitFleetCells({
    counts: Array.isArray(counts) ? counts : [counts],
    width,
    height,
    gap: GAP,
    min: MIN,
    max: MAX,
    ...over,
  });
}

// used re-derives the geometry the result claims, straight from its own
// numbers -- every case below leans on this rather than restating the
// arithmetic per assertion.
function used(r: ReturnType<typeof fit>, over: Over = {}) {
  const gap = over.gap ?? GAP;
  const groupGap = over.groupGap ?? 0;
  const rendered = r.rows.filter((n) => n > 0);
  const height =
    rendered.reduce((h, n) => h + n * r.cell + (n - 1) * gap, 0) + Math.max(0, rendered.length - 1) * groupGap;
  return { width: r.cols * r.cell + (r.cols - 1) * gap, height };
}

const totalRows = (r: ReturnType<typeof fit>) => r.rows.reduce((a, b) => a + b, 0);
const totalCount = (counts: number[]) => counts.reduce((a, b) => a + b, 0);

describe('fitFleetCells', () => {
  // A box small enough for the AREA to bind, which is where count-driven
  // sizing actually lives: in a roomy field at this ceiling, everything
  // from one block to a few dozen simply sits on the ceiling, and the
  // count stops mattering until the area runs out.
  it('gives a tiny fleet big blocks and a large fleet small ones, in the same box', () => {
    const three = fit(3, 400, 300);
    const thirty = fit(30, 400, 300);
    const sixty = fit(60, 400, 300);
    // The ask's own worked example: 3 containers read as LARGE -- which
    // at this ceiling means the ceiling.
    expect(three.cell).toBe(MAX);
    expect(three.cell).toBeGreaterThan(thirty.cell);
    expect(thirty.cell).toBeGreaterThan(sixty.cell);
  });

  it('never overflows its box while a bigger-than-floor cell still fits', () => {
    for (const counts of [[1], [3], [10], [30], [60], [30, 8], [3, 40]]) {
      for (const [w, h] of [
        [640, 420],
        [420, 300],
        [320, 180],
        [900, 640],
        [240, 520],
      ]) {
        const r = fit(counts, w, h, { groupGap: GROUP_GAP });
        const u = used(r, { groupGap: GROUP_GAP });
        const label = `${counts} @ ${w}x${h}`;
        expect(u.width, label).toBeLessThanOrEqual(w);
        expect(r.cols * totalRows(r), label).toBeGreaterThanOrEqual(totalCount(counts));
        if (!r.overflow) expect(u.height, label).toBeLessThanOrEqual(h);
      }
    }
  });

  // Leaves no free size on the table AT THE SAME ROW COST: any bigger
  // cell that still fits must need more rows than the one chosen (which
  // is the only reason the fit would ever have passed it over).
  it('returns the largest cell that fits without costing an extra row', () => {
    for (const counts of [[1], [3], [10], [30], [60], [30, 8]]) {
      for (const [w, h] of [
        [640, 420],
        [420, 300],
        [520, 210],
        [490, 560],
      ]) {
        const r = fit(counts, w, h, { groupGap: GROUP_GAP });
        if (r.overflow) continue;
        for (let bigger = r.cell + 1; bigger <= MAX; bigger++) {
          const cols = Math.floor((w + GAP) / (bigger + GAP));
          if (cols < 1) continue;
          const rows = counts.map((n) => (n > 0 ? Math.ceil(n / cols) : 0));
          const rendered = rows.filter((n) => n > 0);
          const needed =
            rendered.reduce((acc, n) => acc + n * bigger + (n - 1) * GAP, 0) +
            Math.max(0, rendered.length - 1) * GROUP_GAP;
          if (needed > h) continue;
          expect(
            rows.reduce((a, b) => a + b, 0),
            `${counts} @ ${w}x${h}: ${bigger}px fits, ${r.cell}px was chosen`,
          ).toBeGreaterThan(totalRows(r));
        }
      }
    }
  });

  it('clamps to the max ceiling even in a huge box', () => {
    expect(fit(1, 2000, 2000).cell).toBe(MAX);
    expect(fit(3, 2000, 2000).cell).toBe(MAX);
    expect(fit(1, 2000, 2000, { max: 40 }).cell).toBe(40);
  });

  it('sits on the min floor and reports overflow only when the floor itself does not fit', () => {
    const tight = fit(60, 100, 20);
    expect(tight.cell).toBe(MIN);
    expect(tight.overflow).toBe(true);

    const roomy = fit(60, 640, 420);
    expect(roomy.cell).toBeGreaterThan(MIN);
    expect(roomy.overflow).toBe(false);
  });

  it('honours the gap on both axes', () => {
    expect(fitFleetCells({ counts: [4], width: 92, height: 20, gap: 0, min: 20, max: 20 }).cols).toBe(4);
    expect(fitFleetCells({ counts: [4], width: 92, height: 20, gap: 4, min: 20, max: 20 }).cols).toBe(4);
    expect(fitFleetCells({ counts: [4], width: 92, height: 20, gap: 5, min: 20, max: 20 }).cols).toBe(3);
    expect(fit(30, 420, 300, { gap: 16 }).cell).toBeLessThan(fit(30, 420, 300, { gap: 0 }).cell);
  });

  it('handles an empty fleet and an unmeasured box without throwing', () => {
    expect(fit(0, 640, 420)).toEqual({ cell: MIN, cols: 0, rows: [0], overflow: false });
    expect(fit([0, 0], 640, 420).rows).toEqual([0, 0]);
    const unmeasured = fit(12, 0, 0);
    expect(unmeasured.cell).toBe(MIN);
    expect(unmeasured.cols).toBe(1);
    expect(unmeasured.overflow).toBe(true);
    expect(fit(12, NaN, NaN).cell).toBe(MIN);
  });

  it('keeps rows x cols able to hold every block', () => {
    for (let count = 1; count <= 60; count++) {
      const r = fit(count, 500, 360);
      expect(r.cols * totalRows(r), `count ${count}`).toBeGreaterThanOrEqual(count);
      expect(r.rows[0], `count ${count}`).toBe(Math.ceil(count / r.cols));
      expect(r.cell).toBeGreaterThanOrEqual(MIN);
      expect(r.cell).toBeLessThanOrEqual(MAX);
    }
  });

  // The trend is what the ask is about: more containers, smaller
  // blocks. It is not strictly monotonic step by step, and deliberately
  // so -- the row-saving rule can hand one count a slightly smaller cell
  // than the next one up, and below a dozen or so blocks everything sits
  // on the ceiling. What must hold is that a single step never gains
  // MORE than the slack, and that the trend across the range is firmly
  // downward.
  it('shrinks the cell as the fleet grows, never gaining more than the row slack in one step', () => {
    const cells: number[] = [];
    for (let count = 1; count <= 60; count++) cells.push(fit(count, 560, 340).cell);

    for (let i = 1; i < cells.length; i++) {
      expect(cells[i], `count ${i + 1} after ${cells[i - 1]}px`).toBeLessThanOrEqual(Math.ceil(cells[i - 1] / 0.85));
    }
    expect(cells[2]).toBeGreaterThanOrEqual(cells[29]); // 3 vs 30
    expect(cells[29]).toBeGreaterThan(cells[59]); // 30 vs 60
    expect(cells[0]).toBeGreaterThan(cells[59]); // 1 vs 60
  });

  // The row-saving rule, on the case that forced it, restated at the
  // current ceiling: four blocks in a field just too narrow for four
  // full-size columns. The plain largest-cell answer is three-and-one
  // over two rows; giving back a couple of px puts all four on one.
  it('gives back a sliver of cell size to save a whole row', () => {
    const r = fitFleetCells({ counts: [4], width: 265, height: 560, gap: GAP, min: MIN, max: MAX });
    expect(r.rows).toEqual([1]);
    expect(r.cols).toBeGreaterThanOrEqual(4);
    expect(r.cell).toBeGreaterThan(MAX * 0.85);
    expect(r.cell).toBeLessThan(MAX);
    expect(fleetGridHeight(r, GAP)).toBe(r.cell);
  });

  it('does not trade real size for a row on a fleet the area actually binds', () => {
    const r = fitFleetCells({ counts: [40], width: 490, height: 200, gap: GAP, min: MIN, max: MAX });
    const naive = (() => {
      for (let s = MAX; s >= MIN; s--) {
        const cols = Math.floor((490 + GAP) / (s + GAP));
        if (cols < 1) continue;
        const rows = Math.ceil(40 / cols);
        if (rows * s + (rows - 1) * GAP <= 200) return { cell: s, rows };
      }
      return null;
    })()!;
    expect(r.cell).toBeGreaterThanOrEqual(naive.cell * (1 - 0.15));
    expect(totalRows(r)).toBeLessThanOrEqual(naive.rows);
  });
});

// Groups: running and stopped share one pitch and one column count, and
// each takes whole rows of its own.
describe('fitFleetCells with groups', () => {
  it('gives every group the same cell and the same columns', () => {
    const r = fit([30, 8], 640, 420, { groupGap: GROUP_GAP });
    expect(r.rows).toHaveLength(2);
    expect(r.rows[0]).toBe(Math.ceil(30 / r.cols));
    expect(r.rows[1]).toBe(Math.ceil(8 / r.cols));
  });

  it('charges a group gap per boundary, never for a lone or empty group', () => {
    const rowsHeightOf = (r: ReturnType<typeof fit>, n: number) => n * r.cell + (n - 1) * GAP;

    const one = fit([12], 400, 400, { groupGap: GROUP_GAP });
    expect(fleetGridHeight(one, GAP, GROUP_GAP)).toBe(rowsHeightOf(one, one.rows[0]));

    const withEmpty = fit([12, 0], 400, 400, { groupGap: GROUP_GAP });
    expect(withEmpty.rows[1]).toBe(0);
    expect(fleetGridHeight(withEmpty, GAP, GROUP_GAP)).toBe(fleetGridHeight(one, GAP, GROUP_GAP));

    const two = fit([12, 4], 400, 400, { groupGap: GROUP_GAP });
    expect(fleetGridHeight(two, GAP, GROUP_GAP)).toBe(
      rowsHeightOf(two, two.rows[0]) + GROUP_GAP + rowsHeightOf(two, two.rows[1]),
    );
  });

  // Splitting can genuinely cost a row -- a running group that doesn't
  // fill its last row still ends it -- which is the whole reason the
  // group shape is an input to the fit rather than something bolted on
  // after. In a height-bound box that shows up as a smaller cell.
  it('accounts for the row a split costs rather than overflowing', () => {
    const combined = fit([34], 300, 200, { groupGap: GROUP_GAP });
    const split = fit([30, 4], 300, 200, { groupGap: GROUP_GAP });
    expect(split.cell).toBeLessThanOrEqual(combined.cell);
    expect(used(split, { groupGap: GROUP_GAP }).height).toBeLessThanOrEqual(200);
  });

  it('never lets a group overflow the box it was fitted to', () => {
    for (const counts of [
      [40, 7],
      [1, 60],
      [25, 25],
      [60, 1],
    ]) {
      const r = fit(counts, 520, 360, { groupGap: GROUP_GAP });
      if (r.overflow) continue;
      expect(used(r, { groupGap: GROUP_GAP }).height, `${counts}`).toBeLessThanOrEqual(360);
    }
  });
});

describe('fleetGridHeight', () => {
  it('is zero for an empty fleet -- nothing to hold space open for', () => {
    expect(fleetGridHeight(fit(0, 640, 420), GAP)).toBe(0);
  });

  it('is one row of max cells for a small fleet that hit the ceiling', () => {
    const r = fit(3, 640, 420);
    expect(r.cell).toBe(MAX);
    expect(r.rows).toEqual([1]);
    expect(fleetGridHeight(r, GAP)).toBe(MAX);
  });

  it('never exceeds the offered height unless the fit overflowed', () => {
    for (const counts of [[1], [3], [10], [30], [60], [30, 9]]) {
      for (const [w, h] of [
        [640, 420],
        [420, 300],
        [1400, 560],
      ]) {
        const r = fit(counts, w, h, { groupGap: GROUP_GAP });
        if (!r.overflow) expect(fleetGridHeight(r, GAP, GROUP_GAP), `${counts} @ ${w}x${h}`).toBeLessThanOrEqual(h);
      }
    }
  });

  // The reason this is a second pass and not a cap on the input: a small
  // fleet shrinks the FIELD instead of inflating the BLOCKS, while a
  // fleet big enough for the area to bind still gets count-driven sizing.
  it('shrinks the field for a small fleet without pinning a big one to the ceiling', () => {
    const small = fit(3, 1400, 560);
    const big = fit(200, 1400, 560);
    expect(small.cell).toBe(MAX);
    expect(fleetGridHeight(small, GAP)).toBeLessThan(560);
    expect(big.cell).toBeLessThan(MAX);
    expect(big.cell).toBeLessThan(small.cell);
  });
});

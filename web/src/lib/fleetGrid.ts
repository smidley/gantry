// fitFleetCells: FleetStrip's own block sizing -- "objects (containers)
// inside the section should be auto-resized depending on quantity of
// containers. For example, if there's only 3 containers, they will be
// larger, and if there are 30 containers, the blocks will be smaller"
// (Scott). The classic fit-N-squares problem: given N blocks and the
// W x H the grid actually has, pick the largest square cell that still
// fits all N.
//
// Pure and measurement-free on purpose -- the component hands it one
// ResizeObserver reading and renders whatever comes back -- so the
// sizing rule itself is unit-testable across counts and areas with no
// DOM involved, the same split every other lib/*.ts helper here takes.
//
// The search is a descending integer sweep, not a closed form: the
// column count is a floor(), so "do N blocks fit at cell size s" is a
// step function rather than a smooth one, and the first s walking DOWN
// from max whose rows fit the height is therefore the maximum that fits,
// by construction. At most (max - min) iterations of integer arithmetic,
// run once per resize.

export interface FleetFitInput {
  count: number;
  // width/height: the box the BLOCKS themselves get, chrome already
  // subtracted by the caller (the section's head, its summary line and
  // its hover label are fixed rows outside the grid).
  width: number;
  height: number;
  // gap: the grid's own gap, applied on both axes -- N cells across
  // occupy N*cell + (N-1)*gap.
  gap: number;
  // min: the size floor. A fleet too big to fit even at this size
  // scrolls rather than shrinking into illegibility (see overflow).
  min: number;
  // max: the size ceiling. Without one a three-container fleet in a tall
  // column would render three enormous tiles -- "larger" is the ask, not
  // "as large as the arithmetic allows".
  max: number;
}

export interface FleetFit {
  // cell: the square cell's edge in px, always an integer in [min, max].
  cell: number;
  cols: number;
  rows: number;
  // overflow: true only when even the min floor can't fit `height` --
  // the one case the caller lets its grid scroll. Anywhere above the
  // floor the blocks shrink instead, so a fleet never scrolls while
  // there was still size left to give up.
  overflow: boolean;
}

// ROW_SLACK: how much cell size the fit will give back to save a whole
// ROW. A row costs a full row of page height and, on a small fleet,
// leaves a ragged part-filled last row as well; a few percent off each
// block costs nothing anyone can see. Four blocks across 490px is the
// case that forced it: at 120px only three fit per row, so the plain
// largest-cell answer was three-and-one over two rows, 246px tall,
// where 118px cells put all four on one 118px row.
const ROW_SLACK = 0.15;

// colsAt: how many cells of edge `s` fit across `w` at this gap. The
// `+ gap` on both sides is the standard "n cells have n-1 gaps"
// rearrangement: n*s + (n-1)*gap <= w  <=>  n <= (w + gap) / (s + gap).
function colsAt(w: number, s: number, gap: number): number {
  return Math.floor((w + gap) / (s + gap));
}

// rowsHeight: the height `rows` rows of edge `s` actually need.
function rowsHeight(rows: number, s: number, gap: number): number {
  return rows <= 0 ? 0 : rows * s + (rows - 1) * gap;
}

export function fitFleetCells(input: FleetFitInput): FleetFit {
  const min = Math.max(1, Math.floor(input.min));
  const max = Math.max(min, Math.floor(input.max));
  const gap = Math.max(0, input.gap);
  const count = Math.max(0, Math.floor(input.count));

  if (count === 0) return { cell: min, cols: 0, rows: 0, overflow: false };

  // An unmeasured box (the frame before the ResizeObserver first fires,
  // or a hidden section reporting 0x0) is clamped to one cell rather
  // than special-cased: the sweep below then trivially lands on the min
  // floor in a single column, which is exactly the right thing to paint
  // for one frame, and honestly reports overflow if the fleet doesn't
  // fit in it.
  const w = Number.isFinite(input.width) && input.width > 0 ? input.width : min;
  const h = Number.isFinite(input.height) && input.height > 0 ? input.height : min;

  for (let s = max; s >= min; s--) {
    const cols = colsAt(w, s, gap);
    if (cols < 1) continue;
    const rows = Math.ceil(count / cols);
    if (rowsHeight(rows, s, gap) > h) continue;

    // The largest cell that fits -- then one refinement pass over the
    // slack below it, taking any size that fits the same blocks into
    // FEWER rows (see ROW_SLACK). Scanning downward and only ever
    // accepting a strict row improvement lands on the fewest rows
    // reachable within the slack, at the largest cell that reaches them.
    let best = { cell: s, cols, rows, overflow: false };
    const floorS = Math.max(min, Math.ceil(s * (1 - ROW_SLACK)));
    for (let t = s - 1; t >= floorS; t--) {
      const tCols = colsAt(w, t, gap);
      if (tCols < 1) continue;
      const tRows = Math.ceil(count / tCols);
      if (tRows < best.rows && rowsHeight(tRows, t, gap) <= h) {
        best = { cell: t, cols: tCols, rows: tRows, overflow: false };
      }
    }
    return best;
  }

  // Nothing fit: sit on the floor and let the caller scroll. cols is
  // forced to at least 1 for a box narrower than a single min cell --
  // one clipped column still beats dividing by zero.
  const cols = Math.max(1, colsAt(w, min, gap));
  const rows = Math.ceil(count / cols);
  return { cell: min, cols, rows, overflow: rowsHeight(rows, min, gap) > h };
}

// fleetGridHeight: the height a fit actually USES. The caller sizes the
// field to this rather than to whatever space was offered, which is what
// keeps "take up the available space" from turning into a void: a fleet
// small enough to hit the max clamp stops at the row it needs instead of
// holding a half-screen open under three blocks. It is deliberately a
// second pass over the fit, not a cap fed INTO it -- capping the offered
// height at "what fits at max" would make every fleet render at max and
// the whole count-driven sizing would quietly stop happening.
export function fleetGridHeight(fit: FleetFit, gap: number): number {
  return rowsHeight(fit.rows, fit.cell, Math.max(0, gap));
}

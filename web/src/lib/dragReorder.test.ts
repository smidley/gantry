import { describe, expect, it } from 'vitest';
import { dropTargetAt, type DropColumnGeometry } from './dragReorder';

// Two lanes side by side, each holding cards 100px tall with 16px gaps,
// starting at y=0 -- close enough to the real modules band that the
// numbers below read as positions rather than arithmetic.
const WIDE: DropColumnGeometry = { column: 'wide', left: 0, right: 600, midpoints: [50, 166] };
const NARROW: DropColumnGeometry = { column: 'narrow', left: 620, right: 1000, midpoints: [50] };
const COLUMNS = [WIDE, NARROW];

describe('dropTargetAt', () => {
  it('returns null when there is no lane at all', () => {
    expect(dropTargetAt(100, 100, [])).toBeNull();
  });

  it('picks the column the pointer is horizontally inside', () => {
    expect(dropTargetAt(300, 10, COLUMNS)?.column).toBe('wide');
    expect(dropTargetAt(800, 10, COLUMNS)?.column).toBe('narrow');
  });

  it('picks the nearest column when the pointer is in neither', () => {
    expect(dropTargetAt(610, 10, COLUMNS)?.column).toBe('wide'); // 10px past wide, 10px short of narrow -- tie, first wins
    expect(dropTargetAt(615, 10, COLUMNS)?.column).toBe('narrow');
    expect(dropTargetAt(-500, 10, COLUMNS)?.column).toBe('wide');
    expect(dropTargetAt(5000, 10, COLUMNS)?.column).toBe('narrow');
  });

  // The midpoint rule: a card swaps places the instant the pointer
  // passes its centre, which is what makes a drag feel like it tracks
  // the cursor rather than lagging a whole card behind it.
  it('inserts before a card while above its midpoint and after it below', () => {
    expect(dropTargetAt(300, 0, COLUMNS)).toEqual({ column: 'wide', index: 0 });
    expect(dropTargetAt(300, 49, COLUMNS)).toEqual({ column: 'wide', index: 0 });
    expect(dropTargetAt(300, 51, COLUMNS)).toEqual({ column: 'wide', index: 1 });
    expect(dropTargetAt(300, 165, COLUMNS)).toEqual({ column: 'wide', index: 1 });
    expect(dropTargetAt(300, 167, COLUMNS)).toEqual({ column: 'wide', index: 2 });
  });

  it('clamps to the end of the lane far below the last card', () => {
    expect(dropTargetAt(300, 99_999, COLUMNS)).toEqual({ column: 'wide', index: 2 });
  });

  // An empty lane is a real drop target -- it is how the last module
  // dragged out of a column gets put back. A flex lane with nothing in
  // it collapses to zero width, so the nearest-column fallback is what
  // actually resolves this case.
  it('accepts a drop into an empty lane at index 0', () => {
    const empty: DropColumnGeometry = { column: 'narrow', left: 620, right: 620, midpoints: [] };
    expect(dropTargetAt(700, 400, [WIDE, empty])).toEqual({ column: 'narrow', index: 0 });
    expect(dropTargetAt(621, 400, [WIDE, empty])).toEqual({ column: 'narrow', index: 0 });
  });

  it('is unaffected by how many cards the OTHER lane holds', () => {
    const crowded: DropColumnGeometry = { column: 'narrow', left: 620, right: 1000, midpoints: [10, 20, 30, 40] };
    expect(dropTargetAt(300, 51, [WIDE, crowded])).toEqual({ column: 'wide', index: 1 });
  });
});

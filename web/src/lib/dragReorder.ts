// Drop-target geometry for the Overview's hand-rolled module drag --
// the pure half, split out from the pointer plumbing in Overview.svelte
// so the one piece with real arithmetic in it is testable without a
// browser.
//
// Hand-rolled rather than a drag library on purpose: the problem is two
// vertical lists of card-height targets, which is a midpoint comparison
// and a column pick. A library would add a dependency, a bundle, and its
// own opinions about DOM ownership -- and this app's module cards hold
// LIVE uPlot canvases that must never be destroyed and rebuilt by a
// reorder, which means the DOM has to stay exactly where Svelte's keyed
// each put it. Nothing here touches the DOM at all; the caller snapshots
// rects once at pointerdown and asks this function where a pointer
// currently points.

export interface DropColumnGeometry {
  // column is echoed back on the answer -- an opaque tag as far as this
  // module is concerned, so the caller's own lane names are the only
  // vocabulary in play.
  column: string;
  left: number;
  right: number;
  // midpoints: the vertical centre of each drop-eligible card in this
  // column, in visual (top to bottom) order, EXCLUDING the card being
  // dragged. Excluding it is what makes the returned index directly
  // usable as a splice position in a list the dragged id has already
  // been removed from -- see moveOverviewModule's own doc.
  midpoints: number[];
}

export interface DropTarget {
  column: string;
  index: number;
}

// columnDistance is how far x sits outside a column's horizontal span --
// 0 while inside it. Used only to break the "pointer is over neither
// column" case (the page gutters, or a pointer dragged clean off the
// side of the window), where the nearest lane is the honest guess.
function columnDistance(x: number, col: DropColumnGeometry): number {
  if (x < col.left) return col.left - x;
  if (x > col.right) return x - col.right;
  return 0;
}

// dropTargetAt answers "if the pointer released here, where would the
// card land": which column, and which insertion index within it.
//
// Column: the one whose horizontal span contains x, else the nearest.
// Ties (overlapping or zero-width spans -- an empty lane really is
// zero-width in a flex row until something is in it) go to the first
// one listed, which keeps the answer stable while a pointer hovers
// exactly on a boundary instead of flickering between lanes.
//
// Index: how many of that column's cards have their midpoint above y.
// The midpoint (not the top edge, not the gap) is the standard rule
// because it makes the boundary between "before this card" and "after
// it" fall exactly halfway through it, so a card swaps places the
// instant the pointer passes its centre.
//
// Returns null only for an empty geometry list -- there is no lane to
// drop into at all, which the caller reads as "this drag commits
// nothing".
export function dropTargetAt(x: number, y: number, columns: DropColumnGeometry[]): DropTarget | null {
  if (columns.length === 0) return null;

  let best = columns[0];
  let bestDistance = columnDistance(x, best);
  for (const col of columns.slice(1)) {
    const distance = columnDistance(x, col);
    if (distance < bestDistance) {
      best = col;
      bestDistance = distance;
    }
  }

  let index = 0;
  for (const mid of best.midpoints) {
    if (y > mid) index++;
  }
  return { column: best.column, index };
}

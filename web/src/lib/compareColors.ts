// Pure series-color assignment for the compare view -- one categorical
// hue per member, assigned by POSITION in the member list (index 0 ->
// --series-1, ...), the same "color follows position, not entity
// identity" rule the Metrics page's own hero chart already uses (see
// TopConsumers.svelte's heroSeries doc): a categorical hue is only
// meaningful for as long as a member holds a slot at all, and every
// chart on the compare page (CPU/Memory/Net/IO/GPU) reads the same
// member list in the same order, so a given member's line is the same
// color everywhere on the page, matching its own header chip. Removing a
// member reflows every LATER member's color -- the same unavoidable
// consequence position-based color assignment already has on the
// Metrics page when its own ranking changes.
const SERIES_COLOR_COUNT = 8;

// seriesColorVar returns the CSS var() reference for member index `i`
// (0-based) -- wrapped (not clamped) into tokens.css's own 8-slot
// categorical palette, though Compare.svelte never actually calls this
// past index 7: MAX_COMPARE_MEMBERS (compareRoute.ts) already caps how
// many members ever reach a chart/chip in the first place, so the wrap
// is defensive only.
export function seriesColorVar(i: number): string {
  const slot = ((i % SERIES_COLOR_COUNT) + SERIES_COLOR_COUNT) % SERIES_COLOR_COUNT;
  return `--series-${slot + 1}`;
}

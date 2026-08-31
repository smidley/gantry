// Pure series-color assignment by POSITION in a list (index 0 ->
// --series-1, ...) -- correct only for a caller whose SLOTS are the
// stable identity, not whatever currently occupies them (Storage's own
// per-drive header chart: a physical bay keeps its slot for the life of
// the array, regardless of which drive is in it -- see Storage.svelte's
// own doc). The Metrics hero chart and Compare used to share this same
// position-based rule too, which is exactly why a container's line/chip
// used to repaint a new color whenever the ranking merely reordered, or
// disagreed with its color on another page entirely -- both now use
// containerColor.ts's identity-hash instead (D2 chart-integrity pass).
const SERIES_COLOR_COUNT = 10;

// seriesColorVar returns the CSS var() reference for slot index `i`
// (0-based) -- wrapped (not clamped) into tokens.css's own 10-slot
// categorical palette, though Storage.svelte (this function's one
// remaining caller) never actually reaches this past index 9 for a
// realistic array, so the wrap is defensive only.
export function seriesColorVar(i: number): string {
  const slot = ((i % SERIES_COLOR_COUNT) + SERIES_COLOR_COUNT) % SERIES_COLOR_COUNT;
  return `--series-${slot + 1}`;
}

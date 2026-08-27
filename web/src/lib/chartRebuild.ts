// Shared decision for TimeChart/Sparkline: whether a live uPlot instance
// must be destroyed and recreated, or can be updated cheaply via
// chart.setData -- the Phase 3 review fix for both charts rebuilding on
// every SSE frame (every 2s in Live mode). A destroy+recreate throws away
// uPlot's own cursor/sync/legend state, which is why a stationary-mouse
// tooltip on a Live-range chart never survived past one frame, and why 20+
// charts rebuilding every tick (the Containers table's per-row Sparklines)
// showed up as long main-thread tasks.
//
// A rebuild is only actually needed when the chart's STRUCTURE changes --
// something build() bakes into the uPlot config itself (series count/
// labels/colors, resolved theme colors, axis unit/formatValue) -- never
// for a data-only change, which setData handles without losing any
// internal state. Markers are deliberately NOT part of this shape:
// TimeChart's draw/cursor hooks close over its own reactive `markers`
// binding directly (Svelte 5 props are signals -- every hook invocation
// reads the CURRENT value, not one snapshotted at build() time), so a
// marker-only change needs nothing more than the redraw setData already
// triggers.

export interface SeriesShape {
  label: string;
  colorVar: string;
}

export interface ChartShape {
  series: SeriesShape[];
  theme: string;
  unit?: string;
  hasFormatValue: boolean;
}

// sameSeriesShape compares two series lists by label+colorVar, in order --
// exactly what each series contributes to build()'s uPlot config, ignoring
// the points each one carries (a data-only difference).
export function sameSeriesShape(a: SeriesShape[], b: SeriesShape[]): boolean {
  return a.length === b.length && a.every((s, i) => s.label === b[i].label && s.colorVar === b[i].colorVar);
}

// needsRebuild is true when there's no previous shape yet (first build) or
// `next` differs structurally from `prev`; false means the caller can call
// chart.setData(...) instead of destroy+recreate.
export function needsRebuild(prev: ChartShape | null, next: ChartShape): boolean {
  if (!prev) return true;
  return (
    prev.theme !== next.theme ||
    prev.unit !== next.unit ||
    prev.hasFormatValue !== next.hasFormatValue ||
    !sameSeriesShape(prev.series, next.series)
  );
}

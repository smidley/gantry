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
  // width/dash (additive, optional -- the Metrics page's own multi-line
  // hero): a muted, dashed "host total" reference line among otherwise-
  // solid container lines is also baked into build()'s uPlot config, so
  // a change here (today: only ever tied to a series' fixed identity --
  // "the host line" vs. "a container line" -- never independently) must
  // trigger a rebuild the same as label/colorVar would.
  width?: number;
  dash?: number[];
}

export interface ChartShape {
  series: SeriesShape[];
  theme: string;
  unit?: string;
  hasFormatValue: boolean;
  // showLegend (additive, optional, default-true semantics owned by the
  // caller -- see TimeChart's own doc): affects whether build() passes
  // legend.show at all, so flipping it needs a rebuild too.
  showLegend?: boolean;
}

// sameArr is a small order-sensitive equality check for an optional
// numeric array (dash) -- undefined/undefined counts as equal, same as
// every other optional shape field here.
function sameArr(a: number[] | undefined, b: number[] | undefined): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

// sameSeriesShape compares two series lists by everything each one
// contributes to build()'s uPlot config, in order, ignoring the points
// each one carries (a data-only difference).
export function sameSeriesShape(a: SeriesShape[], b: SeriesShape[]): boolean {
  return (
    a.length === b.length &&
    a.every((s, i) => s.label === b[i].label && s.colorVar === b[i].colorVar && s.width === b[i].width && sameArr(s.dash, b[i].dash))
  );
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
    prev.showLegend !== next.showLegend ||
    !sameSeriesShape(prev.series, next.series)
  );
}

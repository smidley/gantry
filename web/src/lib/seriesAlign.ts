// buildAlignedData: TimeChart's own join step, pulled out into a plain,
// unit-testable module (the same split lib/streamdriver.ts and
// lib/livering.ts already use for their own pure logic).
//
// uPlot requires one shared x-axis across every series on a chart. This
// unions every series' own timestamps into that shared axis, filling any
// series' missing timestamps with null -- a real gap, not a false zero.
//
// Root-cause note (D2 chart-integrity pass): the "container lines exist
// only in disconnected patches" bug this was written to fix turned out
// NOT to be cross-series timestamp misalignment -- verified directly
// against the real box (both the live ring and the samples_1m SQL tier
// return byte-identical timestamps across every container, because every
// collector Tick shares ONE `now` for its whole pass, see docker.go/
// cgroupv2.go). The actual cause was TopConsumers.svelte's hero slots
// being keyed by RANK POSITION rather than container identity, wiping a
// slot's entire ring whenever two members merely swapped rank (fixed
// there, not here). This module still adds one genuine, independent
// robustness fix: bridgeSmallGaps below, a backstop for a real but
// narrow per-series miss (a rate-tracker's first tick after it warms up,
// a transient docker-API read failure for one container on one tick) --
// exactly the "spanGaps for small gaps" half of the brief, implemented
// as a bounded linear interpolation rather than uPlot's own boolean
// spanGaps (which would just as happily bridge an hours-wide real
// outage, silently fabricating a trend line across it).
export interface AlignInputSeries {
  points: readonly (readonly [number, number])[];
}

// DEFAULT_BRIDGE_FACTOR: a null run gets bridged only when its total
// span is at most this many multiples of the CHART'S OWN typical sample
// spacing (the median gap across every series' union of timestamps) --
// self-calibrating to whatever cadence this particular chart actually
// samples at (live 2s ticks, a 1-minute SQL tier, a 10-minute one for a
// 7d window, ...) rather than a fixed-seconds constant that would be
// right for one tier and wrong for every other. 2.5x is generous enough
// to bridge "this series missed exactly one expected tick" (the common
// real case) without reaching into "this series was genuinely absent for
// a while," which must stay a real, visible gap.
const DEFAULT_BRIDGE_FACTOR = 2.5;

function median(nums: number[]): number | undefined {
  if (nums.length === 0) return undefined;
  const sorted = [...nums].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
}

// bridgeSmallGaps mutates each column in place, filling an interior null
// run (flanked by a real value on BOTH sides -- never the leading edge of
// a series that hasn't started yet, never the trailing edge of one whose
// newest sample just hasn't arrived) via linear interpolation between
// those two real neighbors, but only when the run's total span is small
// relative to how densely this chart is actually sampled. A series with
// too few points to have a meaningful "typical spacing" (fewer than 2
// gaps) is left entirely alone -- there's no signal yet to judge "small"
// against.
function bridgeSmallGaps(xs: number[], ys: (number | null)[][], bridgeFactor: number): void {
  const steps: number[] = [];
  for (let i = 1; i < xs.length; i++) steps.push(xs[i] - xs[i - 1]);
  const typicalStep = median(steps);
  if (!typicalStep || typicalStep <= 0) return;
  const maxBridgeSpan = typicalStep * bridgeFactor;

  for (const col of ys) {
    let i = 0;
    while (i < col.length) {
      if (col[i] !== null) {
        i++;
        continue;
      }
      let j = i;
      while (j < col.length && col[j] === null) j++;
      if (i > 0 && j < col.length && xs[j] - xs[i - 1] <= maxBridgeSpan) {
        const spanSec = xs[j] - xs[i - 1];
        const prevVal = col[i - 1] as number;
        const nextVal = col[j] as number;
        for (let k = i; k < j; k++) {
          const t = (xs[k] - xs[i - 1]) / spanSec;
          col[k] = prevVal + (nextVal - prevVal) * t;
        }
      }
      i = j;
    }
  }
}

export function buildAlignedData(
  seriesList: readonly AlignInputSeries[],
  bridgeFactor: number = DEFAULT_BRIDGE_FACTOR,
): (number | null)[][] {
  const tsSet = new Set<number>();
  for (const s of seriesList) for (const [ts] of s.points) tsSet.add(ts);
  const xs = Array.from(tsSet).sort((a, b) => a - b);
  const idx = new Map(xs.map((ts, i) => [ts, i]));
  const ys: (number | null)[][] = seriesList.map(() => new Array(xs.length).fill(null));
  seriesList.forEach((s, si) => {
    for (const [ts, val] of s.points) {
      const i = idx.get(ts);
      if (i !== undefined) ys[si][i] = val;
    }
  });

  bridgeSmallGaps(xs, ys, bridgeFactor);

  return [xs, ...ys];
}

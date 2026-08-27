// Pure hover-scrub math shared by Sparkline/ContainerRow's own hover
// handling: mapping a pointer's horizontal fraction across the CURRENT
// live window (streamdriver's own liveWindowRange, not the data's own
// span -- see the design's own note on why) into a timestamp, then
// finding the ring point nearest that timestamp. Deliberately plain (no
// DOM, no runes) so it's trivially unit-testable, same split as every
// other lib/*.ts pure helper in this app.
import type { RingPoint } from './livering';

export interface ScrubHit {
  ts: number;
  value: number;
  index: number;
}

// tsAtFraction maps a pointer's [0,1] fraction across [min, max] to a
// timestamp -- fraction is clamped first, so a pointer event that
// overshoots an element's own bounds by a pixel still lands a sane
// in-window value rather than extrapolating past it.
export function tsAtFraction(fraction: number, min: number, max: number): number {
  const clamped = Math.min(1, Math.max(0, fraction));
  return min + clamped * (max - min);
}

// nearestPointAt finds the point in `points` (ascending by ts, as every
// RingPoint[] in this app is) whose timestamp is closest to `ts`,
// clamping to the first/last point when ts falls outside the ring's own
// range entirely -- an empty region of the live window still scrubs to
// the nearest REAL sample rather than showing nothing. Ties resolve to
// the earlier point. Returns null only when there is no point to find at
// all (an empty ring, or a non-finite ts).
export function nearestPointAt(points: RingPoint[], ts: number): ScrubHit | null {
  if (points.length === 0 || !Number.isFinite(ts)) return null;

  const lastIdx = points.length - 1;
  if (ts <= points[0][0]) return { ts: points[0][0], value: points[0][1], index: 0 };
  if (ts >= points[lastIdx][0]) return { ts: points[lastIdx][0], value: points[lastIdx][1], index: lastIdx };

  // Binary search for the first index whose ts is >= the target -- this
  // brackets it between lo-1 (<=ts) and lo (>=ts), one of which is the
  // nearest point.
  let lo = 0;
  let hi = lastIdx;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (points[mid][0] < ts) lo = mid + 1;
    else hi = mid;
  }
  const after = points[lo];
  const before = points[lo - 1];
  return ts - before[0] <= after[0] - ts
    ? { ts: before[0], value: before[1], index: lo - 1 }
    : { ts: after[0], value: after[1], index: lo };
}

// --- Scrub bus (synced scrubbing across related metrics) --------------
//
// The bus itself is one page-global {ts, sourceId} pair (see
// lib/scrubbus.svelte.ts for the thin $state wrapper around this pure
// state) -- ts is the single shared instant every mounted scrub-aware
// surface (every Sparkline, StatTile, ContainerRow) renders itself
// against, and sourceId is an opaque token identifying whichever
// surface is CURRENTLY the one actually tracking the pointer. The only
// real decision this needs to make is clearScrubIfOwner's own guard,
// below; publishScrub is a plain unconditional overwrite (whoever is
// generating real pointer events right now always wins ownership,
// unconditionally superseding whatever the previous owner published).
export interface ScrubBusState {
  ts: number | null;
  sourceId: unknown | null;
}

export const initialScrubBusState: ScrubBusState = { ts: null, sourceId: null };

export function publishScrub(ts: number, sourceId: unknown): ScrubBusState {
  return { ts, sourceId };
}

// clearScrubIfOwner is the one piece of real logic: a clear only takes
// effect if `sourceId` is STILL the bus's current owner, a no-op
// otherwise. This is what makes a stale clear harmless -- e.g. the
// pointer crossing directly from sparkline A to sparkline B fires A's
// pointerleave (and, in Sparkline's own onDestroy, an unmounting owner's
// cleanup) slightly after B may have already published; without this
// guard, that late clear would wipe out B's fresh scrub and strand every
// OTHER surface back on "live" a frame after they'd just synced to B.
export function clearScrubIfOwner(state: ScrubBusState, sourceId: unknown): ScrubBusState {
  return state.sourceId === sourceId ? initialScrubBusState : state;
}

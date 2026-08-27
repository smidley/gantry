// Pure ring-buffer logic for Overview's stat-tile sparklines and
// Container Detail's Live-range charts: "the last N minutes, fed
// straight from SSE frames as they arrive," not a historical /api/series
// query. Deliberately plain (no runes, no svelte/sse import) so it's
// trivially unit-testable and carries no import-time dependency on
// anything browser-only -- see lib/livering.svelte.ts for the small
// reactive wrapper that wires this to the live store from inside a
// component.
//
// Live-seed history (mergeSeed/appendAfterSeed/seriesPointsToRing, below
// pushRing) extends this same file rather than living apart from it: a
// seeded ring is still just a RingPoint[] with the same window/cap
// invariants pushRing already guarantees, and every seed-aware helper is
// built directly out of pushRing rather than re-deriving that trim logic.
import type { SeriesPoint } from './api';

export type RingPoint = [number, number]; // [ts (unix seconds), value]

// HARD_CAP bounds ring length independent of frame cadence -- "ring
// buffers fixed-size" per the design's bounded-everything rule. Generous
// even at a hypothetical 1s cadence held for 20 minutes.
const HARD_CAP = 1200;

// pushRing appends one (ts, value) sample to points and prunes anything
// older than windowSec relative to ts, returning a NEW array rather than
// mutating points -- callers hold the result in their own reactive state
// and reassign, the same convention sse.svelte.ts's own frame field uses.
// Non-finite ts/value are dropped silently (a malformed frame must not
// corrupt the ring). A repeated ts (e.g. a re-render before a genuinely
// new frame arrived) replaces the prior point at that instant rather than
// appending a same-instant duplicate. At least one point is always kept
// once any valid sample has been pushed, even if windowSec is 0 -- a
// pathological window must not silently empty the ring.
export function pushRing(points: RingPoint[], ts: number, value: number, windowSec = 900): RingPoint[] {
  if (!Number.isFinite(ts) || !Number.isFinite(value)) return points;
  const withoutStaleDup = points.length > 0 && points[points.length - 1][0] === ts ? points.slice(0, -1) : points;
  const next: RingPoint[] = [...withoutStaleDup, [ts, value]];
  const cutoff = ts - windowSec;
  let start = 0;
  while (start < next.length - 1 && next[start][0] < cutoff) start++;
  const trimmed = start > 0 ? next.slice(start) : next;
  return trimmed.length > HARD_CAP ? trimmed.slice(trimmed.length - HARD_CAP) : trimmed;
}

// appendAfterSeed is pushRing's sibling for a ring that has been seeded
// from server history (mergeSeed, below): once seeded, a push at or
// before the ring's own newest point is dropped outright rather than
// replacing it the way pushRing's same-instant rule does for every other
// ring. That distinction matters exactly once, at the seed->stream
// handoff -- the seed fetch's own "to" and the first SSE frame delivered
// right after mount can carry the same instant (or, since `live`'s own
// frame/frameCount are a module-level singleton that outlives navigation,
// an already-cached frame liveRing's effect re-processes the moment it
// mounts, one already covered by the seed) -- and re-processing that
// instant must be a true no-op, never a value replace. Every OTHER ring
// (never seeded) keeps using plain pushRing, unchanged.
export function appendAfterSeed(points: RingPoint[], ts: number, value: number, windowSec = 900): RingPoint[] {
  if (!Number.isFinite(ts) || !Number.isFinite(value)) return points;
  if (points.length > 0 && ts <= points[points.length - 1][0]) return points;
  return pushRing(points, ts, value, windowSec);
}

// mergeSeed folds a freshly-fetched batch of history (already-held's own
// seed(), below) in as a ring's initial contents: the seed forms the
// base -- sanitized, sorted ascending, and trimmed to the same
// window/cap pushRing would apply, by literally folding each point
// through pushRing itself rather than re-deriving that logic -- and
// `held` (whatever the ring had already accumulated live, if anything
// arrived before the seed fetch resolved) folds in on top through
// appendAfterSeed's own ignore-dedup rule, in its own already-ascending
// order.
//
// An empty (or entirely non-finite) seed returns `held` completely
// unchanged -- same reference, not just an equal value -- so a failed
// seed fetch, or a fresh restart's honestly-short live ring, is a true
// no-op: whatever pushRing has already built up (today's exact
// behavior, seeding or not) is never disturbed just because history came
// back empty. Callers use reference equality against `held` to tell
// "nothing to seed" apart from "seeded, and it happened to still be
// empty after trimming" -- see livering.svelte.ts's own seed() method.
export function mergeSeed(held: RingPoint[], seed: RingPoint[], windowSec = 900): RingPoint[] {
  const valid = seed.filter(([ts, value]) => Number.isFinite(ts) && Number.isFinite(value));
  if (valid.length === 0) return held;
  const sorted = [...valid].sort((a, b) => a[0] - b[0]);
  let merged: RingPoint[] = [];
  for (const [ts, value] of sorted) merged = pushRing(merged, ts, value, windowSec);
  for (const [ts, value] of held) merged = appendAfterSeed(merged, ts, value, windowSec);
  return merged;
}

// seriesPointsToRing converts an /api/series result's wire points ([ts,
// avg, max] -- see api.ts's own SeriesPoint doc) into a ring's plain
// [ts, value] shape, keeping avg: a live-ring-path point already has
// avg===max (a single instantaneous sample carries no aggregation -- see
// query.go's own SeriesPoint doc), and every existing fetched-range
// caller in this app charts avg for the same series anyway, never max.
export function seriesPointsToRing(points: SeriesPoint[]): RingPoint[] {
  return points.map(([ts, avg]): RingPoint => [ts, avg]);
}

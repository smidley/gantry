// Pure ring-buffer logic for Overview's stat-tile sparklines and
// Container Detail's Live-range charts: "the last N minutes, fed
// straight from SSE frames as they arrive," not a historical /api/series
// query. Deliberately plain (no runes, no svelte/sse import) so it's
// trivially unit-testable and carries no import-time dependency on
// anything browser-only -- see lib/livering.svelte.ts for the small
// reactive wrapper that wires this to the live store from inside a
// component.
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

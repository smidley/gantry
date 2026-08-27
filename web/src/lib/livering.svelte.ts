// Reactive wrapper around lib/livering.ts's pure pushRing, wiring it to
// the live SSE store -- see that file's doc for why the pure logic lives
// separately (testability, no browser-only import at module-eval time).
// This file needs the ".svelte.ts" suffix (not livering.ts's plain
// ".ts") because $state/$effect are Svelte compiler macros: vite-plugin-
// svelte only compiles them inside a .svelte file or a module ending in
// ".svelte.js"/".svelte.ts". A bare .ts file calling $state would throw
// at runtime -- nothing would ever have compiled the call away.
import { untrack } from 'svelte';
import { live } from './sse.svelte';
import { pushRing, appendAfterSeed, mergeSeed, type RingPoint } from './livering';
import type { SnapshotDTO } from './api';

// liveRing wires pushRing to the live store: call it once per metric
// from a component's own top-level script. That's a synchronous call
// during the component's setup phase, which is all $state/$effect
// require -- textually living inside a helper function rather than
// directly in the .svelte file's <script> block doesn't change that, as
// long as the helper itself runs synchronously from there (it does: this
// is a plain function call, not a callback deferred to a microtask or
// event).
//
// extract reads whatever this ring tracks off one frame; returning
// undefined for a frame that doesn't (yet) carry the metric skips that
// frame rather than pushing a false 0.
export function liveRing(extract: (frame: SnapshotDTO) => number | undefined, windowSec = 900) {
  let points = $state<RingPoint[]>([]);
  // seeded flips true the first time seed() below actually applies real
  // history (never on an empty/failed seed -- see mergeSeed's own doc).
  // A plain closure variable, not $state: nothing ever needs to react to
  // IT changing on its own, only the `points` write that always
  // accompanies its flip. Gates which append rule the effect below uses
  // -- a ring seed() was never called for (or that only ever saw an
  // empty seed) keeps pushRing's plain replace-on-tie behavior exactly
  // as it is today; only a genuinely-seeded ring switches to
  // appendAfterSeed's stricter ignore-on-tie-or-older rule, for the
  // seed->stream handoff race that rule exists for (see its own doc).
  let seeded = false;

  $effect(() => {
    live.frameCount;
    const frame = live.frame;
    if (!frame) return;
    const value = extract(frame);
    if (value === undefined) return;
    // untrack the read of `points` itself: this effect's only intended
    // dependency is live.frameCount/live.frame (one run per SSE frame).
    // Reading points normally here would ALSO register it as a
    // dependency, and the very next line's write to it would then
    // retrigger this same effect -- a self-referential loop Svelte's own
    // runtime catches and throws effect_update_depth_exceeded on
    // (reproduced live while building this). untrack breaks that cycle:
    // the write below is unaffected (writes never register as reads).
    points = untrack(() =>
      seeded ? appendAfterSeed(points, frame.ts, value, windowSec) : pushRing(points, frame.ts, value, windowSec),
    );
  });
  return {
    get points() {
      return points;
    },
    // seed folds an /api/series history fetch's result in as this ring's
    // initial contents -- see mergeSeed's own doc for the exact merge
    // rule (the seed as the base, anything this ring already pushed
    // live before the fetch resolved folded in on top, deduped rather
    // than duplicated). Callers pass seriesPointsToRing(result.points);
    // an empty/all-invalid seed is a deliberate no-op (mergeSeed returns
    // `held` back unchanged, by reference) -- `seeded` stays false and
    // every future push keeps behaving exactly as it does today.
    //
    // `held` is read ONCE, under untrack, and reused for the no-op check
    // below rather than re-reading the live `points` getter a second
    // time -- a caller (ContainerRow, for the Containers view's seeding)
    // can call seed() SYNCHRONOUSLY from inside its own $effect, unlike
    // every other caller, which only ever schedules a fetch whose
    // .then() runs later, detached from any effect. A second untracked
    // read wouldn't be untracked at all if it happened outside untrack's
    // callback -- reproduced live: `points` got attributed as a
    // dependency of THAT caller's effect, which this same line's write
    // then re-dirtied, re-running the effect, calling seed() again,
    // forever -- effect_update_depth_exceeded. Comparing against the
    // already-captured `held` instead needs no second read at all.
    seed(seedPoints: RingPoint[]) {
      const held = untrack(() => points);
      const merged = mergeSeed(held, seedPoints, windowSec);
      if (merged === held) return; // empty seed -- see mergeSeed's own doc
      seeded = true;
      points = merged;
    },
  };
}

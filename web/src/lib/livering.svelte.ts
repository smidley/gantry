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
import { pushRing, type RingPoint } from './livering';
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
    points = pushRing(untrack(() => points), frame.ts, value, windowSec);
  });
  return {
    get points() {
      return points;
    },
  };
}

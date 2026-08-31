// Reactive wrapper around lib/scrub.ts's pure publishScrub/
// clearScrubIfOwner -- see that file's own doc for the ownership-guard
// rationale. This needs the ".svelte.ts" suffix (not scrub.ts's plain
// ".ts") purely because $state is a Svelte compiler macro -- same split
// as every other lib/*.ts pure module and its own reactive sibling
// (livering/streamdriver/theme).
//
// A SINGLE page-global instance, exported below, not a per-caller
// factory like liveRing(): Scott's own requirement ("scrubbing over one
// metric should auto scrub the other metrics that are related to the
// same point in time") means every scrub-aware surface currently
// mounted -- Overview's 4 tiles, Settings' 2, every Containers row --
// needs to render off the SAME shared instant, which one shared bus
// gives for free. "The current page" scoping this needs falls out of
// component lifecycle alone: only surfaces that are actually mounted can
// possibly be reading `ts`, and Sparkline's own onDestroy clears the bus
// if it was the owner, so navigating away can never strand a bus stuck
// mid-scrub for whatever gets mounted next.
import { publishScrub, clearScrubIfOwner, initialScrubBusState, type ScrubBusState } from './scrub';

class ScrubBus {
  #state = $state<ScrubBusState>(initialScrubBusState);

  // ts is the only thing a FOLLOWER ever needs: null means "nobody is
  // scrubbing, render live"; a number is the shared instant to run your
  // own nearestPointAt against. sourceId stays bus-internal -- it only
  // ever matters to clear()'s own ownership check, below.
  get ts() {
    return this.#state.ts;
  }

  publish(ts: number, sourceId: unknown) {
    this.#state = publishScrub(ts, sourceId);
  }

  clear(sourceId: unknown) {
    this.#state = clearScrubIfOwner(this.#state, sourceId);
  }

  isOwner(sourceId: unknown) {
    return this.#state.sourceId === sourceId;
  }
}

export const scrubBus = new ScrubBus();

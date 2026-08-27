// The actual shared "smooth streaming" animation driver: ONE
// requestAnimationFrame loop for every live-mode TimeChart/Sparkline on
// the page, never one per chart -- a Containers view with 23 per-row
// sparklines fans a single rAF tick out to 23 tiny callbacks instead of
// running 23 independent loops. The throttle/gating DECISIONS
// (shouldBroadcast, gateReducer/isDriverActive) are pure and unit-tested
// in lib/streamdriver.ts; this file is the browser-only rAF loop +
// IntersectionObserver wiring around them, same split as sse.svelte.ts/
// livering.svelte.ts vs. their own pure siblings.
//
// Reduced-motion detection deliberately mirrors theme.svelte.ts's own
// hand-rolled `window.matchMedia(...).addEventListener('change', ...)`
// (rather than svelte/motion's prefersReducedMotion) for the same reason
// that file doesn't use svelte's built-in MediaQuery either: keeping
// this module's own browser dependency to plain DOM APIs, with no
// svelte/motion import here at all, is what guarantees nothing pulls
// window.matchMedia in at import time outside an actual browser -- the
// mechanism-3 Tween components import prefersReducedMotion directly
// instead (safe there: .svelte files are never imported by vitest).
import { gateReducer, isDriverActive, shouldBroadcast, type GateState } from './streamdriver';

type Tick = (nowMs: number) => void;

function systemPrefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true;
}

class StreamDriver {
  #gate: GateState = { subscribers: 0, reducedMotion: false };
  #callbacks = new Set<Tick>();
  #rafId: number | null = null;
  #lastBroadcastMs: number | null = null;

  // #tick discards requestAnimationFrame's own callback timestamp (a
  // performance.now()-relative DOMHighResTimeStamp) in favor of
  // Date.now() -- every consumer of the broadcast nowMs (the live
  // sliding x-window, head-easing's own arrivalMs bookkeeping) compares
  // it against wall-clock chart/ring timestamps, so mixing epochs would
  // make every duration computed from it meaningless. This is also the
  // literal "broadcasting the current wall-clock ms" from the design.
  #tick = () => {
    this.#rafId = null;
    if (!isDriverActive(this.#gate)) return;
    const nowMs = Date.now();
    if (shouldBroadcast(this.#lastBroadcastMs, nowMs)) {
      this.#lastBroadcastMs = nowMs;
      for (const cb of this.#callbacks) cb(nowMs);
    }
    this.#schedule();
  };

  #schedule() {
    if (this.#rafId === null && isDriverActive(this.#gate)) {
      this.#rafId = requestAnimationFrame(this.#tick);
    }
  }

  #stop() {
    if (this.#rafId !== null) {
      cancelAnimationFrame(this.#rafId);
      this.#rafId = null;
    }
  }

  // subscribe joins the shared loop (starting it if this is the first
  // subscriber) and returns the matching unsubscribe -- callers never
  // touch #gate/#rafId directly, only ever through this pair, so the
  // pure gateReducer above stays the single source of truth for whether
  // the loop should be running.
  subscribe(callback: Tick): () => void {
    this.#callbacks.add(callback);
    this.#gate = gateReducer(this.#gate, { type: 'subscribe' });
    this.#schedule();
    return () => {
      this.#callbacks.delete(callback);
      this.#gate = gateReducer(this.#gate, { type: 'unsubscribe' });
      if (!isDriverActive(this.#gate)) this.#stop();
    };
  }

  setReducedMotion(value: boolean) {
    this.#gate = gateReducer(this.#gate, { type: 'reduced-motion', value });
    if (isDriverActive(this.#gate)) this.#schedule();
    else this.#stop();
  }
}

const driver = new StreamDriver();

if (typeof window !== 'undefined' && window.matchMedia) {
  driver.setReducedMotion(systemPrefersReducedMotion());
  window.matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', (e) => {
    driver.setReducedMotion(e.matches);
  });
}

// subscribeWhileVisible is what TimeChart/Sparkline call from their own
// onMount: getEl() is read once, up front (both callers only ever pass a
// stable bind:this ref that's already attached by the time onMount/
// $effect runs -- see TimeChart's own doc for why). The chart only
// actually joins the shared driver while that element is intersecting
// the viewport; scrolling it off-screen unsubscribes immediately, which
// combined with prefersReducedMotion zeroing the driver out globally
// (above) is the other half of "stops entirely when there are zero
// subscribers" -- a live-mode chart that isn't currently on-screen costs
// nothing.
export function subscribeWhileVisible(getEl: () => Element | null | undefined, onTick: Tick): () => void {
  const el = getEl();
  if (!el || typeof IntersectionObserver === 'undefined') {
    return () => {};
  }

  let unsubscribe: (() => void) | null = null;
  let disposed = false;

  const observer = new IntersectionObserver((entries) => {
    if (disposed) return;
    const intersecting = entries[entries.length - 1]?.isIntersecting ?? false;
    if (intersecting) {
      if (!unsubscribe) unsubscribe = driver.subscribe(onTick);
    } else {
      unsubscribe?.();
      unsubscribe = null;
    }
  });
  observer.observe(el);

  return () => {
    disposed = true;
    observer.disconnect();
    unsubscribe?.();
    unsubscribe = null;
  };
}

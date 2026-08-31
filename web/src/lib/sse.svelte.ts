// Live SSE store: connects /api/live (event: frame, full SnapshotDTO
// JSON per tick), exposing the latest frame plus connection health.
// Reconnection is entirely native EventSource behavior -- we never
// re-create it ourselves on error, only reflect `connected` going
// false while the browser retries in the background.
import type { SnapshotDTO } from './api';
import { isReconnectGap, updateCadenceEma } from './streamdriver';

// staleAfterMs: no frame for this long marks the store `stale` even
// while `connected` stays true (a connection can be open but idle if
// the server-side publish loop stalls) -- LivePulse renders this as an
// amber dot + "stale" text per the design direction.
const STALE_AFTER_MS = 6000;
const STALE_CHECK_INTERVAL_MS = 1000;

class LiveStore {
  frame = $state<SnapshotDTO | null>(null);
  connected = $state(false);
  stale = $state(false);
  // frameCount increments once per received frame. LivePulse's CSS
  // pulse retriggers off a change to this counter (e.g. wrapped in a
  // Svelte {#key} block) rather than running on a free-running timer --
  // "the dot emits one soft pulse PER RECEIVED FRAME... it's honest"
  // per the design direction.
  frameCount = $state(0);
  lastFrameAt = $state(0);

  // glideMs is the duration (ms) every live head-ease/tween should span
  // for the leg starting with the frame that just arrived -- the
  // perpetual-glide motion pass's own retiming (see streamdriver.ts's
  // "Cadence-driven glide" doc): every live surface (StatTile, TopBarRow,
  // Sparkline/TimeChart heads, ...) reads this instead of a fixed guessed
  // duration, so its glide spans roughly the REAL wait until the next
  // frame rather than settling early and sitting frozen. 0 (snap, no
  // glide) for the very first frame ever -- nothing to glide FROM yet --
  // and for any frame following a genuine gap (isReconnectGap: a
  // backgrounded tab resuming, an SSE reconnect); every other frame gets
  // the freshly-updated #cadenceEma.
  glideMs = $state(0);

  #es: EventSource | null = null;
  #staleTimer: ReturnType<typeof setInterval> | null = null;
  #lastFrameAt = 0;
  // #cadenceEma is deliberately NOT reset on a reconnect gap (only
  // skipped for that one frame, below) -- it's the best estimate of the
  // steady-state cadence, which the gap itself says nothing new about,
  // so the very next ordinary frame after a reconnect can resume gliding
  // at roughly the right pace immediately rather than re-learning it from
  // scratch.
  #cadenceEma: number | null = null;

  // connect is idempotent (a second call while already connected is a
  // no-op) and safe to call during SSR/module init -- it does nothing
  // outside a browser.
  connect() {
    if (this.#es || typeof window === 'undefined' || typeof EventSource === 'undefined') return;

    const es = new EventSource('/api/live');
    this.#es = es;

    es.onopen = () => {
      this.connected = true;
    };
    // EventSource retries on its own after an error (native browser
    // behavior, no reconnect logic needed here) -- this only reflects
    // "not currently connected" while that retry is pending. onopen
    // flips it back once the retry succeeds.
    es.onerror = () => {
      this.connected = false;
    };
    es.addEventListener('frame', (ev: MessageEvent<string>) => {
      let parsed: SnapshotDTO;
      try {
        parsed = JSON.parse(ev.data) as SnapshotDTO;
      } catch {
        return; // a malformed frame is dropped, not fatal to the stream
      }
      const now = Date.now();
      if (this.#lastFrameAt === 0) {
        this.glideMs = 0; // first frame ever -- nothing to glide from
      } else {
        const delta = now - this.#lastFrameAt;
        if (isReconnectGap(delta)) {
          this.glideMs = 0; // snap across the gap rather than crawl across it
        } else {
          this.#cadenceEma = updateCadenceEma(this.#cadenceEma, delta);
          this.glideMs = this.#cadenceEma;
        }
      }
      this.frame = parsed;
      this.connected = true;
      this.stale = false;
      this.#lastFrameAt = now;
      this.lastFrameAt = now;
      this.frameCount++;
    });

    this.#staleTimer = setInterval(() => {
      if (this.#lastFrameAt && Date.now() - this.#lastFrameAt > STALE_AFTER_MS) {
        this.stale = true;
      }
    }, STALE_CHECK_INTERVAL_MS);
  }

  disconnect() {
    this.#es?.close();
    this.#es = null;
    if (this.#staleTimer !== null) clearInterval(this.#staleTimer);
    this.#staleTimer = null;
  }

  reconnect() {
    this.disconnect();
    this.connected = false;
    this.stale = false;
    this.connect();
  }
}

export const live = new LiveStore();

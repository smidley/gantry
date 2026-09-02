// The Overview's saved module arrangement as a shared reactive
// singleton -- the same class-based-store shape groups.svelte.ts and
// theme.svelte.ts use, holding the live document while
// overviewLayout.ts (its rune-free sibling) owns every rule about what a
// valid document is and how each gesture transforms one.
//
// A singleton rather than per-component state for the same reason
// GroupsStore is one: the layout is one server-side document, and the
// Overview can mount, unmount and remount (navigating away and back)
// without wanting to re-fetch or lose an in-flight save.
import { debounce } from './debounce';
import { fetchOverviewLayout, putOverviewLayout } from './api';
import {
  defaultOverviewLayout,
  hideOverviewModule,
  isModuleHidden,
  mergeOverviewLayout,
  moveOverviewModule,
  sameOverviewLayout,
  setOverviewModuleSize,
  setOverviewRatio,
  showOverviewModule,
  type OverviewColumn,
  type OverviewLayoutDoc,
  type OverviewSize,
} from './overviewLayout';

// SAVE_DEBOUNCE_MS coalesces a burst of edits into one PUT. There is no
// unsaved-state concept in this UI -- every committed gesture (a drop, a
// hide, a reset) persists on its own, and "Done" only leaves edit mode
// -- so this window exists purely to keep a fast sequence of gestures
// from firing a request each. Short enough that leaving the page right
// after a drag still saves it.
const SAVE_DEBOUNCE_MS = 400;

class OverviewLayoutStore {
  // doc starts at the default rather than empty: the Overview renders
  // immediately, before ensureLoaded's fetch resolves, and a blank
  // modules band flashing into a populated one on every page load would
  // be worse than the (overwhelmingly common) case of the default being
  // exactly right.
  doc = $state<OverviewLayoutDoc>(defaultOverviewLayout());
  loaded = $state(false);
  loadError = $state<string | null>(null);
  saveError = $state<string | null>(null);
  saving = $state(false);

  #loadPromise: Promise<void> | null = null;

  // #saveSeq counts committed edits. A PUT's response is the server's
  // own merged document and is normally worth adopting (it is what got
  // stored), but adopting it AFTER the user has already made another
  // edit would silently undo that edit -- so the response is only taken
  // when the sequence hasn't moved since the request went out.
  #saveSeq = 0;

  // ensureLoaded fetches at most once per page load, GroupsStore's exact
  // contract. A failed load is not fatal: doc stays at the default, the
  // page renders, and the next gesture will happily PUT over whatever is
  // stored -- which is the same thing a first-time customization does.
  ensureLoaded(): Promise<void> {
    if (!this.#loadPromise) {
      this.#loadPromise = fetchOverviewLayout()
        .then((resp) => {
          this.doc = mergeOverviewLayout(resp);
          this.loadError = null;
        })
        .catch(() => {
          this.loadError = "Couldn't load your saved layout.";
        })
        .finally(() => {
          this.loaded = true;
        });
    }
    return this.#loadPromise;
  }

  #flush = debounce(() => {
    void this.#persist();
  }, SAVE_DEBOUNCE_MS);

  async #persist(): Promise<void> {
    const seq = this.#saveSeq;
    const sending = this.doc;
    this.saving = true;
    this.saveError = null;
    try {
      const stored = await putOverviewLayout(sending);
      // Only adopt the server's answer if nothing has changed under us
      // (see #saveSeq) -- and even then only when it actually differs,
      // so an identical document doesn't churn every keyed each that
      // reads this.
      if (seq === this.#saveSeq) {
        const merged = mergeOverviewLayout(stored);
        if (!sameOverviewLayout(merged, this.doc)) this.doc = merged;
      }
    } catch (err) {
      this.saveError = err instanceof Error ? err.message : String(err);
    } finally {
      this.saving = false;
    }
  }

  // #apply is the one write path: every gesture below hands it a fresh
  // document. A no-op edit (dropping a card back exactly where it was)
  // costs nothing -- no state churn, no request.
  #apply(next: OverviewLayoutDoc): void {
    if (sameOverviewLayout(next, this.doc)) return;
    this.doc = next;
    this.#saveSeq++;
    this.#flush();
  }

  move(id: string, column: OverviewColumn, index: number): void {
    this.#apply(moveOverviewModule(this.doc, id, column, index));
  }

  // setRatio is called ONCE per divider gesture, on release (or on an
  // arrow press) -- never per pointermove. The live preview during a
  // drag is the view's own local state, so a drag across the band costs
  // exactly one PUT no matter how many frames it took, without relying
  // on the debounce below to swallow the other sixty.
  setRatio(ratio: number): void {
    this.#apply(setOverviewRatio(this.doc, ratio));
  }

  setSize(id: string, size: OverviewSize): void {
    this.#apply(setOverviewModuleSize(this.doc, id, size));
  }

  hide(id: string): void {
    this.#apply(hideOverviewModule(this.doc, id));
  }

  show(id: string): void {
    this.#apply(showOverviewModule(this.doc, id));
  }

  toggleHidden(id: string): void {
    if (isModuleHidden(this.doc, id)) this.show(id);
    else this.hide(id);
  }

  reset(): void {
    this.#apply(defaultOverviewLayout());
  }
}

export const overviewLayout = new OverviewLayoutStore();

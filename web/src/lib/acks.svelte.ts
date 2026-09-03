// Overview acknowledgements: a shared reactive singleton (the
// alertRules.svelte.ts / groups.svelte.ts class-store shape) so the ack
// control and the derivation's own filter always read ONE list --
// CalloutRow writes here (Events' own "Needs you" strip, the counts
// pass's relocated home for it), and every view that needs the
// derivation -- Overview for its chip counts, Events for the strip
// itself -- hands this same `acks.list` into deriveOverviewStatus's
// `acks` input alongside its own live frame.
//
// Acks are deliberately NOT in the SSE frame: they change only when the
// user acts (or when one expires), so a fetch-once-then-mutate-locally
// store is the whole data path. Expiry needs no bookkeeping here either
// -- deriveOverviewStatus checks each ack's own `until` per run (every
// ~2s frame tick), so a lapsed entry in this list simply stops
// filtering and its anomaly reappears; the server's GET already
// excludes expired rows on any later real fetch, and Maintain prunes
// them from disk.
import { createAck, deleteAck, fetchAcks } from './api';
import type { OverviewAckDTO } from './api';

class AcksStore {
  list = $state<OverviewAckDTO[]>([]);
  loaded = $state(false);
  loadError = $state<string | null>(null);

  #loadPromise: Promise<void> | null = null;

  // ensureLoaded fetches at most once per page load -- the same "never
  // re-fetch, never race" contract groups.svelte.ts/alertRules.svelte.ts
  // document. A load failure leaves the list empty (every anomaly shows,
  // the safe direction for a noise filter) and records loadError.
  ensureLoaded(): Promise<void> {
    if (!this.#loadPromise) {
      this.#loadPromise = fetchAcks()
        .then((resp) => {
          this.list = resp.acks;
          this.loadError = null;
        })
        .catch(() => {
          this.loadError = "Couldn't load acknowledgements.";
        })
        .finally(() => {
          this.loaded = true;
        });
    }
    return this.#loadPromise;
  }

  // ack posts one concrete (kind, entity) acknowledgement and folds the
  // created row into the local list -- the next deriveOverviewStatus run
  // (at latest the next ~2s frame tick) filters the row out, no refetch.
  // Throws on failure (the caller surfaces the server's own message, the
  // putSettings/putGroups convention); the list is untouched then.
  async ack(kind: string, entity: string, hours: number): Promise<OverviewAckDTO> {
    const created = await createAck({ kind, entity, hours });
    this.list = [...this.list, created];
    return created;
  }

  // unack lifts one ack early -- deleteAck is idempotent server-side, so
  // the local filter runs even for an id the server already pruned.
  async unack(id: number): Promise<void> {
    await deleteAck(id);
    this.list = this.list.filter((a) => a.id !== id);
  }
}

export const acks = new AcksStore();

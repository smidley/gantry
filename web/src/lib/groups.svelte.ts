// Custom container groups: a shared reactive singleton (same
// class-based-store shape as theme.svelte.ts) rather than per-component
// state, since the Containers view's own chip row and the Compare
// view's "Save as group" both read/write the SAME server-side list and
// must see each other's changes without an explicit page reload.
import { fetchGroups, putGroups } from './api';
import type { Group } from './api';
import { removeGroup, renameGroup, upsertGroup } from './groups';

class GroupsStore {
  list = $state<Group[]>([]);
  loaded = $state(false);
  loadError = $state<string | null>(null);
  saving = $state(false);
  saveError = $state<string | null>(null);

  #loadPromise: Promise<void> | null = null;

  // ensureLoaded fetches at most once per page load: the first caller
  // (whichever view mounts first) triggers the real fetch; every other
  // caller -- including this store's own save/rename/remove below,
  // which must never mutate a not-yet-loaded (and therefore
  // incomplete) list -- reuses the same in-flight or already-settled
  // promise instead of re-fetching.
  ensureLoaded(): Promise<void> {
    if (!this.#loadPromise) {
      this.#loadPromise = fetchGroups()
        .then((resp) => {
          this.list = resp.groups;
          this.loadError = null;
        })
        .catch(() => {
          this.loadError = "Couldn't load groups.";
        })
        .finally(() => {
          this.loaded = true;
        });
    }
    return this.#loadPromise;
  }

  async #persist(next: Group[]): Promise<boolean> {
    this.saving = true;
    this.saveError = null;
    try {
      const resp = await putGroups(next);
      this.list = resp.groups;
      return true;
    } catch (err) {
      this.saveError = err instanceof Error ? err.message : String(err);
      return false;
    } finally {
      this.saving = false;
    }
  }

  // saveAsGroup backs Compare's own "Save as group…" affordance: saving
  // under a name that already exists overwrites that group's own
  // membership (upsertGroup's own doc) rather than erroring.
  async saveAsGroup(name: string, members: string[]): Promise<boolean> {
    await this.ensureLoaded();
    return this.#persist(upsertGroup(this.list, name, members));
  }

  async rename(oldName: string, newName: string): Promise<boolean> {
    await this.ensureLoaded();
    return this.#persist(renameGroup(this.list, oldName, newName));
  }

  // remove deletes the group itself; it never touches the containers
  // it named (removeGroup's own doc -- a pure list edit, same PUT path
  // every other write here goes through).
  async remove(name: string): Promise<boolean> {
    await this.ensureLoaded();
    return this.#persist(removeGroup(this.list, name));
  }
}

export const groups = new GroupsStore();

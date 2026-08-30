// Alert rules: a shared reactive singleton (same class-based-store shape
// as groups.svelte.ts), for one reason beyond the usual "more than one
// view reads it" -- Task 12's band unification needs setBands called
// from exactly ONE place, at boot and after every successful rules PUT,
// never per-component (thresholds.ts's own doc: "App.svelte fetches...
// No per-component fetch"). Routing every rules read AND write through
// this one store is what makes that true: App.svelte's boot call and
// the Alerts view's own rule editor share the same ensureLoaded/save
// methods below, so setBands is never at risk of being called from two
// places with two different ideas of "the current rules."
import { fetchAlertRules, putAlertRules } from './api';
import type { AlertRuleDTO } from './api';
import { setBands } from './thresholds';

class AlertRulesStore {
  list = $state<AlertRuleDTO[]>([]);
  loaded = $state(false);
  loadError = $state<string | null>(null);
  saving = $state(false);

  #loadPromise: Promise<void> | null = null;

  // ensureLoaded fetches at most once per page load -- the first caller
  // (App.svelte's own boot effect, or whichever view mounts first if
  // that hasn't run yet) triggers the real fetch; every other caller
  // reuses the same in-flight or already-settled promise, the same
  // "never re-fetch, never race" contract groups.svelte.ts's own
  // ensureLoaded documents.
  ensureLoaded(): Promise<void> {
    if (!this.#loadPromise) {
      this.#loadPromise = fetchAlertRules()
        .then((resp) => {
          this.list = resp.rules;
          setBands(resp.rules);
          this.loadError = null;
        })
        .catch(() => {
          this.loadError = "Couldn't load alert rules.";
        })
        .finally(() => {
          this.loaded = true;
        });
    }
    return this.#loadPromise;
  }

  // save performs the whole-document PUT (the rule editor submits its
  // own already-edited full list, builtins included -- see
  // putAlertRules' own doc) and re-derives the runtime band table from
  // the server's own response, so a saved threshold change is reflected
  // in every displayed color on the very next render, with no reload.
  // Throws on failure (the caller surfaces the server's own message,
  // the same putSettings/putGroups convention) -- list/bands are left
  // exactly as they were before the attempt.
  async save(rules: AlertRuleDTO[]): Promise<AlertRuleDTO[]> {
    this.saving = true;
    try {
      const resp = await putAlertRules(rules);
      this.list = resp.rules;
      setBands(resp.rules);
      return resp.rules;
    } finally {
      this.saving = false;
    }
  }
}

export const alertRules = new AlertRulesStore();

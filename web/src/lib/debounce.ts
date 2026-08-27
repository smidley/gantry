// debounce wraps fn so the returned function only actually calls fn once
// no further call arrives within `ms` -- each call resets its own
// internal timer, coalescing rapid-fire calls (Events.svelte's entity
// text filter, one call per keystroke) into a single trailing call with
// the LAST call's arguments. Self-contained: correct regardless of how
// the caller wires cleanup (no reliance on an $effect's own teardown
// timing), unlike a bare setTimeout/clearTimeout pair split across call
// sites.
export function debounce<Args extends unknown[]>(fn: (...args: Args) => void, ms: number): (...args: Args) => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  return (...args: Args) => {
    if (timer !== undefined) clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}

// Pure route-building/parsing for the compare view's own "#/compare/
// name1,name2,..." hash. router.ts's generic ":param" capture already
// runs ONE decodeURIComponent pass over the whole raw segment before
// parseCompareNames ever sees it (same as every other :param -- see its
// own doc) -- which is exactly right here too: every real docker
// container name is restricted to [a-zA-Z0-9_.-] (enforced by the daemon
// itself), a charset with nothing that needs URL-escaping and, crucially,
// no comma, so decoding the whole joined segment before splitting on ","
// can never mis-split a real name. The encodeURIComponent/decodeURIComponent
// pair below exists for defensiveness (a hand-typed URL, a future name
// source with a looser charset), not because any name this app will
// actually see needs it.
import type { ContainerDTO } from './api';

// MAX_COMPARE_MEMBERS bounds how many members the compare page actually
// charts/fetches series for (see Compare.svelte) -- the same cap, and the
// same reason, as the Metrics page's own MAX_HERO_LINES: one categorical
// series color per member, and this app's palette (tokens.css) has
// exactly 10 (--series-1..10).
export const MAX_COMPARE_MEMBERS = 10;

// buildCompareHash returns the "#/compare/..." hash for a set of
// container names -- ALWAYS deduplicated and sorted ascending first, so
// the same SET of members always produces the exact same URL regardless
// of the order they were checked/selected in (bookmarking/sharing a
// link is only meaningful if two people picking the same team land on
// the identical URL; a selection-order-dependent hash would also
// reshuffle every time a member is toggled off and back on in a
// different order). Each name is individually URL-encoded before
// joining with a literal comma.
export function buildCompareHash(names: string[]): string {
  const unique = [...new Set(names)].sort((a, b) => a.localeCompare(b));
  return `#/compare/${unique.map(encodeURIComponent).join(',')}`;
}

// parseCompareNames splits a compare route's raw ":names" param back into
// individual names: trims whitespace, drops empty entries (a leading/
// trailing/doubled comma degrades gracefully, same convention as the
// server's own splitCSV), and deduplicates while preserving first-seen
// order -- the order the member chips/charts render in, for a hand-typed
// URL that didn't go through buildCompareHash's own canonical sort.
// undefined/empty input -> [].
export function parseCompareNames(raw: string | undefined): string[] {
  if (!raw) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(',')) {
    const name = part.trim();
    if (name === '' || seen.has(name)) continue;
    seen.add(name);
    out.push(name);
  }
  return out;
}

// knownCompareNames narrows `names` down to the ones `containers` (the
// live frame's own SnapshotDTO.Containers) actually knows about --
// members removed from the fleet since a compare URL was bookmarked
// should quietly drop out of the header/charts/totals, not render as a
// phantom row with every value defaulting to 0 (which would read as "this
// container is truly idle" rather than "this container is gone").
export function knownCompareNames(names: string[], containers: Record<string, ContainerDTO>): string[] {
  return names.filter((name) => name in containers);
}

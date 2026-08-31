// eventHref: "recent events should be clickable and take you to the
// thing that's going on" (Scott). Maps one event's kind (prefix-matched,
// so a future kind under an already-known family -- "container.restart",
// say -- routes correctly without this needing an update) to a route:
// container.*/docker.* names a container, so it needs the entity itself
// to link anywhere; disk.*/array.*/parity.*/mover.* all land on the one
// Storage page (no per-slot deep link -- Storage isn't addressable below
// the page level today); alert.* (Phase 4: alert.fired/alert.resolved,
// internal/alert/engine.go's fire/resolveNotify calls) lands on the
// Alerts view -- its own dot-namespaced prefix ("alert"), distinct from
// every kind above even though the underlying rule may itself be about
// a container or a disk; insight.* (Phase 5: insight.detected/
// insight.resolved, insight/engine.go's upsertFinding/resolve) lands on
// the Insights view, the same "its own dot-namespaced prefix" treatment
// alert.* gets -- image.* is a plain row for now (the images view
// doesn't exist yet); anything else unrecognized is a plain row too.
//
// This same function also backs the Insights view/ImpactPanel's own
// victim/culprit links (Task 11/12), called there with the BARE
// victim_kind word ("container"|"host"|"array"|"disk"|"gpu",
// insight_instances.victim_kind) rather than a dot-namespaced event
// kind -- kind.split('.')[0] on a dot-free string is just the string
// itself, so "container"/"disk"/"array" already route correctly with
// no change needed here; "host"/"gpu" fall through to null (no
// per-entity page for either), same as they would for a container-
// detail-less event kind today.
const CONTAINER_KIND_PREFIXES: ReadonlySet<string> = new Set(['container', 'docker']);
const STORAGE_KIND_PREFIXES: ReadonlySet<string> = new Set(['disk', 'array', 'parity', 'mover']);

export function eventHref(kind: string, entity: string): string | null {
  const prefix = kind.split('.')[0];
  if (prefix === 'alert') {
    return '#/alerts';
  }
  if (prefix === 'insight') {
    return '#/insights';
  }
  if (CONTAINER_KIND_PREFIXES.has(prefix)) {
    return entity ? `#/containers/${encodeURIComponent(entity)}` : null;
  }
  if (STORAGE_KIND_PREFIXES.has(prefix)) {
    return '#/storage';
  }
  return null;
}

// eventHref: "recent events should be clickable and take you to the
// thing that's going on" (Scott). Maps one event's kind (prefix-matched,
// so a future kind under an already-known family -- "container.restart",
// say -- routes correctly without this needing an update) to a route:
// container.*/docker.* names a container, so it needs the entity itself
// to link anywhere; disk.*/array.*/parity.*/mover.* all land on the one
// Storage page (no per-slot deep link -- Storage isn't addressable below
// the page level today); image.* is a plain row for now (the images view
// doesn't exist yet); anything else unrecognized is a plain row too.
const CONTAINER_KIND_PREFIXES: ReadonlySet<string> = new Set(['container', 'docker']);
const STORAGE_KIND_PREFIXES: ReadonlySet<string> = new Set(['disk', 'array', 'parity', 'mover']);

export function eventHref(kind: string, entity: string): string | null {
  const prefix = kind.split('.')[0];
  if (CONTAINER_KIND_PREFIXES.has(prefix)) {
    return entity ? `#/containers/${encodeURIComponent(entity)}` : null;
  }
  if (STORAGE_KIND_PREFIXES.has(prefix)) {
    return '#/storage';
  }
  return null;
}

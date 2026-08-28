// Pure display-shaping helpers for ContainerDetail's Storage section:
// how to read an untrusted wire "kind" string safely, and what order
// mount rows render in -- kept in their own module, matching disks.ts's
// one-file-per-view-concern convention, rather than folded into
// metrics.ts's more general helpers.

export type ContainerStorageKind = 'share' | 'pool' | 'disk' | 'flash' | 'other';

const STORAGE_KINDS: ReadonlySet<string> = new Set<ContainerStorageKind>(['share', 'pool', 'disk', 'flash', 'other']);

// normalizeStorageKind narrows an arbitrary string (straight off the
// wire, never typechecked at its source -- same "don't trust the
// network" convention disks.ts's diskMetaKind uses for a disk's kind)
// to ContainerStorageKind, falling back to "other" for anything a
// future server build might send that this one doesn't recognize yet,
// rather than rendering an unstyled/blank badge.
export function normalizeStorageKind(kind: string): ContainerStorageKind {
  return STORAGE_KINDS.has(kind) ? (kind as ContainerStorageKind) : 'other';
}

// KIND_RANK fixes the mount list's group order to StorageRefDTO's own
// documented kind order (share, pool, disk, flash, other).
const KIND_RANK: Record<ContainerStorageKind, number> = { share: 0, pool: 1, disk: 2, flash: 3, other: 4 };

export interface MountLike {
  destination: string;
  storage: { kind: string; name: string };
}

// sortMounts orders a container's mounts by storage system (kind, then
// name), then destination -- so every mount backed by the SAME system
// (e.g. two mounts both into "rocket_pool") lands next to each other and
// reads as one cluster, rather than scattered in whatever order docker
// happened to report them.
export function sortMounts<T extends MountLike>(mounts: T[]): T[] {
  return [...mounts].sort((a, b) => {
    const ra = KIND_RANK[normalizeStorageKind(a.storage.kind)];
    const rb = KIND_RANK[normalizeStorageKind(b.storage.kind)];
    if (ra !== rb) return ra - rb;
    if (a.storage.name !== b.storage.name) return a.storage.name.localeCompare(b.storage.name);
    return a.destination.localeCompare(b.destination);
  });
}

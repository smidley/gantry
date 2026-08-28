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

// FLASH_SLOT is Unraid's own fixed, version-independent slot name for
// the boot device (unraid.DiskKind's own doc, backend) -- a "flash"
// StorageRefDTO carries no Name (there's only ever one), so reading its
// capacity off the live frame's disk-keyed maps needs this literal.
const FLASH_SLOT = 'flash';

// mountCapacitySlot returns the live frame's disks[slot]/disk_meta[slot]
// key backing one mount's capacity, or null when that mount's kind has
// no single disk slot to show capacity for at all: a "share" spans
// however many disks a user share happens to land on -- Unraid tracks
// no true per-share usage (Storage.svelte's own shares-table caption
// says as much) -- and "other" names nothing resolvable in the first
// place. Returning null for either is deliberate (show nothing rather
// than a wrong or misleadingly-partial number), not a gap to fill in
// later.
export function mountCapacitySlot(mount: MountLike): string | null {
  const kind = normalizeStorageKind(mount.storage.kind);
  if (kind === 'flash') return FLASH_SLOT;
  if (kind === 'pool' || kind === 'disk') return mount.storage.name;
  return null;
}

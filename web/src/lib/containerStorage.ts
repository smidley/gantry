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

// RECENT_IO_WINDOW_MS is the Live IO device rows' own noise floor: a
// device stays listed for this long after its last nonzero read/write
// sample, then drops out once it's been genuinely idle the whole window
// -- long enough that an ordinary bursty container's device doesn't
// flicker in and out between polls, short enough that a device that
// truly never does anything (Unraid's own bzmodules kernel-image loop
// device is the case that prompted this -- containers touch it once via
// module autoload, then it sits at 0 forever) actually disappears
// instead of camping in the list at a permanent 0 B/s.
export const RECENT_IO_WINDOW_MS = 60_000;

export interface DeviceIOLike {
  device: string;
  read_bps: number;
  write_bps: number;
}

// recordDeviceActivity updates `lastActiveAt` (device -> the ms
// timestamp it was last seen with nonzero read/write) for every device
// that's active THIS poll -- the one place this map is written. Callers
// own the map (a plain, non-reactive one, not framework state -- there's
// nothing to render off it directly, only off recentlyActiveDevices'
// own read of it) and pass the same instance in on every poll so a
// device's own "last seen active" survives across ticks even once it
// goes idle.
export function recordDeviceActivity(devices: DeviceIOLike[], lastActiveAt: Map<string, number>, nowMs: number): void {
  for (const d of devices) {
    if (d.read_bps > 0 || d.write_bps > 0) lastActiveAt.set(d.device, nowMs);
  }
}

// recentlyActiveDevices filters `devices` down to the ones with an
// activity timestamp inside RECENT_IO_WINDOW_MS of `nowMs` -- a device
// recordDeviceActivity has never once recorded (never active since this
// panel opened) is correctly excluded, same as one that fell silent too
// long ago.
export function recentlyActiveDevices<T extends { device: string }>(
  devices: T[],
  lastActiveAt: ReadonlyMap<string, number>,
  nowMs: number,
): T[] {
  return devices.filter((d) => {
    const seenAt = lastActiveAt.get(d.device);
    return seenAt !== undefined && nowMs - seenAt <= RECENT_IO_WINDOW_MS;
  });
}

export interface SharePlacementLike {
  mode: string;
  pool?: string;
}

// sharePlacementText renders a share's own cache-pool placement as a
// short, muted sentence -- Scott's own report: "you can see that the
// downloads share is used, but you don't know that the drive it's
// stored on is the nvme cache drive... we need to connect the dots."
// poolKind (additive, optional -- diskKind's own output for the named
// pool, looked up by the caller off the live frame's disk_meta) folds
// straight into "only"/"prefer"'s own wording when known; omitted (the
// pool isn't a currently-present disk/pool slot, or unraid's disk_meta
// simply hasn't reported on it yet) reads the pool name alone, still
// useful on its own. null for a mode this function doesn't recognize
// (shares.ini's own dialect --see unraid.SharePlacement's own doc for
// the fixed "yes"|"no"|"only"|"prefer" vocabulary -- so this should
// only ever happen against a future value this build predates), or for
// "only"/"prefer" with no pool name at all (malformed input; shares.ini
// always pairs a cache mode with a pool in practice).
export function sharePlacementText(placement: SharePlacementLike | undefined, poolKind?: string | null): string | null {
  if (!placement) return null;
  const poolLabel = (pool: string) => `${pool}${poolKind ? ` (${poolKind})` : ''}`;
  switch (placement.mode) {
    case 'only':
      return placement.pool ? `→ lives on ${poolLabel(placement.pool)}` : null;
    case 'prefer':
      return placement.pool ? `→ prefers ${poolLabel(placement.pool)}, spills to array` : null;
    case 'yes':
      return '→ cache then array';
    case 'no':
      return '→ array';
    default:
      return null;
  }
}

// isUnraidOSLoopDevice recognizes Unraid's own boot-image loop devices
// (bzimage/bzroot/bzmodules/bzfirmware -- ResolveDeviceLabel resolves a
// loop device's label to its backing file's basename, and these are the
// literal filenames Unraid boots from its flash drive) -- Scott's own
// report, landing on one of these once it actually showed live IO:
// "what is bzmodules?" with nothing in the row itself saying it's the OS
// rather than a stray/misconfigured mount.
export function isUnraidOSLoopDevice(label: string): boolean {
  return label.startsWith('bz');
}

export interface ShfsMountLike {
  storage: { kind: string; name: string; shfs?: boolean };
}

// shfsFrontedShares names the shares whose mounts reach their data
// through Unraid's shfs FUSE layer -- StorageRefDTO.shfs, set server-side
// for every /mnt/user0 path and every /mnt/user path whose share Unraid
// hasn't made EXCLUSIVE. That IO is issued by the host-wide shfs daemon
// rather than by the container, so it lands in no per-container counter
// on the box at all: not the cgroup's io.stat (a 1.5 GB write through
// such a mount moved it by 5 KB on a real 7.3.2 box), and not
// /proc/<pid>/io either (its block-layer read counter never saw the read
// back). Naming the shares is the honest thing the panel CAN do -- see
// shfsNote for the sentence it renders, and StorageRefDTO.Shfs' own
// backend doc for the full measurement.
//
// Deduped (several mounts often point into one share) and returned in
// first-seen order, which sortMounts has already made a stable one.
export function shfsFrontedShares(mounts: ShfsMountLike[]): string[] {
  const seen: string[] = [];
  for (const m of mounts) {
    if (m.storage.shfs && !seen.includes(m.storage.name)) seen.push(m.storage.name);
  }
  return seen;
}

// shfsNote renders shfsFrontedShares' own output as the one muted line
// the Live IO section shows when a container has such a mount -- so an
// empty (or array-free) device list reads as "this IO can't be measured"
// rather than "this container isn't touching the array". null when
// there's nothing to explain, in which case no line renders at all.
export function shfsNote(shares: string[]): string | null {
  if (shares.length === 0) return null;
  const subject =
    shares.length === 1 ? shares[0] : `${shares.slice(0, -1).join(', ')} and ${shares[shares.length - 1]}`;
  const verb = shares.length === 1 ? 'goes' : 'go';
  return `${subject} ${verb} through Unraid's shfs layer, which does that disk IO on the container's behalf. None of it can be counted here or in the IO chart.`;
}

// Pure disk-entity display-shaping helpers for the Storage view: which
// order disk cards render in, what "role" group a slot's name implies,
// and how to read its temp/spin-state -- kept in their own module
// (matching containersSort.ts/topFromFrame.ts's one-file-per-view-concern
// convention) rather than folded into metrics.ts's more general,
// disk-agnostic helpers.
import type { SeriesPoint } from './api';

export type DiskRole = 'parity' | 'data' | 'pool' | 'flash';

// diskRole classifies a disk ENTITY NAME into one of Storage's four grid
// groups. There's no dedicated "is this a pool" field anywhere this
// collector reads (see ArrayCard's own doc on the same gap) -- a slot is
// "pool" by ELIMINATION: not parity, not a numbered disk*, not the
// literal boot "flash" device. That correctly buckets both the literal
// "cache" slot and a real named pool (e.g. "rocket_pool", per
// fixtures.md) the same way, with no hardcoded name list to keep in
// sync with a real array's actual pool names.
export function diskRole(name: string): DiskRole {
  if (name.startsWith('parity')) return 'parity'; // "parity", "parity2" (dual parity)
  if (/^disk\d+$/.test(name)) return 'data';
  if (name === 'flash') return 'flash';
  return 'pool';
}

const ROLE_RANK: Record<DiskRole, number> = { parity: 0, data: 1, pool: 2, flash: 3 };

// naturalSuffix extracts a name's trailing digit run, for a numeric-
// aware sort ("disk2" before "disk10", "parity" before "parity2") --
// null for a name with no trailing digits (every pool name, "flash"),
// which falls back to plain alpha ordering instead.
function naturalSuffix(name: string): number | null {
  const m = /(\d+)$/.exec(name);
  return m ? parseInt(m[1], 10) : null;
}

// sortDiskEntities orders disk entity names into the Storage grid's
// fixed group order (parity* first, then disk* by number, then cache/
// pools, then the boot flash device last), breaking every tie by name
// ascending for deterministic, reproducible output.
export function sortDiskEntities(names: string[]): string[] {
  return [...names].sort((a, b) => {
    const ra = ROLE_RANK[diskRole(a)];
    const rb = ROLE_RANK[diskRole(b)];
    if (ra !== rb) return ra - rb;
    const na = naturalSuffix(a);
    const nb = naturalSuffix(b);
    if (na !== null && nb !== null && na !== nb) return na - nb;
    return a.localeCompare(b);
  });
}

export type DiskMediaType = 'hdd' | 'ssd';

// diskMediaType reads a disk's rotational value (disks.go's own "0/1 per
// present disk from disks.ini" contract) into the two-way distinction
// Storage/Overview render a glyph for: 0 is solid-state, anything else
// (1, the only other real-world value, but any nonzero reading is
// treated the same) is spinning. null -- absent, never guessed -- covers
// a not-present disk (never rendered here anyway) and a stale/pre-
// upgrade frame that hasn't started sending this metric yet.
//
// Superseded by diskKind below as Storage/Overview's own primary read
// (rotational alone can't tell a USB flash stick or an NVMe pool member
// apart from an ordinary spinning/SATA-SSD disk -- Scott's own report),
// but kept, tested, and exported as-is: diskKind falls back to it
// whenever a frame's disk_meta doesn't (yet) cover a slot.
export function diskMediaType(metrics: Record<string, number> | undefined | null): DiskMediaType | null {
  const rotational = metrics?.['rotational'];
  if (rotational === undefined) return null;
  return rotational === 0 ? 'ssd' : 'hdd';
}

export type DiskKind = 'hdd' | 'ssd' | 'nvme' | 'usb';

const DISK_KINDS: ReadonlySet<string> = new Set<DiskKind>(['hdd', 'ssd', 'nvme', 'usb']);

// diskMetaKind narrows an arbitrary string (straight off the wire, never
// typechecked at its source) to DiskKind -- same "don't trust the network"
// convention topFromFrame.ts's isTopResource already uses for a route
// param. Exported: containerStorage's own device rows reuse this exact
// narrowing for DeviceIODTO's Kind (unraid.ResolveDeviceLabel's own
// hdd/ssd/nvme/usb vocabulary, straight off the same disk_meta-derived
// source) rather than re-declaring the DISK_KINDS set a second time.
export function diskMetaKind(value: string | undefined): DiskKind | null {
  return value !== undefined && DISK_KINDS.has(value) ? (value as DiskKind) : null;
}

// diskKind is Storage/Overview's own primary type read: the server now
// classifies every present disk (unraid.DiskKind, disks.go) into one of
// four kinds -- rotational alone can conflate a USB flash stick with an
// ordinary spinning disk, and an NVMe pool member with a plain SATA SSD,
// which is exactly the bug report this exists to fix. meta is that
// slot's own disk_meta entry (SnapshotDTO.DiskMeta, keyed by slot);
// falling back to the older rotational-only diskMediaType (never hidden
// outright) covers a frame from before this feature shipped, or a slot
// disk_meta hasn't (yet) reported on this tick.
export function diskKind(
  meta: { kind?: string } | undefined | null,
  metrics: Record<string, number> | undefined | null,
): DiskKind | null {
  return diskMetaKind(meta?.kind) ?? diskMediaType(metrics);
}

export type DiskTempState = { kind: 'reading'; celsius: number } | { kind: 'spun-down' } | { kind: 'no-sensor' };

// diskTempState reads one disk's temp/spin-state metrics. Absence of the
// "temp.c" key is itself the signal a real spun-down disk gives (Unraid
// never reports a temp for a sleeping drive -- disks.go's own doc) --
// this checks KEY PRESENCE, not mere falsiness, since 0C is a
// theoretically valid reading that must never be confused with "no
// sample at all". spun_up===0 explains the absence as an expected sleep
// state ("spun-down"); anything else -- spun_up===1, or spun_up itself
// absent -- reads as "no-sensor" instead: the real boot flash device is
// the documented case (reports spundown="0" but temp="*", i.e. no
// sensor at all, unrelated to spin state -- see fixtures.md discrepancy
// 11), and a disk with no spundown key at all degrades the same safe
// way rather than guessing.
export function diskTempState(metrics: Record<string, number> | undefined | null): DiskTempState {
  const temp = metrics?.['temp.c'];
  if (temp !== undefined) return { kind: 'reading', celsius: temp };
  return metrics?.['spun_up'] === 0 ? { kind: 'spun-down' } : { kind: 'no-sensor' };
}

// diskUsagePct computes a disk's fill percentage from its fs bytes pair,
// or null when either half is absent (e.g. parity, which has no
// filesystem view at all -- disks.go's own doc).
export function diskUsagePct(metrics: Record<string, number> | undefined | null): number | null {
  const used = metrics?.['fs.used_bytes'];
  const free = metrics?.['fs.free_bytes'];
  if (used === undefined || free === undefined) return null;
  const total = used + free;
  return total > 0 ? (used / total) * 100 : 0;
}

// diskUsagePctSeries is diskUsagePct's history-shaped sibling, for the
// Storage chart's "Used" line: fs.used_bytes and fs.free_bytes are two
// separate /api/series results (there's no server-side usage-percent
// series), zipped point-by-point by exact ts -- both are recorded on
// the same collector tick, so a ts missing from one side (a store gap)
// is skipped rather than guessed at.
export function diskUsagePctSeries(usedPoints: SeriesPoint[], freePoints: SeriesPoint[]): [number, number][] {
  const freeByTs = new Map(freePoints.map(([ts, avg]) => [ts, avg]));
  const out: [number, number][] = [];
  for (const [ts, used] of usedPoints) {
    const free = freeByTs.get(ts);
    if (free === undefined) continue;
    const total = used + free;
    out.push([ts, total > 0 ? (used / total) * 100 : 0]);
  }
  return out;
}

// defaultDiskChartVisible decides the storage chart's own starting
// legend state ("keep 12+ lines calm"): pools and parity default
// visible regardless of activity (the array's own backbone -- and
// parity carries no usage/IO of its own to judge activity by anyway),
// an ordinary data/flash disk only if it's actually doing something
// right now; everything else starts toggled off.
export function defaultDiskChartVisible(slot: string, hasRecentIO: boolean): boolean {
  const role = diskRole(slot);
  return role === 'pool' || role === 'parity' || hasRecentIO;
}

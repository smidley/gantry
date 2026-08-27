// Pure disk-entity display-shaping helpers for the Storage view: which
// order disk cards render in, what "role" group a slot's name implies,
// and how to read its temp/spin-state -- kept in their own module
// (matching containersSort.ts/topFromFrame.ts's one-file-per-view-concern
// convention) rather than folded into metrics.ts's more general,
// disk-agnostic helpers.

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

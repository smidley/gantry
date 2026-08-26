// Pure sort logic for the Containers table. SORT STABILITY is the whole
// point of splitting this out: the table must re-sort on a header click
// or when a container is added/removed, and MUST NOT re-sort just
// because a value ticked on a live frame (rows visibly jumping around
// every 2s would make the table unreadable). This module only computes
// an ORDER given a snapshot of names+values -- deciding WHEN to call it
// (header click / name-set change, not every frame) is the calling
// component's job, via an effect that depends on the sort column/
// direction and the container name SET, not on the live frame itself.
// See Containers.svelte's own comment at its sorting $effect for that
// half of the contract.
import type { ContainerDTO } from './api';

export type ContainerSortColumn = 'health' | 'name' | 'cpu' | 'mem' | 'net' | 'io' | 'gpu' | 'pids' | 'uptime' | 'image';
export type SortDir = 'asc' | 'desc';

import { containerHealthStatus } from './containerStatus';

// HEALTH_RANK orders HealthStatus from least to most severe, for sorting
// the health column -- "desc" then means "most severe first," matching
// how every numeric column's "desc" means "biggest first."
const HEALTH_RANK: Record<string, number> = { good: 0, warning: 1, serious: 2, critical: 3 };

// sortKey extracts one column's comparable value for one container.
// Every numeric column defaults a missing metric to 0 (consistent with
// format.ts's own "missing degrades to a harmless placeholder" rule) --
// a container with no GPU activity sorts as 0%, not undefined/NaN, which
// would otherwise scatter unpredictably through a numeric sort.
function sortKey(name: string, c: ContainerDTO, column: ContainerSortColumn, nowTs: number): number | string {
  const m = c.metrics;
  switch (column) {
    case 'health':
      return HEALTH_RANK[containerHealthStatus(c.state, c.health)] ?? 0;
    case 'name':
      return name;
    case 'cpu':
      return m['cpu.pct'] ?? 0;
    case 'mem':
      return m['mem.bytes'] ?? 0;
    case 'net':
      return (m['net.rx_bps'] ?? 0) + (m['net.tx_bps'] ?? 0);
    case 'io':
      return (m['io.read_bps'] ?? 0) + (m['io.write_bps'] ?? 0);
    case 'gpu':
      return (m['gpu.video.busy_pct'] ?? 0) + (m['gpu.render.busy_pct'] ?? 0);
    case 'pids':
      return m['pids'] ?? 0;
    case 'uptime':
      return m['meta.started_at'] !== undefined ? nowTs - m['meta.started_at'] : 0;
    case 'image':
      return c.image ?? '';
  }
}

// sortContainerNames returns `names` sorted by `column`/`dir`, breaking
// every tie by name ascending for deterministic, reproducible output. A
// name absent from `containers` (defensive -- shouldn't happen in
// practice, since names always come from Object.keys of the same map)
// sorts as if every metric were 0/empty.
export function sortContainerNames(
  names: string[],
  containers: Record<string, ContainerDTO>,
  column: ContainerSortColumn,
  dir: SortDir,
  nowTs: number,
): string[] {
  const empty: ContainerDTO = { state: '', health: '', image: '', metrics: {} };
  return [...names].sort((a, b) => {
    const ka = sortKey(a, containers[a] ?? empty, column, nowTs);
    const kb = sortKey(b, containers[b] ?? empty, column, nowTs);
    let primary = typeof ka === 'string' || typeof kb === 'string' ? String(ka).localeCompare(String(kb)) : ka - kb;
    if (dir === 'desc') primary = -primary;
    // Tie-break always breaks by name ascending, independent of dir --
    // a stable, predictable secondary order rather than one that flips
    // whenever the user toggles the primary column's direction.
    return primary !== 0 ? primary : a.localeCompare(b);
  });
}

// matchesContainerFilter is the Containers table's filter-box predicate:
// a case-insensitive substring match against name OR image. An
// empty/whitespace-only filter matches everything.
export function matchesContainerFilter(name: string, image: string, filter: string): boolean {
  const needle = filter.trim().toLowerCase();
  if (!needle) return true;
  return name.toLowerCase().includes(needle) || (image ?? '').toLowerCase().includes(needle);
}

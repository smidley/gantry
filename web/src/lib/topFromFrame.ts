// Client-side "top consumers" derivation for the live frame -- Overview's
// Top Consumers module and Top Consumers view's "Now" window both need
// the exact same leaderboard the backend computes for window=now
// (server's topFromSnapshot, api_history.go), just without a network
// round-trip: the live SSE frame already has everything needed.
import type { ContainerDTO, SnapshotDTO, TopResource } from './api';

export interface TopFrameRow {
  entity: string;
  value: number;
  // secondary (additive, optional): a quiet annotation alongside value --
  // today only cpu's cpu.cores, read straight off the same frame (see
  // resourceSecondaryMetricKey). Absent whenever the resource has none,
  // or the container has no sample for it yet.
  secondary?: number;
}

// TOP_RESOURCES is the one fixed list/order of "top consumers" resources --
// shared by the full Top Consumers view's own tabs (label) and Overview's
// compact module-header switcher (shortLabel, the CPU/Mem/Net/IO/GPU
// abbreviations Scott's own ask spelled out), so both surfaces stay in
// lockstep with resourceMetricKeys below and never drift into two
// independently-maintained resource lists.
export const TOP_RESOURCES: { key: TopResource; label: string; shortLabel: string }[] = [
  { key: 'cpu', label: 'CPU', shortLabel: 'CPU' },
  { key: 'mem', label: 'Memory', shortLabel: 'Mem' },
  { key: 'net', label: 'Network', shortLabel: 'Net' },
  { key: 'io', label: 'Disk IO', shortLabel: 'IO' },
  { key: 'gpu', label: 'GPU', shortLabel: 'GPU' },
];

// isTopResource narrows an arbitrary string (a route param, a localStorage
// read -- neither typechecked at their source) to TopResource, so a
// malformed deep link or a stale/hand-edited storage value falls back to
// the caller's own default instead of silently propagating an invalid
// resource key into resourceMetricKeys' switch.
export function isTopResource(value: string | null | undefined): value is TopResource {
  return TOP_RESOURCES.some((r) => r.key === value);
}

// resourceMetricKeys mirrors the server's resourceMetrics (api_history.go)
// exactly -- the metric name(s) /api/top sums per entity for each
// resource. gpu deliberately excludes gpu.nvidia.mem_mib (VRAM, not a
// busy percentage) from the engine-busy sum, same exclusion and reason as
// the backend's own comment there.
export function resourceMetricKeys(resource: TopResource): string[] {
  switch (resource) {
    case 'cpu':
      return ['cpu.pct'];
    case 'mem':
      return ['mem.bytes'];
    case 'net':
      return ['net.rx_bps', 'net.tx_bps'];
    case 'io':
      return ['io.read_bps', 'io.write_bps'];
    case 'gpu':
      return ['gpu.render.busy_pct', 'gpu.video.busy_pct', 'gpu.video-enhance.busy_pct', 'gpu.copy.busy_pct'];
  }
}

// resourceSecondaryMetricKey names the one extra per-entity metric worth
// annotating a row with, alongside resourceMetricKeys' own primary sum --
// today only cpu, whose host-share primary value (cpu.pct) leaves the
// underlying core count implicit; see the collector's cgroupv2.go
// cpu.cores doc for why that number is worth surfacing on its own.
export function resourceSecondaryMetricKey(resource: TopResource): string | undefined {
  return resource === 'cpu' ? 'cpu.cores' : undefined;
}

// sumPresentMetrics sums metricKeys' values on one container, reporting
// whether ANY of them was actually present -- a container with none of
// the resource's metrics (e.g. no GPU activity at all) must be excluded
// entirely, not included tied at the bottom with a false 0.
function sumPresentMetrics(c: ContainerDTO, metricKeys: string[]): { sum: number; present: boolean } {
  let sum = 0;
  let present = false;
  for (const metric of metricKeys) {
    const v = c.metrics[metric];
    if (v !== undefined) {
      sum += v;
      present = true;
    }
  }
  return { sum, present };
}

// resourceScaleMax names the denominator a leaderboard bar's width should
// read against, so a lone quiet container doesn't render a nearly-full
// bar just because it happens to be the busiest thing running right now.
// cpu (host-share, already 0-100) and gpu (a busy_pct sum, also read on
// a 0-100 scale -- see resourceMetricKeys) are absolute: 100 always means
// "the whole machine." mem's rows are mem.bytes, not a percentage, so its
// 100-equivalent is the host's own total memory, backed into from the
// host tile's mem.used_bytes/mem.used_pct pair (frame.host carries no
// total directly) -- undefined (no scale yet, or a stat this old
// snapshot never carried) falls the caller back to the previous
// dynamic-max-of-rows behavior, same as net/io get unconditionally.
// net/io are byte rates with no natural ceiling -- deliberately left
// relative-to-max, not absolute; see this fix's own commit for why.
export function resourceScaleMax(resource: TopResource, frame: SnapshotDTO | null | undefined): number | undefined {
  switch (resource) {
    case 'cpu':
    case 'gpu':
      return 100;
    case 'mem': {
      const usedBytes = frame?.host?.['mem.used_bytes'];
      const usedPct = frame?.host?.['mem.used_pct'];
      if (usedBytes === undefined || !usedPct) return undefined;
      return (usedBytes * 100) / usedPct;
    }
    default:
      return undefined;
  }
}

// topFromFrame mirrors the server's topFromSnapshot (api_history.go):
// sums resourceMetricKeys(resource)'s metrics per container from one live
// frame, ranks descending by value (ties broken by entity name ascending,
// for deterministic output -- same tie-break the backend uses), and cuts
// to limit.
export function topFromFrame(frame: SnapshotDTO | null | undefined, resource: TopResource, limit = 10): TopFrameRow[] {
  const metricKeys = resourceMetricKeys(resource);
  const secondaryKey = resourceSecondaryMetricKey(resource);
  const rows: TopFrameRow[] = [];
  for (const [entity, c] of Object.entries(frame?.containers ?? {})) {
    const { sum, present } = sumPresentMetrics(c, metricKeys);
    if (!present) continue;
    const row: TopFrameRow = { entity, value: sum };
    const secondary = secondaryKey ? c.metrics[secondaryKey] : undefined;
    if (secondary !== undefined) row.secondary = secondary;
    rows.push(row);
  }
  rows.sort((a, b) => (b.value !== a.value ? b.value - a.value : a.entity.localeCompare(b.entity)));
  return rows.slice(0, limit);
}

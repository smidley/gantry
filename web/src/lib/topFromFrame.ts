// Client-side "top consumers" derivation for the live frame -- Overview's
// Top Consumers module and Top Consumers view's "Now" window both need
// the exact same leaderboard the backend computes for window=now
// (server's topFromSnapshot, api_history.go), just without a network
// round-trip: the live SSE frame already has everything needed.
import type { ContainerDTO, SnapshotDTO, TopResource } from './api';

export interface TopFrameRow {
  entity: string;
  value: number;
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

// topFromFrame mirrors the server's topFromSnapshot (api_history.go):
// sums resourceMetricKeys(resource)'s metrics per container from one live
// frame, ranks descending by value (ties broken by entity name ascending,
// for deterministic output -- same tie-break the backend uses), and cuts
// to limit.
export function topFromFrame(frame: SnapshotDTO | null | undefined, resource: TopResource, limit = 10): TopFrameRow[] {
  const metricKeys = resourceMetricKeys(resource);
  const rows: TopFrameRow[] = [];
  for (const [entity, c] of Object.entries(frame?.containers ?? {})) {
    const { sum, present } = sumPresentMetrics(c, metricKeys);
    if (present) rows.push({ entity, value: sum });
  }
  rows.sort((a, b) => (b.value !== a.value ? b.value - a.value : a.entity.localeCompare(b.entity)));
  return rows.slice(0, limit);
}

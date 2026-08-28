// Client-side "top consumers" derivation for the live frame -- Overview's
// Top Consumers module and Top Consumers view's "Now" window both need
// the exact same leaderboard the backend computes for window=now
// (server's topFromSnapshot, api_history.go), just without a network
// round-trip: the live SSE frame already has everything needed.
import type { ContainerDTO, SeriesPoint, SnapshotDTO, TopResource } from './api';
import { sumMetricsByPattern } from './metrics';

export interface TopFrameRow {
  entity: string;
  value: number;
  // secondary (additive, optional): a quiet annotation alongside value --
  // today only cpu's cpu.cores, read straight off the same frame (see
  // resourceSecondaryMetricKey). Absent whenever the resource has none,
  // or the container has no sample for it yet.
  secondary?: number;
  // direction (additive, optional -- Top Consumers view's attribution
  // page): [down/read, up/write] raw values for a resource with a
  // natural pair (see resourceDirectionKeys), only ever attached when the
  // caller opts in via topFromFrame's own `direction` option -- Overview's
  // compact module never passes it, so its rows are unaffected.
  direction?: [number, number];
  // linkable (additive, optional, default true): false for a summary row
  // that isn't a real container (the attribution page's own "unattributed"
  // row) -- TopBarRow renders it as plain text, no container link/icon.
  linkable?: boolean;
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

// resourceDirectionKeys names the two metric keys behind a directional
// resource's own down/up (net) or read/write (io) pair -- the same two
// keys resourceMetricKeys sums together, kept apart here for a row that
// shows both instead of just their sum. undefined for a resource with no
// natural direction (cpu/mem/gpu).
export function resourceDirectionKeys(resource: TopResource): [string, string] | undefined {
  switch (resource) {
    case 'net':
      return ['net.rx_bps', 'net.tx_bps'];
    case 'io':
      return ['io.read_bps', 'io.write_bps'];
    default:
      return undefined;
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
// to limit. opts.direction (additive, optional, default false) also
// attaches each row's own [down/read, up/write] pair when the resource
// has one (resourceDirectionKeys) AND the container reports at least one
// side of it -- only the Top Consumers view's own attribution page opts
// in; Overview's compact module leaves this off, so its rows are
// byte-for-byte what they were before.
export function topFromFrame(
  frame: SnapshotDTO | null | undefined,
  resource: TopResource,
  limit = 10,
  opts: { direction?: boolean } = {},
): TopFrameRow[] {
  const metricKeys = resourceMetricKeys(resource);
  const secondaryKey = resourceSecondaryMetricKey(resource);
  const dirKeys = opts.direction ? resourceDirectionKeys(resource) : undefined;
  const rows: TopFrameRow[] = [];
  for (const [entity, c] of Object.entries(frame?.containers ?? {})) {
    const { sum, present } = sumPresentMetrics(c, metricKeys);
    if (!present) continue;
    const row: TopFrameRow = { entity, value: sum };
    const secondary = secondaryKey ? c.metrics[secondaryKey] : undefined;
    if (secondary !== undefined) row.secondary = secondary;
    if (dirKeys) {
      const d0 = c.metrics[dirKeys[0]];
      const d1 = c.metrics[dirKeys[1]];
      if (d0 !== undefined || d1 !== undefined) row.direction = [d0 ?? 0, d1 ?? 0];
    }
    rows.push(row);
  }
  rows.sort((a, b) => (b.value !== a.value ? b.value - a.value : a.entity.localeCompare(b.entity)));
  return rows.slice(0, limit);
}

// --- Attribution page: host totals + unattributed row -------------------
//
// The Top Consumers view's own breakdown page needs a whole-machine
// figure per resource to headline with and to subtract the leaderboard's
// own containers from -- neither of which /api/top provides (it's
// per-container only). cpu/mem/net/io each have one honest whole-machine
// number; gpu deliberately doesn't (a busy_pct is inherently per-engine/
// per-device -- summing engines isn't a percentage of anything, and
// there's no single-device assumption safe to make), so callers simply
// never ask hostTotalNow/reduceSeriesPoints for it.

export interface HostTotal {
  value: number;
  direction?: [number, number];
}

// hostTotalNow reads resource's current whole-machine total straight off
// the live frame -- the header's own value, in the SAME units as the
// leaderboard's per-container values (mem is bytes, not used_pct, for
// exactly that reason: unattributedValue below only makes sense when
// both sides of the subtraction agree on units).
export function hostTotalNow(frame: SnapshotDTO | null | undefined, resource: TopResource): HostTotal | undefined {
  const host = frame?.host;
  if (!host) return undefined;
  switch (resource) {
    case 'cpu':
      return host['cpu.total'] === undefined ? undefined : { value: host['cpu.total'] };
    case 'mem':
      return host['mem.used_bytes'] === undefined ? undefined : { value: host['mem.used_bytes'] };
    case 'net': {
      const rx = sumMetricsByPattern(host, 'net', '.rx_bps');
      const tx = sumMetricsByPattern(host, 'net', '.tx_bps');
      return { value: rx + tx, direction: [rx, tx] };
    }
    case 'io': {
      const read = sumMetricsByPattern(host, 'diskio', '.read_bps');
      const write = sumMetricsByPattern(host, 'diskio', '.write_bps');
      return { value: read + write, direction: [read, write] };
    }
    default:
      return undefined; // gpu -- see module doc above
  }
}

// HOST_SERIES_METRICS names the host-kind metric(s) hostTotalFromSeries
// needs fetched for a resource's non-"now" window -- cpu/mem are one
// fixed key; net/io have no fixed key at all (real host.go emits one key
// per device/interface, discovered at call time via keysByPattern, same
// as Overview's own rail-tile seeding) so those two are absent here and
// handled by the caller instead.
export function hostSeriesMetricKeys(resource: TopResource): string[] | undefined {
  switch (resource) {
    case 'cpu':
      return ['cpu.total'];
    case 'mem':
      return ['mem.used_bytes'];
    default:
      return undefined; // net/io: dynamic per-device keys; gpu: no host total at all
  }
}

// reduceSeriesPoints collapses one metric's fetched [ts,avg,max] points to
// a single scalar for a window's own agg toggle -- avg averages the avg
// column, peak takes the max of the max column, mirroring topFromStore's
// own avg/peak semantics against the same store data (just computed here
// client-side, since there's no per-entity /api/top equivalent for a
// whole-host total). undefined for an empty series (no host history for
// that window) rather than a fabricated zero.
export function reduceSeriesPoints(points: SeriesPoint[], agg: 'avg' | 'peak'): number | undefined {
  if (points.length === 0) return undefined;
  if (agg === 'peak') return Math.max(...points.map((p) => p[2]));
  return points.reduce((sum, p) => sum + p[1], 0) / points.length;
}

// reorderByLastDisplayedValue re-sorts `rows` by each entity's own value
// from the PREVIOUS call (falling back to its current value the first
// time an entity appears), not the brand-new value just computed for
// THIS tick -- rank should track what's actually ON SCREEN, and
// TopBarRow's own Tween is still gliding the display from last tick's
// value toward this one for the whole ~glideMs leg (sse.svelte.ts's
// cadence-driven glide), so ranking by the fresh target instead of the
// value still settling toward it let a row hop ahead of another whose
// number, on screen, was still visibly larger (reproduced live: a
// leaderboard flashing "4.2 KB/s" ranked above "5.9 KB/s"). In the
// common case (glideMs already elapsed since the prior tick) the old
// glide has fully settled, so "last tick's raw value" already equals
// what's on screen -- this is a close, cheap stand-in for re-deriving
// the exact in-flight eased value, without needing TopBarRow's own
// Tween state to escape that component.
//
// A row with linkable===false (the attribution page's own pinned
// "unattributed" summary) is never reordered -- it stays wherever the
// caller put it (trailing every real row), so it can't get shuffled
// into the middle of the leaderboard just because its own value happens
// to rank high.
//
// `previous` is the caller's own persistent Map (keyed by
// "metricKey::entity", so a resource switch can't read a stale value
// left by a different metric), mutated in place across ticks -- see
// TopBarList.svelte's own call site.
export function reorderByLastDisplayedValue<T extends { entity: string; value: number; linkable?: boolean }>(
  rows: T[],
  previous: Map<string, number>,
  metricKey: string,
): T[] {
  const pinned = rows.filter((r) => r.linkable === false);
  const ranked = rows.filter((r) => r.linkable !== false);

  const sorted = [...ranked].sort((a, b) => {
    const av = previous.get(`${metricKey}::${a.entity}`) ?? a.value;
    const bv = previous.get(`${metricKey}::${b.entity}`) ?? b.value;
    return bv !== av ? bv - av : a.entity.localeCompare(b.entity);
  });

  for (const row of ranked) previous.set(`${metricKey}::${row.entity}`, row.value);

  return [...sorted, ...pinned];
}

// unattributedValue is the attribution page's own honest-labeling math:
// whatever the host is doing that no tracked container accounts for
// (kernel, other host processes, SMB/mover/parity IO, ...), clamped at
// zero rather than going negative on a momentary reading where the
// container sum overshoots a slightly-stale host total.
export function unattributedValue(hostValue: number, containersSum: number): number {
  return Math.max(0, hostValue - containersSum);
}

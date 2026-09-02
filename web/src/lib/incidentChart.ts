// incidentChart: the evidence drawer's own "graph of the incident"
// (owner's own ask: "insight history should also provide a graph of the
// incident if possible" -- the map answers WHO, this answers HOW BAD AND
// WHEN). Pure, DOM-free, vitest-tested -- the exact split insights.ts'
// own doc names as this app's standing convention.
//
// Entirely client-side, the same reasoning insights.ts' own
// selectOverlappingInsights/buildInsightGraph doc gives for the drawer's
// map: every metric this needs is already reachable through the
// EXISTING GET /api/series (kind/entity/metric, already tier-selecting
// and already degrading a pruned or never-recorded window to an empty
// points array rather than an error -- internal/store/query.go's own
// QuerySeries doc) -- no server endpoint of this feature's own.
import type { SeriesResult } from './api';
import { culpritNames, type InsightCulpritLike } from './insights';
import type { ChartMarker } from './eventMarkers';

// --- window + padding ------------------------------------------------------

// MIN_PAD_SEC/MAX_PAD_SEC: "roughly one window-length of padding, floored
// at ~15 minutes each side, capped sensibly" (the owner's own brief). The
// floor keeps a brief, seconds-long spike from rendering as a single
// illegible sliver with no surrounding context; the 2-hour ceiling stops
// a multi-hour incident from demanding a multi-hour pad on EACH side (a
// 3-hour incident would otherwise ask for a 9-hour-wide chart) that would
// both shove the incident itself down to a thin sliver of the x-axis
// again -- just the opposite failure mode -- and push an otherwise
// 1-minute-tier window into the coarser 10-minute tier for no real
// benefit (internal/store/query.go's own tierTable, keyed off the
// requested span).
export const MIN_PAD_SEC = 15 * 60;
export const MAX_PAD_SEC = 2 * 60 * 60;

// IncidentWindowLike is the minimal shape incidentChartWindow/
// incidentMarkers/incidentBand all need -- deliberately narrower than
// InsightDTO, the exact OverlapWindowLike/InsightCulpritLike precedent
// (insights.ts), so a hand-built test fixture never has to carry every
// InsightDTO field just to exercise this window math.
export interface IncidentWindowLike {
  state: string;
  started_at: number;
  resolved_at: number;
}

// incidentChartWindow returns the padded [from, to] to request from GET
// /api/series -- TimeChart's own xDomain prop, unit-for-unit (both are
// [number, number] unix-second tuples), so the caller hands this straight
// through with no reshaping.
//
// The trailing pad is deliberately OMITTED for a still-ACTIVE insight:
// "padded on each side" describes an incident that HAS a far side to pad
// -- an active one doesn't yet, and padding past nowSec would only widen
// the chart with a dead, dataless gap rather than genuine context. to is
// exactly nowSec for that case; the leading pad still applies either way,
// since "what led up to this" is always real history regardless of
// whether the incident has resolved yet.
export function incidentChartWindow(inst: IncidentWindowLike, nowSec: number): [number, number] {
  const isActive = inst.state === 'active';
  const end = isActive ? nowSec : inst.resolved_at;
  const duration = Math.max(0, end - inst.started_at);
  const pad = Math.min(Math.max(duration, MIN_PAD_SEC), MAX_PAD_SEC);
  return [inst.started_at - pad, isActive ? end : end + pad];
}

// incidentBand is the UNPADDED [start, end] -- the incident's own active
// span, for TimeChart's own `band` prop (a shaded rect behind the plotted
// lines, see that component's own doc) -- distinct from
// incidentChartWindow's PADDED fetch range above; the band marks only the
// span the insight actually claims, never the context padding around it.
export function incidentBand(inst: IncidentWindowLike, nowSec: number): [number, number] {
  return [inst.started_at, inst.state === 'active' ? nowSec : inst.resolved_at];
}

// --- markers -----------------------------------------------------------

// IncidentMarkerLike is incidentMarkers' own minimal shape.
export interface IncidentMarkerLike {
  state: string;
  fired_at: number;
  resolved_at: number;
  resolve_reason: string;
}

// incidentMarkers marks fired (always) and resolved (only once resolved)
// -- the owner's own "mark fired and resolved at minimum." Both use
// severity 'info', matching eventMarkers.ts' own insight.detected
// precedent verbatim ("the quietest marker... an insight is a
// correlational claim, never as alarming as a fired alert by design,
// Global Constraints: insights never page") -- color is not how this
// chart tells fired apart from resolved, the label text is.
export function incidentMarkers(inst: IncidentMarkerLike): ChartMarker[] {
  const out: ChartMarker[] = [{ ts: inst.fired_at, severity: 'info', label: 'Fired' }];
  if (inst.state !== 'active' && inst.resolved_at > 0) {
    out.push({ ts: inst.resolved_at, severity: 'info', label: `Resolved (${inst.resolve_reason})` });
  }
  return out;
}

// --- pruned/missing-data fallback -------------------------------------

// hasChartableData reports whether ANY of a chart's own fetched series
// actually carries a point -- the honest gate between rendering the real
// chart and the quiet "no history for this window" line (owner's own
// "if possible" clause): GET /api/series never errors for a pruned or
// never-recorded window, it simply comes back with an empty points array
// (internal/store/query.go's own QuerySeries: an unknown series id is a
// plain `continue`, not a failure) -- so an empty result is the ONLY
// signal this ever has to read.
export function hasChartableData(results: SeriesResult[]): boolean {
  return results.some((r) => r.points.length > 0);
}

// --- rule -> series mapping ---------------------------------------------

// ChartFormatter names which format.ts function a chart's own numbers
// render through -- a plain string enum (not the function itself) so
// planIncidentCharts stays a pure, trivially-comparable-in-tests value
// rather than handing back closures; the drawer's own template maps this
// back to fmtPct/fmtRate/undefined.
export type ChartFormatter = 'pct' | 'rate' | 'plain';

// ChartPlanLine is one drawn line: metrics (plural) names every RAW
// /api/series metric this one line sums together (lib/metrics.ts' own
// sumSeriesByMetric) -- a single-element array for most lines (e.g.
// ["cpu.pct"]), two for an IO line that has no single already-aggregated
// key of its own (["io.read_bps","io.write_bps"], the docker collector's
// own per-container totals -- see planIncidentCharts' own doc for why
// this is the container's TOTAL disk IO rather than a per-device figure).
export interface ChartPlanLine {
  kind: string;
  entity: string;
  metrics: string[];
  label: string;
  colorVar: string;
}

export interface ChartPlan {
  key: string;
  title: string;
  formatter: ChartFormatter;
  lines: ChartPlanLine[];
}

// DiskMetaLike/PlanContext carry the two pieces of CURRENT, live-frame
// state planIncidentCharts needs that never lived on the stored insight
// row itself -- see the doc below for exactly which rules need which,
// and why an unresolvable one degrades to simply omitting that one line
// rather than guessing wrong or throwing.
export interface DiskMetaLike {
  device: string;
}
export interface PlanContext {
  // diskMeta: slot -> current raw device name (live.frame.disk_meta,
  // the EXACT join Storage.svelte's own seedDiskSlot already performs
  // for its own per-drive IO chart -- see that function's own "no
  // device join yet for this slot" doc for the identical degrade this
  // mirrors). A resource label that ISN'T a key here is used AS the
  // device name directly instead (resourceLabel's own RoleUnknown
  // fallback in insight/evidence.go: an unplaced device's own Resource
  // IS already its raw kernel name, e.g. "sdc", not a slot) -- either
  // way the worst case is an honestly-empty /api/series result, never a
  // wrong answer that LOOKS like data.
  diskMeta?: Record<string, DiskMetaLike>;
  // gpuEntities: every GPU entity id CURRENTLY reporting (live.frame.gpu's
  // own keys) -- gpu-engine-contention's stored row keeps only the bare
  // ENGINE name (e.g. "video"), never which physical GPU it fired on
  // (api_insights.go's own GraphEdgeDTO doc names this exact gap for the
  // map feature; it applies here identically). Resolvable only when
  // exactly one GPU is currently known -- the realistic common case on a
  // single-GPU Unraid box -- ambiguous (0 or 2+) degrades to omitting the
  // victim line rather than guessing which one.
  gpuEntities?: string[];
}

const VICTIM_COLOR = '--series-1';
const CULPRIT_COLORS = ['--series-2', '--series-3', '--series-4', '--series-5'];
// MAX_CULPRIT_LINES: a legend-sanity cap -- every rule but gpu-engine-
// contention already caps a shared culprit set at 3 (insight.Dominant's
// own maxN), and that rule's own "every container clearing its own share
// floor independently" loop (evalGPUEngineContention) has no such cap in
// principle; 4 lines is comfortably inside the 9-color budget
// (tokens.css' own --series-1..9) with room for the victim's own line
// too.
const MAX_CULPRIT_LINES = 4;

function culpritLines(inst: InsightCulpritLike, metrics: string[]): ChartPlanLine[] {
  return culpritNames(inst)
    .slice(0, MAX_CULPRIT_LINES)
    .map((name, i) => ({ kind: 'container', entity: name, metrics, label: name, colorVar: CULPRIT_COLORS[i % CULPRIT_COLORS.length] }));
}

// PlanInput is planIncidentCharts' own minimal shape -- every field of
// InsightDTO (culprit/culprits via InsightCulpritLike, plus these four)
// an evaluator below actually reads, the exact OverlapWindowLike/
// InsightCulpritLike precedent above.
export interface PlanInput extends InsightCulpritLike {
  rule_id: string;
  resource: string;
  victim_kind: string;
  victim: string;
}

// planIncidentCharts is the "small per-rule-id mapping (7 tier-1 rules)"
// the owner's own brief asks for, built by reading internal/insight's
// rules (rules.go) and the collectors that actually feed them
// (internal/collect/host, docker/cgroupv2.go, unraid/{disks,var}.go,
// gpu/collector.go) rather than guessed -- each rule's own comment below
// names the exact Go evaluator/collector line it mirrors.
//
// One chart per rule when the victim and culprit signal genuinely share
// a unit (both plain percentages, or both bytes/sec) -- TimeChart is
// single-y-axis by design (its own doc), so two DIFFERENT units on one
// canvas would silently mislead via a shared axis; two compact charts
// otherwise. A line that can't be resolved from ctx (an unknown device,
// an ambiguous GPU) is simply omitted -- see DiskMetaLike/PlanContext's
// own doc -- never a wrong guess and never a thrown error.
//
// culprit's own "driving metric" is deliberately the container's TOTAL
// disk IO (io.read_bps+io.write_bps, the docker collector's own per-
// container aggregate across every device -- cgroupv2.go) for every
// IO-flavoured rule below, not a device-scoped live:io.<dev>.* figure:
// the rule's own attribution math for io-driven-cpu-load already sums
// across every device (totalContainerIO, rules.go), so the total is an
// EXACT match there, and a documented, simpler approximation for
// disk-io-contention/parity-slowdown/disk-spinup-churn (each scoped to
// one device, or the array's data devices, at attribution time) --
// resolving a live:io.* device key would additionally need this file to
// re-implement foldPartitionDevice/canonicalDevice's own partition-and-
// md-alias folding (evidence.go) purely to ask a question this simpler,
// always-resolvable total already answers honestly: "how much disk IO
// was this culprit driving overall during the incident."
export function planIncidentCharts(inst: PlanInput, ctx: PlanContext = {}): ChartPlan[] {
  const diskMeta = ctx.diskMeta ?? {};
  const gpuEntities = ctx.gpuEntities ?? [];
  const IO_METRICS = ['io.read_bps', 'io.write_bps'];

  switch (inst.rule_id) {
    // evalDiskIOContention (rules.go): victim = the DEVICE's own
    // util_pct/await_ms (host.go's diskio.<dev>.*, host-kind, keyed by
    // the RAW device name -- resourceLabel's own doc for why Resource
    // itself is usually a SLOT, not that raw name); culprit = Dominant()
    // over that same device's container IO share.
    case 'disk-io-contention': {
      const device = diskMeta[inst.resource]?.device ?? inst.resource;
      return [
        { key: 'victim', title: `${inst.resource} utilisation`, formatter: 'pct',
          lines: [{ kind: 'host', entity: '', metrics: [`diskio.${device}.util_pct`], label: inst.resource, colorVar: VICTIM_COLOR }] },
        { key: 'culprit', title: 'Culprit disk IO', formatter: 'rate', lines: culpritLines(inst, IO_METRICS) },
      ];
    }

    // evalIODrivenCPULoad: victim = host.go's cpu.iowait_pct; culprit =
    // Dominant() over totalContainerIO (rules.go), the exact same
    // all-devices sum IO_METRICS already is.
    case 'io-driven-cpu-load':
      return [
        { key: 'victim', title: 'Host IO-wait', formatter: 'pct',
          lines: [{ kind: 'host', entity: '', metrics: ['cpu.iowait_pct'], label: 'Host IO-wait', colorVar: VICTIM_COLOR }] },
        { key: 'culprit', title: 'Culprit disk IO', formatter: 'rate', lines: culpritLines(inst, IO_METRICS) },
      ];

    // evalCPUStarvation: victim = the named container's own
    // cpu.throttled_pct (cgroupv2.go); culprit = Dominant() over
    // ContainerCPUPct (cpu.pct). Both plain 0-100 percentages -- one
    // combined chart.
    case 'cpu-starvation':
      return [
        { key: 'combined', title: 'CPU pressure', formatter: 'pct',
          lines: [
            { kind: 'container', entity: inst.victim, metrics: ['cpu.throttled_pct'], label: `${inst.victim} (throttled)`, colorVar: VICTIM_COLOR },
            ...culpritLines(inst, ['cpu.pct']),
          ] },
      ];

    // evalParitySlowdown: victim = unraid/var.go's parity.speed_bps
    // (kind "unraid", entity "array" -- var.go's own literal); culprit =
    // Dominant() over containers' DATA-device IO share -- IO_METRICS'
    // all-devices total is this rule's one documented approximation, see
    // this function's own top doc. Both bytes/sec -- one combined chart.
    case 'parity-slowdown':
      return [
        { key: 'combined', title: 'Parity speed vs. culprit IO', formatter: 'rate',
          lines: [
            { kind: 'unraid', entity: 'array', metrics: ['parity.speed_bps'], label: 'Parity speed', colorVar: VICTIM_COLOR },
            ...culpritLines(inst, IO_METRICS),
          ] },
      ];

    // evalDiskSpinupChurn: victim = unraid/disks.go's own disk.<slot>.
    // spun_up 0/1 gauge -- entity IS the slot (Resource itself), no
    // device join needed at all, unlike disk-io-contention above; culprit
    // (never shared, rules.go's own doc: "names a single dominant
    // culprit") = the same all-devices IO total. 'plain' (not 'pct'): a
    // 0/1 gauge read through fmtPct would render "0.0%"/"100.0%", which
    // reads as a saturation level rather than the on/off state it is.
    case 'disk-spinup-churn':
      return [
        { key: 'victim', title: `${inst.resource} spin state (1 = spinning, 0 = parked)`, formatter: 'plain',
          lines: [{ kind: 'disk', entity: inst.resource, metrics: ['spun_up'], label: inst.resource, colorVar: VICTIM_COLOR }] },
        { key: 'culprit', title: 'Culprit disk IO', formatter: 'rate', lines: culpritLines(inst, IO_METRICS) },
      ];

    // evalGPUEngineContention: victim = gpu/collector.go's engine.<eng>.
    // busy_pct (kind "gpu", entity a PHYSICAL gpu id the stored row never
    // keeps -- PlanContext.gpuEntities' own doc); culprit = that SAME
    // engine's gpu.<eng>.busy_pct on the culprit container. Both busy_pct
    // on the SAME engine -- the cleanest single-chart case of the seven.
    case 'gpu-engine-contention': {
      const lines: ChartPlanLine[] = [];
      if (gpuEntities.length === 1) {
        lines.push({ kind: 'gpu', entity: gpuEntities[0], metrics: [`engine.${inst.victim}.busy_pct`], label: `Engine ${inst.victim}`, colorVar: VICTIM_COLOR });
      }
      lines.push(...culpritLines(inst, [`gpu.${inst.victim}.busy_pct`]));
      return [{ key: 'combined', title: `GPU engine ${inst.victim}`, formatter: 'pct', lines }];
    }

    // evalMemorySqueeze: victim is EITHER a named container's own mem.pct
    // (the OOM path, VictimKind "container") or the host-wide mem.
    // used_pct (VictimKind "host", the sustained-threshold path); culprit
    // = Dominant() over ContainerMemPct (mem.pct) either way. Both plain
    // memory percentages -- one combined chart.
    case 'memory-squeeze': {
      const victimLine: ChartPlanLine =
        inst.victim_kind === 'container'
          ? { kind: 'container', entity: inst.victim, metrics: ['mem.pct'], label: `${inst.victim} (OOM)`, colorVar: VICTIM_COLOR }
          : { kind: 'host', entity: '', metrics: ['mem.used_pct'], label: 'Host memory', colorVar: VICTIM_COLOR };
      return [{ key: 'combined', title: 'Memory pressure', formatter: 'pct', lines: [victimLine, ...culpritLines(inst, ['mem.pct'])] }];
    }

    default:
      // Defensive only: the rule library is fixed and closed (seven
      // compiled-in ids, insight.DefaultRules()) -- describeRule's own
      // identical fallback posture (insights.ts) for the same reason.
      return [];
  }
}

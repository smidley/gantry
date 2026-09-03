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
//
// Owner-reported bug, fixed here: "the highlighted part of the incident
// timeline doesn't line up with the required info" -- the band used to
// be the ADMINISTRATIVE span [started_at, resolved_at], but a sustained
// rule only ever fires AFTER its own evaluation window has already
// elapsed (Sustained(), window.go: "every sample in the trailing forSecs
// seconds... crosses threshold"), so the CAUSAL spike that actually
// produced the finding lives BEFORE started_at, not after it -- exactly
// where the owner's own screenshot showed it sitting, over flat data.
// incidentLookbackSecs below (verified per rule against rules.go/
// engine.go, not guessed) is the fix: the band now covers [started_at -
// lookback, resolved_at], and incidentChartWindow's own padding is
// computed off THAT extended span so context still surrounds the full
// band. The Fired/Resolved markers stay exactly where they were --
// still real timestamps, now legible as the seam between "building up"
// (the wider shaded look-back) and "actively firing" (the narrower
// span between the two markers) within one uniform band, rather than a
// second shade this file deliberately does not add (see
// incidentLookbackSecs' own doc).
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
// incidentBand both need for the temporal half of their own math --
// deliberately narrower than InsightDTO, the exact OverlapWindowLike/
// InsightCulpritLike precedent (insights.ts), so a hand-built test
// fixture never has to carry every InsightDTO field just to exercise
// this window math. incidentMarkers uses its own separate
// IncidentMarkerLike, below -- markers don't need the look-back at all.
export interface IncidentWindowLike {
  state: string;
  started_at: number;
  resolved_at: number;
}

// EVIDENCE_WINDOW_SECS mirrors internal/insight/rules.go's own
// EvidenceWindowSecs (120s) EXACTLY -- a compiled-in, NEVER user-
// overridable package constant (contrast sustain_secs, a per-rule,
// per-install TUNABLE threshold the Rules editor can change), so this
// one specific number carries no drift risk the way mirroring
// sustain_secs would.
//
// Verified against the engine, not guessed: every "sustained" rule's
// own gather step (engine.go's gather()) fetches its samples from
// now-EvidenceWindowSecs forward, and even the two rules that fetch
// FURTHER back for their own rolling-median BASELINE
// (disk-io-contention's HostDiskIO, parity-slowdown's ParitySpeedBps,
// both now-BaselineLookbackSecs=600s) still split that longer fetch at
// now-EvidenceWindowSecs and test ONLY the recent (<=120s) half for the
// actual sustained breach (splitWindow, evalDiskIOContention/
// evalParitySlowdown) -- the causal spike itself is always bounded by
// this 120s figure regardless of how much further back the baseline
// comparison alone reaches. This is also exactly where the owner's own
// bug report empirically landed ("the spike aligns exactly with fired
// minus 2 minutes"): 120s is 2 minutes, and fake-mode's own sustain_secs
// compression (Engine.FakeSustainSecs, engine.go) never touches this
// constant at all, so the alignment holds on a throwaway fake-mode box
// exactly as it does on a real one with real PSI.
export const EVIDENCE_WINDOW_SECS = 120;

// DISK_SPINUP_CHURN_DEFAULT_WINDOW_SECS mirrors rules.go's own
// librarySpecs default for disk-spinup-churn's "window_minutes"
// threshold (60) -- used only as a defensive fallback on top of a LIVE
// read (see incidentLookbackSecs' own doc on why this one rule can read
// its real, possibly-overridden value directly instead of leaning on a
// compiled-in constant the way the other six do).
export const DISK_SPINUP_CHURN_DEFAULT_WINDOW_SECS = 60 * 60;

// LookbackLike is incidentLookbackSecs' own minimal shape.
export interface LookbackLike {
  rule_id: string;
  victim_kind: string;
  evidence?: { window_minutes?: number; spin_window_minutes?: number };
}

// incidentLookbackSecs answers "how far before started_at does this
// insight's own CAUSAL evidence extend" -- verified per rule against
// internal/insight/rules.go's own Eval functions and engine.go's gather
// step, not guessed (this repo's own standing rule):
//
//   disk-io-contention, io-driven-cpu-load, cpu-starvation,
//   parity-slowdown, gpu-engine-contention, and memory-squeeze's
//   HOST-WIDE path: every one of these evaluates a Sustained() breach
//   over a window bounded by EvidenceWindowSecs (see that export's own
//   doc) -- the fixed 120s look-back applies uniformly. evidence.
//   window_minutes is preferred over the constant WHEN the engine
//   happens to have populated it (today: only the PSI-confirmed
//   branches of disk-io-contention/io-driven-cpu-load/memory-squeeze,
//   plus cpu-starvation's own shared cpuStarvationFinding constructor,
//   which sets it for EVERY confidence tier -- a real inconsistency in
//   the engine, surfaced while diagnosing this bug: even where
//   populated, the field is ITSELF hardcoded to EvidenceWindowSecs/60,
//   never the rule's own actual sustain_secs, so preferring it costs
//   nothing today and only pays off if a future engine revision
//   corrects that). Absent or zero (every tier-1/likely finding from
//   the other rules, today) falls back to the verified constant.
//
//   memory-squeeze's CONTAINER/OOM path is a discrete EVENT (a kill
//   either happened or didn't, at one instant -- evalMemorySqueeze's own
//   in.OOMEvents loop, no Sustained() call at all) with no look-back to
//   invent -- don't guess one just because every other rule has one.
//
//   disk-spinup-churn is a different shape entirely: it counts rising
//   edges over its OWN rule-level "window_minutes" threshold (default
//   60 MINUTES, SpinupLookbackSecs=3600 in rules.go), unrelated to
//   EvidenceWindowSecs -- and unlike the other six, this one's own
//   window is ALWAYS populated on evidence with no confidence-tier
//   gating (evalDiskSpinupChurn's one call site sets SpinWindowMinutes
//   unconditionally), so the LIVE, possibly-overridden value is read
//   directly, with the compiled-in default purely as a defensive
//   fallback should evidence somehow be missing it.
export function incidentLookbackSecs(inst: LookbackLike): number {
  switch (inst.rule_id) {
    case 'disk-io-contention':
    case 'io-driven-cpu-load':
    case 'cpu-starvation':
    case 'parity-slowdown':
    case 'gpu-engine-contention': {
      const windowMinutes = inst.evidence?.window_minutes;
      return windowMinutes && windowMinutes > 0 ? windowMinutes * 60 : EVIDENCE_WINDOW_SECS;
    }
    case 'memory-squeeze': {
      if (inst.victim_kind === 'container') return 0; // the OOM-kill path: an instant, not a sustained trend
      const windowMinutes = inst.evidence?.window_minutes;
      return windowMinutes && windowMinutes > 0 ? windowMinutes * 60 : EVIDENCE_WINDOW_SECS;
    }
    case 'disk-spinup-churn': {
      const minutes = inst.evidence?.spin_window_minutes;
      return minutes && minutes > 0 ? minutes * 60 : DISK_SPINUP_CHURN_DEFAULT_WINDOW_SECS;
    }
    default:
      // Defensive only: the rule library is fixed and closed -- the
      // exact posture planIncidentCharts' own switch below takes (and
      // moot in practice, since that function already returns no charts
      // at all for a rule id it doesn't recognize, so this value is
      // never actually rendered against).
      return 0;
  }
}

// IncidentBandLike is incidentBand's (and, in turn, incidentChartWindow's)
// own combined shape -- the temporal fields plus whatever
// incidentLookbackSecs needs to size the look-back correctly.
export interface IncidentBandLike extends IncidentWindowLike, LookbackLike {}

// incidentBand is the UNPADDED [start, end] TimeChart's own `band` prop
// draws (a shaded rect behind the plotted lines, see that component's
// own doc) -- now [started_at - lookback, resolved_at] rather than just
// [started_at, resolved_at] (this file's own top-of-file doc has the
// full bug/fix story). Distinct from incidentChartWindow's own PADDED
// fetch range below, which surrounds this band with context rather than
// stopping exactly at its own edges.
export function incidentBand(inst: IncidentBandLike, nowSec: number): [number, number] {
  const lookback = incidentLookbackSecs(inst);
  return [inst.started_at - lookback, inst.state === 'active' ? nowSec : inst.resolved_at];
}

// incidentChartWindow returns the padded [from, to] to request from GET
// /api/series -- TimeChart's own xDomain prop, unit-for-unit (both are
// [number, number] unix-second tuples), so the caller hands this straight
// through with no reshaping. Built ON TOP of incidentBand (not a second,
// separately-computed look-back) specifically so the two can never
// disagree about where the band's own left edge sits.
//
// The trailing pad is deliberately OMITTED for a still-ACTIVE insight:
// "padded on each side" describes an incident that HAS a far side to pad
// -- an active one doesn't yet, and padding past nowSec would only widen
// the chart with a dead, dataless gap rather than genuine context. to is
// exactly nowSec for that case; the leading pad still applies either way,
// since "what led up to this" is always real history regardless of
// whether the incident has resolved yet.
export function incidentChartWindow(inst: IncidentBandLike, nowSec: number): [number, number] {
  const isActive = inst.state === 'active';
  const [bandStart, bandEnd] = incidentBand(inst, nowSec);
  const duration = Math.max(0, bandEnd - bandStart);
  const pad = Math.min(Math.max(duration, MIN_PAD_SEC), MAX_PAD_SEC);
  return [bandStart - pad, isActive ? bandEnd : bandEnd + pad];
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

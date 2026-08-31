// Pure helpers for the Insights view (Task 11), the ContainerDetail
// impact panel (Task 12), and the alert annotation bridge (Task 13) --
// kept DOM-free and vitest-tested, the exact split alerts.ts's own doc
// names as this app's standing convention.
import { fmtDuration, fmtPct } from './format';

// --- confidence -----------------------------------------------------------

// confidenceLabel renders store.InsightInstance.Confidence's two-slot
// wire vocabulary in plain words for the Active row's own chip --
// "Likely" / "Confirmed", never the raw lowercase machine string, and
// NEVER colour-alone (the chip's own class carries a shape/border
// difference too -- see Insights.svelte's template).
export function confidenceLabel(confidence: string): string {
  return confidence === 'confirmed' ? 'Confirmed' : 'Likely';
}

// --- ordering ---------------------------------------------------------------

// SEVERITY_RANK is store.Event's own three-slot vocabulary (info <
// warning < alert), the exact rank table alerts.ts's own SEVERITY_RANK
// uses -- an insight's Severity column shares that identical
// vocabulary (insight/rules.go's own Finding.Severity doc).
const SEVERITY_RANK: Record<string, number> = { alert: 2, warning: 1, info: 0 };
const CONFIDENCE_RANK: Record<string, number> = { confirmed: 1, likely: 0 };

export interface SortableInsight {
  severity: string;
  confidence: string;
  fired_at: number;
}

// sortActiveInsights orders the Active section: severity descending,
// then confidence descending (a confirmed finding of equal severity
// outranks a merely-likely one -- it's the stronger claim), then
// fired_at ascending within a tie -- the oldest still-active finding
// surfaces first, mirroring sortActiveAlerts' own ordering instinct
// (alerts.ts). Returns a NEW array; never mutates its input.
export function sortActiveInsights<T extends SortableInsight>(insights: T[]): T[] {
  return [...insights].sort((a, b) => {
    const severityDiff = (SEVERITY_RANK[b.severity] ?? 0) - (SEVERITY_RANK[a.severity] ?? 0);
    if (severityDiff !== 0) return severityDiff;
    const confidenceDiff = (CONFIDENCE_RANK[b.confidence] ?? 0) - (CONFIDENCE_RANK[a.confidence] ?? 0);
    if (confidenceDiff !== 0) return confidenceDiff;
    return a.fired_at - b.fired_at;
  });
}

// --- durations --------------------------------------------------------------

// activeDuration is "active for <relative>" -- the firingDuration
// precedent (alerts.ts), just keyed off started_at instead of
// fired_at: an insight's own lifecycle collapses "pending" into a
// single-tick formality (insight/engine.go's own Tick doc), so
// started_at and fired_at are stamped to the identical tick anyway,
// but started_at is the semantically correct field to read here.
export function activeDuration(startedAtSec: number, nowSec: number): string {
  return fmtDuration(nowSec - startedAtSec);
}

// --- culprit identity --------------------------------------------------------

// InsightCulpritLike is the minimal shape culpritNames/insightsAffecting/
// insightsCausedBy need -- deliberately narrower than the full
// InsightDTO so a caller can pass a frame item OR a REST one
// interchangeably.
export interface InsightCulpritLike {
  culprit: string;
  culprits: string;
}

// culpritNames splits one insight's culprit identity into a plain name
// list regardless of shape: a single-culprit row's culprit alone, or a
// Shared row's comma-joined culprits (server's culpritColumns encoding)
// -- the client-side mirror of api_insights.go's own culpritNames, kept
// in lockstep by TestBuildInsightGraph*'s Go-side tests and
// insights.test.ts's own cases below.
export function culpritNames(inst: InsightCulpritLike): string[] {
  if (inst.culprit) return [inst.culprit];
  if (!inst.culprits) return [];
  return inst.culprits.split(',');
}

export interface InsightVictimLike extends InsightCulpritLike {
  victim_kind: string;
  victim: string;
}

// insightsAffecting is the impact panel's "Being slowed by" half: every
// insight where containerName is the NAMED victim -- see
// api_insights.go's GraphEdgeDTO doc for why VictimKind must be
// "container" (a host/array/disk/gpu-wide finding's own Victim field
// can hold a non-container identity, e.g. gpu-engine-contention's bare
// engine name, which must never be mistaken for this container).
export function insightsAffecting<T extends InsightVictimLike>(insights: T[], containerName: string): T[] {
  return insights.filter((i) => i.victim_kind === 'container' && i.victim === containerName);
}

// insightsCausedBy is the impact panel's "Slowing" half: every insight
// where containerName appears among the culprits, single or shared.
export function insightsCausedBy<T extends InsightCulpritLike>(insights: T[], containerName: string): T[] {
  return insights.filter((i) => culpritNames(i).includes(containerName));
}

// --- share strip (Task 12's "always-present, no engine required" view) -----

// deviceSharePct computes one container's share of one device's total
// IO: its own read+write bytes/s (from GET /api/containers/{name}/
// storage, the impact panel's own reused StorageRefDTO plumbing) over
// the HOST's own total for that same device (live.frame.host's own
// diskio.<dev>.read_bps/.write_bps) -- the same ratio insight/
// evidence.go's Share() computes when ranking every container, just for
// one already-known container rather than a full ranking. This is
// honest at every moment and needs no engine: it's a plain live-frame
// division, which is exactly why the plan calls this strip
// "always-present" rather than something that waits for a finding to
// fire.
export function deviceSharePct(containerReadBps: number, containerWriteBps: number, hostReadBps: number, hostWriteBps: number): number {
  const hostTotal = hostReadBps + hostWriteBps;
  if (!Number.isFinite(hostTotal) || hostTotal <= 0) return 0;
  const containerTotal = containerReadBps + containerWriteBps;
  return Math.max(0, Math.min(100, (containerTotal / hostTotal) * 100));
}

// --- evidence formatting -----------------------------------------------------

// EVIDENCE_UNIT names each EvidenceDTO field's own unit family --
// formatEvidenceNumber's whole dispatch table, so the evidence drawer
// never has to know which of the twelve fields it's currently
// rendering to format it correctly.
type EvidenceUnit = 'pct' | 'ms' | 'minutes' | 'count' | 'plain';

const EVIDENCE_UNIT: Record<string, EvidenceUnit> = {
  culprit_share_pct: 'pct',
  device_util_pct: 'pct',
  victim_stall_pct: 'pct',
  iowait_pct: 'pct',
  host_cpu_pct: 'pct',
  engine_busy_pct: 'pct',
  baseline_pct: 'pct',
  await_ms: 'ms',
  window_minutes: 'minutes',
  spin_window_minutes: 'minutes',
  spin_count: 'count',
};

// formatEvidenceNumber renders one EvidenceDTO field's raw value with
// its own unit -- "the numbers, shares, window, tier" the evidence
// drawer's whole job is to show plainly (plan: "visibly rather than
// mysteriously wrong"). An unrecognized key (defensive -- every real
// key is named above) falls back to a bare number rather than throwing.
export function formatEvidenceNumber(key: string, value: number): string {
  switch (EVIDENCE_UNIT[key]) {
    case 'pct':
      return fmtPct(value);
    case 'ms':
      return `${value.toFixed(0)} ms`;
    case 'minutes':
      return `${value} min`;
    case 'count':
      return `${value}×`;
    default:
      return String(value);
  }
}

// EVIDENCE_LABEL is formatEvidenceNumber's companion: a plain-English
// name for each field, for the drawer's own key/value rows -- never the
// bare Go/wire field name.
export const EVIDENCE_LABEL: Record<string, string> = {
  culprit_share_pct: "Culprit's share",
  device_util_pct: 'Device utilisation',
  victim_stall_pct: 'Victim stall (PSI)',
  iowait_pct: 'Host IO-wait',
  host_cpu_pct: 'Host CPU',
  engine_busy_pct: 'Engine busy',
  baseline_pct: '% of baseline speed',
  await_ms: 'Average latency',
  window_minutes: 'Window',
  spin_window_minutes: 'Spin-up window',
  spin_count: 'Spin-ups observed',
};

// --- rule descriptions (Task 11's own Rules section) -------------------------

// RULE_DESCRIPTIONS covers the fixed, compiled-in seven-rule library
// (insight/rules.go's librarySpecs -- there is no user-authored insight
// rule in v1, plan Global Constraints) with a template per rule that
// interpolates ITS OWN current threshold values, so an edited number is
// reflected immediately with no separate copy to keep in sync -- the
// exact "generated fresh from the rule's current fields" contract
// alerts.ts's own describeRule follows for threshold rules, applied
// here per-rule instead of from one generic metric/op/threshold shape
// (these seven evaluators are each bespoke, unlike alerts' uniform
// threshold rules).
const RULE_DESCRIPTIONS: Record<string, (t: Record<string, number>) => string> = {
  'disk-io-contention': (t) =>
    `Flags a container driving ${t.culprit_share_floor_pct}%+ of a disk's IO while the disk sits ${t.util_pct_floor}%+ busy.`,
  'io-driven-cpu-load': (t) =>
    `Flags a container's disk IO (${t.culprit_share_floor_pct}%+ of all host IO) loading the CPU (host IO-wait ${t.iowait_pct_floor}%+).`,
  'cpu-starvation': (t) =>
    `Flags a throttled container (${t.throttled_pct_floor}%+) while another holds ${t.culprit_cpu_pct_floor}%+ of host CPU and the host itself runs ${t.host_cpu_total_floor}%+.`,
  'parity-slowdown': (t) =>
    `Flags the parity check running below ${Math.round(t.speed_floor_fraction_of_baseline * 100)}% of its usual speed while a container drives ${t.culprit_share_floor_pct}%+ of the array's data IO.`,
  'disk-spinup-churn': (t) =>
    `Flags a disk spinning up ${t.min_transitions}+ times within ${t.window_minutes} minutes, each time following the same container's reads.`,
  'gpu-engine-contention': (t) =>
    `Flags a GPU engine ${t.engine_busy_floor}%+ busy with ${t.min_culprits}+ containers each holding ${t.culprit_share_floor_pct}%+ of it.`,
  'memory-squeeze': (t) =>
    `Flags host memory ${t.mem_used_pct_floor}%+ used (or a container OOM-killed) while another holds ${t.culprit_mem_pct_floor}%+ of host memory.`,
};

// describeRule renders one rule's plain-English restatement from its
// OWN effective thresholds (InsightRuleDTO.thresholds -- already merged
// against any override, server-side). Falls back to the rule's own
// Title for an id this table doesn't recognize (defensive only: the
// library is fixed and closed, so this should never actually fire).
export function describeRule(ruleID: string, thresholds: Record<string, number>, title: string): string {
  const template = RULE_DESCRIPTIONS[ruleID];
  return template ? template(thresholds) : title;
}

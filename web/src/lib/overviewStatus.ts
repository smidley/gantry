// Pure derivation for Overview's D2 status headline: "Everything is
// running" vs "N things need you", plus the ordered list of individual
// anomalies that back the attention module's rows. Kept plain (no
// Svelte/DOM) so the counting rule itself -- the one piece of new product
// logic this redesign adds -- is directly unit-testable, same split as
// every other lib/*.ts pure helper in this app.
//
// "One thing" is one individually-addressable concern, not one
// CATEGORY: each unhealthy container gets its own anomaly (a real,
// comparatively rare signal worth its own row and its own link into that
// container's detail page), each flagged disk gets its own anomaly, and
// so on. Stopped containers are deliberately NOT a concern at all
// (Scott: "stopped containers are not something that needs you") --
// state!=running is a common, often-intentional condition in a home-lab
// (containers turned off on purpose), so it contributes nothing to the
// anomaly list or the headline count; the fleet sentence
// ("X running · Y stopped", fleetSentence below) still states it as a
// plain fact. The headline's own count is always anomalies.length, so
// "N things" and "N rows in the attention module" can never disagree.
import { diskUsagePct } from './disks';
import { fmtPct } from './format';
import type { HealthStatus } from './containerStatus';

// AnomalyBase.severityOverride, when present, is the MAX of an anomaly's
// own default severity and a firing alert's severity that the dedup
// table below maps onto the same concern -- e.g. array-stopped's
// frame-derived "serious" becomes "critical" once the array-stopped
// ALERT RULE (severity "alert") is actually firing on it. describeAnomaly
// takes the max, never a plain overwrite, so an edited rule with a
// LOWER severity than the frame-derived floor can never read as less
// urgent than the unconfigurable frame check already found it to be.
// Absent (the overwhelming common case -- no matching alert firing) it
// changes nothing.
interface AnomalyBase {
  severityOverride?: HealthStatus;
}

// Alerts and anomalies coexist as a fifth source, not a replacement (see
// deriveOverviewStatus' own doc for the full rationale): the 'alert'
// variant covers everything a firing alert reports that the frame-
// derived checks above structurally cannot -- sustained-for, host CPU/
// memory, both disk-temp bands, container memory-limit, OOM, nonzero
// exit, parity errors -- each gets its own row linking to the Alerts
// view. severity here is the alert's OWN mapped severity (there is no
// "default" to override, unlike the five kinds above).
export type OverviewAnomaly =
  | ({ kind: 'unhealthy'; name: string } & AnomalyBase)
  | ({ kind: 'disk-usage'; slot: string; usagePct: number } & AnomalyBase)
  | ({ kind: 'disk-errors'; slot: string; errors: number } & AnomalyBase)
  | ({ kind: 'array-stopped' } & AnomalyBase)
  | ({ kind: 'source-critical'; source: string; detail: string } & AnomalyBase)
  | ({
      kind: 'alert';
      ruleId: string;
      ruleName: string;
      entity: string;
      severity: HealthStatus;
      // metric/summary ride along from the firing alert so
      // describeAnomalyCore can tell a threshold alert (metric set --
      // detail stays the bare entity, unchanged) from an event alert
      // (metric "" -- there is no value/threshold to show at all, so
      // detail becomes the instance's own summary sentence instead).
      metric?: string;
      summary?: string;
    } & AnomalyBase);

// FiringAlertLike is the narrow slice of api.ts's FiringAlertDTO this
// module actually needs -- kept local rather than importing the wider
// API surface, the same "no dependency beyond what's used" convention
// thresholds.ts's own AlertRuleBandLike follows.
export interface FiringAlertLike {
  rule_id: string;
  rule_name: string;
  // severity is store.Event's own three-slot wire vocabulary (info|
  // warning|alert), NOT HealthStatus -- see alertSeverityToHealth.
  severity: string;
  entity: string;
  silenced: boolean;
  // metric/summary: see OverviewAnomaly's 'alert' variant doc. Optional
  // so a caller that hasn't wired the fuller FiringAlertDTO through yet
  // (or a test fixture that doesn't care) still type-checks.
  metric?: string;
  summary?: string;
}

export interface OverviewStatusInput {
  unhealthyNames: string[];
  // array['array.started'] straight off the live frame -- undefined
  // (no unraid/array data yet) is deliberately NOT treated as "stopped":
  // an absent reading isn't evidence of a problem, only 0 is.
  arrayStarted: number | undefined;
  disks: Record<string, Record<string, number>> | undefined | null;
  sources: Record<string, string> | undefined | null;
  // alerts: the live frame's alerts.firing block (undefined on a page
  // that hasn't wired alerts through yet -- treated as "nothing
  // firing", not an error).
  alerts?: FiringAlertLike[] | null;
}

// alertSeverityToHealth maps store.Event's three-slot vocabulary onto
// the four-slot HealthStatus this file's anomalies use. "alert" maps to
// "critical" (EventFeedItem.svelte's own identical mapping). "warning"
// AND "info" both map to "warning", not "good": an anomaly appearing in
// the "needs a look" list at all is definitionally not fine, and a
// green-colored "needs attention" row would contradict itself -- no
// default rule fires at severity "info" today, but a future
// user-created one could, and this is the safer reading if it ever does.
function alertSeverityToHealth(severity: string): HealthStatus {
  return severity === 'alert' ? 'critical' : 'warning';
}

// DEDUP_RULE_TO_ANOMALY: a firing rule listed here whose concern a
// frame-derived anomaly already covers suppresses its own 'alert' row
// entirely and instead upgrades the matching row's severity (see
// AnomalyBase.severityOverride) -- the frame-derived row is more
// specific and already links correctly. Every other rule (host CPU/
// memory, both disk-temp rules, container memory-limit, OOM, nonzero
// exit, parity errors) has no entry here and always gets its own
// 'alert' anomaly.
const DEDUP_RULE_TO_ANOMALY: Record<string, (a: OverviewAnomaly, entity: string) => boolean> = {
  'disk-usage-high': (a, entity) => a.kind === 'disk-usage' && a.slot === entity,
  'disk-errors': (a, entity) => a.kind === 'disk-errors' && a.slot === entity,
  'array-stopped': (a) => a.kind === 'array-stopped',
  'container-unhealthy': (a, entity) => a.kind === 'unhealthy' && a.name === entity,
};

export interface OverviewStatus {
  ok: boolean;
  headline: string;
  anomalies: OverviewAnomaly[];
  // Every disk slot behind a 'disk-usage' or 'disk-errors' anomaly above
  // -- the bay schematic's own callout targets, and the closing line's
  // "N OTHER members" count subtracts this set's size from the total.
  flaggedDiskSlots: string[];
}

// DISK_USAGE_WARN_PCT matches Storage.svelte's own "High usage" threshold
// exactly (`{#if usagePct > 90}`) -- one warning line, not two different
// numbers for the same condition depending which view you're looking at.
const DISK_USAGE_WARN_PCT = 90;

// CRITICAL_SOURCES: the one collector source SourcesBanner itself treats
// as prominent and non-dismissible (docker -- "the fleet view depends on
// it") is the only one that also earns a headline anomaly. Every other
// degraded source (nvidia, pressure, ...) stays exactly what
// SourcesBanner already renders it as, a quiet dismissible hint, and is
// correctly never promoted into "needs you" territory here either --
// same critical/non-critical split, one shared definition of "critical."
const CRITICAL_SOURCES = ['docker'];

export function deriveOverviewStatus(input: OverviewStatusInput): OverviewStatus {
  const anomalies: OverviewAnomaly[] = [];

  for (const name of input.unhealthyNames) {
    anomalies.push({ kind: 'unhealthy', name });
  }

  const disks = input.disks ?? {};
  const diskSlots = Object.keys(disks).sort();
  const flaggedDiskSlots: string[] = [];

  // disk-usage: at most ONE anomaly, the single worst (highest-%) disk
  // over the threshold -- the mockup's own "one callout on the fullest,"
  // not one row per over-90% disk (a real array failing this badly on
  // more than one member at once has bigger problems than this page can
  // usefully enumerate one row at a time).
  let worst: { slot: string; usagePct: number } | null = null;
  for (const slot of diskSlots) {
    const pct = diskUsagePct(disks[slot]);
    if (pct !== null && pct > DISK_USAGE_WARN_PCT && (!worst || pct > worst.usagePct)) {
      worst = { slot, usagePct: pct };
    }
  }
  if (worst) {
    anomalies.push({ kind: 'disk-usage', slot: worst.slot, usagePct: worst.usagePct });
    flaggedDiskSlots.push(worst.slot);
  }

  // disk-errors: unlike usage, one row PER erroring disk -- a real error
  // count rising on more than one disk at once is exactly the kind of
  // thing that shouldn't get quietly collapsed into a single line.
  for (const slot of diskSlots) {
    const errors = disks[slot]?.['errors'] ?? 0;
    if (errors > 0) {
      anomalies.push({ kind: 'disk-errors', slot, errors });
      flaggedDiskSlots.push(slot);
    }
  }

  if (input.arrayStarted === 0) {
    anomalies.push({ kind: 'array-stopped' });
  }

  const sources = input.sources ?? {};
  for (const source of CRITICAL_SOURCES) {
    const detail = sources[source];
    if (detail !== undefined && detail !== 'ok') {
      anomalies.push({ kind: 'source-critical', source, detail });
    }
  }

  // Alerts merge (Task 12) -- see this function's own top-of-file doc
  // for the full "coexist, not replace" rationale. A SILENCED firing
  // alert contributes nothing here: silencing is a deliberate "don't
  // nag me about this" gesture, and it would be a strange product to
  // honor that everywhere except the one place a user can't dismiss it.
  for (const alert of input.alerts ?? []) {
    if (alert.silenced) continue;
    const mappedSeverity = alertSeverityToHealth(alert.severity);
    const matches = DEDUP_RULE_TO_ANOMALY[alert.rule_id];
    const existing = matches ? anomalies.find((a) => matches(a, alert.entity)) : undefined;
    if (existing) {
      const currentSeverity = existing.severityOverride ?? describeAnomaly(existing).severity;
      if (SEVERITY_RANK[mappedSeverity] > SEVERITY_RANK[currentSeverity]) {
        existing.severityOverride = mappedSeverity;
      }
      continue;
    }
    anomalies.push({
      kind: 'alert', ruleId: alert.rule_id, ruleName: alert.rule_name, entity: alert.entity, severity: mappedSeverity,
      metric: alert.metric, summary: alert.summary,
    });
  }

  // The headline count stays anomalies.length either way (Task 12's own
  // invariant, unchanged from before alerts existed): "N things need
  // you" and the number of rows can never disagree, whether a row came
  // from an instantaneous frame check or a sustained-for alert.
  const ok = anomalies.length === 0;
  const headline = ok
    ? 'Everything is running'
    : anomalies.length === 1
      ? '1 thing needs you'
      : `${anomalies.length} things need you`;

  return { ok, headline, anomalies, flaggedDiskSlots };
}

export interface AnomalyText {
  title: string;
  detail: string;
  severity: HealthStatus;
  // Present only for an anomaly that names one specific container --
  // the attention row's own title text already names it, but callers
  // (the row's own link target) need the bare name separately from the
  // human sentence it's embedded in.
  linkContainer?: string;
  // Present only for an 'alert' anomaly -- links the row to the Alerts
  // view (Task 12: "the callouts link to the Alerts view"). The other
  // five kinds either link via linkContainer or don't link at all.
  href?: string;
}

// describeAnomalyCore renders one anomaly's row text from its OWN kind
// alone -- kept a separate pure function (rather than inlined per-kind
// in the component template) so the exact wording, including every
// pluralization boundary, is unit-tested the same way format.ts's own
// formatters are. describeAnomaly (below) is the public entry point;
// this one exists separately so the severity-override check has a
// "what would this kind's severity be on its own" to compare against
// without recursing into itself.
function describeAnomalyCore(a: OverviewAnomaly): AnomalyText {
  switch (a.kind) {
    case 'unhealthy':
      return {
        severity: 'critical',
        title: `${a.name} is unhealthy`,
        detail: 'Failing its health check.',
        linkContainer: a.name,
      };
    case 'disk-usage':
      return {
        severity: 'warning',
        title: `${a.slot} is nearest to full`,
        detail: `${fmtPct(a.usagePct)} capacity`,
      };
    case 'disk-errors':
      return {
        severity: 'serious',
        title: `${a.slot} is reporting errors`,
        detail: `${a.errors} error${a.errors === 1 ? '' : 's'}`,
      };
    case 'array-stopped':
      return { severity: 'serious', title: 'Array is stopped', detail: 'No parity protection while it is down.' };
    case 'source-critical':
      return { severity: 'critical', title: `${a.source} needs attention`, detail: a.detail };
    case 'alert':
      // A threshold alert's metric is always non-empty -- detail stays
      // the bare entity, unchanged. An event alert has no metric (and
      // so no meaningful value/threshold at all -- see FiringAlertDTO's
      // own doc): its summary sentence is the only real description,
      // falling back to entity on the off chance summary is also empty.
      return { severity: a.severity, title: a.ruleName, detail: a.metric ? a.entity : a.summary || a.entity, href: '#/alerts' };
  }
}

// describeAnomaly is describeAnomalyCore plus AnomalyBase.severityOverride
// applied on top, when present: an "upgrade", never a plain overwrite,
// so a dedup-matched anomaly's severity can only ever end up AT LEAST as
// severe as its own frame-derived default (see AnomalyBase's own doc for
// why that direction matters).
export function describeAnomaly(a: OverviewAnomaly): AnomalyText {
  const base = describeAnomalyCore(a);
  if (a.severityOverride && SEVERITY_RANK[a.severityOverride] > SEVERITY_RANK[base.severity]) {
    return { ...base, severity: a.severityOverride };
  }
  return base;
}

// calloutTextBySlot aggregates every disk anomaly's own describeAnomaly()
// detail onto its slot, for BaySchematic's per-bar title/aria-label (its
// one accessible string for a bar that's otherwise a bare `role="img"`
// div). A slot can carry two disk anomalies at once (nearest-to-full AND
// reporting errors) -- joining keeps both in that one string instead of
// the later anomaly's detail silently replacing the earlier one's, the
// same "nothing drops silently" contract the "Needs a look" list above
// it already gives every anomaly, disk or not.
export function calloutTextBySlot(anomalies: OverviewAnomaly[]): Map<string, string> {
  const bySlot = new Map<string, string>();
  for (const a of anomalies) {
    if (a.kind !== 'disk-usage' && a.kind !== 'disk-errors') continue;
    const detail = describeAnomaly(a).detail;
    const prior = bySlot.get(a.slot);
    bySlot.set(a.slot, prior ? `${prior} · ${detail}` : detail);
  }
  return bySlot;
}

// fleetSentence is the headline's own fleet subline, right under the D2
// status headline. total===runningCount keeps the original "all running"
// phrasing; once the frame carries stopped-but-known containers too (not
// just a brief post-stop grace window), that split is worth stating
// plainly instead.
export function fleetSentence(total: number, runningCount: number, stoppedCount: number): string {
  if (stoppedCount > 0) return `${runningCount} running · ${stoppedCount} stopped.`;
  const noun = total === 1 ? 'container' : 'containers';
  return `${total} ${noun}, all running.`;
}

const SEVERITY_RANK: Record<HealthStatus, number> = { good: 0, warning: 1, serious: 2, critical: 3 };

// worstSeverity picks the single most severe reading across every
// anomaly -- drives the headline dot's own color (and the leader line's
// matching echo dot), the same "worst wins" rule HealthDot's four-color
// vocabulary already implies everywhere else it's used.
export function worstSeverity(anomalies: OverviewAnomaly[]): HealthStatus {
  let worst: HealthStatus = 'good';
  for (const a of anomalies) {
    const severity = describeAnomaly(a).severity;
    if (SEVERITY_RANK[severity] > SEVERITY_RANK[worst]) worst = severity;
  }
  return worst;
}

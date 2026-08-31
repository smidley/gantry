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
import { anomalyHref } from './anomalyHref';
import { diskUsagePct } from './disks';
import { fmtPct } from './format';
import { confidenceLabel, sortActiveInsights } from './insights';
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
//
// Insights (Phase 5 Task 13) join the same way, one rung further out:
// the 'insight' variant is an active finding's own statement, rendered
// as a row linking to the Insights view -- but only when no alert row
// already covers its victim (the insights merge below owns that rule;
// see its doc). severity here is the finding's mapped severity, same
// vocabulary and mapping as 'alert'.
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
      // why: the best active insight naming this alert's entity as its
      // victim (the insights merge below) -- describeAnomaly folds it
      // into the row's detail as a "Cause:"/"Likely cause:" suffix,
      // annotateAlerts' exact wording. The alert stays the actionable
      // row; the full annotation (with its link into #/insights) lives
      // on the Alerts view this row already routes to.
      why?: { statement: string; confidence: string };
    } & AnomalyBase)
  | ({ kind: 'insight'; statement: string; severity: HealthStatus; confidence: string } & AnomalyBase);

// OverviewAckLike is the narrow slice of api.ts's OverviewAckDTO this
// module actually needs (the FiringAlertLike convention right below):
// the concrete (kind, entity) identity an acknowledgement suppresses,
// plus its own expiry. until is checked HERE, per derivation run,
// rather than trusting the list to be pre-pruned -- the frame
// re-derives every ~2s tick, so an ack lapsing between store refetches
// makes its anomaly reappear on the very next tick with no fetch.
export interface OverviewAckLike {
  kind: string;
  entity: string;
  until: number; // unix seconds
}

// ackKeyFor maps one anomaly onto the concrete (kind, entity) identity
// an acknowledgement row carries (POST /api/acks' own closed
// vocabulary): the container name, the disk slot, the literal "array"
// (there is only ever one), the source name. null for 'alert' --
// acknowledging an alert-backed callout IS an alert silence (one
// mechanism per system; see deriveOverviewStatus's ack doc), so no ack
// identity exists for it, and no ack row can ever quiet one. null for
// 'insight' by the same rule: quieting a finding is DISMISSING it on
// the Insights view, never a second parallel mechanism here.
export function ackKeyFor(a: OverviewAnomaly): { kind: string; entity: string } | null {
  switch (a.kind) {
    case 'unhealthy':
      return { kind: a.kind, entity: a.name };
    case 'disk-usage':
    case 'disk-errors':
      return { kind: a.kind, entity: a.slot };
    case 'array-stopped':
      return { kind: a.kind, entity: 'array' };
    case 'source-critical':
      return { kind: a.kind, entity: a.source };
    case 'alert':
    case 'insight':
      return null;
  }
}

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
  // kind: the alert's own subject vocabulary (container|host|array|
  // disk|gpu), matched against an insight's victim_kind by the insights
  // merge (annotateAlerts' exact rule). Optional the same way metric/
  // summary are: a caller or fixture without it just never matches an
  // insight, it doesn't fail to type-check.
  kind?: string;
  // metric/summary: see OverviewAnomaly's 'alert' variant doc. Optional
  // so a caller that hasn't wired the fuller FiringAlertDTO through yet
  // (or a test fixture that doesn't care) still type-checks.
  metric?: string;
  summary?: string;
}

// OverviewInsightLike is the narrow slice of api.ts's InsightDTO the
// insights merge needs (the FiringAlertLike convention above): the
// victim identity an alert row is matched on, the statement/severity/
// confidence a row or "why" suffix renders, and fired_at so
// sortActiveInsights can rank competing findings the same way every
// other consumer does.
export interface OverviewInsightLike {
  victim_kind: string;
  victim: string;
  statement: string;
  // severity is the finding's own wire vocabulary (info|warning|alert)
  // -- insight/rules.go shares store.Event's exact three slots.
  severity: string;
  confidence: string;
  fired_at: number;
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
  // acks: every live acknowledgement (GET /api/acks, fetched by the
  // acks store ALONGSIDE the frame -- acks deliberately don't ride in
  // the frame itself). undefined on a page that hasn't wired acks
  // through yet -- treated as "nothing acked", not an error.
  acks?: OverviewAckLike[] | null;
  // insights: the live frame's insights.active block (undefined on a
  // page that hasn't wired insights through yet -- treated as "nothing
  // active", not an error).
  insights?: OverviewInsightLike[] | null;
  // now (unix seconds) is read only to decide ack expiry -- injectable
  // so tests can pin both sides of an ack's until deterministically.
  // Defaults to the real clock; every other part of this derivation
  // stays clock-free exactly as before.
  now?: number;
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
  const frameAnomalies: OverviewAnomaly[] = [];

  for (const name of input.unhealthyNames) {
    frameAnomalies.push({ kind: 'unhealthy', name });
  }

  const disks = input.disks ?? {};
  const diskSlots = Object.keys(disks).sort();

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
    frameAnomalies.push({ kind: 'disk-usage', slot: worst.slot, usagePct: worst.usagePct });
  }

  // disk-errors: unlike usage, one row PER erroring disk -- a real error
  // count rising on more than one disk at once is exactly the kind of
  // thing that shouldn't get quietly collapsed into a single line.
  for (const slot of diskSlots) {
    const errors = disks[slot]?.['errors'] ?? 0;
    if (errors > 0) {
      frameAnomalies.push({ kind: 'disk-errors', slot, errors });
    }
  }

  if (input.arrayStarted === 0) {
    frameAnomalies.push({ kind: 'array-stopped' });
  }

  const sources = input.sources ?? {};
  for (const source of CRITICAL_SOURCES) {
    const detail = sources[source];
    if (detail !== undefined && detail !== 'ok') {
      frameAnomalies.push({ kind: 'source-critical', source, detail });
    }
  }

  // Acknowledgements (Scott: "We need to be able to acknowledge things
  // that need you so they stop showing up for a period of time"): an
  // acked (kind, entity) pair contributes nothing until its ack's own
  // until passes -- the silenced-alert treatment one block down, applied
  // to the frame-derived kinds. Filtered BEFORE the alerts merge, on
  // purpose: an ack quiets the frame-derived row only, never a firing
  // alert. If the same concern's alert rule fires unsilenced while the
  // frame row is acked, the dedup below finds no surviving row to fold
  // into and the alert surfaces on its own line -- an ack is not a
  // silence, and only a silence may quiet an alert.
  const now = input.now ?? Date.now() / 1000;
  const liveAcks = (input.acks ?? []).filter((ack) => ack.until > now);
  const anomalies = frameAnomalies.filter((a) => {
    const key = ackKeyFor(a);
    return !key || !liveAcks.some((ack) => ack.kind === key.kind && ack.entity === key.entity);
  });

  // flaggedDiskSlots derives from the SURVIVING disk anomalies (in list
  // order, one entry per anomaly -- a slot flagged for usage AND errors
  // appears twice, as before): an acked disk callout un-flags its bay-
  // schematic bar too, the same "acked means quiet" the row itself gets.
  const flaggedDiskSlots: string[] = [];
  for (const a of anomalies) {
    if (a.kind === 'disk-usage' || a.kind === 'disk-errors') {
      flaggedDiskSlots.push(a.slot);
    }
  }

  // Alerts merge (Task 12) -- see this function's own top-of-file doc
  // for the full "coexist, not replace" rationale. A SILENCED firing
  // alert contributes nothing here: silencing is a deliberate "don't
  // nag me about this" gesture, and it would be a strange product to
  // honor that everywhere except the one place a user can't dismiss it.
  // alertRows remembers each pushed row alongside its source alert's
  // own kind -- the insights merge below matches on kind+entity, and
  // the anomaly itself deliberately doesn't carry the alert's kind.
  const alertRows: { row: Extract<OverviewAnomaly, { kind: 'alert' }>; sourceKind?: string }[] = [];
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
    const row: Extract<OverviewAnomaly, { kind: 'alert' }> = {
      kind: 'alert', ruleId: alert.rule_id, ruleName: alert.rule_name, entity: alert.entity, severity: mappedSeverity,
      metric: alert.metric, summary: alert.summary,
    };
    anomalies.push(row);
    alertRows.push({ row, sourceKind: alert.kind });
  }

  // Insights merge (Phase 5 Task 13, the Overview half) -- the same
  // "coexist, not replace" shape as the alerts merge above, one rung
  // further out: an insight is a diagnosis (culprit -> resource ->
  // victim), so it earns an attention row only when it's load-bearing
  // on its own. Three rules:
  //
  // - Only a finding at severity warning or worse becomes a row. An
  //   info insight belongs on the Insights view, not in the headline
  //   count -- and that holds regardless of confidence, so even a
  //   confirmed info finding never lands here.
  // - An insight never duplicates an alert row for the same entity,
  //   matched on the alert's own kind+entity against the finding's
  //   victim_kind+victim (annotateAlerts' exact rule; an empty victim
  //   never matches): when both exist the alert row wins and gains the
  //   "why" suffix instead (see the 'alert' variant's own doc) -- the
  //   alert is the actionable one. Findings are walked best-first
  //   (sortActiveInsights), so the suffix a row ends up with is the
  //   same one annotateAlerts itself would pick: one annotation per
  //   row, never a stack, and every lower-ranked finding naming the
  //   same victim stays off the list entirely.
  // - The headline count stays anomalies.length either way, the same
  //   standing invariant the alerts merge already preserves.
  const eligibleInsights = (input.insights ?? []).filter((i) => i.severity === 'warning' || i.severity === 'alert');
  for (const insight of sortActiveInsights(eligibleInsights)) {
    const host = insight.victim
      ? alertRows.find((r) => r.sourceKind === insight.victim_kind && r.row.entity === insight.victim)
      : undefined;
    if (host) {
      if (!host.row.why) {
        host.row.why = { statement: insight.statement, confidence: insight.confidence };
      }
      continue;
    }
    anomalies.push({
      kind: 'insight',
      statement: insight.statement,
      severity: alertSeverityToHealth(insight.severity),
      confidence: insight.confidence,
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
  // The route that explains this anomaly (anomalyHref -- Scott:
  // "anything in the NEEDS YOU section needs to be clickable to get to
  // information about that item"), carried on EVERY kind that has a
  // page to land on: container concerns to that container's detail,
  // disk/array concerns to Storage, alert-backed callouts to the Alerts
  // view, a critical docker source to the fleet view it degrades.
  // Absent only when no page exists for the concern (anomalyHref's own
  // null convention). For 'unhealthy' it duplicates linkContainer's
  // destination on purpose -- consumers that render generically read
  // href alone.
  href?: string;
}

// describeAnomalyCore renders one anomaly's row text from its OWN kind
// alone -- kept a separate pure function (rather than inlined per-kind
// in the component template) so the exact wording, including every
// pluralization boundary, is unit-tested the same way format.ts's own
// formatters are. describeAnomaly (below) is the public entry point;
// this one exists separately so the severity-override check has a
// "what would this kind's severity be on its own" to compare against
// without recursing into itself. href is attached once, after the
// switch, from anomalyHref -- the one shared routing table -- rather
// than each case naming its own destination.
function describeAnomalyCore(a: OverviewAnomaly): AnomalyText {
  const href = anomalyHref(a);
  const text = anomalyTextFor(a);
  return href ? { ...text, href } : text;
}

function anomalyTextFor(a: OverviewAnomaly): AnomalyText {
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
    case 'alert': {
      // A threshold alert's metric is always non-empty -- detail stays
      // the bare entity, unchanged. An event alert has no metric (and
      // so no meaningful value/threshold at all -- see FiringAlertDTO's
      // own doc): its summary sentence is the only real description,
      // falling back to entity on the off chance summary is also empty.
      // A matched insight's "why" rides the same detail slot, in
      // annotateAlerts' exact wording -- see the variant's own doc.
      const base = a.metric ? a.entity : a.summary || a.entity;
      const why = a.why ? `${a.why.confidence === 'confirmed' ? 'Cause' : 'Likely cause'}: ${a.why.statement}` : null;
      return { severity: a.severity, title: a.ruleName, detail: why ? `${base} · ${why}` : base };
    }
    case 'insight':
      // The statement IS the finding ("qbittorrent is starving jellyfin
      // on disk3") -- it carries subject and reason in one sentence, so
      // the detail slot only adds the confidence reading, the same
      // Likely/Confirmed vocabulary the Insights view's chip uses.
      return { severity: a.severity, title: a.statement, detail: confidenceLabel(a.confidence) };
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

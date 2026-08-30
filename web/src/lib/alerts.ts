// Pure helpers for the Alerts view (Task 10) and its rule editor (Task
// 11) -- kept DOM-free and vitest-tested, the same split every other
// lib/*.ts pure helper in this app already follows (overviewStatus.ts's
// own doc names the precedent this file matches: sortActiveAlerts'
// ordering is deliberately the same "worst severity first, then oldest
// within it" rule that file's own worstSeverity/anomaly ordering uses).
import { fmtDuration } from './format';

// --- resolve reasons ---------------------------------------------------

// RESOLVE_REASON_TEXT renders store.AlertInstance.ResolveReason's own
// six wire values (internal/alert/engine.go's resolve* calls) in plain
// words for the History section -- never the raw machine string.
// Exported so alerts.test.ts can check this map's own keys against the
// engine's emitted set directly, the same cross-file discipline
// describeRule's own DEFAULT_RULES test fixture already follows.
export const RESOLVE_REASON_TEXT: Record<string, string> = {
  cleared: 'recovered',
  'no-data': 'stopped reporting',
  timeout: 'auto-closed',
  'rule-disabled': 'rule turned off',
  // restarted: container-exit-nonzero's own churn-probation resolve
  // (resolveRestarted) -- Fleet() showed the entity running again, so
  // this was a routine stop/restart (Unraid's Appdata Backup/CA
  // auto-update plugins), never a real problem.
  restarted: 'routine restart',
  // out-of-scope: a rule edit narrowed entity_glob/entity_class out from
  // under a still-active instance (resolveOutOfScope) -- the rule
  // itself is still live for everyone else, just not for this entity
  // anymore.
  'out-of-scope': 'no longer in scope',
};

export function describeResolveReason(reason: string): string {
  return RESOLVE_REASON_TEXT[reason] ?? reason;
}

// --- durations -----------------------------------------------------------

// firingDuration is "how long has this been firing" for an Active row --
// a thin, testable wrapper over fmtDuration, which already clamps a
// negative span (e.g. a clock skew between the frame's fired_at and the
// browser's own Date.now()) to "0s" rather than a nonsensical negative
// duration.
export function firingDuration(firedAtSec: number, nowSec: number): string {
  return fmtDuration(nowSec - firedAtSec);
}

// --- silences --------------------------------------------------------------

export interface SilenceLike {
  rule_id: string;
  entity: string;
  until: number;
  scope?: 'all';
}

// silenceLabel renders one silence's full display string: WHAT it
// covers -- honest about rule-wide vs entity-wide vs one exact pair vs
// a true global mute, never a bare "Silenced" that hides how broad it
// actually is -- plus how much time is left, e.g. "Silenced (every
// entity on this rule) · 3h left". An exact (rule_id AND entity both
// set) pair needs no extra qualifier: the row it's rendered against
// already names both.
export function silenceLabel(s: SilenceLike, nowSec: number): string {
  const scope =
    s.scope === 'all'
      ? 'everything'
      : s.rule_id && s.entity
        ? null
        : s.rule_id
          ? 'every entity on this rule'
          : s.entity
            ? 'every rule on this entity'
            : 'everything';
  const remaining = s.until - nowSec;
  const timeLeft = remaining <= 0 ? 'expiring' : `${fmtDuration(remaining)} left`;
  return scope ? `Silenced (${scope}) · ${timeLeft}` : `Silenced · ${timeLeft}`;
}

// SILENCE_PRESET_HOURS backs the Active row's own snooze menu (1h/8h/
// 24h/7d -- Task 10's own contract).
export const SILENCE_PRESET_HOURS: { label: string; hours: number }[] = [
  { label: '1h', hours: 1 },
  { label: '8h', hours: 8 },
  { label: '24h', hours: 24 },
  { label: '7d', hours: 24 * 7 },
];

// --- ordering --------------------------------------------------------------

// SEVERITY_RANK is store.Event's own three-slot vocabulary (info <
// warning < alert -- see EventFeedItem.svelte's identical mapping),
// NOT containerStatus.ts's four-slot HealthStatus one: a firing
// alert's own Severity field always carries this narrower vocabulary.
const SEVERITY_RANK: Record<string, number> = { alert: 2, warning: 1, info: 0 };

export interface SortableAlert {
  severity: string;
  fired_at: number;
}

// sortActiveAlerts orders the Active section: severity descending (the
// worst problem first), then fired_at ascending within a severity --
// the oldest still-unresolved problem surfaces first, matching how
// overviewStatus.ts's own anomaly ordering favors what's been wrong
// longest. Returns a NEW array; never mutates its input.
export function sortActiveAlerts<T extends SortableAlert>(alerts: T[]): T[] {
  return [...alerts].sort((a, b) => {
    const rankDiff = (SEVERITY_RANK[b.severity] ?? 0) - (SEVERITY_RANK[a.severity] ?? 0);
    if (rankDiff !== 0) return rankDiff;
    return a.fired_at - b.fired_at;
  });
}

// alertEntityHref links an Active row's own entity to wherever it lives
// -- container kind to its detail page, disk/unraid kind to the Storage
// page (no per-slot deep link, same as eventHref.ts's own STORAGE_KIND_
// PREFIXES); host/gpu have no per-entity page to link to at all. Unlike
// eventHref, this takes an alert INSTANCE's bare entity-kind vocabulary
// (host|container|disk|gpu|unraid, store.AlertRule.Kind), not an
// event's own dot-namespaced kind string -- the two are different
// vocabularies over the same underlying concepts.
export function alertEntityHref(kind: string, entity: string): string | null {
  if (kind === 'container') return entity ? `#/containers/${encodeURIComponent(entity)}` : null;
  if (kind === 'disk' || kind === 'unraid') return '#/storage';
  return null;
}

// --- value/threshold formatting --------------------------------------------

// formatMetricValue renders a live threshold-rule reading with its own
// unit -- the Active row's "value vs threshold" cell. array.started is
// the one metric with no natural unit (a plain 1/0 gate, not a scale);
// every metric not named here (an event rule's instances carry no
// numeric value at all, and Metric is "" for those) falls back to a
// bare one-decimal number rather than guessing a unit.
export function formatMetricValue(metric: string, value: number): string {
  switch (metric) {
    case 'temp.c':
      return `${value.toFixed(1)} °C`;
    case 'cpu.total':
    case 'mem.used_pct':
    case 'fs.used_pct':
    case 'mem.limit_pct':
      return `${value.toFixed(1)}%`;
    case 'array.started':
      return value >= 1 ? 'started' : 'stopped';
    default:
      return value.toFixed(1);
  }
}

// --- channels ---------------------------------------------------------------

// channelLabel renders one delivery channel's own Channel.ID() (alert/
// dispatch.go: "notify" or "webhook:<target-id>") as a friendly label
// for the Channels strip -- the raw id is a wire identifier, not
// copy meant for a human.
export function channelLabel(id: string): string {
  if (id === 'notify') return 'Unraid notifications';
  if (id.startsWith('webhook:')) return `Webhook: ${id.slice('webhook:'.length)}`;
  return id;
}

// --- rule descriptions (Task 11) -------------------------------------------

export interface DescribableRule {
  type: 'threshold' | 'event';
  kind: string;
  entity_glob: string;
  entity_class: string;
  metric: string;
  op: string;
  threshold: number;
  for_seconds: number;
  event_kinds: string;
  severity: string;
}

const SEVERITY_VERB: Record<string, string> = { warning: 'Warn', alert: 'Alert', info: 'Note' };

function severityVerb(severity: string): string {
  return SEVERITY_VERB[severity] ?? 'Alert';
}

// describeScope renders WHO a rule watches in plain words -- "the
// host", "the array", "any disk", "any NVMe disk", "container
// \"sonarr\"" -- from its kind/entity_glob/entity_class, the same three
// fields MatchEntity/MatchClass (internal/alert/rule.go) actually
// evaluate against, so this description can never claim a scope the
// rule doesn't really have.
function describeScope(kind: string, entityGlob: string, entityClass: string): string {
  if (kind === 'host') return 'the host';
  if (kind === 'unraid') return entityGlob === 'array' ? 'the array' : entityGlob || 'unraid';

  const noun = kind === 'disk' ? 'disk' : kind === 'container' ? 'container' : kind === 'gpu' ? 'GPU' : kind;
  if (entityGlob === '*') {
    if (entityClass === 'nvme') return `any NVMe ${noun}`;
    if (entityClass === '!nvme') return `any non-NVMe ${noun}`;
    return `any ${noun}`;
  }
  if (entityGlob.endsWith('*')) return `any ${noun} named "${entityGlob.slice(0, -1)}*"`;
  return `${noun} "${entityGlob}"`;
}

// describeDurationWords spells a sustained-for/timeout window out in
// prose ("10 minutes", "90 seconds", "1.5 hours") -- deliberately NOT
// format.ts's fmtDuration, whose compact "2h14m" shape reads as a
// clock, not a sentence.
function describeDurationWords(seconds: number): string {
  if (seconds < 60) return `${seconds} second${seconds === 1 ? '' : 's'}`;
  if (seconds < 3600) {
    const m = Math.round(seconds / 60);
    return `${m} minute${m === 1 ? '' : 's'}`;
  }
  const h = Math.round((seconds / 3600) * 10) / 10;
  return `${h} hour${h === 1 ? '' : 's'}`;
}

// describeThresholdComparison renders a threshold rule's own op+metric+
// value as the plain-language predicate half of the sentence -- e.g.
// "goes over 55 °C" or "goes over 90% full". array.started (the one
// metric with no natural unit, see formatMetricValue) is special-cased
// entirely rather than forced through the generic "goes under 1" shape,
// which reads as a database column, not a sentence.
function describeThresholdComparison(metric: string, op: string, threshold: number): string {
  const verb = op === '<' ? 'drops below' : 'goes over';
  switch (metric) {
    case 'temp.c':
      return `${verb} ${threshold} °C`;
    case 'fs.used_pct':
      return `${verb} ${threshold}% full`;
    case 'mem.limit_pct':
      return `${verb} ${threshold}% of its memory limit`;
    case 'cpu.total':
    case 'mem.used_pct':
      return `${verb} ${threshold}%`;
    default:
      return `${verb} ${threshold}`;
  }
}

// EVENT_RULE_DESCRIPTIONS covers the fixed set of builtin event rules
// (Task 11: "threshold rules only in v1" for user-created ones, so this
// never needs to describe an event rule it doesn't already know by id)
// with hand-written, specific prose -- clearer than any generic
// event_kinds-derived formula could read for a fixed five-rule
// vocabulary.
const EVENT_RULE_DESCRIPTIONS: Record<string, string> = {
  'container-unhealthy': 'Alert when any container becomes unhealthy',
  'container-oom': 'Alert when any container is killed for using too much memory',
  'container-exit-nonzero': 'Warn when any container exits with an error',
  'disk-errors': 'Alert when any disk reports new errors',
  'parity-errors': 'Alert when a parity check finishes with a warning or worse',
};

// describeRule renders one rule's own numbers as a single plain-English
// sentence, e.g. "Warn when any non-NVMe disk goes over 55 °C for 10
// minutes" -- Task 11's own "one-line plain-English restatement" next
// to every rule in the editor, generated fresh from the rule's current
// fields (never a static per-id string for a THRESHOLD rule) so an
// edited number is reflected immediately, with no separate copy to keep
// in sync. id is accepted separately from the rest of the shape because
// only the fixed builtin EVENT rules need it (see EVENT_RULE_DESCRIPTIONS'
// own doc); a user-created rule's id plays no part in a threshold
// description at all.
export function describeRule(id: string, r: DescribableRule): string {
  if (r.type === 'event') {
    return EVENT_RULE_DESCRIPTIONS[id] ?? `${severityVerb(r.severity)} on ${r.event_kinds || 'a matching event'}`;
  }
  if (r.metric === 'array.started') {
    return `${severityVerb(r.severity)} when the array stops running for ${describeDurationWords(r.for_seconds)}`;
  }
  const scope = describeScope(r.kind, r.entity_glob, r.entity_class);
  const comparison = describeThresholdComparison(r.metric, r.op, r.threshold);
  return `${severityVerb(r.severity)} when ${scope} ${comparison} for ${describeDurationWords(r.for_seconds)}`;
}

// containerAnomaly: Container Detail's "why does this need me" banner
// (Scott: "if I click into a container that says it needs me, I expect
// to see some explanation about why it needs me instead of having to
// try and figure out what's alerting"). Pure derivation, same split as
// every other lib/*.ts helper -- the Svelte component only renders what
// this decides.
//
// Deliberately its OWN 3-way classification, not a reuse of
// containerHealthStatus's 4-color dot verbatim: that function's
// "warning" bucket also covers "created" and "paused", both intentional,
// nothing-to-explain states (see containerRunState's own doc on why a
// never-started container has nothing to monitor) -- surfacing a banner
// for those would explain a non-problem. What IS reused is the
// underlying state/health READING itself: health only counts while
// running (post-R2.8 semantics), the exact gate unhealthyContainerNames
// already applies for Overview's own anomaly list, so a stopped
// container's stale "unhealthy" health string can never masquerade as a
// live health-check failure here either.
import type { GantryEvent } from './api';
import { describeExitCode } from './exitCode';
import { fmtRelTime } from './format';
import type { HealthStatus } from './containerStatus';

export type ContainerAnomalyKind = 'unhealthy' | 'restarting' | 'stopped';

// deriveContainerAnomalyKind decides WHETHER this container currently
// has something worth explaining, and which of the three shapes it is.
// null covers running+healthy (the common case -- no banner at all) AND
// created/paused/any other unrecognized transitional state: those are
// either fine or ambiguous, not a confirmed problem this banner should
// assert an explanation for.
export function deriveContainerAnomalyKind(state: string, health: string): ContainerAnomalyKind | null {
  if (state === 'running') {
    return health === 'unhealthy' ? 'unhealthy' : null;
  }
  if (state === 'restarting') return 'restarting';
  if (state === 'exited' || state === 'dead') return 'stopped';
  return null;
}

// containerAnomalySeverity reuses the exact HealthStatus vocabulary (and,
// for unhealthy/stopped, the exact colors) containerHealthStatus already
// assigns the SAME container's header HealthDot -- the banner's tint
// deliberately echoes the dot the user just clicked through on, not a
// second, independently-decided color language.
export function containerAnomalySeverity(kind: ContainerAnomalyKind): HealthStatus {
  switch (kind) {
    case 'unhealthy':
      return 'critical';
    case 'restarting':
      return 'warning';
    case 'stopped':
      return 'serious';
  }
}

// stoppedHeadline composes the exit-code table (exitCode.ts) into the
// "Stopped — exit code N (...)" headline verbatim -- undefined (no exit
// code known, e.g. state "dead" or a container that predates this field)
// reads as a plain "Stopped", never a fabricated "exit code 0".
function stoppedHeadline(exitCode: number | undefined): string {
  if (exitCode === undefined) return 'Stopped';
  const meaning = describeExitCode(exitCode);
  return meaning ? `Stopped — exit code ${exitCode} (${meaning})` : `Stopped — exit code ${exitCode}`;
}

// containerAnomalyHeadline renders the banner's own plain-language
// headline for a derived kind.
export function containerAnomalyHeadline(kind: ContainerAnomalyKind, exitCode: number | undefined): string {
  switch (kind) {
    case 'unhealthy':
      return 'Failing its health check';
    case 'restarting':
      return 'Restarting repeatedly';
    case 'stopped':
      return stoppedHeadline(exitCode);
  }
}

export interface ContainerAnomaly {
  kind: ContainerAnomalyKind;
  headline: string;
  severity: HealthStatus;
}

// deriveContainerAnomaly composes the three functions above into the one
// call ContainerDetail actually needs: null when there's nothing to
// explain (no banner at all), otherwise everything the banner's headline
// and tint need.
export function deriveContainerAnomaly(state: string, health: string, exitCode: number | undefined): ContainerAnomaly | null {
  const kind = deriveContainerAnomalyKind(state, health);
  if (!kind) return null;
  return { kind, headline: containerAnomalyHeadline(kind, exitCode), severity: containerAnomalySeverity(kind) };
}

// --- Evidence -----------------------------------------------------------

// EVIDENCE_KINDS is the subset of registry.go's own event vocabulary
// that answers "why" for a container -- container.start also carries
// plain boot events, but a restart detail ("restart count N") is exactly
// the kind of corroborating history this list exists to surface, and a
// bare start is harmless clutter at worst.
const EVIDENCE_KINDS: ReadonlySet<string> = new Set(['container.health', 'container.start', 'container.die', 'container.oom']);

// MAX_EVIDENCE_LINES caps the banner at a glanceable handful -- this is a
// quick "why", not a full history (the Events view and this page's own
// chart markers already cover that).
const MAX_EVIDENCE_LINES = 4;

// describeEvidenceEvent renders one event's own evidence-line text in
// plain language -- deliberately NOT GantryEvent.Detail verbatim (a raw
// "restart count 4" or "state: exited" reads as debug output, not an
// answer to "why does this need me").
function describeEvidenceEvent(e: GantryEvent): string {
  switch (e.Kind) {
    case 'container.health':
      return `Became ${e.Detail || 'unhealthy'}`;
    case 'container.oom':
      return 'Killed by the out-of-memory killer';
    case 'container.die':
      return e.Detail ? `Stopped (${e.Detail})` : 'Stopped';
    case 'container.start':
      return e.Detail ? `Started (${e.Detail})` : 'Started';
    default:
      return e.Detail || e.Kind;
  }
}

export interface AnomalyEvidence {
  ts: number;
  text: string;
  relTime: string;
}

// containerAnomalyEvidence picks the most recent evidence-worthy events
// (already entity-filtered by the caller's own fetchEvents query -- see
// ContainerDetail's existing events fetch) and renders each as one
// {text, relTime} line, newest first. Sorts defensively by ts rather
// than trusting the caller's own ordering, so this stays correct
// regardless of how `events` was fetched. nowMs threads through to
// fmtRelTime for deterministic tests, same convention fmtRelTime itself
// already establishes.
export function containerAnomalyEvidence(events: GantryEvent[], nowMs?: number): AnomalyEvidence[] {
  return events
    .filter((e) => EVIDENCE_KINDS.has(e.Kind))
    .slice()
    .sort((a, b) => b.TS - a.TS)
    .slice(0, MAX_EVIDENCE_LINES)
    .map((e) => ({ ts: e.TS, text: describeEvidenceEvent(e), relTime: fmtRelTime(e.TS, nowMs) }));
}

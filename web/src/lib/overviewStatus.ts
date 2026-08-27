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
// so on. The one deliberate exception is "stopped" containers, which
// stays a single aggregated anomaly no matter how many are stopped --
// state=running is a common, often-intentional condition in a home-lab
// (containers turned off on purpose), and per-container rows for that
// would flood the attention module exactly the way the design's own
// "spend each device once, stay calm" rule warns against. The headline's
// own count is always anomalies.length, so "N things" and "N rows in the
// attention module" can never disagree.
import { diskUsagePct } from './disks';
import { fmtPct } from './format';
import type { HealthStatus } from './containerStatus';

export type OverviewAnomaly =
  | { kind: 'unhealthy'; name: string }
  | { kind: 'stopped'; count: number }
  | { kind: 'disk-usage'; slot: string; usagePct: number }
  | { kind: 'disk-errors'; slot: string; errors: number }
  | { kind: 'array-stopped' }
  | { kind: 'source-critical'; source: string; detail: string };

export interface OverviewStatusInput {
  unhealthyNames: string[];
  stoppedCount: number;
  // array['array.started'] straight off the live frame -- undefined
  // (no unraid/array data yet) is deliberately NOT treated as "stopped":
  // an absent reading isn't evidence of a problem, only 0 is.
  arrayStarted: number | undefined;
  disks: Record<string, Record<string, number>> | undefined | null;
  sources: Record<string, string> | undefined | null;
}

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
  if (input.stoppedCount > 0) {
    anomalies.push({ kind: 'stopped', count: input.stoppedCount });
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
}

// describeAnomaly renders one anomaly's row text -- kept a separate pure
// function (rather than inlined per-kind in the component template) so
// the exact wording, including every pluralization boundary, is
// unit-tested the same way format.ts's own formatters are.
export function describeAnomaly(a: OverviewAnomaly): AnomalyText {
  switch (a.kind) {
    case 'unhealthy':
      return {
        severity: 'critical',
        title: `${a.name} is unhealthy`,
        detail: 'Failing its health check.',
        linkContainer: a.name,
      };
    case 'stopped':
      return {
        severity: 'warning',
        title: a.count === 1 ? '1 container is stopped' : `${a.count} containers are stopped`,
        detail: '',
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
  }
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

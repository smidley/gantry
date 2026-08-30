// Status-band thresholds for the handful of metrics whose raw NUMBER
// should read as elevated on its own, without a separate dot/badge next
// to it (Scott: "colors of numbers for metrics should reflect their
// status... high cpu usage would be red" -- read here as the calmer
// status-serious/critical family, not literal red, per the app-wide
// "don't use alarm colors for non-bad things" rule elsewhere in this
// rollout). Deliberately NOT every metric: gpu busy, net/io rates, and a
// container's own cpu host-share have no family here at all, so a caller
// showing one of those just never calls band() -- there's no "unbanded"
// input to accept and no-op on.
//
// Boundaries are strict ">" (matching Storage's own pre-existing disk-
// usage-90% convention), so a value sitting exactly ON a threshold reads
// as the band below it, not the one above.

export type MetricFamily =
  | 'host.cpu'
  | 'host.mem'
  | 'disk.capacity'
  | 'disk.temp'
  | 'disk.temp.nvme'
  | 'container.mem_limit_pct';

export type Band = 'normal' | 'warn' | 'serious' | 'critical';

interface FamilyThresholds {
  warn: number;
  serious: number;
  // critical (optional): disk temps have no fourth tier -- a value can
  // read at most "serious" for those two families, never "critical".
  critical?: number;
}

// THRESHOLDS is the compiled-in FALLBACK table -- today's original
// values, verbatim. It never changes at runtime and is what band()
// falls back to for a family setBands hasn't (yet, or ever) supplied:
// before the boot fetch resolves, for an older server with no /api/
// alerts/rules route, or for a family whose matching rule a user
// deleted outright. band() must never throw or read "undefined" for any
// of the six families, in any of those cases.
const THRESHOLDS: Record<MetricFamily, FamilyThresholds> = {
  'host.cpu': { warn: 70, serious: 85, critical: 95 },
  'host.mem': { warn: 70, serious: 85, critical: 95 },
  'disk.capacity': { warn: 70, serious: 90, critical: 95 },
  'disk.temp': { warn: 45, serious: 55 },
  'disk.temp.nvme': { warn: 60, serious: 70 },
  'container.mem_limit_pct': { warn: 75, serious: 85, critical: 95 },
};

// runtimeBands is the live override table setBands installs (Task 12's
// band unification): populated from GET /api/alerts/rules so a value's
// display color and the alert engine's own fire/clear numbers can never
// disagree -- one set of numbers, not two hand-kept copies. Starts
// empty (every family reads from the compiled-in THRESHOLDS fallback
// above) until the first successful load.
let runtimeBands: Partial<Record<MetricFamily, FamilyThresholds>> = {};

// AlertRuleBandLike is the narrow slice of AlertRuleDTO (web/src/lib/
// api.ts) setBands actually needs -- kept local rather than importing
// api.ts's full DTO so this module has no dependency on the wider API
// surface, just like every other lib/*.ts pure helper.
export interface AlertRuleBandLike {
  band_family: string;
  warn_threshold: number;
  threshold: number;
  critical_threshold: number;
}

// setBands installs the runtime band table derived from the current
// alert rules: each rule whose band_family names one of the six
// families above contributes its own warn_threshold/threshold/
// critical_threshold as that family's warn/serious/critical -- the
// exact numbers the alert engine fires and clears on, so a value's
// color can never disagree with whether it's actually alerting.
//
// A DISABLED rule still supplies its bands: disabling delivery answers
// "should this page tell me", which is a different question from "what
// does this number's color mean" -- silently re-coloring the whole app
// because someone muted a notification would be a confusing side
// effect, so enabled is deliberately never checked here.
//
// critical_threshold's wire value 0 means "this family has no fourth
// tier" (store.AlertRule's own doc: disk.temp/disk.temp.nvme are the
// two examples), NOT "critical at anything above zero" -- `|| undefined`
// maps that literal 0 to undefined so band() correctly stops at
// "serious" for those two families, exactly like the compiled-in
// fallback table does. A real critical threshold is never legitimately
// 0 (every seeded one is comfortably positive), so this coercion never
// discards a meaningful value.
//
// A rule whose band_family isn't one of the six known names (or is "",
// the "this rule drives no display band" default) is silently skipped
// -- not an error, just nothing to derive.
export function setBands(rules: AlertRuleBandLike[]): void {
  const next: Partial<Record<MetricFamily, FamilyThresholds>> = {};
  for (const r of rules) {
    if (!isMetricFamily(r.band_family)) continue;
    next[r.band_family] = {
      warn: r.warn_threshold,
      serious: r.threshold,
      critical: r.critical_threshold || undefined,
    };
  }
  runtimeBands = next;
}

function isMetricFamily(v: string): v is MetricFamily {
  return v in THRESHOLDS;
}

// band classifies one metric family's value into normal/warn/serious/
// critical, reading the runtime band table when setBands has supplied
// this family, the compiled-in fallback otherwise. Non-finite input
// reads as "normal" -- an absent/malformed reading is not evidence of a
// problem.
export function band(family: MetricFamily, value: number): Band {
  if (!Number.isFinite(value)) return 'normal';
  const t = runtimeBands[family] ?? THRESHOLDS[family];
  if (t.critical !== undefined && value > t.critical) return 'critical';
  if (value > t.serious) return 'serious';
  if (value > t.warn) return 'warn';
  return 'normal';
}

// Mixed 55% status-color/45% --ink rather than the bare token: --ink
// darkens the mix in light mode and lightens it in dark mode, the
// direction that improves contrast against the page in BOTH themes --
// --status-warning/-serious read under AA-large contrast on their own
// against the light theme's near-white page (verified: ~1.7:1/~2.5:1),
// the same problem the R2 work's amber (--series-4) had as text, fixed
// the same way there (StatTile's own pairedTint).
const BAND_TOKEN: Record<Band, string | undefined> = {
  // No green for normal -- ink is the calm default; a status color is
  // reserved for something actually elevated.
  normal: undefined,
  warn: 'color-mix(in oklab, var(--status-warning) 55%, var(--ink) 45%)',
  serious: 'color-mix(in oklab, var(--status-serious) 55%, var(--ink) 45%)',
  critical: 'color-mix(in oklab, var(--status-critical) 55%, var(--ink) 45%)',
};

// bandToken maps a Band to the CSS color a value should render in --
// undefined for "normal" (render plain ink, the caller's own default).
export function bandToken(b: Band): string | undefined {
  return BAND_TOKEN[b];
}

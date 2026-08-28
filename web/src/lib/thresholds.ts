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

const THRESHOLDS: Record<MetricFamily, FamilyThresholds> = {
  'host.cpu': { warn: 70, serious: 85, critical: 95 },
  'host.mem': { warn: 70, serious: 85, critical: 95 },
  'disk.capacity': { warn: 70, serious: 90, critical: 95 },
  'disk.temp': { warn: 45, serious: 55 },
  'disk.temp.nvme': { warn: 60, serious: 70 },
  'container.mem_limit_pct': { warn: 75, serious: 85, critical: 95 },
};

// band classifies one metric family's value into normal/warn/serious/
// critical. Non-finite input reads as "normal" -- an absent/malformed
// reading is not evidence of a problem.
export function band(family: MetricFamily, value: number): Band {
  if (!Number.isFinite(value)) return 'normal';
  const t = THRESHOLDS[family];
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

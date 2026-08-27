// Small, pure metric-shaping helpers shared across views: summing a
// dynamic-suffix family of keys (host-level per-device disk IO),
// extracting a dynamic-name family of keys (per-share usage), a fixed
// display order for GPU engines, a magnitude->color-ramp step, and a
// parity-progress ETA estimate. Kept framework-free and dependency-free
// so every one of these is trivially unit-testable.

// GPU_ENGINE_ORDER is the one fixed display/series-slot order for GPU
// engines used everywhere the UI shows more than one (Overview's GPU
// strip, Container Detail's per-engine chart) -- render=1, video=2,
// video-enhance=3, copy=4, matching Task 19's own GPU view contract, so
// engine identity maps to the same categorical slot across every view
// rather than each page inventing its own order.
export const GPU_ENGINE_ORDER = ['render', 'video', 'video-enhance', 'copy'] as const;
export type GPUEngine = (typeof GPU_ENGINE_ORDER)[number];

// sumMetricsByPattern sums every value in `metrics` whose key starts with
// `prefix` and ends with `suffix`. This covers both a flat key (prefix
// and suffix directly adjacent, e.g. "diskio.read_bps") and a key with a
// dynamic middle segment (e.g. real host.go's per-device
// "diskio.sda.read_bps") with one rule: fake mode emits the flat shape
// (no per-device dimension at all), real mode emits one key per device,
// and a flat key trivially also "ends with" its own suffix segment, so
// no special-casing between the two shapes is needed. Non-finite values
// are skipped rather than poisoning the sum.
export function sumMetricsByPattern(
  metrics: Record<string, number> | undefined | null,
  prefix: string,
  suffix: string,
): number {
  if (!metrics) return 0;
  let sum = 0;
  for (const [key, val] of Object.entries(metrics)) {
    if (key.startsWith(prefix) && key.endsWith(suffix) && Number.isFinite(val)) {
      sum += val;
    }
  }
  return sum;
}

export interface ShareUsage {
  name: string;
  usedBytes: number;
}

// SHARE_USED_BYTES_RE matches shares.go's own wire shape exactly:
// "share.<slug>.used_bytes", one key per share, all on the "unraid"/
// "array" entity alongside array.started/parity.*/mover.running. The
// slug segment never contains a literal "." (collect.SlugSegment maps
// every non [a-z0-9_-] character, dots included, to "_"), so a plain
// greedy capture between the two fixed dots is unambiguous.
const SHARE_USED_BYTES_RE = /^share\.(.+)\.used_bytes$/;

// sharesFromMetrics extracts every per-share usage figure from the
// "unraid"/"array" entity's metric map, sorted by used bytes descending
// (ties broken by name ascending, for deterministic output) -- the
// Storage view's Shares table order.
export function sharesFromMetrics(metrics: Record<string, number> | undefined | null): ShareUsage[] {
  const out: ShareUsage[] = [];
  if (!metrics) return out;
  for (const [key, val] of Object.entries(metrics)) {
    const m = SHARE_USED_BYTES_RE.exec(key);
    if (m && Number.isFinite(val)) out.push({ name: m[1], usedBytes: val });
  }
  out.sort((a, b) => (b.usedBytes !== a.usedBytes ? b.usedBytes - a.usedBytes : a.name.localeCompare(b.name)));
  return out;
}

// enginesPresent reads which of GPU_ENGINE_ORDER's engines have a
// "engine.<name>.busy_pct" (device entity) or "gpu.<name>.busy_pct"
// (container entity) key present in metrics, returning them in
// GPU_ENGINE_ORDER's fixed order (not object insertion order, which
// JSON's map-shaped gpu/metrics fields don't guarantee reflects anything
// meaningful anyway).
export function enginesPresent(metrics: Record<string, number> | undefined | null, keyFor: (engine: string) => string): GPUEngine[] {
  if (!metrics) return [];
  return GPU_ENGINE_ORDER.filter((engine) => metrics[keyFor(engine)] !== undefined);
}

// seqStep buckets a 0-100 percentage onto tokens.css's 7-stop sequential
// ramp (--seq-100..--seq-700), returning a ready-to-use var() reference --
// a coarse step function, not a continuous interpolation, since the ramp
// itself only has 7 discrete stops. Shared by every magnitude-fill bar
// (ArrayCard's per-pool bars, Storage's per-disk usage bars).
export function seqStep(pct: number): string {
  const step = Math.min(7, Math.max(1, Math.ceil((pct / 100) * 7)));
  return `var(--seq-${step}00)`;
}

// etaFromProgress estimates seconds-to-completion from two progress
// percentage observations spaced apart in time -- a parity check's
// backend-reported progress_pct and speed_bps use unrelated units
// (percent vs. bytes/sec) with no total-size figure in the DTO to
// convert between them, so ETA here is derived purely from the
// progress percentage's own rate of change across live frames, not from
// speed_bps at all (speed_bps is still shown, just not fed into this
// math). Returns null when a rate can't be estimated yet -- identical or
// non-advancing samples, a non-positive rate, or non-finite input --
// rather than a fabricated number; callers should render that as
// "calculating..." (an empty state as direction, not a moody blank).
export function etaFromProgress(prevTs: number, prevPct: number, ts: number, pct: number): number | null {
  if (![prevTs, prevPct, ts, pct].every(Number.isFinite)) return null;
  const dt = ts - prevTs;
  const dPct = pct - prevPct;
  if (dt <= 0 || dPct <= 0) return null;
  const remaining = 100 - pct;
  if (remaining <= 0) return 0;
  return remaining / (dPct / dt);
}

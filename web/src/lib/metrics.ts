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

// GPU_ENTITY_ENGINE_ORDER extends GPU_ENGINE_ORDER with the Nvidia v1
// path's one pseudo-engine name, "gpu" -- nvidia.go's NvidiaCollector
// records a single entity-level series, "engine.gpu.busy_pct", since
// nvidia-smi's CSV output has no per-engine breakdown the way the DRM
// fdinfo path's drm-engine-* counters do (the whole GPU's utilization is
// modeled as one series named after the entity kind itself). Scoped to
// GPU-ENTITY code (GPUStrip, the GPU view) only: container-side
// attribution ("gpu.<engine>.busy_pct") never uses "gpu" as an engine
// name -- Nvidia's v1 per-container data is VRAM only (gpu.nvidia.mem_mib)
// -- so GPU_ENGINE_ORDER itself, and every container-attribution call
// site that uses it, stays unchanged.
export const GPU_ENTITY_ENGINE_ORDER = [...GPU_ENGINE_ORDER, 'gpu'] as const;

// gpuTitle builds a GPU entity's card title from its vendor+driver meta
// (server.GPUMetaDTO, threaded through the live frame's gpu_meta map) --
// "Intel GPU (i915)" -- rather than the bare entity id, which for the
// DRM fdinfo path IS a raw PCI address (e.g. "0000:00:02.0": Scott's own
// question, "what does this mean?"). Falls back to the bare entity id
// when no meta is known for it (a snapshot that hasn't caught up yet, or
// a caller with none at all) -- the same fallback the backend's own
// vendor lookup uses for a missing/unreadable vendor file.
export function gpuTitle(entity: string, meta: { vendor: string; driver: string } | undefined): string {
  if (!meta) return entity;
  return `${meta.vendor} GPU (${meta.driver})`;
}

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

// sumSeriesPoints is sumMetricsByPattern's history-shaped counterpart:
// where that function sums a pattern-matched key family at one instant
// (the live frame), this sums several metrics' own /api/series results
// across TIME, aligning by ts -- Overview's ioReadRing seeds this way
// because "diskio.<dev>.read_bps" has no fixed metric name to fetch by
// itself (real mode; fake mode's flat "diskio.read_bps" is the
// single-array degenerate case of the same sum). A ts present in only
// some of the inputs still contributes just those metrics' values, the
// same graceful-partial behavior sumMetricsByPattern has for a metric
// that's momentarily absent from one frame; non-finite avg/ts entries
// are skipped rather than poisoning the sum. Output is sorted ascending
// by ts, matching every other ring-point shape in this app.
export function sumSeriesPoints(pointArrays: [number, number, number][][]): [number, number][] {
  const sums = new Map<number, number>();
  for (const points of pointArrays) {
    for (const [ts, avg] of points) {
      if (!Number.isFinite(ts) || !Number.isFinite(avg)) continue;
      sums.set(ts, (sums.get(ts) ?? 0) + avg);
    }
  }
  return Array.from(sums.entries()).sort((a, b) => a[0] - b[0]);
}

// keysByPattern is sumMetricsByPattern's discovery-side sibling, same
// prefix+suffix rule (a flat key -- fake mode's "net.rx_bps" -- matches
// on its own, the identical degenerate case sumMetricsByPattern's own
// doc describes; no special-casing between the flat and per-device
// shapes here either). Used when a caller needs the CONCRETE key names
// themselves rather than a live-frame sum -- seeding a sum-of-pattern
// sparkline has to ask /api/series for history by exact metric name, and
// there's no fixed name to ask for when the real device/interface count
// is only known from whatever's actually present in the current frame.
export function keysByPattern(
  metrics: Record<string, number> | undefined | null,
  prefix: string,
  suffix: string,
): string[] {
  if (!metrics) return [];
  return Object.keys(metrics).filter((k) => k.startsWith(prefix) && k.endsWith(suffix));
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

// enginesPresent reads which of `order`'s engines have a
// "engine.<name>.busy_pct" (device entity) or "gpu.<name>.busy_pct"
// (container entity) key present in metrics, returning them in `order`'s
// fixed sequence (not object insertion order, which JSON's map-shaped
// gpu/metrics fields don't guarantee reflects anything meaningful
// anyway). `order` defaults to GPU_ENGINE_ORDER for every existing
// container-attribution call site; a GPU-entity caller (GPUStrip, the
// GPU view) passes GPU_ENTITY_ENGINE_ORDER instead, to also recognize
// the Nvidia path's solo "gpu" pseudo-engine.
export function enginesPresent(
  metrics: Record<string, number> | undefined | null,
  keyFor: (engine: string) => string,
  order: readonly string[] = GPU_ENGINE_ORDER,
): string[] {
  if (!metrics) return [];
  return order.filter((engine) => metrics[keyFor(engine)] !== undefined);
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

// NICE_CEILING_STEPS is the classic 1-2-5-per-decade "nice round number"
// ladder -- 10 is included so every decade's own top step is exact
// (100, 1000, ...) rather than falling through to the next decade's 1x.
const NICE_CEILING_STEPS = [1, 2, 5, 10];

// niceCeiling rounds `max` up to the next step of that ladder, scaled to
// max's own magnitude -- a byte-rate leaderboard's bar scale (Top
// Consumers, net/io) reads against this instead of the busiest row's own
// value, so a quiet fleet's bars don't all read as nearly-full just
// because nothing happening right now is any busier (Scott: "NET can
// obviously go higher, but it looks like it's maxed out"). Works at any
// magnitude (B/s through GB/s and beyond) since it operates on the raw
// number, not a formatted string. 0 for a non-positive/non-finite max --
// nothing to scale against.
export function niceCeiling(max: number): number {
  if (!Number.isFinite(max) || max <= 0) return 0;
  const exp = Math.floor(Math.log10(max));
  const base = 10 ** exp;
  for (const step of NICE_CEILING_STEPS) {
    const candidate = step * base;
    if (candidate >= max) return candidate;
  }
  return 10 * base; // unreachable (NICE_CEILING_STEPS's own last step is 10), kept so this stays total
}

// parityIsRunning applies the parity-progress wire semantic var.go's
// collector and the fake generator both now guarantee: a check is
// running iff parity.progress_pct is present AND strictly positive.
// Presence alone used to be sufficient (the metric was only ever written
// while a check was active), but the finish tick now ALSO writes one
// explicit terminal sample of 0 for both parity.progress_pct/speed_bps
// (see var.go's tickArray and fake.go's emitArray) precisely so the live
// frame has a permanent, unambiguous "not running" value instead of the
// last real sample (e.g. 99.9%) sticking forever -- the store's live
// ring has no sample expiry of its own. A genuinely-running check can
// never legitimately read exactly 0: progress is pos/size*100 with
// pos>0 by definition of "running", a strictly positive float for any
// pos>=1 (down to a small fraction of a percent on a multi-TB array), so
// ">0" never misclassifies an early-but-real check as idle.
export function parityIsRunning(pct: number | undefined): boolean {
  return pct !== undefined && pct > 0;
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

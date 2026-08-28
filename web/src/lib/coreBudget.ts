// Pure segment math for the CPU breakdown page's core-budget ribbon: one
// horizontal bar, hostCores wide, split into per-container segments (top
// N by cpu.cores, categorical series colors), an "others" bucket for the
// long tail, an unattributed-host segment, and whatever's left over as
// free headroom. Kept framework-free, matching every other lib/*.ts pure
// helper in this app.

export interface CoreSegment {
  // key identifies the segment for a keyed {#each} -- a container name,
  // or the fixed pseudo-keys 'others'/'unattributed' below.
  key: string;
  label: string;
  cores: number;
  // colorVar is a ready-to-use CSS color value (a var(--series-N)
  // reference for a named container, or a plain color-mix() expression
  // for the two neutral buckets) -- the component applies it directly,
  // no further lookup.
  colorVar: string;
}

export interface CoreBudget {
  segments: CoreSegment[];
  // freeCores: host capacity nothing (tracked or not) is using right
  // now -- the bar's own empty remainder.
  freeCores: number;
}

// MAX_NAMED_SEGMENTS caps how many containers get their own named,
// individually-colored segment before the rest fold into one "Others"
// bucket -- tokens.css's own categorical palette only has 8 series slots,
// and a ribbon with 20+ slivers each fighting for a sliver of hue stops
// being readable well before then anyway.
export const MAX_NAMED_SEGMENTS = 8;

const OTHERS_COLOR = 'var(--ink-2)';
const UNATTRIBUTED_COLOR = 'color-mix(in oklab, var(--ink) 30%, transparent)';

export interface CoreBudgetContainer {
  name: string;
  cores: number;
}

// buildCoreBudget lays out one host's worth of CPU cores: named segments
// (sorted desc by cores, ties broken by name for deterministic output),
// then "Others" for the long tail past MAX_NAMED_SEGMENTS, then an
// unattributed-host segment (hostCpuTotalPct's own share of hostCores,
// minus every container segment above -- kernel/other host processes,
// clamped >=0 so a momentary reading where containers overshoot a
// slightly-stale host total never goes negative), then whatever's left
// of hostCores as free headroom. hostCores<=0 (no reading yet) returns
// an empty budget rather than dividing by it anywhere.
export function buildCoreBudget(
  hostCores: number,
  hostCpuTotalPct: number,
  containers: CoreBudgetContainer[],
): CoreBudget {
  if (!Number.isFinite(hostCores) || hostCores <= 0) return { segments: [], freeCores: 0 };

  const sorted = containers
    .filter((c) => Number.isFinite(c.cores) && c.cores > 0)
    .sort((a, b) => (b.cores !== a.cores ? b.cores - a.cores : a.name.localeCompare(b.name)));
  const named = sorted.slice(0, MAX_NAMED_SEGMENTS);
  const rest = sorted.slice(MAX_NAMED_SEGMENTS);

  const segments: CoreSegment[] = named.map((c, i) => ({
    key: c.name,
    label: c.name,
    cores: c.cores,
    colorVar: `var(--series-${i + 1})`,
  }));

  const restSum = rest.reduce((sum, c) => sum + c.cores, 0);
  if (restSum > 0) {
    segments.push({ key: 'others', label: `Others (${rest.length})`, cores: restSum, colorVar: OTHERS_COLOR });
  }

  const namedSum = named.reduce((sum, c) => sum + c.cores, 0);
  const attributedSum = namedSum + restSum;
  const hostTotalCores = Number.isFinite(hostCpuTotalPct) ? Math.max(0, (hostCpuTotalPct / 100) * hostCores) : 0;
  const unattributedCores = Math.max(0, hostTotalCores - attributedSum);
  if (unattributedCores > 0) {
    segments.push({ key: 'unattributed', label: 'Unattributed (host)', cores: unattributedCores, colorVar: UNATTRIBUTED_COLOR });
  }

  const usedCores = attributedSum + unattributedCores;
  const freeCores = Math.max(0, hostCores - usedCores);

  return { segments, freeCores };
}

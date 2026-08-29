// Ordering stability for a LIVE leaderboard/hero-chart selection --
// replaces topFromFrame.ts's old withGracePeriod/reorderByLastDisplayedValue
// pair, which only ever smoothed a single boundary flicker or a one-tick
// Tween-lag mismatch. Neither survives the real box: 38+ containers with
// a dozen genuinely tied around 0.1% CPU have real per-tick sampling
// noise (not a display artifact) big enough to reorder the instant-value
// ranking almost every 2s tick -- reproduced live: the old grace
// bookkeeping never converges under that much churn, ballooning past its
// own 2x-limit bound (opacity:0 rows piling up, hundreds of abandoned
// zero-duration transitions stuck on one node) instead of settling.
//
// The fix ranks by a ROLLING AVERAGE instead of the instant sample (noise
// this small mostly washes out over a minute), breaks a tie by entity
// name so an exact tie never flaps on its own, and gates EVICTIONS behind
// ENTRY_EXIT_TICKS consecutive ticks of real evidence PLUS a
// RESORT_INTERVAL_SEC cooldown since the last one -- so a member only
// ever leaves rarely and deliberately. There is deliberately no
// symmetric "entry" gate: applyMembershipHysteresis only ever removes.
// Whoever fills the resulting free slot is decided separately, by
// stableTopN's own top-up step below, and admitted IMMEDIATELY, next
// call, with no streak or cooldown of its own -- there's no incumbent
// left to protect once the slot is genuinely open, and a genuine
// challenger still only ever gets in by way of the SAME 3-tick eviction
// that made room for it.
//
// That split -- an eviction (pure removal) lands on its own call, its
// own free slot's fill lands on the NEXT one -- exists for a second
// reason too, found the hard way while verifying this fix against real
// churn: asking Svelte's keyed each-block to intro and outro in the SAME
// reconciliation could leave one row permanently stuck either way
// (confirmed live: an entering row frozen invisible, or a leaving one
// frozen visible, its transition never finishing) -- animate:flip
// present or not. Never combining the two in one adopted update sidesteps
// that regardless of its exact Svelte-side cause.
export const ROLLING_WINDOW_SEC = 60;
export const ENTRY_EXIT_TICKS = 3;
export const RESORT_INTERVAL_SEC = 10;

interface Sample {
  ts: number;
  value: number;
}

export interface RankStabilityState {
  samples: Map<string, Sample[]>;
  lastRow: Map<string, unknown>;
  leaveStreak: Map<string, number>;
  displayOrder: Map<string, string[]>;
  lastResort: Map<string, number>;
}

export function createRankStabilityState(): RankStabilityState {
  return {
    samples: new Map(),
    lastRow: new Map(),
    leaveStreak: new Map(),
    displayOrder: new Map(),
    lastResort: new Map(),
  };
}

// ns namespaces every per-entity bookkeeping key by metricKey too (same
// convention withGracePeriod/reorderByLastDisplayedValue used) -- one
// state object safely serves every resource tab a caller can switch
// between (Overview's compact module, the Metrics page).
function ns(metricKey: string, entity: string): string {
  return `${metricKey}::${entity}`;
}

// recordAndAverage feeds one fresh sample into entity's own rolling
// window (oldest-first, pruned to windowSec) and returns the window's
// average INCLUDING this sample -- the ranking key stableTopN uses
// instead of the instant value.
export function recordAndAverage(
  state: RankStabilityState,
  metricKey: string,
  entity: string,
  value: number,
  nowSec: number,
  windowSec: number = ROLLING_WINDOW_SEC,
): number {
  const key = ns(metricKey, entity);
  let list = state.samples.get(key);
  if (!list) {
    list = [];
    state.samples.set(key, list);
  }
  list.push({ ts: nowSec, value });
  const cutoff = nowSec - windowSec;
  while (list.length > 0 && list[0].ts < cutoff) list.shift();
  let sum = 0;
  for (const s of list) sum += s.value;
  return sum / list.length;
}

// canResortNow is the re-sort cadence gate on its own, exact-tested in
// isolation from stableTopN's own bookkeeping: true once intervalSec has
// elapsed since lastResortSec, or immediately the first time
// (lastResortSec undefined -- nothing has ever been evicted yet).
export function canResortNow(
  lastResortSec: number | undefined,
  nowSec: number,
  intervalSec: number = RESORT_INTERVAL_SEC,
): boolean {
  return lastResortSec === undefined || nowSec - lastResortSec >= intervalSec;
}

// applyMembershipHysteresis decides which of `members` (stableTopN's own
// current, full-capacity display -- always what's actually shown, never
// an independently-advancing tracker) survive this tick: one leaves only
// once it's ranked outside the natural top-`limit` of `rankedEntities`
// for `ticks` consecutive calls in a row, not merely outranked once.
// Pure removal only -- see this module's own doc for why entry is a
// separate, unconditional concern.
export function applyMembershipHysteresis(
  state: RankStabilityState,
  metricKey: string,
  members: string[],
  rankedEntities: string[],
  limit: number,
  ticks: number = ENTRY_EXIT_TICKS,
): string[] {
  const naturalTopSet = new Set(rankedEntities.slice(0, Math.max(0, limit)));
  const survivors: string[] = [];
  for (const entity of members) {
    const key = ns(metricKey, entity);
    if (naturalTopSet.has(entity)) {
      state.leaveStreak.delete(key);
      survivors.push(entity);
      continue;
    }
    const n = (state.leaveStreak.get(key) ?? 0) + 1;
    if (n >= ticks) {
      state.leaveStreak.delete(key); // evicted -- outranked `ticks` calls running
    } else {
      state.leaveStreak.set(key, n);
      survivors.push(entity); // still hanging on
    }
  }
  return rankedEntities.filter((e) => survivors.includes(e));
}

export interface StableRow {
  entity: string;
  value: number;
  linkable?: boolean;
}

// stableTopN is the one entry point a live leaderboard or hero-chart
// selection calls every tick: record this tick's value into the rolling
// average, rank by that average (ties broken by entity name -- the same
// deterministic tie-break topFromFrame's own instant ranking uses), then
// resolve membership in exactly one of three mutually exclusive ways per
// call -- see this module's own doc for why they're kept mutually
// exclusive:
//   1. First call for this metricKey: populate immediately, no wait.
//   2. Currently under `limit` (a past eviction's own free slot):
//      top up immediately from the best-ranked entities not already
//      shown -- a pure intro, nothing leaving alongside it.
//   3. At full capacity: evict, if hysteresis AND the cadence gate both
//      agree -- a pure outro, nothing entering alongside it (its own
//      free slot fills on the NEXT call, via case 2).
// Values stay live on every call regardless -- only membership/order is
// gated, never the numbers a caller's own Tween glides toward.
//
// linkable:false rows (the attribution page's own pinned "unattributed"
// summary) never enter ranking at all -- they trail every real row,
// unconditionally, every call, the same carve-out reorderByLastDisplayedValue
// used to have.
export function stableTopN<T extends StableRow>(
  rows: readonly T[],
  state: RankStabilityState,
  metricKey: string,
  limit: number,
  nowSec: number,
): T[] {
  const pinned = rows.filter((r) => r.linkable === false);
  const ranked = rows.filter((r) => r.linkable !== false);

  for (const row of ranked) {
    state.lastRow.set(ns(metricKey, row.entity), row);
  }

  const withAvg = ranked.map((row) => ({
    entity: row.entity,
    avg: recordAndAverage(state, metricKey, row.entity, row.value, nowSec),
  }));
  withAvg.sort((a, b) => (b.avg !== a.avg ? b.avg - a.avg : a.entity.localeCompare(b.entity)));
  const rankedEntities = withAvg.map((w) => w.entity);

  const currentDisplay = state.displayOrder.get(metricKey) ?? [];
  let members: string[];

  if (currentDisplay.length === 0) {
    members = rankedEntities.slice(0, limit);
  } else if (currentDisplay.length < limit) {
    const shown = new Set(currentDisplay);
    const fill = rankedEntities.filter((e) => !shown.has(e)).slice(0, limit - currentDisplay.length);
    members = [...currentDisplay, ...fill];
  } else {
    const survivors = applyMembershipHysteresis(state, metricKey, currentDisplay, rankedEntities, limit);
    if (survivors.length < currentDisplay.length && canResortNow(state.lastResort.get(metricKey), nowSec)) {
      members = survivors;
      state.lastResort.set(metricKey, nowSec);
    } else {
      members = currentDisplay;
    }
  }

  const display = rankedEntities.filter((e) => members.includes(e));
  state.displayOrder.set(metricKey, display);

  const displayedRows: T[] = [];
  for (const entity of display) {
    const row = state.lastRow.get(ns(metricKey, entity)) as T | undefined;
    if (row !== undefined) displayedRows.push(row);
  }
  return pinned.length > 0 ? [...displayedRows, ...(pinned as T[])] : displayedRows;
}

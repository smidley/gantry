// Stable per-container line/swatch color: a deterministic hash of the
// container's own NAME into tokens.css's 10-slot categorical palette, so
// a given container renders the same hue everywhere that colors it by
// identity (the Metrics hero chart, its own legend chips, Compare, the
// core-budget ribbon) and stays that color across reloads and sessions.
//
// This replaces "color follows chart POSITION" (index i -> --series-
// ${i+1}), which every one of those surfaces used to do: the same rule
// TopBarList's own doc calls out as wrong for anything whose membership
// churns ("a leaderboard's entities churn from one window to the next,
// so per-rank color would misleadingly suggest a stable identity"), and
// the exact mechanism behind two more visible bugs -- a container
// repainting a new color the instant the Metrics hero's live ranking
// reorders (rankStability.ts's own rolling-average re-sort is frequent,
// deliberately so), and its line/chip disagreeing with its OWN color on
// Compare or the core-budget ribbon, which each ran their own,
// independent position assignment.
//
// A hash collision (two different names landing on the same slot) is
// accepted rather than disambiguated with e.g. a dash-variant overflow
// scheme (the Storage chart's own answer to needing MORE than 10
// distinct lines at once, see disks.ts's diskChartDash) -- every caller
// of this function caps at 10 simultaneous lines anyway (the palette's
// own size: MAX_HERO_LINES, MAX_COMPARE_MEMBERS, MAX_NAMED_SEGMENTS), so
// a collision there is already the best a positional scheme could do
// too. Stability -- the SAME container always the SAME color, forever --
// is the one property that actually matters here.
const SERIES_COLOR_COUNT = 10;

// hashName: a small, fast, well-distributed 32-bit string hash (the
// classic djb2-xor variant) -- deterministic across every JS engine and
// session (no reliance on iteration order, object identity, or
// Math.random), which is the one hard requirement: the same name must
// always fold to the same slot.
function hashName(name: string): number {
  let hash = 5381;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) + hash) ^ name.charCodeAt(i);
  }
  return hash >>> 0; // unsigned, so the modulo below is never negative
}

// containerColor returns the bare CSS custom-property NAME (e.g.
// "--series-7"), not a var(...) reference -- matching every existing
// colorVar convention this app's chart series already hand TimeChart
// (theme.ts's resolveToken accepts either form, but every real call
// site so far uses the bare name).
export function containerColor(name: string): string {
  const slot = hashName(name) % SERIES_COLOR_COUNT;
  return `--series-${slot + 1}`;
}

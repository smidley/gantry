// Pure, DOM-free theme-token helpers split out from theme.svelte.ts
// (whose $state/$derived-using ThemeStore needs the Svelte compiler
// plugin, which vitest.config.ts doesn't wire in) -- mirrors livering.
// ts/livering.svelte.ts's own split, for the same reason: this is the
// piece of resolveToken's logic that's worth unit-testing directly.

// extractTokenName recognizes a CSS custom property reference in
// EITHER form a caller might supply -- a bare name ("--series-1", the
// shape TimeChart's series.colorVar and GPUEntityCard's SERIES_VAR
// pass) or a full var() reference ("var(--series-1)", the shape
// Sparkline/StatTile's own color props pass) -- returning just the bare
// name either way, or null for anything else (a literal color, an
// already-resolved hex value, ...).
//
// Both forms MUST resolve the same way: TimeChart's own resolveToken
// call sites were passing the bare form and silently getting back that
// same bare string UNCHANGED (resolveToken's original regex only
// recognized the var()-wrapped form) -- an invalid canvas strokeStyle,
// which every browser silently ignores rather than erroring, so every
// multi-series chart's lines rendered in whatever fallback/inherited
// color happened to be active instead of their assigned categorical
// color. Reproduced live: every TimeChart line rendered as flat ink-2
// gray in both themes, indistinguishable from each other despite each
// series naming a different --series-N slot.
export function extractTokenName(value: string): string | null {
  const trimmed = value.trim();
  const varMatch = /^var\((--[\w-]+)\)$/.exec(trimmed);
  if (varMatch) return varMatch[1];
  return /^--[\w-]+$/.test(trimmed) ? trimmed : null;
}

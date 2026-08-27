// Theme preference: system|light|dark, persisted to localStorage,
// applied as data-theme on <html> (tokens.css keys off that attribute).
// TimeChart/Sparkline re-read the resolved CSS custom properties via
// `theme.resolved` whenever it changes (see their own $effect) since
// uPlot draws literal colors onto a canvas, not live var() references.
import { extractTokenName } from './theme';

export type ThemePreference = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'gantry.theme';
const CYCLE_ORDER: ThemePreference[] = ['system', 'light', 'dark'];

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches === true;
}

function resolve(pref: ThemePreference): ResolvedTheme {
  return pref === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : pref;
}

function loadStoredPreference(): ThemePreference {
  if (typeof localStorage === 'undefined') return 'system';
  const stored = localStorage.getItem(STORAGE_KEY);
  return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system';
}

class ThemeStore {
  preference = $state<ThemePreference>(loadStoredPreference());
  resolved = $derived(resolve(this.preference));

  constructor() {
    this.#apply(this.resolved);
    if (typeof window !== 'undefined' && window.matchMedia) {
      // The OS-level media query is not itself reactive to Svelte --
      // when it fires and we're following "system", re-apply the
      // newly-resolved value directly rather than relying on
      // `resolved`'s derivation to notice on its own.
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
        if (this.preference === 'system') this.#apply(resolve('system'));
      });
    }
  }

  #apply(resolved: ResolvedTheme) {
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', resolved);
    }
  }

  set(pref: ThemePreference) {
    this.preference = pref;
    this.#apply(resolve(pref));
    if (typeof localStorage !== 'undefined') localStorage.setItem(STORAGE_KEY, pref);
  }

  // cycle advances system -> light -> dark -> system, the toggle order
  // the design direction specifies.
  cycle() {
    const next = CYCLE_ORDER[(CYCLE_ORDER.indexOf(this.preference) + 1) % CYCLE_ORDER.length];
    this.set(next);
  }
}

export const theme = new ThemeStore();

// resolveToken resolves a CSS custom property reference -- either a
// bare name ("--series-1") or a full var() reference ("var(--series-1)"),
// see extractTokenName's own doc for why both forms must work -- to its
// current computed value. Canvas-based rendering (uPlot, in Sparkline/
// TimeChart) can't consume a live var() reference the way DOM/CSS
// can -- ctx.fillStyle/strokeStyle are parsed outside any cascade
// context, so a var() reference OR a bare custom-property name in that
// string is simply invalid there. Callers re-resolve this whenever
// `theme.resolved` changes. A value that's neither form (e.g. one an
// entity-color hash has already resolved to a literal) passes through
// unchanged.
export function resolveToken(value: string): string {
  if (typeof document === 'undefined') return value;
  const tokenName = extractTokenName(value);
  if (!tokenName) return value;
  return getComputedStyle(document.documentElement).getPropertyValue(tokenName).trim() || value;
}

// withAlpha appends an alpha channel to a 6-digit "#rrggbb" hex color,
// producing 8-digit "#rrggbbaa" -- canvas fillStyle (uPlot's
// series.fill, for Sparkline/TimeChart area fills) accepts this
// directly. alphaPct is 0-100.
export function withAlpha(hex: string, alphaPct: number): string {
  const clamped = Math.min(100, Math.max(0, alphaPct));
  const alphaByte = Math.round((clamped / 100) * 255);
  return `${hex}${alphaByte.toString(16).padStart(2, '0')}`;
}

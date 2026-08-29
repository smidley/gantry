// Hash router: no history API, no path-based routing -- the whole app
// lives at "/" and every view is addressed by a "#/..." fragment, which
// never leaves the browser. `route` is a plain svelte/store (not a
// $state rune -- this module is a singleton with a native
// `window.addEventListener('hashchange', ...)` side effect, and a
// store's `$route` auto-subscription sugar works from a plain .ts
// module the same as it would from a .svelte.ts one).
import { writable } from 'svelte/store';

export type RouteName =
  | 'overview'
  | 'containers'
  | 'container-detail'
  | 'top'
  | 'storage'
  | 'gpu'
  | 'events'
  | 'settings'
  | 'not-found';

export interface Route {
  name: RouteName;
  params: Record<string, string>;
}

interface RouteDef {
  name: RouteName;
  // Static segments must match exactly; a segment starting with ":"
  // captures into params under that name (e.g. ":name" -> params.name).
  pattern: string[];
}

const routeDefs: RouteDef[] = [
  { name: 'overview', pattern: [] },
  { name: 'containers', pattern: ['containers'] },
  { name: 'container-detail', pattern: ['containers', ':name'] },
  { name: 'top', pattern: ['top'] },
  // Overview's compact Top Consumers switcher deep-links "View all" to
  // the SAME resource, e.g. "#/top/mem" -- a second pattern for the same
  // route name, same shape as container-detail's own :name capture,
  // rather than a query string (this router has no query-string support,
  // and every other param anywhere in this table is already a segment).
  { name: 'top', pattern: ['top', ':resource'] },
  { name: 'storage', pattern: ['storage'] },
  { name: 'gpu', pattern: ['gpu'] },
  { name: 'events', pattern: ['events'] },
  { name: 'settings', pattern: ['settings'] },
];

// parseHash parses a location.hash value into a Route. An empty hash
// ("", from a fragment-less URL) and a bare "#" or "#/" all parse the
// same as the overview route -- every one of them has zero path
// segments once the leading "#" and empty splits are stripped.
export function parseHash(hash: string): Route {
  const path = hash.replace(/^#/, '');
  const segments = path.split('/').filter((s) => s.length > 0);

  for (const def of routeDefs) {
    if (def.pattern.length !== segments.length) continue;
    const params: Record<string, string> = {};
    let matched = true;
    for (let i = 0; i < def.pattern.length; i++) {
      const part = def.pattern[i];
      if (part.startsWith(':')) {
        params[part.slice(1)] = decodeURIComponent(segments[i]);
      } else if (part !== segments[i]) {
        matched = false;
        break;
      }
    }
    if (matched) return { name: def.name, params };
  }
  return { name: 'not-found', params: {} };
}

// route is the app-wide current-route store, kept in sync with
// window.location.hash via the native "hashchange" event. Guarded so
// importing this module under Node (vitest) never touches `window`,
// which doesn't exist there -- parseHash itself has no such dependency
// and is fully testable on its own.
export const route = writable<Route>({ name: 'overview', params: {} });

if (typeof window !== 'undefined') {
  const sync = () => route.set(parseHash(window.location.hash));
  sync();
  window.addEventListener('hashchange', sync);
}

// NavItem is one entry in the single nav table Sidebar (desktop) and
// TabBar (mobile) both render from -- one source of truth for icon,
// label, and target hash, so the two presentations can never drift.
// icon is raw inline SVG markup (rendered via {@html} at the call
// site): the approved-deps list has no icon font/library, and an
// inline <svg> is the simplest way to keep every icon self-hosted with
// zero extra requests.
export interface NavItem {
  name: RouteName;
  hash: string;
  label: string;
  icon: string;
  // mobileLabel, when set, is what TabBar renders instead of `label` --
  // Sidebar always uses `label`. Only needed for the two labels wide
  // enough to need a mid-word wrap at TabBar's ~50px-per-item width
  // even after word-boundary wrapping; each embeds a soft hyphen
  // (U+00AD) at a sensible syllable point so the fallback break, if it
  // happens at all, lands somewhere readable rather than at an
  // arbitrary character count.
  mobileLabel?: string;
}

const strokeIcon = (paths: string): string =>
  `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths}</svg>`;

const ICON_OVERVIEW = strokeIcon(
  '<rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/>',
);
const ICON_CONTAINERS = strokeIcon(
  '<path d="M3 7l9-4 9 4-9 4-9-4Z"/><path d="M3 7v10l9 4 9-4V7"/><path d="M12 11v10"/>',
);
const ICON_TOP = strokeIcon('<path d="M4 20V10"/><path d="M12 20V4"/><path d="M20 20v-7"/>');
const ICON_STORAGE = strokeIcon(
  '<rect x="3" y="3" width="18" height="7" rx="1.5"/><rect x="3" y="14" width="18" height="7" rx="1.5"/><circle cx="7" cy="6.5" r="0.8" fill="currentColor" stroke="none"/><circle cx="7" cy="17.5" r="0.8" fill="currentColor" stroke="none"/>',
);
const ICON_GPU = strokeIcon(
  '<rect x="4" y="6" width="16" height="12" rx="1.5"/><path d="M8 2v4M16 2v4M8 18v4M16 18v4M2 9h2M2 15h2M20 9h2M20 15h2"/>',
);
const ICON_EVENTS = strokeIcon(
  '<path d="M18 8a6 6 0 1 0-12 0c0 5-2 6-2 6h16s-2-1-2-6"/><path d="M9.5 20a2.5 2.5 0 0 0 5 0"/>',
);
const ICON_SETTINGS = strokeIcon(
  '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h0a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h0a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v0a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/>',
);

// routes is the single nav table -- 7 entries for Phase 3 (Alerts, the
// spec's 7th view, arrives with Phase 4's alerting engine; the 8th
// route below, container-detail, is reached by clicking a container
// row rather than through nav).
export const routes: NavItem[] = [
  { name: 'overview', hash: '#/', label: 'Overview', icon: ICON_OVERVIEW },
  {
    name: 'containers',
    hash: '#/containers',
    label: 'Containers',
    icon: ICON_CONTAINERS,
    mobileLabel: 'Contain­ers',
  },
  // label is "Metrics" (route/name stay "top"/"#/top" -- only the display
  // text renamed, once the page stopped being just a leaderboard and
  // grew a real per-metric chart): short enough on its own that it
  // needs no mobileLabel soft-hyphen variant, unlike its "Top Consumers"
  // predecessor.
  { name: 'top', hash: '#/top', label: 'Metrics', icon: ICON_TOP },
  { name: 'storage', hash: '#/storage', label: 'Storage', icon: ICON_STORAGE },
  { name: 'gpu', hash: '#/gpu', label: 'GPU', icon: ICON_GPU },
  { name: 'events', hash: '#/events', label: 'Events', icon: ICON_EVENTS },
  { name: 'settings', hash: '#/settings', label: 'Settings', icon: ICON_SETTINGS },
];

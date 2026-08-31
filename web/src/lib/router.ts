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
  | 'compare'
  | 'top'
  | 'storage'
  | 'maintenance'
  | 'gpu'
  | 'events'
  | 'insights'
  | 'alerts'
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
  // compare's own ":names" capture holds the RAW comma-joined segment
  // (e.g. "jellyfin,plex") after this loop's ordinary single-decode pass
  // -- lib/compareRoute.ts's parseCompareNames splits it the rest of the
  // way; see that file's own doc for why a second decode per name isn't
  // needed (every real docker container name is already restricted to a
  // charset with no comma in it). A bare "#/compare" (or "#/compare/",
  // its own trailing slash reducing to the same zero-segment tail --
  // filtered out above like every other route's) has no :names segment
  // at all -- same two-pattern shape as "top"/"top/:resource" above --
  // so it still routes to the Compare view (params.names undefined,
  // parseCompareNames(undefined) === []) rather than falling through to
  // not-found: Compare's own "no containers selected" hint is the
  // sensible landing for it, not a dead end.
  { name: 'compare', pattern: ['compare'] },
  { name: 'compare', pattern: ['compare', ':names'] },
  { name: 'top', pattern: ['top'] },
  // Overview's compact Top Consumers switcher deep-links "View all" to
  // the SAME resource, e.g. "#/top/mem" -- a second pattern for the same
  // route name, same shape as container-detail's own :name capture,
  // rather than a query string (this router has no query-string support,
  // and every other param anywhere in this table is already a segment).
  { name: 'top', pattern: ['top', ':resource'] },
  { name: 'storage', pattern: ['storage'] },
  { name: 'maintenance', pattern: ['maintenance'] },
  { name: 'gpu', pattern: ['gpu'] },
  { name: 'events', pattern: ['events'] },
  { name: 'insights', pattern: ['insights'] },
  // Phase 5 Task 14: the interaction map is a `.segmented` MODE inside
  // the Insights view, not a separate page (see routes[] below's own
  // doc on why it gets no second nav item) -- so "#/insights/map" is a
  // second pattern for the SAME route name, capturing into
  // params.mode, exactly the "top"/"top/:resource" precedent just
  // above (Overview's compact switcher deep-linking into a specific
  // resource tab within TopConsumers). Insights.svelte reads
  // params.mode itself; any value other than "map" (including absent)
  // means "let the view's own default -- map when something's active,
  // list otherwise -- decide."
  { name: 'insights', pattern: ['insights', ':mode'] },
  { name: 'alerts', pattern: ['alerts'] },
  { name: 'settings', pattern: ['settings'] },
];

// parseHash parses a location.hash value into a Route. An empty hash
// ("", from a fragment-less URL) and a bare "#" or "#/" all parse the
// same as the overview route -- every one of them has zero path
// segments once the leading "#" and empty splits are stripped.
export function parseHash(hash: string): Route {
  const raw = hash.replace(/^#/, '');
  const [path, queryString = ''] = raw.split('?', 2);
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
    if (matched) {
      const query = new URLSearchParams(queryString);
      for (const [key, value] of query) params[key] = value;
      return { name: def.name, params };
    }
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
// ICON_MAINTENANCE: two open sockets on a shaft -- a plain wrench
// abstraction (fill:none from strokeIcon already renders each circle as
// a ring, not a disk), picked over a trash-can glyph so the nav itself
// reads as "upkeep," not "delete" -- the page's own destructive weight
// lives in its confirm dialogs, not the icon that gets you there.
const ICON_MAINTENANCE = strokeIcon('<circle cx="6.5" cy="6.5" r="3.25"/><circle cx="17.5" cy="17.5" r="3.25"/><path d="M8.8 8.8l6.4 6.4"/>');
const ICON_GPU = strokeIcon(
  '<rect x="4" y="6" width="16" height="12" rx="1.5"/><path d="M8 2v4M16 2v4M8 18v4M16 18v4M2 9h2M2 15h2M20 9h2M20 15h2"/>',
);
const ICON_EVENTS = strokeIcon(
  '<path d="M18 8a6 6 0 1 0-12 0c0 5-2 6-2 6h16s-2-1-2-6"/><path d="M9.5 20a2.5 2.5 0 0 0 5 0"/>',
);
// ICON_INSIGHTS: two linked nodes -- a plain link/graph glyph, distinct
// from Alerts' own triangle and Events' own bell (Phase 5's own icon
// contract, mirroring Alerts' Task 10 precedent above): an insight is a
// RELATIONSHIP between two things, and this is the simplest shape that
// reads as one without borrowing either sibling's silhouette.
const ICON_INSIGHTS = strokeIcon('<circle cx="6" cy="7" r="3"/><circle cx="18" cy="17" r="3"/><path d="M8.4 9.1l7.2 5.8"/>');
// ICON_ALERTS: a warning triangle with an exclamation mark -- distinct
// from Events' own bell glyph just above (Events keeps the bell; Phase
// 4's Alerts view gets the triangle, per the plan's own icon contract).
const ICON_ALERTS = strokeIcon('<path d="M12 3.5 2.5 20h19L12 3.5Z"/><path d="M12 9.5v4.5"/><path d="M12 17.7v.01"/>');
const ICON_SETTINGS = strokeIcon(
  '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h0a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h0a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v0a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/>',
);

// routes is the single nav table -- 10 entries now (Phase 5 adds
// Insights, the plan's own "exactly ONE nav item, not two" -- the
// interaction map lives INSIDE it as a `.segmented` mode, see
// routeDefs' own doc above, rather than claiming a second slot).
// Insights sits directly above Alerts ("an explanation precedes an
// escalation" -- Task 11's own ordering rationale); container-detail is
// reached by clicking a container row rather than through nav, so it's
// never in this table at all.
//
// TabBar.svelte/Sidebar.svelte are NOT updated for this tenth entry as
// part of this change (both currently sit as uncommitted, foreign-owned
// work in this tree -- see this branch's own notes) -- both already
// render generically off this exported array (TabBar's moreRoutes
// filter is a subtractive `!primaryNames.has(...)`, so a name absent
// from every explicit list lands in its own "More" overflow rather than
// vanishing), so Insights surfaces on mobile without any edit here.
// Sidebar's own three groups (monitorRoutes/operateRoutes/systemRoutes)
// are each an EXPLICIT allow-list, though, and none currently names
// 'insights' -- until that file's own foreign edits land and add it
// (naturally alongside 'events'/'alerts' in its "Operate" group), the
// desktop sidebar has no visible link to this route; #/insights (and
// TabBar's More menu) both still reach it directly.
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
  {
    name: 'maintenance',
    hash: '#/maintenance',
    label: 'Maintenance',
    icon: ICON_MAINTENANCE,
    mobileLabel: 'Mainte­nance',
  },
  { name: 'gpu', hash: '#/gpu', label: 'GPU', icon: ICON_GPU },
  { name: 'events', hash: '#/events', label: 'Events', icon: ICON_EVENTS },
  { name: 'insights', hash: '#/insights', label: 'Insights', icon: ICON_INSIGHTS },
  { name: 'alerts', hash: '#/alerts', label: 'Alerts', icon: ICON_ALERTS },
  { name: 'settings', hash: '#/settings', label: 'Settings', icon: ICON_SETTINGS },
];

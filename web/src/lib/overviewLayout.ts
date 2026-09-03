// The Overview's saved module arrangement, as pure data -- the client
// half of internal/server/api_layout.go, deliberately DOM-free and
// rune-free so vitest can exercise every rule directly (the same
// motion.ts/motion.svelte.ts split, for the same reason).
//
// Everything here mirrors the Go side one-for-one: the same closed
// module set, the same two lane names, the same merge rule, the same
// ratio bounds and size steps. They are two implementations of one
// contract rather than a client that trusts the server, because the SPA
// has to render a usable layout the instant it loads -- before any GET
// has resolved -- and the edit-mode gestures have to produce their new
// document locally before the debounced PUT that persists it. Both
// sides' merges are unit-tested against the same cases; if they ever
// disagree the server's answer wins on the next load, since it is what
// actually got stored.
//
// The one thing this side owns alone is how many ROWS each height step
// buys (OVERVIEW_MODULES' own `rows`): that is a rendering decision, and
// the server has no business holding a number it can never act on.

import type { OverviewLayoutDTO } from './api';

// OverviewLayoutDoc IS the wire shape (api.ts's OverviewLayoutDTO) --
// aliased rather than redeclared so the two can never drift, and named
// for what it is locally: a whole document, never a partial patch.
export type OverviewLayoutDoc = OverviewLayoutDTO;

export type OverviewColumn = 'wide' | 'narrow';

// OverviewSize is one module's height step. 'normal' is the default and
// is stored by ABSENCE from the document's `sizes` map, so a layout
// nobody has resized carries no sizes at all.
export type OverviewSize = 'compact' | 'normal' | 'tall';

// OVERVIEW_SIZES is the segmented control's own order, smallest first --
// the same left-to-right reading the Top Consumers switcher it borrows
// its look from has.
export const OVERVIEW_SIZES: OverviewSize[] = ['compact', 'normal', 'tall'];

export function isOverviewSize(value: unknown): value is OverviewSize {
  return typeof value === 'string' && (OVERVIEW_SIZES as string[]).includes(value);
}

// OVERVIEW_LAYOUT_VERSION must equal api_layout.go's own
// overviewLayoutVersion. See that constant's doc for what does (a
// document SHAPE change -- 1 -> 2 added the ratio and the sizes) and
// does not (adding or retiring a module) bump it.
export const OVERVIEW_LAYOUT_VERSION = 2;

// The wide lane's share of the modules band. Mirrors api_layout.go's
// overviewRatioMin/Max/Default exactly -- see that block for why these
// particular bounds, and why the default is the band's own shipped
// 1.6:1 flex split rather than a round number.
export const OVERVIEW_RATIO_MIN = 0.6;
export const OVERVIEW_RATIO_MAX = 0.75;
export const OVERVIEW_RATIO_DEFAULT = 0.615;

// OVERVIEW_RATIO_STEP is the keyboard nudge -- one arrow press moves the
// split by one percentage point, which puts the whole 60-75 range 15
// presses across. Small enough to aim with, large enough that holding an
// arrow key crosses the range in about a second.
export const OVERVIEW_RATIO_STEP = 0.01;

// RATIO_PRECISION rounds a GESTURE's result (a drag, an arrow press),
// never a stored value: clampOverviewRatio has to stay a byte-for-byte
// mirror of the Go clamp, and a client that quietly rounded what it read
// would report "changed" against a server that hadn't. Three places
// matches the default's own precision and keeps the saved JSON readable.
const RATIO_PRECISION = 1000;

function roundRatio(ratio: number): number {
  return Math.round(ratio * RATIO_PRECISION) / RATIO_PRECISION;
}

export interface OverviewModule {
  id: string;
  column: OverviewColumn;
  // label names the module in the edit-mode chrome -- the drag handle's,
  // hide toggle's and size switcher's own accessible names, and the
  // ghost card's text once a module is hidden and its real content is
  // gone. It is UI copy and free to change; `id` is the thing that gets
  // persisted.
  label: string;
  // rows: how many list rows this module renders at each step, or null
  // for a module with no elastic body at all (see storage below).
  // A step is a ROW BUDGET, not a pixel height -- every module here
  // renders a list at a fixed row pitch, so an integer row count lands
  // on the page's existing vertical rhythm by construction, and `tall`
  // buys actual content (more events to read, more of the leaderboard)
  // instead of the same content in a taller box.
  rows: Record<OverviewSize, number> | null;
}

// OVERVIEW_MODULES is the closed known set, in default order within each
// column -- the mirror of api_layout.go's overviewModules table, and the
// only place a module's default home is written down on this side.
//
// Only the modules BAND is rearrangeable. The status headline, the
// attention chips, the fleet strip, the metrics rail and the GPU strip
// are all pinned and deliberately absent: burying the "needs you"
// surface under a drag gesture is exactly the failure mode this feature
// must not enable.
//
// The set CHANGED once, and the Go table's own doc carries the full
// story: the metrics rail LEFT it (pinned at the top of the page now --
// Scott: "Move the disk storage section down so that the CPU/Mem/Net/IO
// metrics are at the top of the overview page") and `storage` JOINED,
// taking the narrow lane the rail used to hold. The document version
// does not move for a module-set change; mergeOverviewLayout below
// already drops the retired id and appends the new one at its default,
// which is exactly what a layout saved before this release needs.
//
// The row budgets:
//
//   top-consumers  3 / 5 / 8   normal is the 5 the D2 compact-module
//                              brief shipped. compact still shows a
//                              podium; tall shows most of a 10-ish
//                              container fleet's real spread without
//                              leaving for the full Top Consumers view.
//   events         4 / 8 / 14  normal is the 8 the feed shipped with.
//                              compact keeps "what just happened"
//                              glanceable; tall reaches back far enough
//                              to actually read a restart's own
//                              surrounding sequence.
//
// storage carries null: the bay schematic draws one card per array
// member on a fixed grid, so its height is decided by how many disks
// exist rather than by any budget a step could set -- a "tall" storage
// module is the same twelve devices with more air between them, which is
// the dead space this page's layout passes have spent three rounds
// deleting. It gets no size control at all, exactly as the rail didn't.
export const OVERVIEW_MODULES: OverviewModule[] = [
  { id: 'top-consumers', column: 'wide', label: 'Top consumers', rows: { compact: 3, normal: 5, tall: 8 } },
  { id: 'events', column: 'wide', label: 'Recent events', rows: { compact: 4, normal: 8, tall: 14 } },
  { id: 'storage', column: 'narrow', label: 'Storage array', rows: null },
];

export function isKnownOverviewModule(id: string): boolean {
  return OVERVIEW_MODULES.some((m) => m.id === id);
}

// isResizableOverviewModule mirrors api_layout.go's overviewModule.Resizable:
// a module with no elastic body has no size, is offered no control, and
// has any stored size dropped by the merge.
export function isResizableOverviewModule(id: string): boolean {
  return OVERVIEW_MODULES.find((m) => m.id === id)?.rows != null;
}

// overviewModuleLabel falls back to the raw id rather than an empty
// string: an unlabelled control is worse than one labelled with a slug,
// and this can only ever be reached by a caller holding an id the table
// doesn't have -- which mergeOverviewLayout has already made impossible
// for anything rendered.
export function overviewModuleLabel(id: string): string {
  return OVERVIEW_MODULES.find((m) => m.id === id)?.label ?? id;
}

// clampOverviewRatio is the exact mirror of api_layout.go's function of
// the same name, down to the "0 means absent" rule: a v1 document, or
// any document that simply never mentioned a ratio, resolves to the
// default rather than to an unusable zero-width lane.
export function clampOverviewRatio(ratio: unknown): number {
  if (typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio === 0) return OVERVIEW_RATIO_DEFAULT;
  if (ratio < OVERVIEW_RATIO_MIN) return OVERVIEW_RATIO_MIN;
  if (ratio > OVERVIEW_RATIO_MAX) return OVERVIEW_RATIO_MAX;
  return ratio;
}

// overviewRatioFromDrag is the divider's whole arithmetic, pure so the
// pointer plumbing in Overview.svelte has nothing arithmetic left in it
// (the same split dragReorder.ts already draws for the module drag).
//
// `spanPx` is the width the two lanes actually DIVIDE -- their two rects
// added together, snapshotted at pointerdown -- not the band's own outer
// width: the flex gap and the divider itself sit between them and belong
// to neither, so measuring against the outer width would make the
// divider trail the pointer by a couple of percent over a full drag.
//
// A zero or missing span (a band measured before layout, a lane hidden
// mid-gesture) yields the starting ratio rather than a division by zero:
// a drag with nothing to divide moves nothing.
export function overviewRatioFromDrag(startRatio: number, dx: number, spanPx: number): number {
  if (!Number.isFinite(spanPx) || spanPx <= 0 || !Number.isFinite(dx)) return clampOverviewRatio(startRatio);
  return clampOverviewRatio(roundRatio(clampOverviewRatio(startRatio) + dx / spanPx));
}

// nudgeOverviewRatio is the keyboard half -- `steps` arrow presses' worth
// of movement, positive widening the wide lane. Rounded like the drag so
// a run of presses can't accumulate float dust into a ratio nobody could
// have typed.
export function nudgeOverviewRatio(ratio: number, steps: number): number {
  return clampOverviewRatio(roundRatio(clampOverviewRatio(ratio) + steps * OVERVIEW_RATIO_STEP));
}

// overviewLaneFlex turns the one saved fraction into the two flex-grow
// factors the band's lanes carry -- expressed in the band's own original
// notation, the wide lane relative to a narrow lane pinned at 1, so the
// default really does read as the 1.6 : 1 it shipped with.
//
// Why not the obvious {ratio, 1 - ratio}, which is the same proportion:
// flex distributes only sum(grow) of the free space when that sum is
// BELOW 1. A pair like 0.615 / 0.385 therefore leaves a LONE lane
// occupying 61.5% of the band with a 38% hole beside it -- quietly
// breaking the visibility-driven expansion (rule 2 in Overview.svelte's
// own doc: an emptied lane isn't rendered and the survivor spans the
// whole band). Normalizing the narrow lane to 1 keeps both factors at or
// above 1, so whichever lane survives fills the row on its own, whatever
// split happens to be saved. That is the width half of "a user setting
// never gets in the adaptive layout's way".
export function overviewLaneFlex(ratio: number): { wide: number; narrow: number } {
  const wide = clampOverviewRatio(ratio);
  return { wide: Math.round((wide / (1 - wide)) * 10_000) / 10_000, narrow: 1 };
}

// mergeOverviewLayout reconciles a stored (or in-flight, or hand-edited)
// document against the module set THIS build knows about -- the exact
// rule api_layout.go's function of the same name implements:
//
//   - an unknown id is dropped, silently;
//   - a known id the document never mentions is appended to the END of
//     its own default column, so a release that adds a module still
//     shows it to someone who saved a layout before it existed, without
//     shoving itself above whatever they deliberately put first;
//   - a duplicate keeps its first occurrence.
//
// A module already listed in `hidden` counts as placed and stays hidden.
//
// It is also the v1 -> v2 migration, for the same reason the Go one is:
// both new fields have a defined "absent". A missing/0 ratio becomes the
// default and an out-of-range one is clamped; `sizes` is normalized to
// its canonical form -- unknown id dropped, non-resizable module dropped,
// unrecognized step normalized to 'normal', and 'normal' itself dropped,
// because absence IS normal. A v1 document has neither field and comes
// out at the default split with nothing resized.
//
// The result always carries this build's version, three real arrays and
// a real sizes object.
export function mergeOverviewLayout(stored: Partial<OverviewLayoutDoc> | null | undefined): OverviewLayoutDoc {
  const merged: OverviewLayoutDoc = {
    version: OVERVIEW_LAYOUT_VERSION,
    wide: [],
    narrow: [],
    hidden: [],
    ratio: clampOverviewRatio(stored?.ratio),
    sizes: {},
  };
  const placed = new Set<string>();

  const keep = (dst: string[], src: unknown) => {
    if (!Array.isArray(src)) return;
    for (const id of src) {
      if (typeof id !== 'string' || !isKnownOverviewModule(id) || placed.has(id)) continue;
      placed.add(id);
      dst.push(id);
    }
  };
  keep(merged.wide, stored?.wide);
  keep(merged.narrow, stored?.narrow);
  keep(merged.hidden, stored?.hidden);

  for (const m of OVERVIEW_MODULES) {
    if (placed.has(m.id)) continue;
    placed.add(m.id);
    (m.column === 'narrow' ? merged.narrow : merged.wide).push(m.id);
  }

  const sizes = stored?.sizes;
  if (sizes !== null && typeof sizes === 'object') {
    for (const [id, size] of Object.entries(sizes as Record<string, unknown>)) {
      if (!isResizableOverviewModule(id) || !isOverviewSize(size) || size === 'normal') continue;
      merged.sizes[id] = size;
    }
  }
  return merged;
}

// defaultOverviewLayout is what a never-customized install renders and
// what "Reset layout" restores: every module at its default home, the
// band at its shipped split, nothing resized.
export function defaultOverviewLayout(): OverviewLayoutDoc {
  return mergeOverviewLayout(null);
}

// withoutModule strips one id from every list -- the shared first step
// of every gesture below, since a module has exactly one home and moving
// it anywhere means leaving the old one. The size map is carried through
// untouched: where a module SITS and how tall it is are independent, and
// dragging a card between lanes must not quietly resize it.
function withoutModule(doc: OverviewLayoutDoc, id: string): OverviewLayoutDoc {
  return {
    version: OVERVIEW_LAYOUT_VERSION,
    wide: doc.wide.filter((x) => x !== id),
    narrow: doc.narrow.filter((x) => x !== id),
    hidden: doc.hidden.filter((x) => x !== id),
    ratio: doc.ratio,
    sizes: { ...doc.sizes },
  };
}

// moveOverviewModule is the drop gesture: place `id` in `column` at
// `index`, wherever it came from (including out of `hidden` -- dragging
// a module back is a legitimate way to un-hide it, and the drop position
// is more specific than the default un-hide would be).
//
// `index` is counted against the target column WITHOUT the dragged
// module in it, which is exactly what the drop-target geometry reports
// (dragReorder.ts) -- so the removal below happens FIRST and the index
// needs no same-column adjustment. Out-of-range indices clamp rather
// than throw: a drop past the end of a lane means "last", which is what
// a user aiming below the final card intends.
export function moveOverviewModule(
  doc: OverviewLayoutDoc,
  id: string,
  column: OverviewColumn,
  index: number,
): OverviewLayoutDoc {
  if (!isKnownOverviewModule(id)) return doc;
  const next = withoutModule(doc, id);
  const list = column === 'narrow' ? next.narrow : next.wide;
  const at = Math.max(0, Math.min(Math.trunc(index), list.length));
  list.splice(at, 0, id);
  return next;
}

// hideOverviewModule takes a module out of both lanes and onto the
// hidden list. It keeps no position: `hidden` is genuinely "not placed",
// which is why showOverviewModule below re-places it the same way a
// brand-new module gets placed. Its SIZE survives being hidden -- a
// module brought back is the size it was, which is the least surprising
// answer and costs one map entry.
export function hideOverviewModule(doc: OverviewLayoutDoc, id: string): OverviewLayoutDoc {
  if (!isKnownOverviewModule(id)) return doc;
  const next = withoutModule(doc, id);
  next.hidden.push(id);
  return next;
}

// showOverviewModule brings a hidden module back at its DEFAULT home --
// by handing the merge a document that simply doesn't mention it
// anywhere, so the one "place an unplaced known id" rule serves both
// un-hiding and the forward-compat append. One rule, one behaviour.
export function showOverviewModule(doc: OverviewLayoutDoc, id: string): OverviewLayoutDoc {
  if (!isKnownOverviewModule(id)) return doc;
  return mergeOverviewLayout(withoutModule(doc, id));
}

export function isModuleHidden(doc: OverviewLayoutDoc, id: string): boolean {
  return doc.hidden.includes(id);
}

// --- ratio + size gestures -------------------------------------------------

// setOverviewRatio is the divider's own commit. The clamp runs here as
// well as in the drag math because the caller can also arrive from the
// keyboard, from a restored preview, or from a hand-written value.
export function setOverviewRatio(doc: OverviewLayoutDoc, ratio: number): OverviewLayoutDoc {
  return { ...doc, version: OVERVIEW_LAYOUT_VERSION, sizes: { ...doc.sizes }, ratio: clampOverviewRatio(ratio) };
}

// overviewModuleSize resolves what a module is CURRENTLY set to. An
// absent entry, an unknown value, and a module that can't be resized at
// all all read as 'normal' -- which is also the "nothing was chosen
// here" answer the adaptive layout below depends on.
export function overviewModuleSize(doc: OverviewLayoutDoc, id: string): OverviewSize {
  if (!isResizableOverviewModule(id)) return 'normal';
  const size = doc.sizes?.[id];
  return isOverviewSize(size) ? size : 'normal';
}

// setOverviewModuleSize writes one module's step, storing 'normal' as
// absence so the canonical form the server merges to and the one the
// client produces are the same document.
export function setOverviewModuleSize(doc: OverviewLayoutDoc, id: string, size: OverviewSize): OverviewLayoutDoc {
  if (!isResizableOverviewModule(id) || !isOverviewSize(size)) return doc;
  const sizes = { ...doc.sizes };
  if (size === 'normal') delete sizes[id];
  else sizes[id] = size;
  return { ...doc, version: OVERVIEW_LAYOUT_VERSION, sizes };
}

// overviewModuleRows is the row budget a module renders at, or null for
// one with no elastic body (storage) -- the value the view turns into an
// actual list length.
export function overviewModuleRows(id: string, size: OverviewSize): number | null {
  const rows = OVERVIEW_MODULES.find((m) => m.id === id)?.rows;
  if (!rows) return null;
  return rows[isOverviewSize(size) ? size : 'normal'];
}

// overviewModuleMaxRows is the widest budget any step asks for -- what
// the events feed FETCHES, once, so switching steps re-renders instead
// of re-requesting. Also the array length TopBarList's rank-stability
// state is asked to hold.
export function overviewModuleMaxRows(id: string): number | null {
  const rows = OVERVIEW_MODULES.find((m) => m.id === id)?.rows;
  if (!rows) return null;
  return Math.max(...OVERVIEW_SIZES.map((s) => rows[s]));
}

// isAdaptivelySized answers the one interplay question the Overview's
// adaptive layout has to ask before it grows anything: may this module's
// height still be decided FOR it?
//
// A user-set step wins, always. 'compact' and 'tall' are a deliberate
// choice about how much of this module the owner wants to see, and no
// adaptive rule -- the all-clear band reclaiming the status band's
// vertical space, an emptied lane handing its width to the survivor --
// gets to overrule it. A module still at 'normal' has expressed no such
// preference, so it keeps the content-driven height it has today and is
// the only thing left for the surrounding layout to grow into.
export function isAdaptivelySized(doc: OverviewLayoutDoc, id: string): boolean {
  return overviewModuleSize(doc, id) === 'normal';
}

// --- comparison ------------------------------------------------------------

// sameOverviewLayout backs the store's "don't PUT what hasn't changed"
// check. Order matters in every list, so the lists compare positionally,
// not as sets; the ratio compares exactly, which is safe because every
// gesture rounds to three places and both JSON encoders round-trip such
// a number unchanged.
export function sameOverviewLayout(a: OverviewLayoutDoc, b: OverviewLayoutDoc): boolean {
  const sameList = (x: string[], y: string[]) => x.length === y.length && x.every((v, i) => v === y[i]);
  const aSizes = Object.keys(a.sizes ?? {});
  const bSizes = Object.keys(b.sizes ?? {});
  const sameSizes =
    aSizes.length === bSizes.length && aSizes.every((id) => (a.sizes ?? {})[id] === (b.sizes ?? {})[id]);
  return (
    a.version === b.version &&
    a.ratio === b.ratio &&
    sameSizes &&
    sameList(a.wide, b.wide) &&
    sameList(a.narrow, b.narrow) &&
    sameList(a.hidden, b.hidden)
  );
}

// isDefaultOverviewLayout backs the Reset control's own disabled state:
// there is nothing to restore when the arrangement already IS the
// default -- which since v2 includes the band's split and every
// module's height, so nudging only the divider still lights Reset up.
export function isDefaultOverviewLayout(doc: OverviewLayoutDoc): boolean {
  return sameOverviewLayout(doc, defaultOverviewLayout());
}

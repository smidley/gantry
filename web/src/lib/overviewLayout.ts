// The Overview's saved module arrangement, as pure data -- the client
// half of internal/server/api_layout.go, deliberately DOM-free and
// rune-free so vitest can exercise every rule directly (the same
// motion.ts/motion.svelte.ts split, for the same reason).
//
// Everything here mirrors the Go side one-for-one: the same closed
// module set, the same two lane names, the same merge rule. They are
// two implementations of one contract rather than a client that trusts
// the server, because the SPA has to render a usable layout the instant
// it loads -- before any GET has resolved -- and the edit-mode gestures
// have to produce their new document locally before the debounced PUT
// that persists it. Both sides' merges are unit-tested against the same
// cases; if they ever disagree the server's answer wins on the next
// load, since it is what actually got stored.

import type { OverviewLayoutDTO } from './api';

// OverviewLayoutDoc IS the wire shape (api.ts's OverviewLayoutDTO) --
// aliased rather than redeclared so the two can never drift, and named
// for what it is locally: a whole document, never a partial patch.
export type OverviewLayoutDoc = OverviewLayoutDTO;

export type OverviewColumn = 'wide' | 'narrow';

// OVERVIEW_LAYOUT_VERSION must equal api_layout.go's own
// overviewLayoutVersion. See that constant's doc for what does (a
// document SHAPE change) and does not (adding or retiring a module) bump
// it.
export const OVERVIEW_LAYOUT_VERSION = 1;

export interface OverviewModule {
  id: string;
  column: OverviewColumn;
  // label names the module in the edit-mode chrome -- the drag handle's
  // and hide toggle's own accessible names, and the ghost card's text
  // once a module is hidden and its real content is gone. It is UI copy
  // and free to change; `id` is the thing that gets persisted.
  label: string;
}

// OVERVIEW_MODULES is the closed known set, in default order within each
// column -- the mirror of api_layout.go's overviewModules table, and the
// only place a module's default home is written down on this side.
//
// Only the modules BAND is rearrangeable. The status headline, the
// attention callouts, the fleet strip, the bay schematic and the GPU
// strip are all pinned and deliberately absent: burying the "needs you"
// surface under a drag gesture is exactly the failure mode this feature
// must not enable.
export const OVERVIEW_MODULES: OverviewModule[] = [
  { id: 'top-consumers', column: 'wide', label: 'Top consumers' },
  { id: 'events', column: 'wide', label: 'Recent events' },
  { id: 'metrics-rail', column: 'narrow', label: 'Metrics rail' },
];

export function isKnownOverviewModule(id: string): boolean {
  return OVERVIEW_MODULES.some((m) => m.id === id);
}

// overviewModuleLabel falls back to the raw id rather than an empty
// string: an unlabelled control is worse than one labelled with a slug,
// and this can only ever be reached by a caller holding an id the table
// doesn't have -- which mergeOverviewLayout has already made impossible
// for anything rendered.
export function overviewModuleLabel(id: string): string {
  return OVERVIEW_MODULES.find((m) => m.id === id)?.label ?? id;
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
// The result always carries this build's version and three real arrays.
export function mergeOverviewLayout(stored: Partial<OverviewLayoutDoc> | null | undefined): OverviewLayoutDoc {
  const merged: OverviewLayoutDoc = { version: OVERVIEW_LAYOUT_VERSION, wide: [], narrow: [], hidden: [] };
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
  return merged;
}

// defaultOverviewLayout is what a never-customized install renders and
// what "Reset layout" restores.
export function defaultOverviewLayout(): OverviewLayoutDoc {
  return mergeOverviewLayout(null);
}

// withoutModule strips one id from every list -- the shared first step
// of every gesture below, since a module has exactly one home and moving
// it anywhere means leaving the old one.
function withoutModule(doc: OverviewLayoutDoc, id: string): OverviewLayoutDoc {
  return {
    version: OVERVIEW_LAYOUT_VERSION,
    wide: doc.wide.filter((x) => x !== id),
    narrow: doc.narrow.filter((x) => x !== id),
    hidden: doc.hidden.filter((x) => x !== id),
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
// brand-new module gets placed.
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

// sameOverviewLayout backs the store's "don't PUT what hasn't changed"
// check. Order matters in every list, so this is a straight positional
// comparison, not a set comparison.
export function sameOverviewLayout(a: OverviewLayoutDoc, b: OverviewLayoutDoc): boolean {
  const sameList = (x: string[], y: string[]) => x.length === y.length && x.every((v, i) => v === y[i]);
  return (
    a.version === b.version && sameList(a.wide, b.wide) && sameList(a.narrow, b.narrow) && sameList(a.hidden, b.hidden)
  );
}

// isDefaultOverviewLayout backs the Reset control's own disabled state:
// there is nothing to restore when the arrangement already IS the
// default.
export function isDefaultOverviewLayout(doc: OverviewLayoutDoc): boolean {
  return sameOverviewLayout(doc, defaultOverviewLayout());
}

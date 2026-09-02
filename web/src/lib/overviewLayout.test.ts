import { describe, expect, it } from 'vitest';
import {
  OVERVIEW_LAYOUT_VERSION,
  OVERVIEW_MODULES,
  OVERVIEW_RATIO_DEFAULT,
  OVERVIEW_RATIO_MAX,
  OVERVIEW_RATIO_MIN,
  OVERVIEW_SIZES,
  clampOverviewRatio,
  defaultOverviewLayout,
  hideOverviewModule,
  isAdaptivelySized,
  isDefaultOverviewLayout,
  isKnownOverviewModule,
  isModuleHidden,
  isResizableOverviewModule,
  mergeOverviewLayout,
  moveOverviewModule,
  nudgeOverviewRatio,
  overviewLaneFlex,
  overviewModuleLabel,
  overviewModuleMaxRows,
  overviewModuleRows,
  overviewModuleSize,
  overviewRatioFromDrag,
  setOverviewModuleSize,
  setOverviewRatio,
  sameOverviewLayout,
  showOverviewModule,
  type OverviewLayoutDoc,
} from './overviewLayout';

// The client half of the layout contract. Every case here has a
// deliberate twin in internal/server/api_layout_test.go: the two merges
// are independent implementations of one rule, and a divergence would
// show up as a layout that changes the moment it round-trips.

const doc = (partial: Partial<OverviewLayoutDoc>): OverviewLayoutDoc => ({
  version: OVERVIEW_LAYOUT_VERSION,
  wide: [],
  narrow: [],
  hidden: [],
  ratio: OVERVIEW_RATIO_DEFAULT,
  sizes: {},
  ...partial,
});

describe('module table', () => {
  it('carries every id exactly once, each with a label', () => {
    const ids = OVERVIEW_MODULES.map((m) => m.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const m of OVERVIEW_MODULES) {
      expect(m.label.length).toBeGreaterThan(0);
      expect(isKnownOverviewModule(m.id)).toBe(true);
    }
    expect(isKnownOverviewModule('not-a-module')).toBe(false);
  });

  // A step has to actually buy something, in the right direction, or the
  // control is a lie -- and `normal` has to stay the number this page
  // shipped with, which is the whole "everything at normal IS today's
  // page" guarantee.
  it('gives every resizable module a strictly increasing row budget', () => {
    for (const m of OVERVIEW_MODULES) {
      if (!m.rows) {
        expect(isResizableOverviewModule(m.id)).toBe(false);
        expect(overviewModuleRows(m.id, 'tall')).toBeNull();
        expect(overviewModuleMaxRows(m.id)).toBeNull();
        continue;
      }
      expect(isResizableOverviewModule(m.id)).toBe(true);
      expect(m.rows.compact).toBeLessThan(m.rows.normal);
      expect(m.rows.normal).toBeLessThan(m.rows.tall);
      expect(overviewModuleMaxRows(m.id)).toBe(m.rows.tall);
    }
  });

  it('pins the shipped budgets as the normal step', () => {
    expect(overviewModuleRows('top-consumers', 'normal')).toBe(5);
    expect(overviewModuleRows('events', 'normal')).toBe(8);
  });

  it('has no size for the rail -- four fixed tiles have no height to choose', () => {
    expect(isResizableOverviewModule('metrics-rail')).toBe(false);
    expect(overviewModuleSize(doc({ sizes: { 'metrics-rail': 'tall' } }), 'metrics-rail')).toBe('normal');
  });

  it('labels a known id and falls back to the raw id for anything else', () => {
    expect(overviewModuleLabel('top-consumers')).toBe('Top consumers');
    expect(overviewModuleLabel('mystery')).toBe('mystery');
  });
});

describe('mergeOverviewLayout', () => {
  it('turns an empty document into the default layout', () => {
    expect(mergeOverviewLayout(null)).toEqual(defaultOverviewLayout());
    expect(mergeOverviewLayout(undefined)).toEqual(defaultOverviewLayout());
    expect(mergeOverviewLayout({})).toEqual(defaultOverviewLayout());
  });

  it('places every known module exactly once by default, with nothing hidden', () => {
    const def = defaultOverviewLayout();
    expect(def.version).toBe(OVERVIEW_LAYOUT_VERSION);
    expect(def.hidden).toEqual([]);
    expect([...def.wide, ...def.narrow].sort()).toEqual(OVERVIEW_MODULES.map((m) => m.id).sort());
  });

  it('drops ids this build does not know about', () => {
    const merged = mergeOverviewLayout({
      version: 1,
      wide: ['events', 'a-module-from-the-future', 'top-consumers'],
      narrow: ['metrics-rail'],
      hidden: ['another-ghost'],
    });
    expect(merged.wide).toEqual(['events', 'top-consumers']);
    expect(merged.narrow).toEqual(['metrics-rail']);
    expect(merged.hidden).toEqual([]);
  });

  // The release-adds-a-module case: someone who saved a layout before a
  // module existed must still see it, in its own default lane, appended
  // after what they arranged rather than inserted above it.
  it('appends a known module the document never mentions to its default column', () => {
    const merged = mergeOverviewLayout({ version: 1, wide: ['events'], narrow: [], hidden: [] });
    expect(merged.wide).toEqual(['events', 'top-consumers']);
    expect(merged.narrow).toEqual(['metrics-rail']);
    expect(merged.hidden).toEqual([]);
  });

  it('leaves a hidden module hidden rather than re-placing it', () => {
    const merged = mergeOverviewLayout({ version: 1, wide: ['top-consumers'], narrow: ['metrics-rail'], hidden: ['events'] });
    expect(merged.hidden).toEqual(['events']);
    expect(merged.wide).toEqual(['top-consumers']);
  });

  it('deduplicates, first occurrence winning', () => {
    const merged = mergeOverviewLayout({
      version: 1,
      wide: ['events', 'events', 'metrics-rail'],
      narrow: ['metrics-rail', 'top-consumers'],
      hidden: ['events'],
    });
    expect(merged.wide).toEqual(['events', 'metrics-rail']);
    expect(merged.narrow).toEqual(['top-consumers']);
    expect(merged.hidden).toEqual([]);
  });

  it('round-trips a fully-populated document unchanged', () => {
    const stored = doc({ wide: ['metrics-rail', 'events'], narrow: ['top-consumers'] });
    expect(mergeOverviewLayout(stored)).toEqual(stored);
  });

  // Whatever comes back off the wire is untrusted shape as far as this
  // function is concerned -- a null list or a non-string entry must
  // degrade to the default, never throw mid-render.
  it('survives malformed input', () => {
    expect(mergeOverviewLayout({ wide: null as never, narrow: undefined, hidden: 'events' as never })).toEqual(
      defaultOverviewLayout(),
    );
    expect(mergeOverviewLayout({ wide: [42, 'events'] as never })).toEqual(
      doc({ wide: ['events', 'top-consumers'], narrow: ['metrics-rail'] }),
    );
  });

  it('always stamps this build s own version', () => {
    expect(mergeOverviewLayout({ version: 99 }).version).toBe(OVERVIEW_LAYOUT_VERSION);
  });
});

// --- v1 -> v2 migration ----------------------------------------------------
//
// The twin of api_layout_test.go's own migration table. A v1 document has
// no ratio and no sizes at all; both have a defined "absent", so the
// migration is a fill-in-the-defaults that leaves the arrangement alone.

describe('v1 -> v2 migration', () => {
  it('accepts a v1 document and fills in the new fields', () => {
    const v1 = { version: 1, wide: ['events', 'top-consumers'], narrow: ['metrics-rail'], hidden: [] };
    expect(mergeOverviewLayout(v1 as Partial<OverviewLayoutDoc>)).toEqual(
      doc({ wide: ['events', 'top-consumers'], narrow: ['metrics-rail'] }),
    );
  });

  it('keeps a v1 hidden module hidden across the migration', () => {
    const v1 = { version: 1, wide: ['top-consumers'], narrow: ['metrics-rail'], hidden: ['events'] };
    const merged = mergeOverviewLayout(v1 as Partial<OverviewLayoutDoc>);
    expect(merged.hidden).toEqual(['events']);
    expect(merged.ratio).toBe(OVERVIEW_RATIO_DEFAULT);
    expect(merged.sizes).toEqual({});
  });

  it('normalizes the sizes map to its canonical form', () => {
    const merged = mergeOverviewLayout({
      sizes: {
        events: 'tall',
        'top-consumers': 'normal', // absence IS normal
        'metrics-rail': 'tall', // no elastic body
        'a-module-from-the-future': 'compact', // unknown id
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } as any,
    });
    expect(merged.sizes).toEqual({ events: 'tall' });
  });

  it('survives a malformed sizes field rather than throwing mid-render', () => {
    for (const sizes of [null, undefined, 'tall', 42, ['tall']]) {
      expect(mergeOverviewLayout({ sizes } as Partial<OverviewLayoutDoc>).sizes).toEqual({});
    }
    expect(mergeOverviewLayout({ sizes: { events: 7 } } as unknown as Partial<OverviewLayoutDoc>).sizes).toEqual({});
  });
});

// --- the divider's arithmetic ---------------------------------------------

describe('ratio clamp + drag math', () => {
  it('treats 0 and anything unusable as absent, not as a zero-width lane', () => {
    for (const value of [0, undefined, null, NaN, Infinity, 'wide', {}]) {
      expect(clampOverviewRatio(value)).toBe(OVERVIEW_RATIO_DEFAULT);
    }
  });

  it('clamps to the designed range and keeps anything inside it exactly', () => {
    expect(clampOverviewRatio(0.2)).toBe(OVERVIEW_RATIO_MIN);
    expect(clampOverviewRatio(-3)).toBe(OVERVIEW_RATIO_MIN);
    expect(clampOverviewRatio(0.99)).toBe(OVERVIEW_RATIO_MAX);
    expect(clampOverviewRatio(OVERVIEW_RATIO_MIN)).toBe(OVERVIEW_RATIO_MIN);
    expect(clampOverviewRatio(OVERVIEW_RATIO_MAX)).toBe(OVERVIEW_RATIO_MAX);
    expect(clampOverviewRatio(0.7)).toBe(0.7);
  });

  // The pointer moves 1:1 with the boundary: dragging a tenth of the
  // lanes' own span moves the split by a tenth.
  it('moves the split by the fraction of the span dragged', () => {
    expect(overviewRatioFromDrag(0.65, 100, 1000)).toBe(0.75);
    expect(overviewRatioFromDrag(0.65, -50, 1000)).toBe(0.6);
    expect(overviewRatioFromDrag(0.65, 0, 1000)).toBe(0.65);
  });

  it('clamps a drag past either end instead of running off', () => {
    expect(overviewRatioFromDrag(0.7, 900, 1000)).toBe(OVERVIEW_RATIO_MAX);
    expect(overviewRatioFromDrag(0.7, -900, 1000)).toBe(OVERVIEW_RATIO_MIN);
  });

  it('moves nothing when there is no span to divide', () => {
    expect(overviewRatioFromDrag(0.7, 200, 0)).toBe(0.7);
    expect(overviewRatioFromDrag(0.7, 200, NaN)).toBe(0.7);
    expect(overviewRatioFromDrag(0.7, NaN, 1000)).toBe(0.7);
  });

  // Rounded at the gesture boundary so a long drag (or a held arrow key)
  // can't accumulate float dust into a ratio nobody could have typed.
  it('rounds a gesture to three places', () => {
    expect(overviewRatioFromDrag(0.615, 1, 999)).toBe(0.616);
    let ratio = OVERVIEW_RATIO_DEFAULT;
    for (let i = 0; i < 7; i++) ratio = nudgeOverviewRatio(ratio, 1);
    expect(ratio).toBe(0.685);
  });

  it('nudges by a point per step and stops at the clamps', () => {
    expect(nudgeOverviewRatio(0.65, 1)).toBe(0.66);
    expect(nudgeOverviewRatio(0.65, -1)).toBe(0.64);
    expect(nudgeOverviewRatio(0.65, 5)).toBe(0.7);
    expect(nudgeOverviewRatio(OVERVIEW_RATIO_MAX, 3)).toBe(OVERVIEW_RATIO_MAX);
    expect(nudgeOverviewRatio(OVERVIEW_RATIO_MIN, -3)).toBe(OVERVIEW_RATIO_MIN);
  });

  it('expresses the split as flex factors against a narrow lane of 1', () => {
    expect(overviewLaneFlex(0.6)).toEqual({ wide: 1.5, narrow: 1 });
    expect(overviewLaneFlex(0.75)).toEqual({ wide: 3, narrow: 1 });
    // The default really is the band's own shipped 1.6 : 1.
    expect(overviewLaneFlex(OVERVIEW_RATIO_DEFAULT).wide).toBeCloseTo(1.6, 2);
  });

  // The bug this shape exists to avoid: flex hands out only sum(grow) of
  // the free space when that sum is below 1, so a lone lane with a
  // sub-1 factor would sit at 61% of the band with a hole beside it --
  // silently breaking the visibility-driven expansion. Every factor must
  // stay at or above 1 across the whole range.
  it('never produces a factor below 1, so a lone lane still spans the band', () => {
    for (let r = OVERVIEW_RATIO_MIN; r <= OVERVIEW_RATIO_MAX + 1e-9; r += 0.005) {
      const flex = overviewLaneFlex(r);
      expect(flex.wide).toBeGreaterThanOrEqual(1);
      expect(flex.narrow).toBeGreaterThanOrEqual(1);
      // ...and the pair still encodes the ratio it was given.
      expect(flex.wide / (flex.wide + flex.narrow)).toBeCloseTo(r, 3);
    }
  });

  it('clamps whatever it is handed, so no caller can produce a bad pair', () => {
    expect(overviewLaneFlex(0.99)).toEqual(overviewLaneFlex(OVERVIEW_RATIO_MAX));
    expect(overviewLaneFlex(0)).toEqual(overviewLaneFlex(OVERVIEW_RATIO_DEFAULT));
  });

  // The default IS the band's shipped 1.6:1 split, which is what makes
  // "nobody has customized this" render as today's page.
  it('defaults to the split the band shipped with', () => {
    expect(OVERVIEW_RATIO_DEFAULT).toBeCloseTo(1.6 / 2.6, 3);
    expect(defaultOverviewLayout().ratio).toBe(OVERVIEW_RATIO_DEFAULT);
  });
});

// --- size + ratio gestures -------------------------------------------------

describe('setOverviewRatio / setOverviewModuleSize', () => {
  it('stores a clamped ratio and leaves everything else alone', () => {
    const before = doc({ wide: ['events', 'top-consumers'], narrow: ['metrics-rail'] });
    expect(setOverviewRatio(before, 0.72)).toEqual({ ...before, ratio: 0.72 });
    expect(setOverviewRatio(before, 5).ratio).toBe(OVERVIEW_RATIO_MAX);
  });

  it('stores a step, and stores normal as absence', () => {
    const before = defaultOverviewLayout();
    const tall = setOverviewModuleSize(before, 'events', 'tall');
    expect(tall.sizes).toEqual({ events: 'tall' });
    expect(overviewModuleSize(tall, 'events')).toBe('tall');
    expect(setOverviewModuleSize(tall, 'events', 'normal').sizes).toEqual({});
  });

  it('refuses a size for a module with no elastic body, and an unknown step', () => {
    const before = defaultOverviewLayout();
    expect(setOverviewModuleSize(before, 'metrics-rail', 'tall')).toBe(before);
    expect(setOverviewModuleSize(before, 'nope', 'tall')).toBe(before);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(setOverviewModuleSize(before, 'events', 'enormous' as any)).toBe(before);
  });

  it('never mutates the document it was handed', () => {
    const before = defaultOverviewLayout();
    const snapshot = structuredClone(before);
    setOverviewModuleSize(before, 'events', 'tall');
    setOverviewRatio(before, 0.7);
    expect(before).toEqual(snapshot);
  });

  // Where a module SITS and how tall it is are independent: a card
  // dragged into the other lane, hidden, or brought back must not
  // quietly resize itself.
  it('carries a size through a move, a hide and a show', () => {
    const tall = setOverviewModuleSize(defaultOverviewLayout(), 'events', 'tall');
    expect(overviewModuleSize(moveOverviewModule(tall, 'events', 'narrow', 0), 'events')).toBe('tall');
    const hidden = hideOverviewModule(tall, 'events');
    expect(overviewModuleSize(hidden, 'events')).toBe('tall');
    expect(overviewModuleSize(showOverviewModule(hidden, 'events'), 'events')).toBe('tall');
  });

  it('reads an absent, unknown or non-resizable entry as normal', () => {
    expect(overviewModuleSize(defaultOverviewLayout(), 'events')).toBe('normal');
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(overviewModuleSize(doc({ sizes: { events: 'enormous' } as any }), 'events')).toBe('normal');
    expect(overviewModuleSize(doc({}), 'metrics-rail')).toBe('normal');
  });

  it('offers the three steps smallest-first', () => {
    expect(OVERVIEW_SIZES).toEqual(['compact', 'normal', 'tall']);
  });
});

// --- the adaptive-layout interplay -----------------------------------------
//
// The one rule the Overview's adaptive sizing has to respect: a user-set
// step WINS. A module left at 'normal' renders the budget this page
// shipped with and keeps its content-driven height -- the only thing the
// surrounding layout (the all-clear band reclaiming the status band's
// vertical space, an emptied lane handing its width to the survivor) can
// grow into. A module set to compact or tall renders the owner's budget
// and nothing adaptive overrules it.
//
// The Playwright half of this lives in tests/overview-customize.spec.ts,
// which puts a compact card next to a real all-clear band; what is pinned
// HERE is the priority itself, independent of any layout.

describe('adaptive-sizing interplay', () => {
  it('marks a module at normal as adaptive and a sized one as pinned', () => {
    const before = defaultOverviewLayout();
    expect(isAdaptivelySized(before, 'events')).toBe(true);
    expect(isAdaptivelySized(before, 'top-consumers')).toBe(true);
    for (const size of ['compact', 'tall'] as const) {
      const sized = setOverviewModuleSize(before, 'events', size);
      expect(isAdaptivelySized(sized, 'events')).toBe(false);
      expect(isAdaptivelySized(sized, 'top-consumers'), 'its neighbour is untouched').toBe(true);
    }
  });

  // A module that can never be resized is never "pinned by the owner"
  // either -- it has simply always been whatever height its content is.
  it('treats the non-resizable rail as adaptive', () => {
    expect(isAdaptivelySized(doc({ sizes: { 'metrics-rail': 'tall' } }), 'metrics-rail')).toBe(true);
  });

  // The guarantee the whole pass rests on: nothing resized means every
  // budget resolves to the number the page shipped with.
  it('renders the shipped budgets when nothing is resized', () => {
    const before = defaultOverviewLayout();
    for (const m of OVERVIEW_MODULES) {
      if (!m.rows) continue;
      expect(overviewModuleRows(m.id, overviewModuleSize(before, m.id))).toBe(m.rows.normal);
    }
  });

  it('resolves a pinned module to its own budget, never the shipped one', () => {
    const sized = setOverviewModuleSize(setOverviewModuleSize(defaultOverviewLayout(), 'events', 'tall'), 'top-consumers', 'compact');
    expect(overviewModuleRows('events', overviewModuleSize(sized, 'events'))).toBe(14);
    expect(overviewModuleRows('top-consumers', overviewModuleSize(sized, 'top-consumers'))).toBe(3);
  });
});

describe('moveOverviewModule', () => {
  it('reorders within a column', () => {
    const before = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    expect(moveOverviewModule(before, 'events', 'wide', 0)).toEqual(
      doc({ wide: ['events', 'top-consumers'], narrow: ['metrics-rail'] }),
    );
  });

  // The index is counted against the column WITHOUT the dragged module,
  // so a same-column move to the end is index 1 of a one-element list --
  // not index 2 of the original two.
  it('treats the index as a position in the column minus the dragged module', () => {
    const before = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    expect(moveOverviewModule(before, 'top-consumers', 'wide', 1).wide).toEqual(['events', 'top-consumers']);
  });

  it('moves between columns', () => {
    const before = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    const after = moveOverviewModule(before, 'events', 'narrow', 0);
    expect(after.wide).toEqual(['top-consumers']);
    expect(after.narrow).toEqual(['events', 'metrics-rail']);
  });

  it('un-hides a module dropped back into a column', () => {
    const before = doc({ wide: ['top-consumers'], narrow: ['metrics-rail'], hidden: ['events'] });
    const after = moveOverviewModule(before, 'events', 'wide', 1);
    expect(after.hidden).toEqual([]);
    expect(after.wide).toEqual(['top-consumers', 'events']);
  });

  it('clamps an out-of-range index instead of throwing', () => {
    const before = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    expect(moveOverviewModule(before, 'metrics-rail', 'wide', 99).wide).toEqual([
      'top-consumers',
      'events',
      'metrics-rail',
    ]);
    expect(moveOverviewModule(before, 'metrics-rail', 'wide', -5).wide).toEqual([
      'metrics-rail',
      'top-consumers',
      'events',
    ]);
  });

  it('ignores an unknown id and never mutates the input document', () => {
    const before = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    const snapshot = structuredClone(before);
    expect(moveOverviewModule(before, 'nope', 'wide', 0)).toBe(before);
    moveOverviewModule(before, 'events', 'narrow', 0);
    expect(before).toEqual(snapshot);
  });
});

describe('hide / show', () => {
  it('hides a module out of its column onto the hidden list', () => {
    const before = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    const after = hideOverviewModule(before, 'events');
    expect(after.wide).toEqual(['top-consumers']);
    expect(after.hidden).toEqual(['events']);
    expect(isModuleHidden(after, 'events')).toBe(true);
  });

  it('is idempotent -- hiding an already-hidden module changes nothing', () => {
    const once = hideOverviewModule(defaultOverviewLayout(), 'events');
    expect(hideOverviewModule(once, 'events')).toEqual(once);
  });

  // Un-hiding uses the same "place an unplaced known id" rule the
  // forward-compat append does: back to the END of its default column,
  // never to a position it no longer has.
  it('shows a hidden module back at the end of its default column', () => {
    const hidden = doc({ wide: ['top-consumers'], narrow: ['metrics-rail'], hidden: ['events'] });
    const after = showOverviewModule(hidden, 'events');
    expect(after.hidden).toEqual([]);
    expect(after.wide).toEqual(['top-consumers', 'events']);
  });

  it('shows a narrow-lane module back into the narrow lane, not the wide one', () => {
    const hidden = doc({ wide: ['top-consumers', 'events'], narrow: [], hidden: ['metrics-rail'] });
    expect(showOverviewModule(hidden, 'metrics-rail').narrow).toEqual(['metrics-rail']);
  });

  it('can empty a whole lane', () => {
    const after = hideOverviewModule(defaultOverviewLayout(), 'metrics-rail');
    expect(after.narrow).toEqual([]);
    expect(after.hidden).toEqual(['metrics-rail']);
  });
});

describe('comparison helpers', () => {
  it('recognises the default layout and any deviation from it', () => {
    expect(isDefaultOverviewLayout(defaultOverviewLayout())).toBe(true);
    expect(isDefaultOverviewLayout(hideOverviewModule(defaultOverviewLayout(), 'events'))).toBe(false);
    expect(isDefaultOverviewLayout(moveOverviewModule(defaultOverviewLayout(), 'events', 'wide', 0))).toBe(false);
  });

  // Reset has to light up for a nudged divider or a resized card too, or
  // the only way back from either is to undo it by hand.
  it('counts a changed ratio or size as a deviation from the default', () => {
    expect(isDefaultOverviewLayout(setOverviewRatio(defaultOverviewLayout(), 0.7))).toBe(false);
    expect(isDefaultOverviewLayout(setOverviewModuleSize(defaultOverviewLayout(), 'events', 'tall'))).toBe(false);
    // ...and back to the default value really is the default again, so a
    // gesture undone by hand disables Reset rather than leaving it armed.
    expect(isDefaultOverviewLayout(setOverviewRatio(defaultOverviewLayout(), OVERVIEW_RATIO_DEFAULT))).toBe(true);
    const there = setOverviewModuleSize(defaultOverviewLayout(), 'events', 'tall');
    expect(isDefaultOverviewLayout(setOverviewModuleSize(there, 'events', 'normal'))).toBe(true);
  });

  it('compares positionally, not as sets', () => {
    const a = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    const b = doc({ wide: ['events', 'top-consumers'], narrow: ['metrics-rail'] });
    expect(sameOverviewLayout(a, a)).toBe(true);
    expect(sameOverviewLayout(a, b)).toBe(false);
  });

  // The store's "don't PUT what hasn't changed" gate reads this, so a
  // ratio or size that moved has to register as a change -- and one that
  // didn't must not, or every gesture would fire a second redundant PUT.
  it('sees a changed ratio and a changed size', () => {
    const a = doc({ ratio: 0.7, sizes: { events: 'tall' } });
    expect(sameOverviewLayout(a, doc({ ratio: 0.71, sizes: { events: 'tall' } }))).toBe(false);
    expect(sameOverviewLayout(a, doc({ ratio: 0.7, sizes: { events: 'compact' } }))).toBe(false);
    expect(sameOverviewLayout(a, doc({ ratio: 0.7, sizes: {} }))).toBe(false);
    expect(sameOverviewLayout(a, doc({ ratio: 0.7, sizes: { events: 'tall' } }))).toBe(true);
  });

  // A ratio survives JSON in both directions exactly (both encoders emit
  // the shortest round-tripping form of a 3-place decimal), which is what
  // lets the comparison above be an equality rather than an epsilon --
  // and what keeps the server's own answer from looking like a change.
  it('round-trips a document through JSON without registering a change', () => {
    const before = setOverviewModuleSize(setOverviewRatio(defaultOverviewLayout(), 0.685), 'events', 'compact');
    expect(sameOverviewLayout(before, mergeOverviewLayout(JSON.parse(JSON.stringify(before))))).toBe(true);
  });
});

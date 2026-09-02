import { describe, expect, it } from 'vitest';
import {
  OVERVIEW_LAYOUT_VERSION,
  OVERVIEW_MODULES,
  defaultOverviewLayout,
  hideOverviewModule,
  isDefaultOverviewLayout,
  isKnownOverviewModule,
  isModuleHidden,
  mergeOverviewLayout,
  moveOverviewModule,
  overviewModuleLabel,
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

  it('compares positionally, not as sets', () => {
    const a = doc({ wide: ['top-consumers', 'events'], narrow: ['metrics-rail'] });
    const b = doc({ wide: ['events', 'top-consumers'], narrow: ['metrics-rail'] });
    expect(sameOverviewLayout(a, a)).toBe(true);
    expect(sameOverviewLayout(a, b)).toBe(false);
  });
});

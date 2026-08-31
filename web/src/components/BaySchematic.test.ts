// BaySchematic structural tests via svelte/server (SSR string render,
// the CalloutRow.test.ts convention) -- pins the render contract the
// region-sizing pass restyled around: one linked bar per entry with its
// usage-proportional inline fill height, flagged/kind modifiers, and
// the member-count microlabel. The fit-content footprint itself is
// computed style, not markup -- tests/overview-layout.spec.ts asserts
// it in a real browser.
import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import BaySchematic from './BaySchematic.svelte';

const ENTRIES = [
  { slot: 'disk1', pct: 42, kind: 'hdd', tempState: { kind: 'reading', celsius: 38 }, usedBytes: 42, freeBytes: 58 },
  { slot: 'disk2', pct: 95, flagged: true, calloutText: '95.0% capacity', kind: 'hdd' },
  { slot: 'cache', pct: 61, kind: 'nvme' },
];

function renderBays(entries: object[], summary: string | null = null): string {
  return render(BaySchematic, { props: { entries, summary } }).body;
}

describe('BaySchematic', () => {
  it('renders one Storage-linked device per entry, with a clear array summary', () => {
    const body = renderBays(ENTRIES);
    expect(body).toContain('Storage array');
    expect(body).toContain('3 devices');
    expect(body).toContain('1 needs attention');
    // The trailing space excludes the container's own bay-schematic__barS class.
    expect(body.match(/class="bay-schematic__bar /g)).toHaveLength(3);
    expect(body).toContain('class="bay-schematic__link');
    expect(body).toContain('href="#/storage">View details');
  });

  it('draws each fill at its own usage-proportional inline width and prints the value', () => {
    const body = renderBays(ENTRIES);
    expect(body).toContain('width: 42%');
    expect(body).toContain('width: 95%');
    expect(body).toContain('width: 61%');
    expect(body).toContain('42.0%');
  });

  it('marks a flagged bar and a non-hdd kind with their own modifier classes, and folds the callout into the aria-label', () => {
    const body = renderBays(ENTRIES);
    expect(body).toContain('bay-schematic__bar--flag');
    expect(body).toContain('bay-schematic__bar--nvme');
    expect(body).toContain('aria-label="disk2: 95.0% used — 95.0% capacity"');
  });

  it('renders nothing at all for an empty entry list (only SSR anchor comments)', () => {
    expect(renderBays([])).not.toContain('bay-schematic');
  });

  it('keeps the healthy-array reassurance inside the storage module', () => {
    expect(renderBays(ENTRIES, '2 other array members are within normal range.')).toContain(
      '2 other array members are within normal range.',
    );
  });

  // The facts-relocation pass (Scott: "Move the warmest disk reading
  // into the storage array section along with the array started mover
  // idle status"): both lines arrive as finished strings and render as
  // a second summary line -- absent props render no second line at all.
  it('renders the array-state and warmest-disk facts as a second summary line', () => {
    const body = render(BaySchematic, {
      props: { entries: ENTRIES, stateLine: 'Array started · mover idle', warmestLine: 'disk2 warmest at 41.5°C' },
    }).body;
    expect(body).toContain('Array started · mover idle');
    expect(body).toContain('disk2 warmest at 41.5°C');
  });

  it('renders either fact alone, still as one extra summary line', () => {
    const body = render(BaySchematic, { props: { entries: ENTRIES, stateLine: 'Array started · mover idle' } }).body;
    expect(body).toContain('Array started · mover idle');
    expect(body.match(/bay-schematic__summary/g)).toHaveLength(2);
  });

  it('renders no facts line when neither fact is provided', () => {
    const body = renderBays(ENTRIES);
    expect(body.match(/bay-schematic__summary/g)).toHaveLength(1);
  });
});

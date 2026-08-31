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

function renderBays(entries: object[]): string {
  return render(BaySchematic, { props: { entries } }).body;
}

describe('BaySchematic', () => {
  it('renders one Storage-linked bar per entry, with the member count in the microlabel', () => {
    const body = renderBays(ENTRIES);
    expect(body).toContain('Array · 3 members');
    // The trailing space excludes the container's own bay-schematic__barS class.
    expect(body.match(/class="bay-schematic__bar /g)).toHaveLength(3);
    expect(body.match(/href="#\/storage"/g)).toHaveLength(3);
  });

  it('draws each fill at its own usage-proportional inline height', () => {
    const body = renderBays(ENTRIES);
    expect(body).toContain('height: 42%');
    expect(body).toContain('height: 95%');
    expect(body).toContain('height: 61%');
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
});

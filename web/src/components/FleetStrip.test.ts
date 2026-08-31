// FleetStrip structural tests via svelte/server (SSR string render, the
// CalloutRow.test.ts convention) -- pins the render contract the
// region-sizing pass restyled around: one unit per container, each a
// real link with a real accessible name, status classes on the flagged
// ones. The grid layout itself (fixed-pitch columns, whole units per
// row) is computed style, not markup -- tests/overview-layout.spec.ts
// asserts it in a real browser.
import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import FleetStrip from './FleetStrip.svelte';

const CONTAINERS = [
  { name: 'jellyfin', state: 'running', health: 'healthy', cpuPct: 12, memBytes: 4e9 },
  { name: 'sonarr', state: 'running', health: 'unhealthy' },
  { name: 'prowlarr', state: 'exited', health: '' },
];

function renderStrip(containers: object[]): string {
  return render(FleetStrip, { props: { containers } }).body;
}

describe('FleetStrip', () => {
  it('renders one linked unit per container, with the fleet total in the list label', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('aria-label="Container fleet, 3 total"');
    expect(body.match(/class="fleet-unit/g)).toHaveLength(3);
    expect(body).toContain('href="#/containers/jellyfin"');
    expect(body).toContain('href="#/containers/sonarr"');
    expect(body).toContain('href="#/containers/prowlarr"');
  });

  it('carries each unit\'s state (and meaningful health) in its own aria-label', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('aria-label="jellyfin: running"');
    expect(body).toContain('aria-label="sonarr: running, unhealthy"');
    expect(body).toContain('aria-label="prowlarr: exited"');
  });

  it('marks stopped and unhealthy units with their own modifier classes', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('fleet-unit--critical'); // sonarr: running but unhealthy
    expect(body).toContain('fleet-unit--stopped'); // prowlarr: exited
  });

  it('renders an empty fleet as an empty (but labeled) list, not an error', () => {
    const body = renderStrip([]);
    expect(body).toContain('aria-label="Container fleet, 0 total"');
    expect(body).not.toContain('fleet-unit');
  });
});

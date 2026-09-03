// FleetStrip structural tests via svelte/server (SSR string render, the
// CalloutRow.test.ts convention) -- pins the render contract the
// auto-sizing pass restyled around: one unit per container, each a real
// link with a real accessible name, status classes on the flagged ones,
// and the glow driven by whichever metric is actually elevated. The
// GRID itself (computed cell size, columns, whether it scrolls) is
// measurement-driven and has no meaning in an SSR string -- the sizing
// rule is unit-tested pure in src/lib/fleetGrid.test.ts and asserted in
// a real browser in tests/overview-layout.spec.ts.
import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import FleetStrip from './FleetStrip.svelte';

const CONTAINERS = [
  { name: 'jellyfin', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 12, 'mem.bytes': 4e9 } },
  { name: 'sonarr', state: 'running', health: 'unhealthy', metrics: {} },
  { name: 'prowlarr', state: 'exited', health: '', metrics: {} },
];

function renderStrip(containers: object[], hostMemBytes?: number): string {
  return render(FleetStrip, { props: { containers, hostMemBytes } }).body;
}

describe('FleetStrip', () => {
  it('renders one linked unit per container, with the fleet total in the list label', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('Container fleet');
    expect(body).toContain('2 running');
    expect(body).toContain('1 stopped');
    expect(body).toContain('1 needs attention');
    expect(body).toContain('aria-label="Container fleet, 3 total"');
    expect(body.match(/class="fleet-unit/g)).toHaveLength(3);
    expect(body).toContain('href="#/containers/jellyfin"');
    expect(body).toContain('href="#/containers/sonarr"');
    expect(body).toContain('href="#/containers/prowlarr"');
  });

  // Two real groups again (Scott: "Keep the stopped containers in a
  // separate section like we had before") -- the running grid, then a
  // labelled stopped sub-section under it. They render as separate
  // lists, in that order, and each says what it holds.
  it('splits running and stopped into two labelled grids, running first', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('aria-label="Running containers, 2"');
    expect(body).toContain('aria-label="Stopped containers, 1"');
    expect(body.indexOf('Running containers')).toBeLessThan(body.indexOf('Stopped containers'));

    const names = [...body.matchAll(/href="#\/containers\/([^"]+)"/g)].map((m) => m[1]);
    expect(names).toEqual(['jellyfin', 'sonarr', 'prowlarr']);
  });

  it('gives the stopped section its own heading, in the legend\'s vocabulary', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('fleet-strip__group-label');
    // The heading counts the same way the legend above it does.
    expect(body.match(/1 stopped/g)?.length).toBe(2);
  });

  it('renders no stopped section at all when nothing is stopped', () => {
    const body = renderStrip([
      { name: 'jellyfin', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 12 } },
      { name: 'sonarr', state: 'running', health: 'healthy', metrics: {} },
    ]);
    expect(body).toContain('aria-label="Running containers, 2"');
    expect(body).not.toContain('Stopped containers');
    expect(body).not.toContain('fleet-strip__group-label');
    expect(body).not.toContain('stopped');
  });

  it('renders only the stopped section when nothing is running', () => {
    const body = renderStrip([{ name: 'prowlarr', state: 'exited', health: '', metrics: {} }]);
    expect(body).toContain('aria-label="Stopped containers, 1"');
    expect(body).not.toContain('Running containers');
    expect(body).toContain('aria-label="Container fleet, 1 total"');
  });

  it("carries each unit's state (and meaningful health) in its own aria-label", () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('aria-label="jellyfin: running, CPU 12.0%"');
    expect(body).toContain('aria-label="sonarr: running, unhealthy"');
    expect(body).toContain('aria-label="prowlarr: exited"');
  });

  it('marks stopped and unhealthy units with their own modifier classes', () => {
    const body = renderStrip(CONTAINERS);
    expect(body).toContain('fleet-unit--critical'); // sonarr: running but unhealthy
    expect(body).toContain('fleet-unit--stopped'); // prowlarr: exited
  });

  // Active is the max elevation across cpu/mem/net/io/gpu now
  // (lib/fleetActivity.ts). CPU's own floors are unchanged -- >1% of the
  // whole host to glow, 10% for the busy tier -- so a CPU-driven fleet
  // reads exactly as it did before the other four could drive it.
  it('marks a unit active only above 1% host-share CPU, and busy at 10%', () => {
    const body = renderStrip([
      { name: 'idle', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 0.6 } },
      { name: 'working', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 2.4 } },
      { name: 'churning', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 12 } },
    ]);
    const unitClasses = [...body.matchAll(/class="(fleet-unit[^"]*)"[^>]*href="#\/containers\/([^"]+)"/g)].map((m) => [
      m[2],
      m[1],
    ]);
    const byName = Object.fromEntries(unitClasses);
    expect(byName['idle']).not.toContain('fleet-unit--active');
    expect(byName['working']).toContain('fleet-unit--active');
    expect(byName['working']).not.toContain('fleet-unit--busy');
    expect(byName['churning']).toContain('fleet-unit--busy');
    expect(body).toContain('2 active now');
  });

  it('glows on a non-CPU metric and names the one that is driving it', () => {
    const body = renderStrip([
      { name: 'seeder', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 0.2, 'io.read_bps': 84e6 } },
      { name: 'chatty', state: 'running', health: 'healthy', metrics: { 'cpu.pct': 0.1, 'net.rx_bps': 30e6 } },
      { name: 'transcoder', state: 'running', health: 'healthy', metrics: { 'gpu.video.busy_pct': 47 } },
      { name: 'cramped', state: 'running', health: 'healthy', metrics: { 'mem.limit_pct': 92 } },
    ]);
    expect(body).toContain('aria-label="seeder: running, disk IO 84.0 MB/s"');
    expect(body).toContain('aria-label="chatty: running, network 30.0 MB/s"');
    expect(body).toContain('aria-label="transcoder: running, GPU 47.0%"');
    expect(body).toContain('aria-label="cramped: running, memory 92.0% of limit"');
    expect(body).toContain('4 active now');
    expect(body.match(/fleet-unit--active/g)).toHaveLength(4);
  });

  it('never glows a stopped container, whatever its last samples said', () => {
    const body = renderStrip([{ name: 'gone', state: 'exited', health: '', metrics: { 'cpu.pct': 40, 'io.read_bps': 9e8 } }]);
    expect(body).not.toContain('fleet-unit--active');
    expect(body).not.toContain('active now');
    expect(body).toContain('aria-label="gone: exited"');
  });

  it('uses the host memory total for a container with no limit of its own', () => {
    const body = renderStrip([{ name: 'hog', state: 'running', health: 'healthy', metrics: { 'mem.bytes': 15e9 } }], 16e9);
    expect(body).toContain('aria-label="hog: running, memory 93.8% of host"');
  });

  it('renders an empty fleet as an empty (but labeled) list, not an error', () => {
    const body = renderStrip([]);
    expect(body).toContain('aria-label="Container fleet, 0 total"');
    expect(body).not.toContain('fleet-unit');
  });
});

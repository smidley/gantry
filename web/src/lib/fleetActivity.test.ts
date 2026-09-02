import { describe, expect, it } from 'vitest';
import { activityInputFor, fleetActivity } from './fleetActivity';

// The glow rule: any of the five metrics can trigger it, the most
// elevated one wins and gets named, and nothing at all triggers it on a
// quiet container -- the "calm fleet stays calm" floor.

describe('fleetActivity', () => {
  it('stays completely idle for a quiet container', () => {
    const quiet = fleetActivity({ cpuPct: 0.4, memHostPct: 3, netBps: 40_000, ioBps: 120_000, gpuPct: 0 });
    expect(quiet.active).toBe(false);
    expect(quiet.busy).toBe(false);
    expect(quiet.metric).toBeNull();
    expect(quiet.label).toBeNull();
    expect(quiet.elevation).toBe(0);
  });

  it('reads an empty input as idle, not as zero-elevated activity', () => {
    expect(fleetActivity({}).active).toBe(false);
    expect(fleetActivity({ cpuPct: NaN, netBps: NaN }).active).toBe(false);
  });

  it('lets EACH metric trigger the glow on its own', () => {
    expect(fleetActivity({ cpuPct: 6 })).toMatchObject({ active: true, metric: 'cpu' });
    expect(fleetActivity({ memLimitPct: 82 })).toMatchObject({ active: true, metric: 'mem' });
    expect(fleetActivity({ netBps: 20e6 })).toMatchObject({ active: true, metric: 'net' });
    expect(fleetActivity({ ioBps: 40e6 })).toMatchObject({ active: true, metric: 'io' });
    expect(fleetActivity({ gpuPct: 35 })).toMatchObject({ active: true, metric: 'gpu' });
  });

  it('keeps each metric quiet at or under its own floor', () => {
    expect(fleetActivity({ cpuPct: 1 }).active).toBe(false);
    expect(fleetActivity({ cpuPct: 1.2 }).active).toBe(true);
    // 75 is container.mem_limit_pct's warn boundary and bands are a
    // strict ">", so exactly-75 is still normal.
    expect(fleetActivity({ memLimitPct: 75 }).active).toBe(false);
    expect(fleetActivity({ memLimitPct: 75.5 }).active).toBe(true);
    expect(fleetActivity({ netBps: 1e6 }).active).toBe(false);
    expect(fleetActivity({ netBps: 1.5e6 }).active).toBe(true);
    expect(fleetActivity({ ioBps: 2e6 }).active).toBe(false);
    expect(fleetActivity({ ioBps: 2.5e6 }).active).toBe(true);
    expect(fleetActivity({ gpuPct: 5 }).active).toBe(false);
    expect(fleetActivity({ gpuPct: 6 }).active).toBe(true);
  });

  it('names the MOST elevated metric when several are up', () => {
    // CPU barely over its floor, disk IO most of the way to saturating
    // an array disk -- IO is the story.
    const io = fleetActivity({ cpuPct: 2, ioBps: 84e6 });
    expect(io.metric).toBe('io');
    expect(io.label).toBe('disk IO 84.0 MB/s');

    // Flip it: CPU well up the ramp, disk IO only just over its floor.
    const cpu = fleetActivity({ cpuPct: 22, ioBps: 3e6 });
    expect(cpu.metric).toBe('cpu');
    expect(cpu.label).toBe('CPU 22.0%');
  });

  it('breaks an exact tie in the app\'s own resource order rather than flickering', () => {
    // Both saturated past their full point, so both clamp to 1.
    const tied = fleetActivity({ cpuPct: 90, ioBps: 900e6 });
    expect(tied.elevation).toBe(1);
    expect(tied.metric).toBe('cpu');
  });

  it('prefers a container\'s own memory limit over its host share, and says which', () => {
    const limited = fleetActivity({ memLimitPct: 96, memHostPct: 2 });
    expect(limited.metric).toBe('mem');
    expect(limited.label).toBe('memory 96.0% of limit');
    expect(limited.elevation).toBe(1);

    const unlimited = fleetActivity({ memHostPct: 88 });
    expect(unlimited.metric).toBe('mem');
    expect(unlimited.label).toBe('memory 88.0% of host');

    // A limit that is comfortably fine is NOT overridden by a scary
    // host share -- the limit is the number the container dies against.
    const finePerLimit = fleetActivity({ memLimitPct: 12, memHostPct: 99 });
    expect(finePerLimit.active).toBe(false);
  });

  it('keeps the busy tier exactly where the CPU-only rule already had it', () => {
    // cpu.pct 10 was the old busy cut and still is.
    expect(fleetActivity({ cpuPct: 9.5 })).toMatchObject({ active: true, busy: false });
    expect(fleetActivity({ cpuPct: 10 })).toMatchObject({ active: true, busy: true });
    // And the tier is shared: a rate metric well up its ramp is busy too.
    expect(fleetActivity({ ioBps: 60e6 }).busy).toBe(true);
    expect(fleetActivity({ ioBps: 5e6 }).busy).toBe(false);
  });

  it('reports elevation clamped into 0..1 whatever the reading', () => {
    for (const input of [
      { cpuPct: 400 },
      { ioBps: 5e9 },
      { netBps: 5e9 },
      { gpuPct: 400 },
      { memLimitPct: 250 },
      { cpuPct: 1.0001 },
    ]) {
      const a = fleetActivity(input);
      expect(a.elevation, JSON.stringify(input)).toBeGreaterThan(0);
      expect(a.elevation, JSON.stringify(input)).toBeLessThanOrEqual(1);
    }
  });

  it('names every metric with its own units', () => {
    expect(fleetActivity({ cpuPct: 14 }).label).toBe('CPU 14.0%');
    expect(fleetActivity({ memLimitPct: 90 }).label).toBe('memory 90.0% of limit');
    expect(fleetActivity({ netBps: 12.5e6 }).label).toBe('network 12.5 MB/s');
    expect(fleetActivity({ ioBps: 250e6 }).label).toBe('disk IO 250.0 MB/s');
    expect(fleetActivity({ gpuPct: 47 }).label).toBe('GPU 47.0%');
  });
});

describe('activityInputFor', () => {
  it('sums the rate and GPU keys the way the rest of the app does', () => {
    const input = activityInputFor({
      'cpu.pct': 4,
      'mem.bytes': 2e9,
      'mem.limit_pct': 40,
      'net.rx_bps': 3e6,
      'net.tx_bps': 1e6,
      'io.read_bps': 10e6,
      'io.write_bps': 2e6,
      'gpu.render.busy_pct': 20,
      'gpu.video.busy_pct': 15,
    });
    expect(input).toMatchObject({ cpuPct: 4, memLimitPct: 40, netBps: 4e6, ioBps: 12e6, gpuPct: 35 });
  });

  it('reports no sample rather than a false zero for a resource the container never touches', () => {
    const input = activityInputFor({ 'cpu.pct': 4 });
    expect(input.netBps).toBeUndefined();
    expect(input.ioBps).toBeUndefined();
    expect(input.gpuPct).toBeUndefined();
    expect(input.memHostPct).toBeUndefined();
    expect(fleetActivity(input).metric).toBe('cpu');
  });

  it('derives the host memory share only when the host total is known', () => {
    expect(activityInputFor({ 'mem.bytes': 8e9 }, 16e9).memHostPct).toBe(50);
    expect(activityInputFor({ 'mem.bytes': 8e9 }).memHostPct).toBeUndefined();
    expect(activityInputFor({ 'mem.bytes': 8e9 }, 0).memHostPct).toBeUndefined();
  });

  it('handles a missing metrics bag', () => {
    expect(fleetActivity(activityInputFor(undefined)).active).toBe(false);
    expect(fleetActivity(activityInputFor(null, 16e9)).active).toBe(false);
  });
});

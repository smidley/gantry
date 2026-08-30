import { describe, expect, it } from 'vitest';
import {
  hostSeriesMetricKeys,
  hostTotalNow,
  isTopResource,
  reduceSeriesPoints,
  resourceDirectionKeys,
  resourceMetricKeys,
  resourceScaleMax,
  resourceSecondaryMetricKey,
  TOP_RESOURCES,
  topFromFrame,
  unattributedValue,
} from './topFromFrame';
import type { SnapshotDTO } from './api';
import { sumSeriesByMetric } from './metrics';

describe('TOP_RESOURCES', () => {
  it('lists every resource resourceMetricKeys knows about, in the fixed CPU/Mem/Net/IO/GPU order', () => {
    expect(TOP_RESOURCES.map((r) => r.key)).toEqual(['cpu', 'mem', 'net', 'io', 'gpu']);
    for (const r of TOP_RESOURCES) {
      expect(resourceMetricKeys(r.key).length).toBeGreaterThan(0);
    }
  });
});

describe('isTopResource', () => {
  it('accepts every real resource key', () => {
    for (const r of TOP_RESOURCES) expect(isTopResource(r.key)).toBe(true);
  });

  it('rejects an invalid, missing, or null value', () => {
    expect(isTopResource('bogus')).toBe(false);
    expect(isTopResource(undefined)).toBe(false);
    expect(isTopResource(null)).toBe(false);
    expect(isTopResource('')).toBe(false);
  });
});

function frameWith(containers: SnapshotDTO['containers']): SnapshotDTO {
  return {
    ts: 1000,
    unraid_version: '',
    host: {},
    containers,
    disks: {},
    unraid: {},
    gpu: {},
    sources: {},
  };
}

describe('resourceMetricKeys', () => {
  it('maps every resource to the backend-mirrored metric key(s)', () => {
    expect(resourceMetricKeys('cpu')).toEqual(['cpu.pct']);
    expect(resourceMetricKeys('mem')).toEqual(['mem.bytes']);
    expect(resourceMetricKeys('net')).toEqual(['net.rx_bps', 'net.tx_bps']);
    expect(resourceMetricKeys('io')).toEqual(['io.read_bps', 'io.write_bps']);
    expect(resourceMetricKeys('gpu')).toEqual([
      'gpu.render.busy_pct',
      'gpu.video.busy_pct',
      'gpu.video-enhance.busy_pct',
      'gpu.copy.busy_pct',
    ]);
  });

  it('excludes gpu.nvidia.mem_mib from the gpu resource (VRAM, not a busy percentage)', () => {
    expect(resourceMetricKeys('gpu')).not.toContain('gpu.nvidia.mem_mib');
  });
});

describe('resourceSecondaryMetricKey', () => {
  it('names cpu.cores for cpu and nothing for every other resource', () => {
    expect(resourceSecondaryMetricKey('cpu')).toBe('cpu.cores');
    expect(resourceSecondaryMetricKey('mem')).toBeUndefined();
    expect(resourceSecondaryMetricKey('net')).toBeUndefined();
    expect(resourceSecondaryMetricKey('io')).toBeUndefined();
    expect(resourceSecondaryMetricKey('gpu')).toBeUndefined();
  });
});

describe('resourceScaleMax', () => {
  it('is a fixed 100 for cpu and gpu, regardless of the frame', () => {
    expect(resourceScaleMax('cpu', frameWith({}))).toBe(100);
    expect(resourceScaleMax('gpu', frameWith({}))).toBe(100);
    expect(resourceScaleMax('cpu', null)).toBe(100);
    expect(resourceScaleMax('gpu', undefined)).toBe(100);
  });

  it('is undefined for net and io -- no natural ceiling, stay relative-to-max', () => {
    expect(resourceScaleMax('net', frameWith({}))).toBeUndefined();
    expect(resourceScaleMax('io', frameWith({}))).toBeUndefined();
  });

  it('derives the host total bytes for mem from used_bytes/used_pct', () => {
    const frame = frameWith({});
    frame.host = { 'mem.used_bytes': 4_000_000_000, 'mem.used_pct': 50 };
    expect(resourceScaleMax('mem', frame)).toBe(8_000_000_000);
  });

  it('falls back to undefined for mem when the host has no mem stats yet', () => {
    expect(resourceScaleMax('mem', frameWith({}))).toBeUndefined();
    expect(resourceScaleMax('mem', null)).toBeUndefined();
    expect(resourceScaleMax('mem', undefined)).toBeUndefined();
  });

  it('falls back to undefined for mem when used_pct is 0 -- can\'t divide by it', () => {
    const frame = frameWith({});
    frame.host = { 'mem.used_bytes': 0, 'mem.used_pct': 0 };
    expect(resourceScaleMax('mem', frame)).toBeUndefined();
  });
});

describe('topFromFrame', () => {
  it('ranks containers descending by the single-metric resource value', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 10 } },
      b: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 30 } },
      c: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 20 } },
    });
    expect(topFromFrame(frame, 'cpu')).toEqual([
      { entity: 'b', value: 30 },
      { entity: 'c', value: 20 },
      { entity: 'a', value: 10 },
    ]);
  });

  it('sums a multi-metric resource (net = rx + tx)', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'net.rx_bps': 10, 'net.tx_bps': 5 } },
    });
    expect(topFromFrame(frame, 'net')).toEqual([{ entity: 'a', value: 15 }]);
  });

  it('excludes a container with none of the resource metrics present, rather than showing it at 0', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 10 } },
      b: { state: 'running', health: '', image: '', metrics: {} }, // no gpu activity at all
    });
    expect(topFromFrame(frame, 'gpu')).toEqual([]);
  });

  it('includes a container with only SOME of a multi-metric resource present', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'gpu.video.busy_pct': 12 } },
    });
    expect(topFromFrame(frame, 'gpu')).toEqual([{ entity: 'a', value: 12 }]);
  });

  it('breaks ties by entity name ascending, deterministically', () => {
    const frame = frameWith({
      zeta: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 10 } },
      alpha: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 10 } },
    });
    expect(topFromFrame(frame, 'cpu').map((r) => r.entity)).toEqual(['alpha', 'zeta']);
  });

  it('cuts to limit', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 1 } },
      b: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 2 } },
      c: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 3 } },
    });
    expect(topFromFrame(frame, 'cpu', 2)).toHaveLength(2);
  });

  it('returns an empty list for a null/undefined frame', () => {
    expect(topFromFrame(null, 'cpu')).toEqual([]);
    expect(topFromFrame(undefined, 'cpu')).toEqual([]);
  });

  it('attaches cpu.cores as each cpu row\'s secondary value', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 25, 'cpu.cores': 2 } },
    });
    expect(topFromFrame(frame, 'cpu')).toEqual([{ entity: 'a', value: 25, secondary: 2 }]);
  });

  it('omits secondary when cpu.cores has no sample yet, without inventing a 0', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 25 } },
    });
    const [row] = topFromFrame(frame, 'cpu');
    expect(row.secondary).toBeUndefined();
  });

  it('never attaches a secondary for a resource other than cpu', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'mem.bytes': 100, 'cpu.cores': 2 } },
    });
    const [row] = topFromFrame(frame, 'mem');
    expect(row.secondary).toBeUndefined();
  });

  it('omits direction by default, even for a directional resource', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'net.rx_bps': 10, 'net.tx_bps': 5 } },
    });
    const [row] = topFromFrame(frame, 'net');
    expect(row.direction).toBeUndefined();
  });

  it('attaches [down, up] direction when opted in for net', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'net.rx_bps': 10, 'net.tx_bps': 5 } },
    });
    const [row] = topFromFrame(frame, 'net', 10, { direction: true });
    expect(row.value).toBe(15);
    expect(row.direction).toEqual([10, 5]);
  });

  it('attaches [read, write] direction when opted in for io, zero-filling the absent half', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'io.write_bps': 7 } },
    });
    const [row] = topFromFrame(frame, 'io', 10, { direction: true });
    expect(row.direction).toEqual([0, 7]);
  });

  it('never attaches direction for a non-directional resource even when opted in', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 10 } },
    });
    const [row] = topFromFrame(frame, 'cpu', 10, { direction: true });
    expect(row.direction).toBeUndefined();
  });
});

describe('resourceDirectionKeys', () => {
  it('names the rx/tx pair for net and read/write for io', () => {
    expect(resourceDirectionKeys('net')).toEqual(['net.rx_bps', 'net.tx_bps']);
    expect(resourceDirectionKeys('io')).toEqual(['io.read_bps', 'io.write_bps']);
  });

  it('is undefined for cpu/mem/gpu -- no natural direction', () => {
    expect(resourceDirectionKeys('cpu')).toBeUndefined();
    expect(resourceDirectionKeys('mem')).toBeUndefined();
    expect(resourceDirectionKeys('gpu')).toBeUndefined();
  });
});

describe('hostTotalNow', () => {
  it('reads cpu.total and mem.used_bytes directly (same units as the per-container rows)', () => {
    const frame = frameWith({});
    frame.host = { 'cpu.total': 42, 'mem.used_bytes': 8_000_000_000 };
    expect(hostTotalNow(frame, 'cpu')).toEqual({ value: 42 });
    expect(hostTotalNow(frame, 'mem')).toEqual({ value: 8_000_000_000 });
  });

  it('sums per-device net/io keys into a value AND a direction pair', () => {
    const frame = frameWith({});
    frame.host = { 'net.eth0.rx_bps': 10, 'net.eth0.tx_bps': 5, 'diskio.sda.read_bps': 3, 'diskio.sda.write_bps': 9 };
    expect(hostTotalNow(frame, 'net')).toEqual({ value: 15, direction: [10, 5] });
    expect(hostTotalNow(frame, 'io')).toEqual({ value: 12, direction: [3, 9] });
  });

  it('is undefined for gpu -- no single honest whole-machine number', () => {
    const frame = frameWith({});
    frame.host = { 'cpu.total': 1 };
    expect(hostTotalNow(frame, 'gpu')).toBeUndefined();
  });

  it('is undefined when the frame/host has nothing yet', () => {
    expect(hostTotalNow(null, 'cpu')).toBeUndefined();
    expect(hostTotalNow(frameWith({}), 'cpu')).toBeUndefined();
  });
});

describe('hostSeriesMetricKeys', () => {
  it('names one fixed key for cpu/mem', () => {
    expect(hostSeriesMetricKeys('cpu')).toEqual(['cpu.total']);
    expect(hostSeriesMetricKeys('mem')).toEqual(['mem.used_bytes']);
  });

  it('is undefined for net/io (dynamic per-device keys) and gpu (no host total)', () => {
    expect(hostSeriesMetricKeys('net')).toBeUndefined();
    expect(hostSeriesMetricKeys('io')).toBeUndefined();
    expect(hostSeriesMetricKeys('gpu')).toBeUndefined();
  });
});

describe('reduceSeriesPoints', () => {
  it('averages the avg column for agg=avg', () => {
    const points: [number, number, number][] = [
      [1, 10, 20],
      [2, 20, 30],
      [3, 30, 40],
    ];
    expect(reduceSeriesPoints(points, 'avg')).toBe(20);
  });

  it('takes the max of the max column for agg=peak', () => {
    const points: [number, number, number][] = [
      [1, 10, 20],
      [2, 20, 45],
      [3, 30, 40],
    ];
    expect(reduceSeriesPoints(points, 'peak')).toBe(45);
  });

  it('is undefined for an empty series -- no host history for that window', () => {
    expect(reduceSeriesPoints([], 'avg')).toBeUndefined();
    expect(reduceSeriesPoints([], 'peak')).toBeUndefined();
  });
});

describe('unattributedValue', () => {
  it('is the host total minus the containers sum', () => {
    expect(unattributedValue(100, 30)).toBe(70);
  });

  it('clamps at zero rather than going negative', () => {
    expect(unattributedValue(10, 30)).toBe(0);
  });

  it('is zero when the containers sum exactly matches the host total', () => {
    expect(unattributedValue(50, 50)).toBe(0);
  });
});

// The bug this section unit-tests: TopConsumers' fetched-window (1h/24h/
// 7d) hero line used to read only resourceMetricKeys(resource)[0] out of
// its own /api/series results instead of summing all of them the way
// topFromFrame already sums a live frame's per-container metrics --
// invisible for cpu/mem (one key each) but silently dropping 3 of gpu's
// 4 engines. sumSeriesByMetric (metrics.ts) is now the ONE composition
// both paths call, so this proves they can't diverge again: for every
// TOP_RESOURCES entry, summing a live frame via topFromFrame and summing
// an equivalent one-point-per-metric series batch via sumSeriesByMetric
// must land on the exact same total.
describe('sumSeriesByMetric parity with the live-frame composition (topFromFrame)', () => {
  const ts = 1000;

  for (const resource of TOP_RESOURCES.map((r) => r.key)) {
    it(`sums the same total as the live frame when every metric is present (${resource})`, () => {
      const keys = resourceMetricKeys(resource);
      const metrics: Record<string, number> = {};
      const byMetric: Record<string, [number, number, number][]> = {};
      keys.forEach((key, i) => {
        const value = (i + 1) * 3; // distinct, nonzero per key -- a real sum, not an accidental 0+0
        metrics[key] = value;
        byMetric[key] = [[ts, value, value]];
      });
      const frame = frameWith({ a: { state: 'running', health: '', image: '', metrics } });
      const [row] = topFromFrame(frame, resource);
      expect(sumSeriesByMetric(byMetric, keys)).toEqual([[ts, row.value]]);
    });
  }

  it('still agrees on a PARTIALLY-present multi-metric resource -- fake mode\'s own gpu shape (only gpu.video.busy_pct ever populated, the other 3 engines never present for any container)', () => {
    const frame = frameWith({
      a: { state: 'running', health: '', image: '', metrics: { 'gpu.video.busy_pct': 20 } },
    });
    const [row] = topFromFrame(frame, 'gpu');
    const byMetric = { 'gpu.video.busy_pct': [[ts, 20, 20]] as [number, number, number][] };
    expect(sumSeriesByMetric(byMetric, resourceMetricKeys('gpu'))).toEqual([[ts, row.value]]);
  });
});

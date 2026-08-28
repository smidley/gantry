import { describe, expect, it } from 'vitest';
import {
  isTopResource,
  resourceMetricKeys,
  resourceScaleMax,
  resourceSecondaryMetricKey,
  TOP_RESOURCES,
  topFromFrame,
} from './topFromFrame';
import type { SnapshotDTO } from './api';

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
});

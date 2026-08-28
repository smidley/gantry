import { describe, expect, it } from 'vitest';
import { isTopResource, resourceMetricKeys, TOP_RESOURCES, topFromFrame } from './topFromFrame';
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
});

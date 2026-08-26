import { describe, expect, it } from 'vitest';
import { enginesPresent, etaFromProgress, GPU_ENGINE_ORDER, sumMetricsByPattern } from './metrics';

describe('sumMetricsByPattern', () => {
  it('sums a flat key with no dynamic middle segment (fake-mode shape)', () => {
    // "diskio.read_bps" both starts with "diskio" and ends with
    // ".read_bps" -- prefix and suffix share the one dot the flat shape
    // has, which startsWith/endsWith are happy to check independently
    // even though their matched ranges overlap.
    expect(sumMetricsByPattern({ 'diskio.read_bps': 42 }, 'diskio', '.read_bps')).toBe(42);
    expect(sumMetricsByPattern({ 'diskio.write_bps': 7 }, 'diskio', '.read_bps')).toBe(0);
  });

  it('sums across dynamic per-device keys (real-mode shape)', () => {
    const host = { 'diskio.sda.read_bps': 10, 'diskio.nvme0n1.read_bps': 20, 'diskio.sda.write_bps': 5 };
    expect(sumMetricsByPattern(host, 'diskio', '.read_bps')).toBe(30);
    expect(sumMetricsByPattern(host, 'diskio', '.write_bps')).toBe(5);
  });

  it('ignores unrelated keys and non-finite values', () => {
    const host = { 'diskio.sda.read_bps': 10, 'cpu.total': 5, 'diskio.sda.read_broken': NaN };
    expect(sumMetricsByPattern(host, 'diskio', '.read_bps')).toBe(10);
  });

  it('returns 0 for undefined/null/empty input', () => {
    expect(sumMetricsByPattern(undefined, 'diskio', '.read_bps')).toBe(0);
    expect(sumMetricsByPattern(null, 'diskio', '.read_bps')).toBe(0);
    expect(sumMetricsByPattern({}, 'diskio', '.read_bps')).toBe(0);
  });
});

describe('enginesPresent', () => {
  it('returns engines in the fixed GPU_ENGINE_ORDER, not object order', () => {
    const metrics = {
      'gpu.copy.busy_pct': 1,
      'gpu.render.busy_pct': 2,
      'gpu.video.busy_pct': 3,
    };
    expect(enginesPresent(metrics, (e) => `gpu.${e}.busy_pct`)).toEqual(['render', 'video', 'copy']);
  });

  it('supports the device-entity key shape too', () => {
    const metrics = { 'engine.video.busy_pct': 10 };
    expect(enginesPresent(metrics, (e) => `engine.${e}.busy_pct`)).toEqual(['video']);
  });

  it('returns empty for absent/undefined metrics', () => {
    expect(enginesPresent(undefined, (e) => `gpu.${e}.busy_pct`)).toEqual([]);
    expect(enginesPresent({}, (e) => `gpu.${e}.busy_pct`)).toEqual([]);
  });

  it('covers every declared engine slot', () => {
    expect(GPU_ENGINE_ORDER).toEqual(['render', 'video', 'video-enhance', 'copy']);
  });
});

describe('etaFromProgress', () => {
  it('estimates seconds remaining from a positive rate', () => {
    // 10% in 60s -> rate 1/6 %/s; 40% remaining -> 240s
    expect(etaFromProgress(0, 50, 60, 60)).toBeCloseTo(240, 5);
  });

  it('returns 0 when already at/over 100%', () => {
    expect(etaFromProgress(0, 90, 60, 100)).toBe(0);
    expect(etaFromProgress(0, 90, 60, 100.4)).toBe(0);
  });

  it('returns null when progress has not advanced', () => {
    expect(etaFromProgress(0, 50, 60, 50)).toBeNull();
  });

  it('returns null when progress moved backwards (a fresh parity run restarting)', () => {
    expect(etaFromProgress(0, 50, 60, 10)).toBeNull();
  });

  it('returns null when time did not advance', () => {
    expect(etaFromProgress(60, 40, 60, 50)).toBeNull();
    expect(etaFromProgress(60, 40, 30, 50)).toBeNull();
  });

  it('returns null for non-finite input', () => {
    expect(etaFromProgress(NaN, 40, 60, 50)).toBeNull();
    expect(etaFromProgress(0, 40, 60, NaN)).toBeNull();
  });
});

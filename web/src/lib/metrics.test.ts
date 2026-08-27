import { describe, expect, it } from 'vitest';
import {
  enginesPresent,
  etaFromProgress,
  GPU_ENGINE_ORDER,
  GPU_ENTITY_ENGINE_ORDER,
  parityIsRunning,
  seqStep,
  sharesFromMetrics,
  sumMetricsByPattern,
  sumSeriesPoints,
} from './metrics';

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

describe('sumSeriesPoints', () => {
  it('sums the single-array degenerate case (fake-mode shape) unchanged', () => {
    expect(
      sumSeriesPoints([
        [
          [100, 10, 10],
          [110, 20, 20],
        ],
      ]),
    ).toEqual([
      [100, 10],
      [110, 20],
    ]);
  });

  it('sums multiple metrics by aligned ts (real-mode per-device shape)', () => {
    const sda = [
      [100, 10, 10],
      [110, 20, 20],
    ];
    const nvme = [
      [100, 5, 5],
      [110, 15, 15],
    ];
    expect(sumSeriesPoints([sda, nvme])).toEqual([
      [100, 15],
      [110, 35],
    ]);
  });

  it('a ts present in only some inputs still contributes just those', () => {
    const sda = [[100, 10, 10]];
    const nvme = [
      [100, 5, 5],
      [110, 15, 15], // sda has no sample at this ts -- not a poisoned/dropped point
    ];
    expect(sumSeriesPoints([sda, nvme])).toEqual([
      [100, 15],
      [110, 15],
    ]);
  });

  it('skips non-finite entries and sorts ascending by ts', () => {
    const a = [
      [110, 2, 2],
      [100, NaN, NaN],
    ];
    const b = [[100, 1, 1]];
    expect(sumSeriesPoints([a, b])).toEqual([
      [100, 1],
      [110, 2],
    ]);
  });

  it('returns an empty ring for no inputs or all-empty inputs', () => {
    expect(sumSeriesPoints([])).toEqual([]);
    expect(sumSeriesPoints([[], []])).toEqual([]);
  });
});

describe('sharesFromMetrics', () => {
  it('extracts every share.<name>.used_bytes key, sorted by used bytes descending', () => {
    const metrics = {
      'array.started': 1,
      'share.appdata.used_bytes': 100,
      'share.media.used_bytes': 900,
      'share.backups.used_bytes': 500,
    };
    expect(sharesFromMetrics(metrics)).toEqual([
      { name: 'media', usedBytes: 900 },
      { name: 'backups', usedBytes: 500 },
      { name: 'appdata', usedBytes: 100 },
    ]);
  });

  it('breaks ties by name ascending', () => {
    const metrics = { 'share.zeta.used_bytes': 10, 'share.alpha.used_bytes': 10 };
    expect(sharesFromMetrics(metrics)).toEqual([
      { name: 'alpha', usedBytes: 10 },
      { name: 'zeta', usedBytes: 10 },
    ]);
  });

  it('ignores unrelated keys on the same entity', () => {
    const metrics = { 'array.started': 1, 'parity.progress_pct': 40, 'mover.running': 0 };
    expect(sharesFromMetrics(metrics)).toEqual([]);
  });

  it('returns an empty array for absent/undefined/null metrics', () => {
    expect(sharesFromMetrics({})).toEqual([]);
    expect(sharesFromMetrics(undefined)).toEqual([]);
    expect(sharesFromMetrics(null)).toEqual([]);
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

  it('recognizes the Nvidia solo "gpu" pseudo-engine when given GPU_ENTITY_ENGINE_ORDER', () => {
    const metrics = { 'engine.gpu.busy_pct': 42 };
    expect(enginesPresent(metrics, (e) => `engine.${e}.busy_pct`, GPU_ENTITY_ENGINE_ORDER)).toEqual(['gpu']);
  });

  it('does not recognize "gpu" as an engine under the default order (container attribution never uses it)', () => {
    const metrics = { 'gpu.gpu.busy_pct': 42 };
    expect(enginesPresent(metrics, (e) => `gpu.${e}.busy_pct`)).toEqual([]);
  });

  it('GPU_ENTITY_ENGINE_ORDER is GPU_ENGINE_ORDER plus the Nvidia fallback, in order', () => {
    expect(GPU_ENTITY_ENGINE_ORDER).toEqual(['render', 'video', 'video-enhance', 'copy', 'gpu']);
  });
});

describe('seqStep', () => {
  it('buckets 0-100% onto the 7-stop ramp', () => {
    expect(seqStep(0)).toBe('var(--seq-100)');
    expect(seqStep(1)).toBe('var(--seq-100)');
    expect(seqStep(100)).toBe('var(--seq-700)');
  });

  it('steps up through the middle of the ramp', () => {
    expect(seqStep(10)).toBe('var(--seq-100)'); // 0.7/7 -> ceil 1
    expect(seqStep(20)).toBe('var(--seq-200)'); // 1.4/7 -> ceil 2
    expect(seqStep(50)).toBe('var(--seq-400)'); // 3.5/7 -> ceil 4
  });

  it('clamps out-of-range input to the nearest end stop', () => {
    expect(seqStep(-10)).toBe('var(--seq-100)');
    expect(seqStep(150)).toBe('var(--seq-700)');
  });
});

describe('parityIsRunning', () => {
  it('is false when parity.progress_pct is absent (never started, or the key genuinely is not there)', () => {
    expect(parityIsRunning(undefined)).toBe(false);
  });

  it('is false at exactly 0 -- the finish-tick sentinel var.go/fake.go now write on completion', () => {
    expect(parityIsRunning(0)).toBe(false);
  });

  it('is true for any positive value, including a fractional just-started reading', () => {
    expect(parityIsRunning(0.0001)).toBe(true);
    expect(parityIsRunning(50)).toBe(true);
    expect(parityIsRunning(99.99)).toBe(true);
    expect(parityIsRunning(100)).toBe(true);
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

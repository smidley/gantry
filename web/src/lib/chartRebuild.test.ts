import { describe, expect, it } from 'vitest';
import { needsRebuild, sameSeriesShape, type ChartShape } from './chartRebuild';

const base: ChartShape = {
  series: [{ label: 'CPU', colorVar: '--series-1' }],
  theme: 'dark',
  unit: '%',
  hasFormatValue: false,
};

describe('needsRebuild', () => {
  it('rebuilds when there is no previous shape (first build)', () => {
    expect(needsRebuild(null, base)).toBe(true);
  });

  it('does not rebuild for an identical shape, even from a freshly-allocated object', () => {
    const next: ChartShape = {
      series: [{ label: 'CPU', colorVar: '--series-1' }],
      theme: 'dark',
      unit: '%',
      hasFormatValue: false,
    };
    expect(needsRebuild(base, next)).toBe(false);
  });

  it('rebuilds when the series count changes', () => {
    const next: ChartShape = { ...base, series: [...base.series, { label: 'Throttled', colorVar: '--series-2' }] };
    expect(needsRebuild(base, next)).toBe(true);
  });

  it('rebuilds when a series label changes', () => {
    const next: ChartShape = { ...base, series: [{ label: 'CPU (renamed)', colorVar: '--series-1' }] };
    expect(needsRebuild(base, next)).toBe(true);
  });

  it('rebuilds when a series colorVar changes', () => {
    const next: ChartShape = { ...base, series: [{ label: 'CPU', colorVar: '--series-2' }] };
    expect(needsRebuild(base, next)).toBe(true);
  });

  it('rebuilds on a theme flip', () => {
    expect(needsRebuild(base, { ...base, theme: 'light' })).toBe(true);
  });

  it('rebuilds when unit changes', () => {
    expect(needsRebuild(base, { ...base, unit: 'MB/s' })).toBe(true);
  });

  it('rebuilds when formatValue is added or removed', () => {
    expect(needsRebuild(base, { ...base, hasFormatValue: true })).toBe(true);
    expect(needsRebuild({ ...base, hasFormatValue: true }, base)).toBe(true);
  });

  it('does not rebuild for a data-only change (same shape, different points live outside ChartShape)', () => {
    // ChartShape deliberately carries no points -- a live-mode tick that
    // only changes series data never even produces a different ChartShape,
    // which is the whole point: the caller's setData path handles it.
    expect(needsRebuild(base, { ...base })).toBe(false);
  });

  it('rebuilds when a series width or dash changes -- the Metrics hero\'s dashed host line', () => {
    const withDash: ChartShape = { ...base, series: [{ ...base.series[0], width: 1, dash: [4, 4] }] };
    expect(needsRebuild(base, withDash)).toBe(true);
    expect(needsRebuild(withDash, { ...withDash })).toBe(false);
    expect(needsRebuild(withDash, { ...base, series: [{ ...base.series[0], width: 1, dash: [2, 2] }] })).toBe(true);
  });

  it('rebuilds when showLegend flips', () => {
    expect(needsRebuild(base, { ...base, showLegend: false })).toBe(true);
  });
});

describe('sameSeriesShape', () => {
  it('treats different-length lists as different', () => {
    expect(sameSeriesShape([], [{ label: 'a', colorVar: '--series-1' }])).toBe(false);
  });

  it('treats same label/colorVar pairs in order as the same', () => {
    const a = [
      { label: 'Down', colorVar: '--series-1' },
      { label: 'Up', colorVar: '--series-2' },
    ];
    const b = [
      { label: 'Down', colorVar: '--series-1' },
      { label: 'Up', colorVar: '--series-2' },
    ];
    expect(sameSeriesShape(a, b)).toBe(true);
  });

  it('treats the same labels in a different order as different', () => {
    const a = [
      { label: 'Down', colorVar: '--series-1' },
      { label: 'Up', colorVar: '--series-2' },
    ];
    const b = [
      { label: 'Up', colorVar: '--series-2' },
      { label: 'Down', colorVar: '--series-1' },
    ];
    expect(sameSeriesShape(a, b)).toBe(false);
  });
});

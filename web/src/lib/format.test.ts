import { describe, expect, it } from 'vitest';
import { fmtBytes, fmtCores, fmtDuration, fmtPct, fmtRate, fmtRelTime } from './format';

describe('fmtBytes', () => {
  it('formats sub-1024 values with the B unit, one decimal', () => {
    expect(fmtBytes(0)).toBe('0.0 B');
    expect(fmtBytes(512)).toBe('512.0 B');
  });

  it('steps through binary (1024-based) units', () => {
    expect(fmtBytes(1024)).toBe('1.0 KiB');
    expect(fmtBytes(1536)).toBe('1.5 KiB');
    expect(fmtBytes(1_048_576)).toBe('1.0 MiB');
    expect(fmtBytes(1_073_741_824)).toBe('1.0 GiB');
    expect(fmtBytes(1_099_511_627_776)).toBe('1.0 TiB');
  });

  it('handles negative and non-finite input defensively', () => {
    expect(fmtBytes(-2048)).toBe('-2.0 KiB');
    expect(fmtBytes(NaN)).toBe('0.0 B');
  });
});

describe('fmtRate', () => {
  it('formats sub-1000 values with the B/s unit, one decimal', () => {
    expect(fmtRate(0)).toBe('0.0 B/s');
    expect(fmtRate(512)).toBe('512.0 B/s');
  });

  it('steps through decimal (1000-based) units, not binary', () => {
    expect(fmtRate(1000)).toBe('1.0 KB/s');
    expect(fmtRate(1500)).toBe('1.5 KB/s');
    expect(fmtRate(1_000_000)).toBe('1.0 MB/s');
    expect(fmtRate(2_500_000)).toBe('2.5 MB/s');
  });

  it('clamps negative/non-finite input to zero', () => {
    expect(fmtRate(-5)).toBe('0.0 B/s');
    expect(fmtRate(NaN)).toBe('0.0 B/s');
  });
});

describe('fmtPct', () => {
  it('formats with one decimal', () => {
    expect(fmtPct(0)).toBe('0.0%');
    expect(fmtPct(45.67)).toBe('45.7%');
  });

  it('clamps display to the 0-100 range', () => {
    expect(fmtPct(103.2)).toBe('100.0%');
    expect(fmtPct(-5)).toBe('0.0%');
  });

  it('handles non-finite input defensively', () => {
    expect(fmtPct(NaN)).toBe('0.0%');
  });
});

describe('fmtCores', () => {
  it('formats with one decimal and a leading ≈', () => {
    expect(fmtCores(1.2)).toBe('≈1.2 cores');
    expect(fmtCores(2)).toBe('≈2.0 cores');
  });

  it('hides negligible usage below 0.05 cores', () => {
    expect(fmtCores(0.049)).toBe('');
    expect(fmtCores(0)).toBe('');
  });

  it('shows the boundary value at 0.05', () => {
    expect(fmtCores(0.05)).toBe('≈0.1 cores');
  });

  it('handles non-finite input defensively', () => {
    expect(fmtCores(NaN)).toBe('');
  });
});

describe('fmtDuration', () => {
  it('formats seconds-only durations', () => {
    expect(fmtDuration(0)).toBe('0s');
    expect(fmtDuration(45)).toBe('45s');
  });

  it('formats minutes+seconds', () => {
    expect(fmtDuration(125)).toBe('2m 5s');
  });

  it('formats hours+minutes', () => {
    expect(fmtDuration(3661)).toBe('1h 1m');
  });

  it('formats days+hours', () => {
    expect(fmtDuration(90000)).toBe('1d 1h');
  });

  it('handles negative/non-finite input defensively', () => {
    expect(fmtDuration(-10)).toBe('0s');
    expect(fmtDuration(NaN)).toBe('0s');
  });
});

describe('fmtRelTime', () => {
  const now = 1_700_000_000_000; // fixed ms reference, so cases are deterministic

  it('reports very recent timestamps as "just now"', () => {
    expect(fmtRelTime(now / 1000 - 2, now)).toBe('just now');
  });

  it('reports seconds ago', () => {
    expect(fmtRelTime(now / 1000 - 30, now)).toBe('30s ago');
  });

  it('reports minutes ago', () => {
    expect(fmtRelTime(now / 1000 - 60, now)).toBe('1m ago');
  });

  it('reports hours ago', () => {
    expect(fmtRelTime(now / 1000 - 18000, now)).toBe('5h ago');
  });

  it('reports days ago', () => {
    expect(fmtRelTime(now / 1000 - 259200, now)).toBe('3d ago');
  });

  it('handles non-finite input defensively', () => {
    expect(fmtRelTime(NaN, now)).toBe('');
  });
});

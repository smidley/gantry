import { describe, expect, it } from 'vitest';
import { band, bandToken } from './thresholds';

describe('band', () => {
  it('reads "normal" for a value at or below the warn threshold', () => {
    expect(band('host.cpu', 0)).toBe('normal');
    expect(band('host.cpu', 69.9)).toBe('normal');
    expect(band('host.cpu', 70)).toBe('normal'); // exactly on the boundary -- strict ">"
  });

  it('steps through warn/serious/critical for host.cpu and host.mem (70/85/95)', () => {
    for (const family of ['host.cpu', 'host.mem'] as const) {
      expect(band(family, 70.1)).toBe('warn');
      expect(band(family, 85)).toBe('warn');
      expect(band(family, 85.1)).toBe('serious');
      expect(band(family, 95)).toBe('serious');
      expect(band(family, 95.1)).toBe('critical');
      expect(band(family, 100)).toBe('critical');
    }
  });

  it('disk.capacity uses 70/90/95, matching Storage.svelte\'s own pre-existing 90% convention', () => {
    expect(band('disk.capacity', 90)).toBe('warn');
    expect(band('disk.capacity', 90.1)).toBe('serious');
    expect(band('disk.capacity', 95.1)).toBe('critical');
  });

  it('container.mem_limit_pct uses 75/85/95', () => {
    expect(band('container.mem_limit_pct', 75)).toBe('normal');
    expect(band('container.mem_limit_pct', 75.1)).toBe('warn');
    expect(band('container.mem_limit_pct', 85.1)).toBe('serious');
    expect(band('container.mem_limit_pct', 95.1)).toBe('critical');
  });

  it('disk temps use 45/55 (nvme 60/70) and never reach "critical" -- only two real tiers', () => {
    expect(band('disk.temp', 45.1)).toBe('warn');
    expect(band('disk.temp', 55.1)).toBe('serious');
    expect(band('disk.temp', 200)).toBe('serious'); // no fourth tier to escalate into

    expect(band('disk.temp.nvme', 60.1)).toBe('warn');
    expect(band('disk.temp.nvme', 70.1)).toBe('serious');
    expect(band('disk.temp.nvme', 200)).toBe('serious');
  });

  it('reads "normal" for non-finite input rather than throwing or escalating', () => {
    expect(band('host.cpu', NaN)).toBe('normal');
    expect(band('host.cpu', Infinity)).toBe('normal');
    expect(band('host.cpu', -Infinity)).toBe('normal');
  });
});

describe('bandToken', () => {
  it('is undefined for "normal" -- render plain ink, no status color at all', () => {
    expect(bandToken('normal')).toBeUndefined();
  });

  it('maps warn/serious/critical to their own status token, mixed toward --ink for contrast', () => {
    expect(bandToken('warn')).toBe('color-mix(in oklab, var(--status-warning) 55%, var(--ink) 45%)');
    expect(bandToken('serious')).toBe('color-mix(in oklab, var(--status-serious) 55%, var(--ink) 45%)');
    expect(bandToken('critical')).toBe('color-mix(in oklab, var(--status-critical) 55%, var(--ink) 45%)');
  });
});

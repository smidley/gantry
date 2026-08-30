import { describe, expect, it } from 'vitest';
import { band, bandToken, setBands } from './thresholds';
import type { AlertRuleBandLike, MetricFamily } from './thresholds';

// SEEDED_DEFAULT_THRESHOLD_RULES mirrors internal/store/alert_defaults.go's
// seven threshold builtins' own band_family/warn_threshold/threshold/
// critical_threshold numbers exactly (Task 5's own guarantee: "no
// number in this table is new" -- every one already equals its
// thresholds.ts family's existing warn/serious/critical). Kept as its
// own fixture, in sync with the Go table by the same convention
// store/alert_defaults_test.go's wantDefaultAlertRules() pins the Go
// side with -- if either drifts, this file's own regression test below
// (and, independently, the Go one) catches it.
const SEEDED_DEFAULT_THRESHOLD_RULES: AlertRuleBandLike[] = [
  { band_family: 'host.cpu', warn_threshold: 70, threshold: 85, critical_threshold: 95 },
  { band_family: 'host.mem', warn_threshold: 70, threshold: 85, critical_threshold: 95 },
  { band_family: 'disk.capacity', warn_threshold: 70, threshold: 90, critical_threshold: 95 },
  { band_family: 'disk.temp', warn_threshold: 45, threshold: 55, critical_threshold: 0 },
  { band_family: 'disk.temp.nvme', warn_threshold: 60, threshold: 70, critical_threshold: 0 },
  { band_family: 'container.mem_limit_pct', warn_threshold: 75, threshold: 85, critical_threshold: 95 },
];

// PROBE_VALUES: a handful of representative readings per family
// (normal/warn/serious/critical, where the family has one), reused by
// both the no-rules-loaded and seeded-defaults-loaded regression checks
// below so they're provably testing the exact same inputs.
const PROBE_VALUES: Record<MetricFamily, number[]> = {
  'host.cpu': [50, 71, 86, 96],
  'host.mem': [50, 71, 86, 96],
  'disk.capacity': [50, 71, 91, 96],
  'disk.temp': [30, 46, 56, 1000],
  'disk.temp.nvme': [40, 61, 71, 1000],
  'container.mem_limit_pct': [50, 76, 86, 96],
};

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

// setBands / band unification (Task 12): these tests share module state
// (runtimeBands is a plain module-level variable, not reset between
// tests), so every test below calls setBands itself first rather than
// assuming a pristine starting point.
describe('setBands (band unification)', () => {
  it('with no rules loaded, band() matches exactly what the compiled-in fallback table already gave for all six families', () => {
    setBands([]);
    expect(band('host.cpu', 71)).toBe('warn');
    expect(band('host.cpu', 86)).toBe('serious');
    expect(band('host.cpu', 96)).toBe('critical');
    expect(band('disk.temp', 56)).toBe('serious');
    expect(band('disk.temp', 1000)).toBe('serious'); // no fourth tier
    expect(band('disk.temp.nvme', 61)).toBe('warn');
    expect(band('container.mem_limit_pct', 96)).toBe('critical');
  });

  it('with the seeded defaults loaded, band() is byte-for-byte identical to the no-rules fallback -- the unification is a no-op on day one', () => {
    setBands([]);
    const before: Record<string, string> = {};
    for (const [family, values] of Object.entries(PROBE_VALUES) as [MetricFamily, number[]][]) {
      for (const v of values) before[`${family}:${v}`] = band(family, v);
    }

    setBands(SEEDED_DEFAULT_THRESHOLD_RULES);
    for (const [family, values] of Object.entries(PROBE_VALUES) as [MetricFamily, number[]][]) {
      for (const v of values) {
        expect(band(family, v), `${family} at ${v} must read the same as the fallback`).toBe(before[`${family}:${v}`]);
      }
    }
  });

  it('a rule\'s own numbers actually drive the band once loaded -- an edited threshold changes the color boundary', () => {
    setBands([{ band_family: 'host.cpu', warn_threshold: 10, threshold: 20, critical_threshold: 30 }]);
    expect(band('host.cpu', 15)).toBe('warn');
    expect(band('host.cpu', 25)).toBe('serious');
    expect(band('host.cpu', 35)).toBe('critical');
    // A family with no matching rule in this call is untouched by it --
    // still the compiled-in fallback, not reset to "normal always".
    expect(band('host.mem', 86)).toBe('serious');
  });

  it('critical_threshold=0 (the wire sentinel for "no fourth tier") must NOT read as critical at any positive value', () => {
    setBands([{ band_family: 'disk.temp', warn_threshold: 45, threshold: 55, critical_threshold: 0 }]);
    expect(band('disk.temp', 56)).toBe('serious'); // the trap: naive code would read this "critical" (56 > 0)
    expect(band('disk.temp', 1_000_000)).toBe('serious'); // still no fourth tier, at any value
  });

  it('a DISABLED rule still supplies its bands -- muting delivery must not silently recolor the app', () => {
    const disabledRule: AlertRuleBandLike & { enabled: boolean } = {
      band_family: 'container.mem_limit_pct',
      warn_threshold: 1,
      threshold: 2,
      critical_threshold: 3,
      enabled: false,
    };
    setBands([disabledRule]);
    expect(band('container.mem_limit_pct', 2.5)).toBe('serious');
  });

  it('a rule with an unknown or empty band_family is silently skipped, not an error', () => {
    setBands([]);
    const before = band('host.cpu', 86);
    setBands([
      { band_family: '', warn_threshold: 1, threshold: 2, critical_threshold: 3 },
      { band_family: 'not-a-real-family', warn_threshold: 1, threshold: 2, critical_threshold: 3 },
    ]);
    expect(band('host.cpu', 86)).toBe(before);
  });
});

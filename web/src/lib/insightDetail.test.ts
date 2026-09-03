import { describe, expect, it } from 'vitest';
import { evidenceRows, formatInstant, isNotFoundError, parseInsightId } from './insightDetail';
import type { EvidenceDTO } from './api';

// A fully-zeroed bundle, the shape the Go side always sends (it omits
// nothing) -- each test populates only the fields its own case is about.
function evidence(overrides: Partial<EvidenceDTO> = {}): EvidenceDTO {
  return {
    culprit_share_pct: 0,
    device_util_pct: 0,
    await_ms: 0,
    victim_stall_pct: 0,
    window_minutes: 0,
    other_users: [],
    iowait_pct: 0,
    host_cpu_pct: 0,
    spin_count: 0,
    spin_window_minutes: 0,
    engine_busy_pct: 0,
    baseline_pct: 0,
    ...overrides,
  };
}

describe('parseInsightId', () => {
  it('accepts a plain positive integer', () => {
    expect(parseInsightId('1')).toBe(1);
    expect(parseInsightId('4207')).toBe(4207);
  });

  it('rejects anything that could never be a rowid', () => {
    expect(parseInsightId('abc')).toBeNull();
    expect(parseInsightId('')).toBeNull();
    expect(parseInsightId('   ')).toBeNull();
    expect(parseInsightId(undefined)).toBeNull();
    expect(parseInsightId('0')).toBeNull();
    expect(parseInsightId('-3')).toBeNull();
    expect(parseInsightId('1.5')).toBeNull();
  });

  it('rejects a mistyped id outright rather than opening the wrong insight', () => {
    // parseInt("12abc") would be 12 -- see parseInsightId's own doc.
    expect(parseInsightId('12abc')).toBeNull();
  });
});

describe('isNotFoundError', () => {
  it('recognizes getJSON\'s own 404 message', () => {
    expect(isNotFoundError(new Error('GET /api/insights/12: 404 Not Found'))).toBe(true);
  });

  it('does not treat other failures as a missing insight', () => {
    expect(isNotFoundError(new Error('GET /api/insights/12: 500 Internal Server Error'))).toBe(false);
    expect(isNotFoundError(new TypeError('Failed to fetch'))).toBe(false);
  });

  it('does not fire on a 404 that is only part of the URL', () => {
    expect(isNotFoundError(new Error('GET /api/insights/404: 500 Internal Server Error'))).toBe(false);
  });
});

describe('evidenceRows', () => {
  it('renders only populated fields, in the declared order', () => {
    const rows = evidenceRows(evidence({ await_ms: 42, culprit_share_pct: 71 }));
    expect(rows.map((r) => r.key)).toEqual(['culprit_share_pct', 'await_ms']);
    expect(rows[0].label).not.toBe('culprit_share_pct'); // a real human label, not the raw key
    expect(rows[1].text).toContain('42');
  });

  it('omits zero-valued fields rather than claiming a measured zero', () => {
    expect(evidenceRows(evidence())).toEqual([]);
  });

  it('returns nothing at all for an absent bundle', () => {
    expect(evidenceRows(undefined)).toEqual([]);
  });
});

describe('formatInstant', () => {
  it('renders the store\'s own never-happened sentinel as an em dash', () => {
    expect(formatInstant(0)).toBe('—');
  });

  it('renders a real second as a local date+time', () => {
    expect(formatInstant(1756900000)).toBe(new Date(1756900000 * 1000).toLocaleString());
  });
});

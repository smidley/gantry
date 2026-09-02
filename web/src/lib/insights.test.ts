import { describe, expect, it } from 'vitest';
import {
  activeDuration,
  buildInsightGraph,
  confidenceLabel,
  culpritNames,
  describeRule,
  deviceSharePct,
  drawerMapAnchor,
  formatEvidenceNumber,
  insightsAffecting,
  insightsCausedBy,
  selectOverlappingInsights,
  sortActiveInsights,
} from './insights';
import type { InsightDTO } from './api';

describe('confidenceLabel', () => {
  it('renders the two-slot wire vocabulary in plain words', () => {
    expect(confidenceLabel('likely')).toBe('Likely');
    expect(confidenceLabel('confirmed')).toBe('Confirmed');
  });

  it('defaults an unrecognized value to Likely rather than throwing', () => {
    expect(confidenceLabel('')).toBe('Likely');
  });
});

describe('sortActiveInsights', () => {
  it('orders severity descending, confidence descending, then fired_at ascending', () => {
    const insights = [
      { id: 1, severity: 'info', confidence: 'likely', fired_at: 100 },
      { id: 2, severity: 'alert', confidence: 'likely', fired_at: 300 },
      { id: 3, severity: 'alert', confidence: 'confirmed', fired_at: 200 },
      { id: 4, severity: 'warning', confidence: 'likely', fired_at: 50 },
    ];
    const sorted = sortActiveInsights(insights).map((i) => i.id);
    // alert/confirmed (id 3) outranks alert/likely (id 2) at equal
    // severity; warning (id 4) beats info (id 1) regardless of timing.
    expect(sorted).toEqual([3, 2, 4, 1]);
  });

  it('returns a new array and never mutates its input', () => {
    const insights = [{ severity: 'info', confidence: 'likely', fired_at: 2 }, { severity: 'alert', confidence: 'likely', fired_at: 1 }];
    const original = [...insights];
    sortActiveInsights(insights);
    expect(insights).toEqual(original);
  });
});

describe('activeDuration', () => {
  it('renders the elapsed time since started_at', () => {
    expect(activeDuration(1000, 1090)).toBe('1m 30s');
  });
});

describe('culpritNames', () => {
  it('returns a single-element list for a single culprit', () => {
    expect(culpritNames({ culprit: 'qbittorrent', culprits: '' })).toEqual(['qbittorrent']);
  });

  it('splits a shared culprit set', () => {
    expect(culpritNames({ culprit: '', culprits: 'qbittorrent,sabnzbd' })).toEqual(['qbittorrent', 'sabnzbd']);
  });

  it('returns an empty list when neither column is set', () => {
    expect(culpritNames({ culprit: '', culprits: '' })).toEqual([]);
  });
});

describe('insightsAffecting / insightsCausedBy', () => {
  const insights = [
    { victim_kind: 'container', victim: 'jellyfin', culprit: 'qbittorrent', culprits: '' },
    { victim_kind: 'host', victim: '', culprit: 'qbittorrent', culprits: '' },
    { victim_kind: 'container', victim: 'sonarr', culprit: '', culprits: 'jellyfin,plex' },
    { victim_kind: 'gpu', victim: 'video', culprit: 'jellyfin', culprits: '' },
  ];

  it('insightsAffecting matches only a NAMED CONTAINER victim, not a host/gpu-kind row whose victim field holds something else', () => {
    const affecting = insightsAffecting(insights, 'jellyfin');
    expect(affecting).toHaveLength(1);
    expect(affecting[0].victim).toBe('jellyfin');
  });

  it('insightsCausedBy matches a single culprit and a shared culprit set alike', () => {
    const causedBy = insightsCausedBy(insights, 'jellyfin');
    // jellyfin is a culprit in the gpu row (single) and in the shared
    // "jellyfin,plex" row -- both must be found, the host-kind
    // qbittorrent row must not.
    expect(causedBy).toHaveLength(2);
  });

  it('a container that is neither culprit nor victim anywhere gets empty results from both, never an error', () => {
    expect(insightsAffecting(insights, 'grafana')).toEqual([]);
    expect(insightsCausedBy(insights, 'grafana')).toEqual([]);
  });
});

describe('deviceSharePct', () => {
  it('computes a container share of a device total', () => {
    expect(deviceSharePct(78e6, 0, 100e6, 0)).toBe(78);
  });

  it('sums read and write on both sides', () => {
    expect(deviceSharePct(30, 20, 100, 100)).toBe(25);
  });

  it('returns 0 rather than NaN/Infinity when the host total is 0', () => {
    expect(deviceSharePct(10, 0, 0, 0)).toBe(0);
  });

  it('clamps to 100 for a transient over-100% reading', () => {
    expect(deviceSharePct(150, 0, 100, 0)).toBe(100);
  });
});

describe('formatEvidenceNumber', () => {
  it('formats percentage fields with fmtPct', () => {
    expect(formatEvidenceNumber('culprit_share_pct', 78)).toBe('78.0%');
    expect(formatEvidenceNumber('device_util_pct', 98)).toBe('98.0%');
  });

  it('formats latency in whole milliseconds', () => {
    expect(formatEvidenceNumber('await_ms', 41.6)).toBe('42 ms');
  });

  it('formats a window in minutes', () => {
    expect(formatEvidenceNumber('window_minutes', 10)).toBe('10 min');
  });

  it('formats a spin count with a multiplication sign', () => {
    expect(formatEvidenceNumber('spin_count', 5)).toBe('5×');
  });

  it('falls back to a bare number for an unrecognized key', () => {
    expect(formatEvidenceNumber('made_up_field', 3)).toBe('3');
  });
});

describe('describeRule', () => {
  // The seven compiled-in rule ids, insight/rules.go's own librarySpecs
  // -- every one must produce a description that actually interpolates
  // its own threshold value, never a static/generic string, so an
  // edited threshold is reflected with no separate copy to keep in sync.
  const DEFAULT_THRESHOLDS: Record<string, Record<string, number>> = {
    'disk-io-contention': { util_pct_floor: 90, await_multiplier: 2, culprit_share_floor_pct: 60, psi_stall_floor: 20, sustain_secs: 90 },
    'io-driven-cpu-load': { iowait_pct_floor: 15, psi_io_some_floor: 20, psi_io_full_floor: 10, culprit_share_floor_pct: 50, sustain_secs: 90 },
    'cpu-starvation': { throttled_pct_floor: 5, psi_cpu_some_floor: 20, culprit_cpu_pct_floor: 40, host_cpu_total_floor: 85, sustain_secs: 90 },
    'parity-slowdown': { speed_floor_fraction_of_baseline: 0.75, culprit_share_floor_pct: 25, sustain_secs: 120 },
    'disk-spinup-churn': { min_transitions: 3, window_minutes: 60, attribution_window_secs: 60 },
    'gpu-engine-contention': { engine_busy_floor: 90, culprit_share_floor_pct: 10, min_culprits: 2, sustain_secs: 90 },
    'memory-squeeze': { mem_used_pct_floor: 92, psi_mem_some_floor: 10, culprit_mem_pct_floor: 30, sustain_secs: 90 },
  };

  it('generates a distinct, non-empty description for every one of the seven compiled-in rules', () => {
    for (const [ruleID, thresholds] of Object.entries(DEFAULT_THRESHOLDS)) {
      const description = describeRule(ruleID, thresholds, 'fallback title');
      expect(description.length).toBeGreaterThan(0);
      expect(description).not.toBe('fallback title');
    }
  });

  it('interpolates the CURRENT threshold value, not a hardcoded default', () => {
    const edited = describeRule('disk-io-contention', { ...DEFAULT_THRESHOLDS['disk-io-contention'], util_pct_floor: 75 }, 'Disk IO contention');
    expect(edited).toContain('75%');
    expect(edited).not.toContain('90%');
  });

  it('falls back to the rule title for an unrecognized id (defensive: the library is fixed)', () => {
    expect(describeRule('made-up-rule', {}, 'Made Up Rule')).toBe('Made Up Rule');
  });
});

describe('drawerMapAnchor', () => {
  it('anchors an active insight to now, not its own fired_at', () => {
    expect(drawerMapAnchor({ state: 'active', fired_at: 100 }, 5000)).toBe(5000);
  });

  it('anchors a resolved insight to its own fired_at, not now', () => {
    expect(drawerMapAnchor({ state: 'resolved', fired_at: 100 }, 5000)).toBe(100);
  });
});

describe('selectOverlappingInsights', () => {
  it('returns [] for an empty pool', () => {
    expect(selectOverlappingInsights([], 1000)).toEqual([]);
  });

  it('returns [] when nothing in a non-empty pool overlaps the anchor', () => {
    const pool = [{ id: 1, started_at: 2000, resolved_at: 2500 }];
    expect(selectOverlappingInsights(pool, 1000)).toEqual([]);
  });

  it('includes a single insight whose closed window contains the anchor', () => {
    const pool = [{ id: 1, started_at: 100, resolved_at: 200 }];
    expect(selectOverlappingInsights(pool, 150)).toEqual(pool);
  });

  it('includes an open-ended (still active, resolved_at 0) insight for any anchor at or after it started', () => {
    const pool = [{ id: 1, started_at: 100, resolved_at: 0 }];
    // Both a near anchor and one far in the future: an open window never
    // stops covering once it has started.
    expect(selectOverlappingInsights(pool, 101)).toEqual(pool);
    expect(selectOverlappingInsights(pool, 10_000_000)).toEqual(pool);
  });

  it('excludes an open-ended insight when the anchor is BEFORE it even started', () => {
    const pool = [{ id: 1, started_at: 5000, resolved_at: 0 }];
    expect(selectOverlappingInsights(pool, 100)).toEqual([]);
  });

  it('treats both bounds as inclusive', () => {
    const pool = [{ id: 1, started_at: 100, resolved_at: 200 }];
    expect(selectOverlappingInsights(pool, 100)).toEqual(pool); // anchor == started_at
    expect(selectOverlappingInsights(pool, 200)).toEqual(pool); // anchor == resolved_at
  });

  it('selects every overlapping insight out of several, sorted by id ascending regardless of input order', () => {
    const pool = [
      { id: 3, started_at: 90, resolved_at: 300 }, // overlaps
      { id: 1, started_at: 10, resolved_at: 20 }, // resolved long before the anchor
      { id: 2, started_at: 50, resolved_at: 0 }, // still active, started before the anchor
    ];
    expect(selectOverlappingInsights(pool, 150).map((i) => i.id)).toEqual([2, 3]);
  });

  it('dedupes by id, keeping one entry when the same insight arrives from more than one source', () => {
    const pool = [
      { id: 1, started_at: 100, resolved_at: 0 },
      { id: 1, started_at: 100, resolved_at: 0 },
    ];
    expect(selectOverlappingInsights(pool, 150)).toHaveLength(1);
  });
});

// dto builds a minimal, valid InsightDTO for buildInsightGraph's own
// tests -- every field buildInsightGraph itself actually reads, with the
// rest defaulted to values that would visibly stand out if accidentally
// read (id 0, empty strings), the same "narrow but honest" fixture style
// mapLayout.test.ts's own graph() helper uses.
function dto(overrides: Partial<InsightDTO>): InsightDTO {
  return {
    id: 0,
    rule_id: 'disk-io-contention',
    victim_kind: '',
    victim: '',
    culprit: '',
    culprits: '',
    resource: 'disk3',
    state: 'active',
    severity: 'warning',
    confidence: 'likely',
    tier: 'proxy',
    statement: '',
    started_at: 0,
    fired_at: 0,
    resolved_at: 0,
    resolve_reason: '',
    ...overrides,
  };
}

describe('buildInsightGraph (client-side mirror of api_insights.go)', () => {
  it('returns an empty graph for no instances', () => {
    expect(buildInsightGraph([])).toEqual({ nodes: [], edges: [] });
  });

  it('a named victim container gets both a culprit edge and a victim edge, sized from its own evidence', () => {
    const g = buildInsightGraph([
      dto({
        id: 1,
        culprit: 'qbittorrent',
        victim_kind: 'container',
        victim: 'jellyfin',
        resource: 'disk3',
        evidence: { culprit_share_pct: 78, victim_stall_pct: 38 } as InsightDTO['evidence'],
      }),
    ]);
    expect(g.nodes).toHaveLength(3); // qbittorrent, resource:disk3, jellyfin
    const culpritEdge = g.edges.find((e) => e.kind === 'culprit');
    const victimEdge = g.edges.find((e) => e.kind === 'victim');
    expect(culpritEdge).toMatchObject({ from: 'qbittorrent', to: 'resource:disk3', share_pct: 78 });
    expect(victimEdge).toMatchObject({ from: 'resource:disk3', to: 'jellyfin', share_pct: 38 });
  });

  it('a host-wide finding (no named victim container) gets only a culprit edge', () => {
    const g = buildInsightGraph([dto({ id: 2, rule_id: 'io-driven-cpu-load', culprit: 'sabnzbd', victim_kind: 'host', victim: '', resource: 'cpu' })]);
    expect(g.edges).toHaveLength(1);
    expect(g.edges[0]).toMatchObject({ kind: 'culprit', from: 'sabnzbd', to: 'resource:cpu' });
  });

  it('a shared culprit set fans into one culprit edge per name, all landing on the same resource node', () => {
    const g = buildInsightGraph([dto({ id: 4, culprit: '', culprits: 'qbittorrent,sabnzbd', resource: 'disk3' })]);
    expect(g.nodes).toHaveLength(3); // qbittorrent, sabnzbd, resource:disk3
    expect(g.edges).toHaveLength(2);
    for (const e of g.edges) {
      expect(e.kind).toBe('culprit');
      expect(e.to).toBe('resource:disk3');
    }
  });

  it('a gpu engine victim (VictimKind "gpu", victim holding the bare engine name) is never mistaken for a container node', () => {
    const g = buildInsightGraph([dto({ id: 3, rule_id: 'gpu-engine-contention', culprit: 'jellyfin', victim_kind: 'gpu', victim: 'video', resource: 'gpu:video' })]);
    expect(g.nodes.some((n) => n.id === 'video')).toBe(false);
    expect(g.edges).toHaveLength(1);
    expect(g.edges[0].kind).toBe('culprit');
  });

  it('a container that is a culprit in one insight and a victim in another is placed exactly once', () => {
    const aSlowsB = dto({ id: 5, culprit: 'qbittorrent', victim_kind: 'container', victim: 'jellyfin', resource: 'disk3' });
    const cSlowsA = dto({ id: 6, rule_id: 'cpu-starvation', culprit: 'plex', victim_kind: 'container', victim: 'qbittorrent', resource: 'cpu' });
    const g = buildInsightGraph([aSlowsB, cSlowsA]);
    expect(g.nodes.filter((n) => n.id === 'qbittorrent')).toHaveLength(1);
    expect(g.edges).toHaveLength(4);
  });

  it('missing evidence defaults share_pct to 0 rather than throwing', () => {
    const g = buildInsightGraph([dto({ id: 7, culprit: 'qbittorrent', evidence: undefined })]);
    expect(g.edges[0].share_pct).toBe(0);
  });

  it('is deterministically ordered: identical (but shuffled) input always sorts the same', () => {
    const insts = [
      dto({ id: 7, culprit: 'jellyfin', resource: 'disk3' }),
      dto({ id: 8, rule_id: 'io-driven-cpu-load', culprit: 'sabnzbd', resource: 'cpu' }),
      dto({ id: 9, culprit: 'plex', victim_kind: 'container', victim: 'sonarr', resource: 'memory' }),
    ];
    const first = buildInsightGraph(insts);
    for (let i = 0; i < 10; i++) {
      expect(buildInsightGraph([...insts].reverse())).toEqual(first);
    }
  });
});

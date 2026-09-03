import { describe, expect, it } from 'vitest';
import {
  DISK_SPINUP_CHURN_DEFAULT_WINDOW_SECS,
  EVIDENCE_WINDOW_SECS,
  MAX_PAD_SEC,
  MIN_PAD_SEC,
  hasChartableData,
  incidentBand,
  incidentChartWindow,
  incidentLookbackSecs,
  incidentMarkers,
  planIncidentCharts,
  type PlanInput,
} from './incidentChart';

// NO_LOOKBACK_RULE: a rule/victim_kind combination incidentLookbackSecs
// itself resolves to a 0 look-back (the memory-squeeze OOM/container
// path -- an instant, not a sustained trend) -- used throughout the pure
// padding-math tests below so they exercise incidentChartWindow's own
// arithmetic in isolation from the look-back extension, which gets its
// own dedicated tests further down.
const NO_LOOKBACK_RULE = { rule_id: 'memory-squeeze', victim_kind: 'container' };

describe('incidentLookbackSecs', () => {
  it('the five sustained-threshold rules default to EVIDENCE_WINDOW_SECS when evidence.window_minutes is absent (every tier-1/likely finding, today)', () => {
    for (const rule_id of ['disk-io-contention', 'io-driven-cpu-load', 'cpu-starvation', 'parity-slowdown', 'gpu-engine-contention']) {
      expect(incidentLookbackSecs({ rule_id, victim_kind: '' })).toBe(EVIDENCE_WINDOW_SECS);
    }
  });

  it('prefers evidence.window_minutes over the constant when the engine populated it (the PSI-confirmed branches, and cpu-starvation at any tier)', () => {
    expect(incidentLookbackSecs({ rule_id: 'io-driven-cpu-load', victim_kind: '', evidence: { window_minutes: 5 } })).toBe(300);
    expect(incidentLookbackSecs({ rule_id: 'cpu-starvation', victim_kind: '', evidence: { window_minutes: 2 } })).toBe(120);
  });

  it('a zero or negative evidence.window_minutes is treated as absent, not as a genuine zero-length look-back', () => {
    expect(incidentLookbackSecs({ rule_id: 'disk-io-contention', victim_kind: '', evidence: { window_minutes: 0 } })).toBe(EVIDENCE_WINDOW_SECS);
  });

  it('memory-squeeze: the OOM/container path is event-shaped -- no look-back, ever, regardless of evidence', () => {
    expect(incidentLookbackSecs({ rule_id: 'memory-squeeze', victim_kind: 'container', evidence: { window_minutes: 99 } })).toBe(0);
  });

  it('memory-squeeze: the host-wide threshold path shares the other five rules\' own EVIDENCE_WINDOW_SECS behavior', () => {
    expect(incidentLookbackSecs({ rule_id: 'memory-squeeze', victim_kind: 'host' })).toBe(EVIDENCE_WINDOW_SECS);
    expect(incidentLookbackSecs({ rule_id: 'memory-squeeze', victim_kind: 'host', evidence: { window_minutes: 2 } })).toBe(120);
  });

  it('disk-spinup-churn reads its own LIVE evidence.spin_window_minutes (reflects a real per-install override), not the compiled-in default', () => {
    expect(incidentLookbackSecs({ rule_id: 'disk-spinup-churn', victim_kind: '', evidence: { spin_window_minutes: 30 } })).toBe(30 * 60);
  });

  it('disk-spinup-churn falls back to its own compiled-in default only when evidence is missing the field entirely', () => {
    expect(incidentLookbackSecs({ rule_id: 'disk-spinup-churn', victim_kind: '' })).toBe(DISK_SPINUP_CHURN_DEFAULT_WINDOW_SECS);
  });

  it('an unrecognized rule id gets no look-back -- the library is fixed and closed, never a guess', () => {
    expect(incidentLookbackSecs({ rule_id: 'made-up-rule', victim_kind: '' })).toBe(0);
  });
});

describe('incidentBand', () => {
  it('is [started_at, resolved_at] with no extension for an event-shaped finding (no look-back)', () => {
    expect(incidentBand({ state: 'resolved', started_at: 100, resolved_at: 200, ...NO_LOOKBACK_RULE }, 999)).toEqual([100, 200]);
  });

  it('is [started_at, nowSec] for a still-active, event-shaped finding', () => {
    expect(incidentBand({ state: 'active', started_at: 100, resolved_at: 0, ...NO_LOOKBACK_RULE }, 999)).toEqual([100, 999]);
  });

  // Regression: the owner's own reported bug (io-driven-cpu-load on
  // "Optimisarr", evidence text "stalled on IO 67.6% of the last 2
  // minutes") -- the host iowait spike peaked ~90s before started_at in
  // his screenshot, and the OLD band ([started_at, resolved_at]) sat
  // entirely after it, over flat data. The band's own left edge must now
  // sit BEFORE started_at by the rule's own look-back, so the spike
  // falls inside the shaded region instead of before it.
  it('extends the band BEFORE started_at for a sustained-threshold rule -- the owner\'s own reported bug', () => {
    const inst = { state: 'resolved', started_at: 1000, resolved_at: 1030, rule_id: 'io-driven-cpu-load', victim_kind: '' };
    expect(incidentBand(inst, 999999)).toEqual([1000 - EVIDENCE_WINDOW_SECS, 1030]);
    // The spike the owner's screenshot showed, ~90s before started_at,
    // must now fall INSIDE the band -- the whole point of the fix.
    const spikeTs = 1000 - 90;
    const [bandStart, bandEnd] = incidentBand(inst, 999999);
    expect(spikeTs).toBeGreaterThanOrEqual(bandStart);
    expect(spikeTs).toBeLessThanOrEqual(bandEnd);
  });

  it('extends the band using disk-spinup-churn\'s own live (possibly-overridden) window, not the fixed EVIDENCE_WINDOW_SECS', () => {
    const inst = { state: 'resolved', started_at: 10_000, resolved_at: 10_030, rule_id: 'disk-spinup-churn', victim_kind: '', evidence: { spin_window_minutes: 45 } };
    expect(incidentBand(inst, 999999)).toEqual([10_000 - 45 * 60, 10_030]);
  });
});

describe('incidentChartWindow', () => {
  it('floors a brief incident\'s padding at MIN_PAD_SEC on both sides (no look-back extension)', () => {
    const inst = { state: 'resolved', started_at: 1000, resolved_at: 1060, ...NO_LOOKBACK_RULE }; // 60s duration
    expect(incidentChartWindow(inst, 999999)).toEqual([1000 - MIN_PAD_SEC, 1060 + MIN_PAD_SEC]);
  });

  it('pads by roughly the incident\'s own duration when between the floor and cap (no look-back extension)', () => {
    const inst = { state: 'resolved', started_at: 1000, resolved_at: 1000 + 1200, ...NO_LOOKBACK_RULE }; // 20 min
    expect(incidentChartWindow(inst, 999999)).toEqual([1000 - 1200, 2200 + 1200]);
  });

  it('caps a long incident\'s padding at MAX_PAD_SEC on both sides (no look-back extension)', () => {
    const inst = { state: 'resolved', started_at: 1000, resolved_at: 1000 + 5 * 3600, ...NO_LOOKBACK_RULE }; // 5h
    const [from, to] = incidentChartWindow(inst, 999999);
    expect(from).toBe(1000 - MAX_PAD_SEC);
    expect(to).toBe(1000 + 5 * 3600 + MAX_PAD_SEC);
  });

  it('an ACTIVE incident is padded only on the leading side -- `to` is exactly nowSec, never past it', () => {
    const nowSec = 100000;
    const inst = { state: 'active', started_at: nowSec - 300, resolved_at: 0, ...NO_LOOKBACK_RULE }; // 5 min old, still active
    const [from, to] = incidentChartWindow(inst, nowSec);
    expect(to).toBe(nowSec);
    expect(from).toBe(nowSec - 300 - MIN_PAD_SEC);
  });

  it('a zero-duration incident (started == resolved) still gets the full floor pad, never zero', () => {
    const inst = { state: 'resolved', started_at: 5000, resolved_at: 5000, ...NO_LOOKBACK_RULE };
    expect(incidentChartWindow(inst, 999999)).toEqual([5000 - MIN_PAD_SEC, 5000 + MIN_PAD_SEC]);
  });

  // The owner's own fix requirement: "incidentChartWindow's padding
  // should then be computed off the extended span so context padding
  // still surrounds the FULL band" -- a windowed rule's own duration
  // (for padding purposes) must include the look-back, not just
  // resolved_at - started_at.
  it('pads off the EXTENDED (band) span, not the bare [started_at, resolved_at], for a sustained-threshold rule', () => {
    const inst = { state: 'resolved', started_at: 1000, resolved_at: 1000 + 1200, rule_id: 'io-driven-cpu-load', victim_kind: '' }; // 1200s active, plus a 120s look-back
    // Band = [1000 - 120, 2200] = [880, 2200]; extended duration = 2200 -
    // 880 = 1320s -> pad = 1320 (between floor/cap), computed off the
    // BAND, not off the bare 1200s active span alone (which would have
    // padded by only 1200).
    const [from, to] = incidentChartWindow(inst, 999999);
    expect(from).toBe(1000 - EVIDENCE_WINDOW_SECS - 1320);
    expect(to).toBe(2200 + 1320);
  });

  it('the requested window always fully contains the band it was padded around', () => {
    const inst = { state: 'resolved', started_at: 1000, resolved_at: 1030, rule_id: 'gpu-engine-contention', victim_kind: '' };
    const [bandStart, bandEnd] = incidentBand(inst, 999999);
    const [from, to] = incidentChartWindow(inst, 999999);
    expect(from).toBeLessThanOrEqual(bandStart);
    expect(to).toBeGreaterThanOrEqual(bandEnd);
  });
});

describe('incidentMarkers', () => {
  it('an active insight gets only a Fired marker', () => {
    const marks = incidentMarkers({ state: 'active', fired_at: 100, resolved_at: 0, resolve_reason: '' });
    expect(marks).toEqual([{ ts: 100, severity: 'info', label: 'Fired' }]);
  });

  it('a resolved insight gets both Fired and Resolved markers, both info severity', () => {
    const marks = incidentMarkers({ state: 'resolved', fired_at: 100, resolved_at: 500, resolve_reason: 'cleared' });
    expect(marks).toEqual([
      { ts: 100, severity: 'info', label: 'Fired' },
      { ts: 500, severity: 'info', label: 'Resolved (cleared)' },
    ]);
  });

  it('the Resolved label carries the real resolve_reason (dismissed, restart, ...)', () => {
    const marks = incidentMarkers({ state: 'resolved', fired_at: 1, resolved_at: 2, resolve_reason: 'dismissed' });
    expect(marks[1].label).toBe('Resolved (dismissed)');
  });
});

describe('hasChartableData', () => {
  it('false for an empty result list', () => {
    expect(hasChartableData([])).toBe(false);
  });

  it('false when every result has zero points', () => {
    expect(hasChartableData([{ metric: 'a', points: [] }, { metric: 'b', points: [] }])).toBe(false);
  });

  it('true when at least one result carries a point, even if others are empty', () => {
    expect(hasChartableData([{ metric: 'a', points: [] }, { metric: 'b', points: [[1, 2, 2]] }])).toBe(true);
  });
});

// dto builds a minimal PlanInput for planIncidentCharts' own tests --
// every field a rule branch actually reads, defaulted to values that
// would visibly stand out if accidentally read, the exact insights.
// test.ts dto() helper precedent.
function dto(overrides: Partial<PlanInput>): PlanInput {
  return { rule_id: 'disk-io-contention', resource: 'disk1', victim_kind: '', victim: '', culprit: 'qbittorrent', culprits: '', ...overrides };
}

describe('planIncidentCharts', () => {
  it('disk-io-contention: two charts (victim util%, culprit IO rate); the victim device resolves through diskMeta', () => {
    const charts = planIncidentCharts(dto({ resource: 'disk1' }), { diskMeta: { disk1: { device: 'sdc' } } });
    expect(charts).toHaveLength(2);
    const victim = charts.find((c) => c.key === 'victim')!;
    expect(victim.formatter).toBe('pct');
    expect(victim.lines).toEqual([{ kind: 'host', entity: '', metrics: ['diskio.sdc.util_pct'], label: 'disk1', colorVar: '--series-1' }]);
    const culprit = charts.find((c) => c.key === 'culprit')!;
    expect(culprit.formatter).toBe('rate');
    expect(culprit.lines).toEqual([{ kind: 'container', entity: 'qbittorrent', metrics: ['io.read_bps', 'io.write_bps'], label: 'qbittorrent', colorVar: '--series-2' }]);
  });

  it('disk-io-contention: an unresolvable device (no diskMeta entry) falls back to using the resource label AS the device name, never omitted', () => {
    const charts = planIncidentCharts(dto({ resource: 'sdz' }), { diskMeta: {} });
    const victim = charts.find((c) => c.key === 'victim')!;
    expect(victim.lines[0].metrics).toEqual(['diskio.sdz.util_pct']);
  });

  it('io-driven-cpu-load: host iowait victim chart + a culprit IO chart with one line PER shared culprit', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'io-driven-cpu-load', culprit: '', culprits: 'sabnzbd,qbittorrent' }));
    expect(charts).toHaveLength(2);
    const victim = charts.find((c) => c.key === 'victim')!;
    expect(victim.lines).toEqual([{ kind: 'host', entity: '', metrics: ['cpu.iowait_pct'], label: 'Host IO-wait', colorVar: '--series-1' }]);
    const culprit = charts.find((c) => c.key === 'culprit')!;
    expect(culprit.lines.map((l) => l.entity)).toEqual(['sabnzbd', 'qbittorrent']);
    expect(culprit.lines.every((l) => l.kind === 'container' && l.metrics[0] === 'io.read_bps')).toBe(true);
  });

  it('cpu-starvation: one combined chart, both lines plain percentages', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'cpu-starvation', victim: 'sonarr', culprit: 'plex' }));
    expect(charts).toHaveLength(1);
    expect(charts[0].key).toBe('combined');
    expect(charts[0].formatter).toBe('pct');
    expect(charts[0].lines).toEqual([
      { kind: 'container', entity: 'sonarr', metrics: ['cpu.throttled_pct'], label: 'sonarr (throttled)', colorVar: '--series-1' },
      { kind: 'container', entity: 'plex', metrics: ['cpu.pct'], label: 'plex', colorVar: '--series-2' },
    ]);
  });

  it('parity-slowdown: one combined chart keyed off the fixed unraid/array parity series', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'parity-slowdown', resource: 'parity', culprit: 'qbittorrent' }));
    expect(charts).toHaveLength(1);
    expect(charts[0].formatter).toBe('rate');
    expect(charts[0].lines[0]).toEqual({ kind: 'unraid', entity: 'array', metrics: ['parity.speed_bps'], label: 'Parity speed', colorVar: '--series-1' });
  });

  it('disk-spinup-churn: victim chart reads the slot directly (no device resolution) with the plain formatter, plus a culprit IO chart', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'disk-spinup-churn', resource: 'disk2', culprit: 'sonarr' }));
    expect(charts).toHaveLength(2);
    const victim = charts.find((c) => c.key === 'victim')!;
    expect(victim.formatter).toBe('plain');
    expect(victim.lines).toEqual([{ kind: 'disk', entity: 'disk2', metrics: ['spun_up'], label: 'disk2', colorVar: '--series-1' }]);
  });

  it('gpu-engine-contention: the victim engine line only appears when exactly ONE gpu entity is currently known', () => {
    const inst = dto({ rule_id: 'gpu-engine-contention', victim: 'video', culprit: 'jellyfin' });
    const withOne = planIncidentCharts(inst, { gpuEntities: ['0000:01:00.0'] });
    expect(withOne[0].lines).toHaveLength(2);
    expect(withOne[0].lines[0]).toEqual({ kind: 'gpu', entity: '0000:01:00.0', metrics: ['engine.video.busy_pct'], label: 'Engine video', colorVar: '--series-1' });

    const withNone = planIncidentCharts(inst, { gpuEntities: [] });
    expect(withNone[0].lines).toHaveLength(1); // victim line omitted, culprit line still present
    expect(withNone[0].lines[0].kind).toBe('container');

    const withAmbiguous = planIncidentCharts(inst, { gpuEntities: ['gpu0', 'gpu1'] });
    expect(withAmbiguous[0].lines).toHaveLength(1);
  });

  it('memory-squeeze: a container-kind victim (the OOM path) charts that container\'s own mem.pct', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'memory-squeeze', victim_kind: 'container', victim: 'sonarr', culprit: 'minecraft' }));
    expect(charts[0].lines[0]).toEqual({ kind: 'container', entity: 'sonarr', metrics: ['mem.pct'], label: 'sonarr (OOM)', colorVar: '--series-1' });
  });

  it('memory-squeeze: a host-kind victim charts host-wide mem.used_pct instead', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'memory-squeeze', victim_kind: 'host', victim: '', culprit: 'minecraft' }));
    expect(charts[0].lines[0]).toEqual({ kind: 'host', entity: '', metrics: ['mem.used_pct'], label: 'Host memory', colorVar: '--series-1' });
  });

  it('a shared culprit set is capped at MAX_CULPRIT_LINES (4) rather than growing the legend unbounded', () => {
    const charts = planIncidentCharts(dto({ rule_id: 'memory-squeeze', victim_kind: 'host', culprit: '', culprits: 'a,b,c,d,e,f' }));
    // 1 victim line + at most 4 culprit lines
    expect(charts[0].lines).toHaveLength(5);
  });

  it('an unrecognized rule id returns no charts rather than throwing (the library is fixed and closed)', () => {
    expect(planIncidentCharts(dto({ rule_id: 'made-up-rule' }))).toEqual([]);
  });
});

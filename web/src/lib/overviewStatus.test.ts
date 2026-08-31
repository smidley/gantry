import { describe, expect, it } from 'vitest';
import {
  ackKeyFor,
  calloutTextBySlot,
  deriveOverviewStatus,
  describeAnomaly,
  fleetSentence,
  worstSeverity,
  type OverviewAnomaly,
} from './overviewStatus';

const BASE = {
  unhealthyNames: [] as string[],
  arrayStarted: 1,
  disks: {},
  sources: {},
};

describe('deriveOverviewStatus', () => {
  it('is ok, with the plain headline, when every signal is quiet', () => {
    const status = deriveOverviewStatus(BASE);
    expect(status.ok).toBe(true);
    expect(status.headline).toBe('Everything is running');
    expect(status.anomalies).toEqual([]);
    expect(status.flaggedDiskSlots).toEqual([]);
  });

  it('treats an absent array.started reading as fine, not stopped', () => {
    const status = deriveOverviewStatus({ ...BASE, arrayStarted: undefined });
    expect(status.ok).toBe(true);
  });

  it('one unhealthy container -> singular headline and one anomaly', () => {
    const status = deriveOverviewStatus({ ...BASE, unhealthyNames: ['sonarr'] });
    expect(status.ok).toBe(false);
    expect(status.headline).toBe('1 thing needs you');
    expect(status.anomalies).toEqual([{ kind: 'unhealthy', name: 'sonarr' }]);
  });

  it('each unhealthy container is its own anomaly, in name order', () => {
    const status = deriveOverviewStatus({ ...BASE, unhealthyNames: ['sonarr', 'radarr'] });
    expect(status.headline).toBe('2 things need you');
    expect(status.anomalies).toEqual([
      { kind: 'unhealthy', name: 'sonarr' },
      { kind: 'unhealthy', name: 'radarr' },
    ]);
  });

  it('stopped containers are NOT an anomaly: the input carries no stopped count at all, and a quiet frame stays ok', () => {
    // Scott: "stopped containers are not something that needs you."
    // The derivation has no stopped input anymore -- the fleet sentence
    // (fleetSentence below) is the one place stopped is still stated,
    // as a fact rather than a callout. This test pins the input SHAPE:
    // nothing here can re-grow a stopped-derived anomaly without a test
    // noticing the union/interface change.
    const status = deriveOverviewStatus(BASE);
    expect(status.ok).toBe(true);
    expect(status.headline).toBe('Everything is running');
  });

  it('a disk at exactly the 90% threshold is not flagged', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk1: { 'fs.used_bytes': 90, 'fs.free_bytes': 10 } },
    });
    expect(status.ok).toBe(true);
  });

  it('a disk just over 90% is flagged, with the exact usage percentage', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk1: { 'fs.used_bytes': 901, 'fs.free_bytes': 99 } }, // 90.1%
    });
    expect(status.anomalies).toHaveLength(1);
    const anomaly = status.anomalies[0];
    expect(anomaly.kind).toBe('disk-usage');
    expect((anomaly as { slot: string }).slot).toBe('disk1');
    expect((anomaly as { usagePct: number }).usagePct).toBeCloseTo(90.1);
    expect(status.flaggedDiskSlots).toEqual(['disk1']);
  });

  it('among several over-90% disks, only the single fullest is flagged', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: {
        disk1: { 'fs.used_bytes': 91, 'fs.free_bytes': 9 }, // 91%
        disk2: { 'fs.used_bytes': 98, 'fs.free_bytes': 2 }, // 98% -- the worst
        disk3: { 'fs.used_bytes': 50, 'fs.free_bytes': 50 }, // fine
      },
    });
    expect(status.anomalies).toEqual([{ kind: 'disk-usage', slot: 'disk2', usagePct: 98 }]);
    expect(status.flaggedDiskSlots).toEqual(['disk2']);
  });

  it('a disk with a positive error count is flagged even under the usage threshold', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk2: { 'fs.used_bytes': 10, 'fs.free_bytes': 90, errors: 1 } },
    });
    expect(status.anomalies).toEqual([{ kind: 'disk-errors', slot: 'disk2', errors: 1 }]);
    expect(status.flaggedDiskSlots).toEqual(['disk2']);
  });

  it('disk errors on more than one disk each get their own anomaly, sorted by slot', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: {
        disk3: { errors: 2 },
        disk1: { errors: 1 },
      },
    });
    expect(status.anomalies).toEqual([
      { kind: 'disk-errors', slot: 'disk1', errors: 1 },
      { kind: 'disk-errors', slot: 'disk3', errors: 2 },
    ]);
  });

  it('a disk can be flagged for both usage and errors at once, contributing two anomalies', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk1: { 'fs.used_bytes': 95, 'fs.free_bytes': 5, errors: 3 } },
    });
    expect(status.anomalies).toEqual([
      { kind: 'disk-usage', slot: 'disk1', usagePct: 95 },
      { kind: 'disk-errors', slot: 'disk1', errors: 3 },
    ]);
    expect(status.flaggedDiskSlots).toEqual(['disk1', 'disk1']);
  });

  it('array.started === 0 is an anomaly', () => {
    const status = deriveOverviewStatus({ ...BASE, arrayStarted: 0 });
    expect(status.anomalies).toEqual([{ kind: 'array-stopped' }]);
  });

  it('a degraded docker source (critical) is an anomaly', () => {
    const status = deriveOverviewStatus({ ...BASE, sources: { docker: 'daemon unreachable' } });
    expect(status.anomalies).toEqual([{ kind: 'source-critical', source: 'docker', detail: 'daemon unreachable' }]);
  });

  it('a degraded non-critical source (nvidia, pressure) never becomes an anomaly', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      sources: { nvidia: 'no nvidia-smi on PATH', pressure: 'PSI disabled' },
    });
    expect(status.ok).toBe(true);
  });

  it('a source reading "ok" is never an anomaly', () => {
    const status = deriveOverviewStatus({ ...BASE, sources: { docker: 'ok' } });
    expect(status.ok).toBe(true);
  });

  it('combines every kind at once, with the headline counting every row', () => {
    const status = deriveOverviewStatus({
      unhealthyNames: ['sonarr'],
      arrayStarted: 0,
      disks: { disk1: { 'fs.used_bytes': 95, 'fs.free_bytes': 5 } },
      sources: { docker: 'daemon unreachable' },
    });
    // unhealthy(1) + disk-usage(1) + array-stopped(1) + source-critical(1) = 4
    expect(status.anomalies).toHaveLength(4);
    expect(status.headline).toBe('4 things need you');
  });
});

describe('deriveOverviewStatus alerts merge (Task 12)', () => {
  it('a firing alert with no dedup match gets its own row', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      alerts: [{ rule_id: 'host-cpu-high', rule_name: 'Host CPU high', severity: 'warning', entity: '', silenced: false }],
    });
    expect(status.anomalies).toEqual([
      { kind: 'alert', ruleId: 'host-cpu-high', ruleName: 'Host CPU high', entity: '', severity: 'warning' },
    ]);
    expect(status.headline).toBe('1 thing needs you');
  });

  it('an EVENT alert (no metric) shows its own summary as the callout detail instead of the bare entity', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      alerts: [
        {
          rule_id: 'container-exit-nonzero',
          rule_name: 'Container exited nonzero',
          severity: 'warning',
          entity: 'sonarr',
          silenced: false,
          metric: '',
          summary: 'sonarr: container.die (exit code 137)',
        },
      ],
    });
    expect(describeAnomaly(status.anomalies[0]).detail).toBe('sonarr: container.die (exit code 137)');
  });

  it('a THRESHOLD alert (metric present) keeps the bare entity as its callout detail, never the fuller summary sentence', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      alerts: [
        {
          rule_id: 'host-cpu-high',
          rule_name: 'Host CPU high',
          severity: 'warning',
          entity: 'sonarr',
          silenced: false,
          metric: 'mem.limit_pct',
          summary: 'sonarr is at 91.0% (over 85.0% for 10m0s)',
        },
      ],
    });
    expect(describeAnomaly(status.anomalies[0]).detail).toBe('sonarr');
  });

  it('a silenced firing alert contributes nothing at all', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      alerts: [{ rule_id: 'host-cpu-high', rule_name: 'Host CPU high', severity: 'warning', entity: '', silenced: true }],
    });
    expect(status.ok).toBe(true);
    expect(status.anomalies).toEqual([]);
  });

  it('disk-usage-high on the SAME slot suppresses its own row and upgrades the existing one\'s severity', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk1: { 'fs.used_bytes': 91, 'fs.free_bytes': 9 } }, // frame anomaly: disk-usage, warning by default
      alerts: [{ rule_id: 'disk-usage-high', rule_name: 'Disk usage high', severity: 'alert', entity: 'disk1', silenced: false }],
    });
    expect(status.anomalies).toHaveLength(1); // no separate 'alert' row
    expect(status.anomalies[0]).toEqual({ kind: 'disk-usage', slot: 'disk1', usagePct: 91, severityOverride: 'critical' });
    expect(describeAnomaly(status.anomalies[0]).severity).toBe('critical');
  });

  it('disk-usage-high on a DIFFERENT slot than the frame flagged does NOT dedup -- it is a distinct concern', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk1: { 'fs.used_bytes': 91, 'fs.free_bytes': 9 } },
      alerts: [{ rule_id: 'disk-usage-high', rule_name: 'Disk usage high', severity: 'warning', entity: 'disk9', silenced: false }],
    });
    expect(status.anomalies).toHaveLength(2);
    expect(status.anomalies).toContainEqual({ kind: 'disk-usage', slot: 'disk1', usagePct: 91 });
    expect(status.anomalies).toContainEqual({
      kind: 'alert',
      ruleId: 'disk-usage-high',
      ruleName: 'Disk usage high',
      entity: 'disk9',
      severity: 'warning',
    });
  });

  it('container-unhealthy dedups against the SAME container name only -- no separate row, and the frame anomaly (already "critical", the ceiling) is unchanged', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      unhealthyNames: ['grafana'],
      alerts: [{ rule_id: 'container-unhealthy', rule_name: 'Container unhealthy', severity: 'alert', entity: 'grafana', silenced: false }],
    });
    // No severityOverride here: 'unhealthy' already defaults to
    // 'critical', the ceiling, so there is nothing left to upgrade to --
    // the meaningful assertion is that no SECOND 'alert' row was added.
    expect(status.anomalies).toEqual([{ kind: 'unhealthy', name: 'grafana' }]);
    expect(describeAnomaly(status.anomalies[0]).severity).toBe('critical');
  });

  it('container-unhealthy on a DIFFERENT container than the frame flagged does NOT dedup', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      unhealthyNames: ['grafana'],
      alerts: [{ rule_id: 'container-unhealthy', rule_name: 'Container unhealthy', severity: 'alert', entity: 'sonarr', silenced: false }],
    });
    expect(status.anomalies).toHaveLength(2);
  });

  it('array-stopped dedups regardless of entity (there is only ever one array)', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      arrayStarted: 0,
      alerts: [{ rule_id: 'array-stopped', rule_name: 'Array stopped', severity: 'alert', entity: 'array', silenced: false }],
    });
    expect(status.anomalies).toEqual([{ kind: 'array-stopped', severityOverride: 'critical' }]);
    expect(describeAnomaly(status.anomalies[0]).severity).toBe('critical');
  });

  it('an upgrade never DOWNGRADES: a lower-severity alert on an already-critical anomaly changes nothing', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      unhealthyNames: ['grafana'], // already 'critical' by default
      alerts: [{ rule_id: 'container-unhealthy', rule_name: 'Container unhealthy', severity: 'warning', entity: 'grafana', silenced: false }],
    });
    expect(describeAnomaly(status.anomalies[0]).severity).toBe('critical');
  });

  it('disk-errors dedups per slot, same as disk-usage-high', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk2: { errors: 1 } },
      alerts: [{ rule_id: 'disk-errors', rule_name: 'Disk errors', severity: 'alert', entity: 'disk2', silenced: false }],
    });
    expect(status.anomalies).toEqual([{ kind: 'disk-errors', slot: 'disk2', errors: 1, severityOverride: 'critical' }]);
  });

  it('a missing alerts field behaves exactly like an empty array -- pages that have not wired alerts through yet are unaffected', () => {
    const withUndefined = deriveOverviewStatus(BASE);
    const withEmpty = deriveOverviewStatus({ ...BASE, alerts: [] });
    expect(withUndefined).toEqual(withEmpty);
  });
});

describe('deriveOverviewStatus acknowledgements', () => {
  // A fixed "now" makes every expiry boundary in this block
  // deterministic -- deriveOverviewStatus only reads the real clock
  // when now is omitted.
  const NOW = 1_000_000;

  it('an acked (kind, entity) pair contributes nothing -- row gone, headline count down', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      unhealthyNames: ['sonarr', 'radarr'],
      acks: [{ kind: 'unhealthy', entity: 'sonarr', until: NOW + 3600 }],
      now: NOW,
    });
    expect(status.anomalies).toEqual([{ kind: 'unhealthy', name: 'radarr' }]);
    expect(status.headline).toBe('1 thing needs you');
  });

  it('acking every anomaly restores the all-clear headline', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      unhealthyNames: ['sonarr'],
      acks: [{ kind: 'unhealthy', entity: 'sonarr', until: NOW + 3600 }],
      now: NOW,
    });
    expect(status.ok).toBe(true);
    expect(status.headline).toBe('Everything is running');
  });

  it('an ack filters only its exact (kind, entity) pair -- neither field matches loosely', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      unhealthyNames: ['sonarr'],
      acks: [
        { kind: 'unhealthy', entity: 'radarr', until: NOW + 3600 }, // wrong entity
        { kind: 'disk-errors', entity: 'sonarr', until: NOW + 3600 }, // wrong kind
      ],
      now: NOW,
    });
    expect(status.anomalies).toEqual([{ kind: 'unhealthy', name: 'sonarr' }]);
  });

  it('an acked anomaly disappears and RETURNS after the ack expires -- same list, only now moves (compressed expiry)', () => {
    const input = {
      ...BASE,
      unhealthyNames: ['sonarr'],
      acks: [{ kind: 'unhealthy', entity: 'sonarr', until: NOW + 60 }],
    };
    const during = deriveOverviewStatus({ ...input, now: NOW });
    expect(during.ok).toBe(true);

    // One second past until: the ack is spent, the anomaly is back --
    // no refetch or list change needed, expiry is checked per run.
    const after = deriveOverviewStatus({ ...input, now: NOW + 61 });
    expect(after.anomalies).toEqual([{ kind: 'unhealthy', name: 'sonarr' }]);
    expect(after.headline).toBe('1 thing needs you');
  });

  it('an ack expiring exactly AT until no longer filters (until > now is the live condition)', () => {
    const input = {
      ...BASE,
      unhealthyNames: ['sonarr'],
      acks: [{ kind: 'unhealthy', entity: 'sonarr', until: NOW }],
    };
    expect(deriveOverviewStatus({ ...input, now: NOW }).ok).toBe(false);
  });

  it('acking a disk callout un-flags its bay-schematic slot too', () => {
    const disks = { disk1: { 'fs.used_bytes': 95, 'fs.free_bytes': 5, errors: 3 } };
    const unacked = deriveOverviewStatus({ ...BASE, disks, now: NOW });
    expect(unacked.flaggedDiskSlots).toEqual(['disk1', 'disk1']);

    const acked = deriveOverviewStatus({
      ...BASE,
      disks,
      acks: [{ kind: 'disk-usage', entity: 'disk1', until: NOW + 3600 }],
      now: NOW,
    });
    // The usage callout is quiet; the errors callout (its own concern,
    // not covered by the disk-usage ack) still flags the slot.
    expect(acked.anomalies).toEqual([{ kind: 'disk-errors', slot: 'disk1', errors: 3 }]);
    expect(acked.flaggedDiskSlots).toEqual(['disk1']);
  });

  it('array-stopped acks under the literal "array" entity (there is only ever one)', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      arrayStarted: 0,
      acks: [{ kind: 'array-stopped', entity: 'array', until: NOW + 3600 }],
      now: NOW,
    });
    expect(status.ok).toBe(true);
  });

  it('an ack can never quiet an alert-backed callout -- that gesture is a SILENCE, not an ack', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      alerts: [{ rule_id: 'host-cpu-high', rule_name: 'Host CPU high', severity: 'warning', entity: 'host', silenced: false }],
      // Even an ack row shaped exactly like the alert's identity does
      // nothing: 'alert' has no ack identity at all (ackKeyFor is null),
      // and the server refuses to create kind:'alert' rows to begin with.
      acks: [{ kind: 'alert', entity: 'host', until: NOW + 3600 }],
      now: NOW,
    });
    expect(status.anomalies).toHaveLength(1);
    expect(status.anomalies[0].kind).toBe('alert');
  });

  it('an acked frame row does not swallow the same concern\'s unsilenced firing alert -- the alert surfaces on its own line', () => {
    const status = deriveOverviewStatus({
      ...BASE,
      disks: { disk1: { 'fs.used_bytes': 91, 'fs.free_bytes': 9 } },
      alerts: [{ rule_id: 'disk-usage-high', rule_name: 'Disk usage high', severity: 'alert', entity: 'disk1', silenced: false }],
      acks: [{ kind: 'disk-usage', entity: 'disk1', until: NOW + 3600 }],
      now: NOW,
    });
    // Without the ack this would be ONE row (the frame row, upgraded by
    // dedup). With the frame row acked, the still-firing alert gets its
    // own row instead of vanishing under a mere ack.
    expect(status.anomalies).toEqual([
      { kind: 'alert', ruleId: 'disk-usage-high', ruleName: 'Disk usage high', entity: 'disk1', severity: 'critical' },
    ]);
  });

  it('a missing acks field behaves exactly like an empty array', () => {
    const withUndefined = deriveOverviewStatus({ ...BASE, unhealthyNames: ['sonarr'], now: NOW });
    const withEmpty = deriveOverviewStatus({ ...BASE, unhealthyNames: ['sonarr'], acks: [], now: NOW });
    expect(withUndefined).toEqual(withEmpty);
  });
});

describe('ackKeyFor', () => {
  it('maps every frame-derived kind to its concrete (kind, entity) identity', () => {
    expect(ackKeyFor({ kind: 'unhealthy', name: 'sonarr' })).toEqual({ kind: 'unhealthy', entity: 'sonarr' });
    expect(ackKeyFor({ kind: 'disk-usage', slot: 'disk3', usagePct: 95 })).toEqual({ kind: 'disk-usage', entity: 'disk3' });
    expect(ackKeyFor({ kind: 'disk-errors', slot: 'disk2', errors: 1 })).toEqual({ kind: 'disk-errors', entity: 'disk2' });
    expect(ackKeyFor({ kind: 'array-stopped' })).toEqual({ kind: 'array-stopped', entity: 'array' });
    expect(ackKeyFor({ kind: 'source-critical', source: 'docker', detail: 'down' })).toEqual({
      kind: 'source-critical',
      entity: 'docker',
    });
  });

  it('is null for an alert-backed callout -- its ack IS an alert silence, one mechanism per system', () => {
    expect(
      ackKeyFor({ kind: 'alert', ruleId: 'host-cpu-high', ruleName: 'Host CPU high', entity: '', severity: 'warning' }),
    ).toBeNull();
  });
});

describe('describeAnomaly', () => {
  it('unhealthy names the container and links to it', () => {
    const text = describeAnomaly({ kind: 'unhealthy', name: 'sonarr' });
    expect(text).toEqual({
      severity: 'critical',
      title: 'sonarr is unhealthy',
      detail: 'Failing its health check.',
      linkContainer: 'sonarr',
      href: '#/containers/sonarr',
    });
  });

  it('every kind carries the route that explains it (Scott: every attention row is clickable)', () => {
    expect(describeAnomaly({ kind: 'unhealthy', name: 'sonarr' }).href).toBe('#/containers/sonarr');
    expect(describeAnomaly({ kind: 'disk-usage', slot: 'disk6', usagePct: 95 }).href).toBe('#/storage');
    expect(describeAnomaly({ kind: 'disk-errors', slot: 'disk2', errors: 1 }).href).toBe('#/storage');
    expect(describeAnomaly({ kind: 'array-stopped' }).href).toBe('#/storage');
    expect(describeAnomaly({ kind: 'source-critical', source: 'docker', detail: 'daemon unreachable' }).href).toBe(
      '#/containers',
    );
    expect(
      describeAnomaly({ kind: 'alert', ruleId: 'host-cpu-high', ruleName: 'Host CPU high', entity: '', severity: 'warning' })
        .href,
    ).toBe('#/alerts');
  });

  it('disk-usage formats the percentage via fmtPct (one decimal, clamped)', () => {
    const text = describeAnomaly({ kind: 'disk-usage', slot: 'disk6', usagePct: 95 });
    expect(text.title).toBe('disk6 is nearest to full');
    expect(text.detail).toBe('95.0% capacity');
    expect(text.severity).toBe('warning');
  });

  it('disk-errors pluralizes "error(s)" on the boundary between 1 and many', () => {
    expect(describeAnomaly({ kind: 'disk-errors', slot: 'disk2', errors: 1 }).detail).toBe('1 error');
    expect(describeAnomaly({ kind: 'disk-errors', slot: 'disk2', errors: 3 }).detail).toBe('3 errors');
    expect(describeAnomaly({ kind: 'disk-errors', slot: 'disk2', errors: 1 }).severity).toBe('serious');
  });

  it('array-stopped and source-critical render fixed/templated text', () => {
    expect(describeAnomaly({ kind: 'array-stopped' }).severity).toBe('serious');
    const src = describeAnomaly({ kind: 'source-critical', source: 'docker', detail: 'daemon unreachable' });
    expect(src.title).toBe('docker needs attention');
    expect(src.detail).toBe('daemon unreachable');
    expect(src.severity).toBe('critical');
  });
});

describe('calloutTextBySlot', () => {
  it('maps a single disk-usage anomaly to its own detail text', () => {
    const bySlot = calloutTextBySlot([{ kind: 'disk-usage', slot: 'disk1', usagePct: 95 }]);
    expect(bySlot.get('disk1')).toBe('95.0% capacity');
  });

  it('maps a single disk-errors anomaly to its own detail text', () => {
    const bySlot = calloutTextBySlot([{ kind: 'disk-errors', slot: 'disk2', errors: 3 }]);
    expect(bySlot.get('disk2')).toBe('3 errors');
  });

  it('aggregates both details when a slot carries usage AND errors anomalies, instead of the later one silently overwriting the earlier', () => {
    const bySlot = calloutTextBySlot([
      { kind: 'disk-usage', slot: 'disk1', usagePct: 95 },
      { kind: 'disk-errors', slot: 'disk1', errors: 3 },
    ]);
    expect(bySlot.get('disk1')).toBe('95.0% capacity · 3 errors');
  });

  it('keeps different slots independent', () => {
    const bySlot = calloutTextBySlot([
      { kind: 'disk-usage', slot: 'disk1', usagePct: 95 },
      { kind: 'disk-errors', slot: 'disk2', errors: 1 },
    ]);
    expect(bySlot.get('disk1')).toBe('95.0% capacity');
    expect(bySlot.get('disk2')).toBe('1 error');
  });

  it('ignores non-disk anomaly kinds', () => {
    const bySlot = calloutTextBySlot([
      { kind: 'unhealthy', name: 'sonarr' },
      { kind: 'array-stopped' },
      { kind: 'source-critical', source: 'docker', detail: 'daemon unreachable' },
    ]);
    expect(bySlot.size).toBe(0);
  });
});

describe('worstSeverity', () => {
  it('is "good" for an empty list', () => {
    expect(worstSeverity([])).toBe('good');
  });

  it('picks the single most severe anomaly present', () => {
    const anomalies: OverviewAnomaly[] = [
      { kind: 'disk-usage', slot: 'disk1', usagePct: 95 }, // warning
      { kind: 'disk-errors', slot: 'disk1', errors: 1 }, // serious
      { kind: 'unhealthy', name: 'sonarr' }, // critical
    ];
    expect(worstSeverity(anomalies)).toBe('critical');
  });

  it('does not let a later, less severe anomaly downgrade the result', () => {
    const anomalies: OverviewAnomaly[] = [
      { kind: 'unhealthy', name: 'sonarr' },
      { kind: 'disk-usage', slot: 'disk1', usagePct: 95 },
    ];
    expect(worstSeverity(anomalies)).toBe('critical');
  });
});

describe('fleetSentence', () => {
  it('reads "all running" when nothing is stopped', () => {
    expect(fleetSentence(20, 20, 0)).toBe('20 containers, all running.');
  });

  it('singularizes "container" for a lone, all-running fleet', () => {
    expect(fleetSentence(1, 1, 0)).toBe('1 container, all running.');
  });

  it('switches to "N running · M stopped" once any container is stopped', () => {
    expect(fleetSentence(20, 18, 2)).toBe('18 running · 2 stopped.');
  });

  it('the stopped phrasing does not depend on total at all', () => {
    expect(fleetSentence(5, 0, 5)).toBe('0 running · 5 stopped.');
  });
});

import { describe, expect, it } from 'vitest';
import {
  calloutTextBySlot,
  deriveOverviewStatus,
  describeAnomaly,
  fleetSentence,
  worstSeverity,
  type OverviewAnomaly,
} from './overviewStatus';

const BASE = {
  unhealthyNames: [] as string[],
  stoppedCount: 0,
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

  it('stopped containers stay ONE aggregated anomaly regardless of count', () => {
    const status = deriveOverviewStatus({ ...BASE, stoppedCount: 5 });
    expect(status.headline).toBe('1 thing needs you');
    expect(status.anomalies).toEqual([{ kind: 'stopped', count: 5 }]);
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
      stoppedCount: 2,
      arrayStarted: 0,
      disks: { disk1: { 'fs.used_bytes': 95, 'fs.free_bytes': 5 } },
      sources: { docker: 'daemon unreachable' },
    });
    // unhealthy(1) + stopped(1) + disk-usage(1) + array-stopped(1) + source-critical(1) = 5
    expect(status.anomalies).toHaveLength(5);
    expect(status.headline).toBe('5 things need you');
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
    });
  });

  it('stopped pluralizes on the boundary between 1 and many', () => {
    expect(describeAnomaly({ kind: 'stopped', count: 1 }).title).toBe('1 container is stopped');
    expect(describeAnomaly({ kind: 'stopped', count: 2 }).title).toBe('2 containers are stopped');
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
      { kind: 'stopped', count: 2 },
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
      { kind: 'stopped', count: 1 }, // warning
      { kind: 'disk-errors', slot: 'disk1', errors: 1 }, // serious
      { kind: 'unhealthy', name: 'sonarr' }, // critical
    ];
    expect(worstSeverity(anomalies)).toBe('critical');
  });

  it('does not let a later, less severe anomaly downgrade the result', () => {
    const anomalies: OverviewAnomaly[] = [{ kind: 'unhealthy', name: 'sonarr' }, { kind: 'stopped', count: 1 }];
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

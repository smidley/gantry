import { describe, expect, it } from 'vitest';
import { anomalyHref } from './anomalyHref';

describe('anomalyHref', () => {
  it('links an unhealthy container to its own detail page, encoding the name', () => {
    expect(anomalyHref({ kind: 'unhealthy', name: 'sonarr' })).toBe('#/containers/sonarr');
    expect(anomalyHref({ kind: 'unhealthy', name: 'my container' })).toBe('#/containers/my%20container');
  });

  it('links every disk/array concern to the plain Storage page', () => {
    expect(anomalyHref({ kind: 'disk-usage', slot: 'disk3', usagePct: 95 })).toBe('#/storage');
    expect(anomalyHref({ kind: 'disk-errors', slot: 'disk2', errors: 3 })).toBe('#/storage');
    expect(anomalyHref({ kind: 'array-stopped' })).toBe('#/storage');
  });

  it('links a critical docker source to the fleet view it degrades, and any other source nowhere', () => {
    expect(anomalyHref({ kind: 'source-critical', source: 'docker', detail: 'daemon unreachable' })).toBe(
      '#/containers',
    );
    // No other source is promoted to an anomaly today (CRITICAL_SOURCES
    // is ['docker']); if one ever is, it renders as a plain row rather
    // than inventing a destination -- eventHref's own null convention.
    expect(anomalyHref({ kind: 'source-critical', source: 'nvidia', detail: 'no nvidia-smi' })).toBeNull();
  });

  it('links an alert-backed callout to the Alerts view, even when the entity names a container/disk that would otherwise misroute', () => {
    expect(
      anomalyHref({ kind: 'alert', ruleId: 'disk-temp-high', ruleName: 'Disk temp high', entity: 'disk4', severity: 'warning' }),
    ).toBe('#/alerts');
    expect(
      anomalyHref({ kind: 'alert', ruleId: 'host-cpu-high', ruleName: 'Host CPU high', entity: '', severity: 'critical' }),
    ).toBe('#/alerts');
  });

  it('links an insight-backed callout to the Insights view, where the finding and its evidence live', () => {
    expect(
      anomalyHref({ kind: 'insight', statement: 'qbittorrent is starving jellyfin on disk3', severity: 'warning', confidence: 'likely' }),
    ).toBe('#/insights');
  });
});

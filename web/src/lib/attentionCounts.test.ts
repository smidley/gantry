import { describe, expect, it } from 'vitest';
import { alertsBucketAnomalies, attentionBucketFor, attentionChips } from './attentionCounts';
import { deriveOverviewStatus } from './overviewStatus';
import type { OverviewAnomaly } from './overviewStatus';

// One case per kind in the derivation's closed vocabulary -- if a kind
// is ever added there, attentionBucketFor stops type-checking and this
// table is the place it has to be answered for.
const ONE_OF_EACH: { anomaly: OverviewAnomaly; bucket: 'alerts' | 'contentions' }[] = [
  { anomaly: { kind: 'unhealthy', name: 'grafana' }, bucket: 'alerts' },
  { anomaly: { kind: 'disk-usage', slot: 'disk3', usagePct: 94 }, bucket: 'alerts' },
  { anomaly: { kind: 'disk-errors', slot: 'disk1', errors: 4 }, bucket: 'alerts' },
  { anomaly: { kind: 'array-stopped' }, bucket: 'alerts' },
  { anomaly: { kind: 'source-critical', source: 'docker', detail: 'socket unreachable' }, bucket: 'alerts' },
  {
    anomaly: { kind: 'alert', ruleId: 'host-cpu-high', ruleName: 'Host CPU high', entity: 'host', severity: 'warning' },
    bucket: 'alerts',
  },
  {
    anomaly: { kind: 'insight', statement: 'qbittorrent is starving jellyfin on disk3', severity: 'warning', confidence: 'likely' },
    bucket: 'contentions',
  },
];

describe('attentionBucketFor', () => {
  it('routes every kind in the vocabulary to exactly one chip', () => {
    for (const { anomaly, bucket } of ONE_OF_EACH) {
      expect(attentionBucketFor(anomaly), anomaly.kind).toBe(bucket);
    }
  });
});

describe('attentionChips', () => {
  it('renders nothing at all when nothing needs you', () => {
    expect(attentionChips([])).toEqual([]);
  });

  it('suppresses a zero chip rather than showing an empty one', () => {
    const alertsOnly = attentionChips([{ kind: 'unhealthy', name: 'grafana' }]);
    expect(alertsOnly).toHaveLength(1);
    expect(alertsOnly[0].bucket).toBe('alerts');

    const insightsOnly = attentionChips([
      { kind: 'insight', statement: 'a starves b', severity: 'warning', confidence: 'likely' },
    ]);
    expect(insightsOnly).toHaveLength(1);
    expect(insightsOnly[0].bucket).toBe('contentions');
  });

  it('counts each bucket and points it at the owner\'s chosen page', () => {
    const chips = attentionChips(ONE_OF_EACH.map((c) => c.anomaly));
    expect(chips.map((c) => [c.bucket, c.count, c.href])).toEqual([
      ['alerts', 6, '#/events'],
      ['contentions', 1, '#/insights'],
    ]);
  });

  it('renders alerts before contentions', () => {
    const chips = attentionChips([
      { kind: 'insight', statement: 'a starves b', severity: 'warning', confidence: 'likely' },
      { kind: 'unhealthy', name: 'grafana' },
    ]);
    expect(chips.map((c) => c.bucket)).toEqual(['alerts', 'contentions']);
  });

  it('says the whole sentence in the accessible name, singular and plural', () => {
    const one = attentionChips([{ kind: 'unhealthy', name: 'grafana' }])[0];
    expect(one.noun).toBe('alert');
    expect(one.ariaLabel).toBe('1 alert needs you, view events');

    const many = attentionChips([
      { kind: 'unhealthy', name: 'grafana' },
      { kind: 'array-stopped' },
    ])[0];
    expect(many.noun).toBe('alerts');
    expect(many.ariaLabel).toBe('2 alerts need you, view events');

    const contention = attentionChips([
      { kind: 'insight', statement: 'a starves b', severity: 'warning', confidence: 'likely' },
    ])[0];
    expect(contention.ariaLabel).toBe('1 contention needs you, view insights');
  });

  // The standing invariant the headline rests on: "N things need you"
  // is anomalies.length, so the chips can never sum to anything else.
  it('sums to the headline count, acknowledged rows excluded', () => {
    const input = {
      unhealthyNames: ['grafana', 'sonarr'],
      arrayStarted: 0,
      disks: { disk1: { errors: 2 } },
      sources: {},
      insights: [
        { victim_kind: 'container', victim: 'jellyfin', statement: 'a starves b', severity: 'warning', confidence: 'likely', fired_at: 10 },
      ],
      now: 1_000,
    };

    const status = deriveOverviewStatus(input);
    const chips = attentionChips(status.anomalies);
    expect(chips.reduce((n, c) => n + c.count, 0)).toBe(status.anomalies.length);
    expect(status.headline).toBe(`${status.anomalies.length} things need you`);
    expect(chips.map((c) => [c.bucket, c.count])).toEqual([
      ['alerts', 4], // grafana, sonarr, array-stopped, disk1 errors
      ['contentions', 1],
    ]);

    // Acknowledge grafana: the derivation drops the row, so the chip
    // count drops with it -- exactly the way the callout list used to.
    const acked = deriveOverviewStatus({
      ...input,
      acks: [{ kind: 'unhealthy', entity: 'grafana', until: 2_000 }],
    });
    const ackedChips = attentionChips(acked.anomalies);
    expect(ackedChips.reduce((n, c) => n + c.count, 0)).toBe(acked.anomalies.length);
    expect(ackedChips.find((c) => c.bucket === 'alerts')!.count).toBe(3);

    // An EXPIRED ack changes nothing -- the row is back and so is the count.
    const expired = deriveOverviewStatus({
      ...input,
      acks: [{ kind: 'unhealthy', entity: 'grafana', until: 500 }],
    });
    expect(attentionChips(expired.anomalies).find((c) => c.bucket === 'alerts')!.count).toBe(4);
  });
});

// alertsBucketAnomalies backs Events' own "Needs you" strip (the counts
// pass's own open question, resolved): the same list the alerts chip
// counts, handed back as the actual rows for CalloutRow to render one
// per anomaly, exactly Overview's own pre-counts rendering.
describe('alertsBucketAnomalies', () => {
  it('keeps only the alerts-bucket kinds, in their original order, dropping every contention', () => {
    const selected = alertsBucketAnomalies(ONE_OF_EACH.map((c) => c.anomaly));
    expect(selected.map((a) => a.kind)).toEqual([
      'unhealthy',
      'disk-usage',
      'disk-errors',
      'array-stopped',
      'source-critical',
      'alert',
    ]);
  });

  it('is empty when every anomaly is a contention', () => {
    const insightOnly: OverviewAnomaly[] = [
      { kind: 'insight', statement: 'a starves b', severity: 'warning', confidence: 'likely' },
    ];
    expect(alertsBucketAnomalies(insightOnly)).toEqual([]);
  });

  it('is empty given an empty list', () => {
    expect(alertsBucketAnomalies([])).toEqual([]);
  });

  // The strip must never show a row for a quieted concern -- same
  // standing invariant attentionChips' own count rests on. This drives
  // deriveOverviewStatus's ack filter directly rather than re-asserting
  // it: alertsBucketAnomalies has no ack logic of its own to test, only
  // the guarantee that it never RE-ADDS what the derivation already
  // dropped.
  it('excludes an acked alerts-bucket anomaly exactly the way the chip count already does', () => {
    const input = {
      unhealthyNames: ['grafana', 'sonarr'],
      arrayStarted: undefined,
      disks: {},
      sources: {},
      now: 1_000,
    };

    const unacked = deriveOverviewStatus(input);
    expect(alertsBucketAnomalies(unacked.anomalies).map((a) => (a as { name: string }).name)).toEqual([
      'grafana',
      'sonarr',
    ]);

    const acked = deriveOverviewStatus({
      ...input,
      acks: [{ kind: 'unhealthy', entity: 'grafana', until: 2_000 }],
    });
    expect(alertsBucketAnomalies(acked.anomalies).map((a) => (a as { name: string }).name)).toEqual(['sonarr']);
  });
});

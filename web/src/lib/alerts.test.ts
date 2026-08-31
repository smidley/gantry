import { describe, expect, it } from 'vitest';
import {
  describeResolveReason,
  RESOLVE_REASON_TEXT,
  firingDuration,
  silenceLabel,
  sortActiveAlerts,
  formatMetricValue,
  describeRule,
  alertEntityHref,
  channelLabel,
  annotateAlerts,
  alertGuidance,
} from './alerts';
import type { AnnotatableAlert, AnnotationInsight, DescribableRule } from './alerts';

describe('alertEntityHref', () => {
  it('links a container to its own detail page', () => {
    expect(alertEntityHref('container', 'sonarr')).toBe('#/containers/sonarr');
  });

  it('links disk and unraid kinds to the Storage page', () => {
    expect(alertEntityHref('disk', 'disk4')).toBe('#/storage');
    expect(alertEntityHref('unraid', 'array')).toBe('#/storage');
  });

  it('host and gpu have no per-entity page to link to', () => {
    expect(alertEntityHref('host', '')).toBeNull();
    expect(alertEntityHref('gpu', 'gpu0')).toBeNull();
  });

  it('a container with no entity name has nowhere to link', () => {
    expect(alertEntityHref('container', '')).toBeNull();
  });
});

describe('alertGuidance', () => {
  it('sends host CPU alerts to CPU consumers with a cautious cause', () => {
    expect(alertGuidance({ rule_id: 'host-cpu-high', kind: 'host', entity: '' })).toEqual({
      cause: 'A sustained workload or runaway process is consuming host CPU.',
      nextStep: 'Inspect CPU consumers',
      href: '#/top/cpu',
    });
  });

  it('links container guidance to the affected container', () => {
    expect(alertGuidance({ rule_id: 'container-unhealthy', kind: 'container', entity: 'sonarr' }).href).toBe(
      '#/containers/sonarr',
    );
  });

  it('falls back safely for future disk rules', () => {
    expect(alertGuidance({ rule_id: 'future-rule', kind: 'disk', entity: 'disk1' }).href).toBe('#/storage');
  });
});

describe('describeResolveReason', () => {
  it('renders the six known machine reasons in plain words', () => {
    expect(describeResolveReason('cleared')).toBe('recovered');
    expect(describeResolveReason('no-data')).toBe('stopped reporting');
    expect(describeResolveReason('timeout')).toBe('auto-closed');
    expect(describeResolveReason('rule-disabled')).toBe('rule turned off');
    expect(describeResolveReason('restarted')).toBe('routine restart');
    expect(describeResolveReason('out-of-scope')).toBe('no longer in scope');
  });

  it('passes an unknown reason through verbatim rather than dropping it', () => {
    expect(describeResolveReason('some-future-reason')).toBe('some-future-reason');
  });

  // ENGINE_RESOLVE_REASONS mirrors the exact set of ResolveReason string
  // literals internal/alert/engine.go's resolve* calls ever emit --
  // resolveDisabled ("rule-disabled"), resolveRestarted ("restarted"),
  // resolveOutOfScope ("out-of-scope"), handleAbsentThreshold/
  // evalThresholdEntity ("no-data"), evalThresholdEntity/
  // processEventForRule ("cleared"), and tickEvents' own timeout sweep
  // ("timeout"). Keep this list and RESOLVE_REASON_TEXT's own keys in
  // lockstep with that file, the same DEFAULT_RULES-mirrors-Go
  // discipline describeRule's own tests below already follow.
  const ENGINE_RESOLVE_REASONS = ['cleared', 'no-data', 'timeout', 'rule-disabled', 'restarted', 'out-of-scope'];

  it('covers exactly the reasons the engine emits -- nothing more, nothing less', () => {
    expect(Object.keys(RESOLVE_REASON_TEXT).sort()).toEqual([...ENGINE_RESOLVE_REASONS].sort());
  });
});

describe('firingDuration', () => {
  it('renders the elapsed span via fmtDuration', () => {
    expect(firingDuration(1000, 1000 + 90)).toBe('1m 30s');
  });

  it('clamps a negative span (clock skew) to zero rather than going negative', () => {
    expect(firingDuration(2000, 1000)).toBe(firingDuration(1000, 1000));
  });
});

describe('silenceLabel', () => {
  const now = 1_000_000;

  it('an exact rule+entity pair needs no scope qualifier', () => {
    expect(silenceLabel({ rule_id: 'disk-temp-high', entity: 'disk4', until: now + 3600 }, now)).toBe(
      'Silenced · 1h 0m left',
    );
  });

  it('a rule-wide silence (entity blank) names its own breadth honestly', () => {
    expect(silenceLabel({ rule_id: 'disk-temp-high', entity: '', until: now + 3600 }, now)).toBe(
      'Silenced (every entity on this rule) · 1h 0m left',
    );
  });

  it('an entity-wide silence (rule blank) names its own breadth honestly', () => {
    expect(silenceLabel({ rule_id: '', entity: 'disk4', until: now + 3600 }, now)).toBe(
      'Silenced (every rule on this entity) · 1h 0m left',
    );
  });

  it('a true global mute reads "everything", whether via scope:"all" or a blank pair', () => {
    expect(silenceLabel({ rule_id: '', entity: '', until: now + 3600, scope: 'all' }, now)).toBe(
      'Silenced (everything) · 1h 0m left',
    );
  });

  it('an already-expired silence reads "expiring" rather than a negative duration', () => {
    expect(silenceLabel({ rule_id: 'x', entity: 'y', until: now - 10 }, now)).toBe('Silenced · expiring');
  });
});

describe('sortActiveAlerts', () => {
  it('orders severity descending (alert > warning > info), then fired_at ascending within a severity', () => {
    const input = [
      { id: 'a', severity: 'warning', fired_at: 200 },
      { id: 'b', severity: 'alert', fired_at: 300 },
      { id: 'c', severity: 'alert', fired_at: 100 },
      { id: 'd', severity: 'info', fired_at: 50 },
    ];
    expect(sortActiveAlerts(input).map((a) => a.id)).toEqual(['c', 'b', 'a', 'd']);
  });

  it('does not mutate its input', () => {
    const input = [
      { severity: 'warning', fired_at: 2 },
      { severity: 'alert', fired_at: 1 },
    ];
    const copy = [...input];
    sortActiveAlerts(input);
    expect(input).toEqual(copy);
  });
});

describe('formatMetricValue', () => {
  it('formats temp.c with the degree unit', () => {
    expect(formatMetricValue('temp.c', 57.04)).toBe('57.0 °C');
  });

  it('formats every percentage metric with a bare %', () => {
    for (const metric of ['cpu.total', 'mem.used_pct', 'fs.used_pct', 'mem.limit_pct']) {
      expect(formatMetricValue(metric, 90)).toBe('90.0%');
    }
  });

  it('array.started renders as a plain state word, not a 1/0 number', () => {
    expect(formatMetricValue('array.started', 1)).toBe('started');
    expect(formatMetricValue('array.started', 0)).toBe('stopped');
  });

  it('an unrecognized metric falls back to a bare one-decimal number', () => {
    expect(formatMetricValue('', 3)).toBe('3.0');
  });
});

describe('channelLabel', () => {
  it('renders "notify" as a friendly label', () => {
    expect(channelLabel('notify')).toBe('Unraid notifications');
  });

  it('strips the "webhook:" prefix and renders the target id plainly', () => {
    expect(channelLabel('webhook:home')).toBe('Webhook: home');
  });

  it('passes an unrecognized id through verbatim', () => {
    expect(channelLabel('something-else')).toBe('something-else');
  });
});

describe('describeRule', () => {
  // DEFAULT_RULES mirrors internal/store/alert_defaults.go's twelve
  // builtins' shape (only the fields DescribableRule needs) -- Task
  // 11's own "vitest-tested across all 12 defaults" requirement.
  const DEFAULT_RULES: Record<string, DescribableRule> = {
    'host-cpu-high': {
      type: 'threshold', kind: 'host', entity_glob: '*', entity_class: '',
      metric: 'cpu.total', op: '>', threshold: 85, for_seconds: 600, event_kinds: '', severity: 'warning',
    },
    'host-mem-high': {
      type: 'threshold', kind: 'host', entity_glob: '*', entity_class: '',
      metric: 'mem.used_pct', op: '>', threshold: 85, for_seconds: 600, event_kinds: '', severity: 'warning',
    },
    'disk-usage-high': {
      type: 'threshold', kind: 'disk', entity_glob: '*', entity_class: '',
      metric: 'fs.used_pct', op: '>', threshold: 90, for_seconds: 900, event_kinds: '', severity: 'warning',
    },
    'disk-temp-high': {
      type: 'threshold', kind: 'disk', entity_glob: '*', entity_class: '!nvme',
      metric: 'temp.c', op: '>', threshold: 55, for_seconds: 600, event_kinds: '', severity: 'warning',
    },
    'disk-temp-nvme-high': {
      type: 'threshold', kind: 'disk', entity_glob: '*', entity_class: 'nvme',
      metric: 'temp.c', op: '>', threshold: 70, for_seconds: 600, event_kinds: '', severity: 'warning',
    },
    'container-mem-limit-high': {
      type: 'threshold', kind: 'container', entity_glob: '*', entity_class: '',
      metric: 'mem.limit_pct', op: '>', threshold: 85, for_seconds: 600, event_kinds: '', severity: 'warning',
    },
    'array-stopped': {
      type: 'threshold', kind: 'unraid', entity_glob: 'array', entity_class: '',
      metric: 'array.started', op: '<', threshold: 1, for_seconds: 300, event_kinds: '', severity: 'alert',
    },
    'container-unhealthy': {
      type: 'event', kind: 'container', entity_glob: '*', entity_class: '',
      metric: '', op: '', threshold: 0, for_seconds: 0, event_kinds: 'container.health', severity: 'alert',
    },
    'container-oom': {
      type: 'event', kind: 'container', entity_glob: '*', entity_class: '',
      metric: '', op: '', threshold: 0, for_seconds: 0, event_kinds: 'container.oom', severity: 'alert',
    },
    'container-exit-nonzero': {
      type: 'event', kind: 'container', entity_glob: '*', entity_class: '',
      metric: '', op: '', threshold: 0, for_seconds: 0, event_kinds: 'container.die', severity: 'warning',
    },
    'disk-errors': {
      type: 'event', kind: 'disk', entity_glob: '*', entity_class: '',
      metric: '', op: '', threshold: 0, for_seconds: 0, event_kinds: 'disk.errors', severity: 'alert',
    },
    'parity-errors': {
      type: 'event', kind: 'unraid', entity_glob: 'array', entity_class: '',
      metric: '', op: '', threshold: 0, for_seconds: 0, event_kinds: 'parity.finish', severity: 'alert',
    },
  };

  it('produces a non-empty, single-line sentence for all twelve defaults', () => {
    expect(Object.keys(DEFAULT_RULES)).toHaveLength(12);
    for (const [id, rule] of Object.entries(DEFAULT_RULES)) {
      const text = describeRule(id, rule);
      expect(text.length, `${id} description must not be empty`).toBeGreaterThan(0);
      expect(text.includes('\n'), `${id} description must be one line`).toBe(false);
    }
  });

  it('disk-temp-high names its own class scoping, unit, and window', () => {
    expect(describeRule('disk-temp-high', DEFAULT_RULES['disk-temp-high'])).toBe(
      'Warn when any non-NVMe disk goes over 55 °C for 10 minutes',
    );
  });

  it('disk-temp-nvme-high names the nvme-only scope', () => {
    expect(describeRule('disk-temp-nvme-high', DEFAULT_RULES['disk-temp-nvme-high'])).toBe(
      'Warn when any NVMe disk goes over 70 °C for 10 minutes',
    );
  });

  it('host-cpu-high scopes to "the host"', () => {
    expect(describeRule('host-cpu-high', DEFAULT_RULES['host-cpu-high'])).toBe(
      'Warn when the host goes over 85% for 10 minutes',
    );
  });

  it('container-mem-limit-high names what the percentage is of', () => {
    expect(describeRule('container-mem-limit-high', DEFAULT_RULES['container-mem-limit-high'])).toBe(
      'Warn when any container goes over 85% of its memory limit for 10 minutes',
    );
  });

  it('array-stopped is special-cased away from a raw "goes under 1" sentence', () => {
    expect(describeRule('array-stopped', DEFAULT_RULES['array-stopped'])).toBe(
      'Alert when the array stops running for 5 minutes',
    );
  });

  it('every builtin event rule gets its own hand-written sentence', () => {
    expect(describeRule('container-unhealthy', DEFAULT_RULES['container-unhealthy'])).toBe(
      'Alert when any container becomes unhealthy',
    );
    expect(describeRule('container-oom', DEFAULT_RULES['container-oom'])).toBe(
      'Alert when any container is killed for using too much memory',
    );
    expect(describeRule('container-exit-nonzero', DEFAULT_RULES['container-exit-nonzero'])).toBe(
      'Warn when any container exits with an error',
    );
    expect(describeRule('disk-errors', DEFAULT_RULES['disk-errors'])).toBe('Alert when any disk reports new errors');
    expect(describeRule('parity-errors', DEFAULT_RULES['parity-errors'])).toBe(
      'Alert when a parity check finishes with a warning or worse',
    );
  });

  it('an unrecognized event rule id falls back to a generic sentence rather than throwing', () => {
    const text = describeRule('some-future-rule', {
      type: 'event', kind: 'container', entity_glob: '*', entity_class: '',
      metric: '', op: '', threshold: 0, for_seconds: 0, event_kinds: 'container.foo', severity: 'warning',
    });
    expect(text).toBe('Warn on container.foo');
  });

  it('a named (non-wildcard) entity_glob is quoted plainly', () => {
    const text = describeRule('user-rule', {
      type: 'threshold', kind: 'container', entity_glob: 'sonarr', entity_class: '',
      metric: 'cpu.total', op: '>', threshold: 50, for_seconds: 120, event_kinds: '', severity: 'warning',
    });
    expect(text).toBe('Warn when container "sonarr" goes over 50% for 2 minutes');
  });
});

describe('annotateAlerts', () => {
  function alert(partial: Partial<AnnotatableAlert>): AnnotatableAlert {
    return { kind: 'container', entity: 'jellyfin', ...partial };
  }
  function insight(partial: Partial<AnnotationInsight>): AnnotationInsight {
    return {
      victim_kind: 'container', victim: 'jellyfin', statement: 'qbittorrent is likely slowing jellyfin on disk3',
      severity: 'warning', confidence: 'likely', fired_at: 100, ...partial,
    };
  }

  it('annotates a matching kind+entity with a "Likely cause" line for a likely finding', () => {
    const [a] = annotateAlerts([alert({})], [insight({})]);
    expect(a.insightAnnotation).toEqual({
      text: 'Likely cause: qbittorrent is likely slowing jellyfin on disk3',
      href: '#/insights',
    });
  });

  it('uses "Cause" (not "Likely cause") for a confirmed finding', () => {
    const [a] = annotateAlerts([alert({})], [insight({ confidence: 'confirmed', statement: 'qbittorrent is starving jellyfin on disk3' })]);
    expect(a.insightAnnotation?.text).toBe('Cause: qbittorrent is starving jellyfin on disk3');
  });

  it('never annotates when kind matches but entity does not, or vice versa', () => {
    const [byEntity] = annotateAlerts([alert({ entity: 'sonarr' })], [insight({})]);
    expect(byEntity.insightAnnotation).toBeUndefined();
    const [byKind] = annotateAlerts([alert({ kind: 'disk' })], [insight({})]);
    expect(byKind.insightAnnotation).toBeUndefined();
  });

  it('leaves an alert with no entity (a host-wide rule) unannotated rather than matching by accident', () => {
    const [a] = annotateAlerts([alert({ kind: 'host', entity: '' })], [insight({ victim_kind: 'host', victim: '' })]);
    expect(a.insightAnnotation).toBeUndefined();
  });

  it('picks the highest-ranked insight (sortActiveInsights order) when more than one matches the same row', () => {
    const weak = insight({ severity: 'info', confidence: 'likely', statement: 'weak finding' });
    const strong = insight({ severity: 'alert', confidence: 'confirmed', statement: 'strong finding' });
    const [a] = annotateAlerts([alert({})], [weak, strong]);
    expect(a.insightAnnotation?.text).toBe('Cause: strong finding');
  });

  it('leaves every other alert field untouched and never mutates the input arrays', () => {
    const alerts = [alert({ entity: 'sonarr' })];
    const insights = [insight({})];
    const [a] = annotateAlerts(alerts, insights);
    expect(a.kind).toBe('container');
    expect(a.entity).toBe('sonarr');
    expect(alerts[0]).not.toHaveProperty('insightAnnotation');
  });
});

import { describe, expect, it } from 'vitest';
import {
  containerAnomalyEvidence,
  containerAnomalyHeadline,
  containerAnomalySeverity,
  deriveContainerAnomaly,
  deriveContainerAnomalyKind,
} from './containerAnomaly';
import type { GantryEvent } from './api';

function event(overrides: Partial<GantryEvent>): GantryEvent {
  return { ID: 1, TS: 1000, Kind: 'container.start', Entity: 'sonarr', Severity: 'info', Detail: '', ...overrides };
}

describe('deriveContainerAnomalyKind', () => {
  it('reads a running container with unhealthy health as unhealthy', () => {
    expect(deriveContainerAnomalyKind('running', 'unhealthy')).toBe('unhealthy');
  });

  it('reads a running container with healthy/no/starting health as no anomaly', () => {
    expect(deriveContainerAnomalyKind('running', '')).toBeNull();
    expect(deriveContainerAnomalyKind('running', 'healthy')).toBeNull();
    expect(deriveContainerAnomalyKind('running', 'starting')).toBeNull();
  });

  it('reads restarting as its own kind', () => {
    expect(deriveContainerAnomalyKind('restarting', '')).toBe('restarting');
  });

  it('reads exited/dead as stopped', () => {
    expect(deriveContainerAnomalyKind('exited', '')).toBe('stopped');
    expect(deriveContainerAnomalyKind('dead', '')).toBe('stopped');
  });

  it('ignores a stale "unhealthy" health string once the container is not running -- health only counts while running', () => {
    // Docker never clears health on stop (same R2.8 gate
    // unhealthyContainerNames applies) -- an exited container that was
    // unhealthy while it ran must still read as plain "stopped", not
    // "unhealthy", or the banner would misname the reason.
    expect(deriveContainerAnomalyKind('exited', 'unhealthy')).toBe('stopped');
  });

  it('treats created/paused/unrecognized states as no anomaly', () => {
    expect(deriveContainerAnomalyKind('created', '')).toBeNull();
    expect(deriveContainerAnomalyKind('paused', '')).toBeNull();
    expect(deriveContainerAnomalyKind('some-future-state', '')).toBeNull();
  });
});

describe('containerAnomalySeverity', () => {
  it('maps each kind to the same HealthStatus color language containerHealthStatus already uses', () => {
    expect(containerAnomalySeverity('unhealthy')).toBe('critical');
    expect(containerAnomalySeverity('restarting')).toBe('warning');
    expect(containerAnomalySeverity('stopped')).toBe('serious');
  });
});

describe('containerAnomalyHeadline', () => {
  it('renders the two fixed headlines verbatim', () => {
    expect(containerAnomalyHeadline('unhealthy', undefined)).toBe('Failing its health check');
    expect(containerAnomalyHeadline('restarting', undefined)).toBe('Restarting repeatedly');
  });

  it('renders a plain "Stopped" when no exit code is known', () => {
    expect(containerAnomalyHeadline('stopped', undefined)).toBe('Stopped');
  });

  it('renders the exit code plus its plain-language meaning when known', () => {
    expect(containerAnomalyHeadline('stopped', 137)).toBe('Stopped — exit code 137 (killed, likely out of memory)');
    expect(containerAnomalyHeadline('stopped', 0)).toBe('Stopped — exit code 0 (clean exit)');
    expect(containerAnomalyHeadline('stopped', 143)).toBe('Stopped — exit code 143 (terminated)');
  });

  it('renders the bare code with no parenthetical for an uncommon one', () => {
    expect(containerAnomalyHeadline('stopped', 2)).toBe('Stopped — exit code 2');
  });
});

describe('deriveContainerAnomaly', () => {
  it('returns null for a healthy running container -- no banner at all', () => {
    expect(deriveContainerAnomaly('running', 'healthy', undefined)).toBeNull();
  });

  it('composes kind/headline/severity for an unhealthy container', () => {
    expect(deriveContainerAnomaly('running', 'unhealthy', undefined)).toEqual({
      kind: 'unhealthy',
      headline: 'Failing its health check',
      severity: 'critical',
    });
  });

  it('composes kind/headline/severity for a stopped container with an exit code', () => {
    expect(deriveContainerAnomaly('exited', '', 137)).toEqual({
      kind: 'stopped',
      headline: 'Stopped — exit code 137 (killed, likely out of memory)',
      severity: 'serious',
    });
  });
});

describe('containerAnomalyEvidence', () => {
  it('keeps only the evidence-worthy kinds', () => {
    const events = [
      event({ Kind: 'container.health', Detail: 'unhealthy', TS: 100 }),
      event({ Kind: 'container.oom', TS: 200 }),
      event({ Kind: 'container.die', Detail: 'exit code 137', TS: 300 }),
      event({ Kind: 'container.start', Detail: 'restart count 2', TS: 400 }),
      event({ Kind: 'disk.errors', TS: 500 }),
    ];
    const evidence = containerAnomalyEvidence(events, 1_000_000);
    expect(evidence.map((e) => e.ts)).toEqual([400, 300, 200, 100]);
  });

  it('renders plain-language text per kind, with and without Detail', () => {
    const nowMs = 1000 * 1000; // matches TS=1000 exactly, so relTime reads "just now"
    const evidence = containerAnomalyEvidence(
      [
        event({ Kind: 'container.health', Detail: 'unhealthy', TS: 1000 }),
        event({ Kind: 'container.oom', Detail: '', TS: 999 }),
        event({ Kind: 'container.die', Detail: 'exit code 137', TS: 998 }),
        event({ Kind: 'container.die', Detail: '', TS: 997 }),
      ],
      nowMs,
    );
    expect(evidence.map((e) => e.text)).toEqual([
      'Became unhealthy',
      'Killed by the out-of-memory killer',
      'Stopped (exit code 137)',
      'Stopped',
    ]);
  });

  it('sorts newest-first regardless of input order', () => {
    const evidence = containerAnomalyEvidence(
      [event({ Kind: 'container.oom', TS: 100 }), event({ Kind: 'container.oom', TS: 300 }), event({ Kind: 'container.oom', TS: 200 })],
      1_000_000,
    );
    expect(evidence.map((e) => e.ts)).toEqual([300, 200, 100]);
  });

  it('caps at 4 lines', () => {
    const events = Array.from({ length: 10 }, (_, i) => event({ Kind: 'container.oom', TS: i }));
    expect(containerAnomalyEvidence(events, 1_000_000)).toHaveLength(4);
  });

  it('computes relTime off the provided nowMs', () => {
    const evidence = containerAnomalyEvidence([event({ Kind: 'container.oom', TS: 1000 })], 1000 * 1000 + 5 * 60 * 1000);
    expect(evidence[0].relTime).toBe('5m ago');
  });

  it('returns an empty array when nothing evidence-worthy is present', () => {
    expect(containerAnomalyEvidence([event({ Kind: 'disk.errors' })], 1_000_000)).toEqual([]);
  });
});

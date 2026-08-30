import { describe, expect, it } from 'vitest';
import { eventsToMarkers } from './eventMarkers';
import type { GantryEvent } from './api';

function ev(partial: Partial<GantryEvent>): GantryEvent {
  return { ID: 1, TS: 100, Kind: 'container.start', Entity: 'web', Severity: 'info', Detail: '', ...partial };
}

describe('eventsToMarkers', () => {
  it('maps container.start to a warning marker', () => {
    expect(eventsToMarkers([ev({ Kind: 'container.start', TS: 10 })])).toEqual([
      { ts: 10, severity: 'warning', label: 'Start' },
    ]);
  });

  it('maps container.oom to a critical marker', () => {
    expect(eventsToMarkers([ev({ Kind: 'container.oom', TS: 20 })])).toEqual([
      { ts: 20, severity: 'critical', label: 'OOM' },
    ]);
  });

  it('maps container.health to a serious marker', () => {
    expect(eventsToMarkers([ev({ Kind: 'container.health', TS: 30, Detail: 'unhealthy' })])).toEqual([
      { ts: 30, severity: 'serious', label: 'Health · unhealthy' },
    ]);
  });

  it('maps container.die to a warning marker', () => {
    expect(eventsToMarkers([ev({ Kind: 'container.die', TS: 40, Detail: 'exit code 1' })])).toEqual([
      { ts: 40, severity: 'warning', label: 'Stopped · exit code 1' },
    ]);
  });

  it('omits Detail from the label when empty', () => {
    expect(eventsToMarkers([ev({ Kind: 'container.start', TS: 10, Detail: '' })])[0].label).toBe('Start');
  });

  it('maps alert.fired to a critical marker, and alert.resolved to no marker at all', () => {
    expect(eventsToMarkers([ev({ Kind: 'alert.fired', TS: 50, Detail: 'disk4 is at 57.0 C' })])).toEqual([
      { ts: 50, severity: 'critical', label: 'Alert · disk4 is at 57.0 C' },
    ]);
    expect(eventsToMarkers([ev({ Kind: 'alert.resolved', TS: 60 })])).toEqual([]);
  });

  it('skips event kinds with no marker mapping (e.g. array/disk events)', () => {
    expect(eventsToMarkers([ev({ Kind: 'array.state', TS: 10 }), ev({ Kind: 'disk.errors', TS: 20 })])).toEqual([]);
  });

  it('handles null/undefined/empty input', () => {
    expect(eventsToMarkers(null)).toEqual([]);
    expect(eventsToMarkers(undefined)).toEqual([]);
    expect(eventsToMarkers([])).toEqual([]);
  });

  it('preserves input order', () => {
    const events = [ev({ Kind: 'container.start', TS: 10 }), ev({ Kind: 'container.oom', TS: 20 })];
    expect(eventsToMarkers(events).map((m) => m.ts)).toEqual([10, 20]);
  });
});

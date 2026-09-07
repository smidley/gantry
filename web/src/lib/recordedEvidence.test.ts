import { describe, it, expect } from 'vitest';
import { mergeRecordedHistory } from './incidentChart';

describe('saved incident evidence', () => {
  const recorded = [{ kind: 'container', entity: 'worker', metric: 'io.read_bps', points: [[10, 2], [20, 9]] as [number, number][] }];
  it('restores a live-only series after ordinary history expires', () => {
    expect(mergeRecordedHistory([], recorded, 'container', 'worker')).toEqual([{ metric: 'io.read_bps', points: [[10, 2, 2], [20, 9, 9]] }]);
  });
  it('keeps measured history at overlapping timestamps and never mixes entities', () => {
    const history = [{ metric: 'io.read_bps', points: [[20, 4, 7], [30, 5, 6]] as [number, number, number][] }];
    expect(mergeRecordedHistory(history, recorded, 'container', 'worker')[0].points).toEqual([[10, 2, 2], [20, 4, 7], [30, 5, 6]]);
    expect(mergeRecordedHistory([], recorded, 'container', 'unrelated')).toEqual([]);
  });
});

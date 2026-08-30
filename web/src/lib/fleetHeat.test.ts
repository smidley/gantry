import { describe, expect, it } from 'vitest';
import { fleetHeatVar } from './fleetHeat';

describe('fleetHeatVar', () => {
  it('returns null at or under the idle floor', () => {
    expect(fleetHeatVar(0)).toBeNull();
    expect(fleetHeatVar(1)).toBeNull();
  });

  it('returns null for non-finite input', () => {
    expect(fleetHeatVar(NaN)).toBeNull();
    expect(fleetHeatVar(Infinity)).toBeNull();
    expect(fleetHeatVar(-Infinity)).toBeNull();
  });

  it('warms up through the seq ramp as usage rises above idle', () => {
    expect(fleetHeatVar(2)).toBe('var(--seq-100)');
    expect(fleetHeatVar(13)).toBe('var(--seq-400)');
  });

  it('reaches the warmest stop at/above the hot ceiling', () => {
    expect(fleetHeatVar(25)).toBe('var(--seq-700)');
    expect(fleetHeatVar(100)).toBe('var(--seq-700)');
  });

  it('never returns a stop below --seq-100 for any above-idle value', () => {
    expect(fleetHeatVar(1.1)).toBe('var(--seq-100)');
  });
});

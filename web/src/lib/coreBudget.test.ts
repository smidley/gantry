import { describe, expect, it } from 'vitest';
import { buildCoreBudget, MAX_NAMED_SEGMENTS } from './coreBudget';

describe('buildCoreBudget', () => {
  it('is empty for a host with no core count yet', () => {
    expect(buildCoreBudget(0, 50, [{ name: 'a', cores: 1 }])).toEqual({ segments: [], freeCores: 0 });
    expect(buildCoreBudget(-1, 50, [])).toEqual({ segments: [], freeCores: 0 });
  });

  it('names every container when there are 8 or fewer, sorted desc by cores', () => {
    const budget = buildCoreBudget(8, 0, [
      { name: 'a', cores: 1 },
      { name: 'b', cores: 3 },
      { name: 'c', cores: 2 },
    ]);
    expect(budget.segments.map((s) => s.key)).toEqual(['b', 'c', 'a']);
    expect(budget.segments.every((s) => s.colorVar.startsWith('var(--series-'))).toBe(true);
    // Distinct series slots, in rank order.
    expect(budget.segments.map((s) => s.colorVar)).toEqual(['var(--series-1)', 'var(--series-2)', 'var(--series-3)']);
  });

  it('breaks a cores tie by name ascending, deterministically', () => {
    const budget = buildCoreBudget(8, 0, [
      { name: 'zeta', cores: 1 },
      { name: 'alpha', cores: 1 },
    ]);
    expect(budget.segments.map((s) => s.key)).toEqual(['alpha', 'zeta']);
  });

  it('filters out a container using zero (or negative) cores', () => {
    const budget = buildCoreBudget(8, 0, [
      { name: 'idle', cores: 0 },
      { name: 'busy', cores: 1 },
    ]);
    expect(budget.segments.map((s) => s.key)).toEqual(['busy']);
  });

  it('buckets everything past the top 8 into one "Others" segment', () => {
    const containers = Array.from({ length: 10 }, (_, i) => ({ name: `c${i}`, cores: 10 - i }));
    const budget = buildCoreBudget(64, 0, containers);
    expect(budget.segments).toHaveLength(MAX_NAMED_SEGMENTS + 1);
    const others = budget.segments[budget.segments.length - 1];
    expect(others.key).toBe('others');
    expect(others.label).toBe('Others (2)');
    // c8 (cores=2) + c9 (cores=1)
    expect(others.cores).toBe(3);
  });

  it('adds an unattributed-host segment for host activity no container accounts for', () => {
    // hostCores=8, cpu.total=50% -> 4 cores host-wide; containers use 1.
    const budget = buildCoreBudget(8, 50, [{ name: 'a', cores: 1 }]);
    const unattributed = budget.segments.find((s) => s.key === 'unattributed');
    expect(unattributed?.cores).toBe(3);
    expect(budget.freeCores).toBe(4);
  });

  it('clamps unattributed at zero rather than going negative on a stale reading', () => {
    // Containers report MORE cores than the host's own total says -- a
    // momentarily-stale host sample, not a real negative attribution.
    const budget = buildCoreBudget(8, 10, [{ name: 'a', cores: 2 }]);
    expect(budget.segments.some((s) => s.key === 'unattributed')).toBe(false);
  });

  it('omits the unattributed segment entirely when there is nothing to attribute', () => {
    const budget = buildCoreBudget(8, 0, [{ name: 'a', cores: 1 }]);
    expect(budget.segments.map((s) => s.key)).toEqual(['a']);
  });

  it('free headroom is whatever is left of hostCores after every segment', () => {
    const budget = buildCoreBudget(4, 100, []); // fully busy, no containers tracked
    const unattributed = budget.segments.find((s) => s.key === 'unattributed');
    expect(unattributed?.cores).toBe(4);
    expect(budget.freeCores).toBe(0);
  });

  it('free headroom clamps at zero rather than going negative', () => {
    // Containers alone (5 cores) already exceed hostCores (4) -- a stale/
    // inconsistent reading, not a real negative amount of free capacity.
    const budget = buildCoreBudget(4, 50, [
      { name: 'a', cores: 3 },
      { name: 'b', cores: 2 },
    ]);
    expect(budget.freeCores).toBe(0);
  });
});

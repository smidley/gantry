import { describe, expect, it } from 'vitest';
import { buildCoreBudget, MAX_NAMED_SEGMENTS } from './coreBudget';
import { containerColor } from './containerColor';

describe('buildCoreBudget', () => {
  it('is empty for a host with no core count yet', () => {
    expect(buildCoreBudget(0, 50, [{ name: 'a', cores: 1 }])).toEqual({ segments: [], freeCores: 0 });
    expect(buildCoreBudget(-1, 50, [])).toEqual({ segments: [], freeCores: 0 });
  });

  it('names every container when there are 10 or fewer, sorted desc by cores', () => {
    const budget = buildCoreBudget(8, 0, [
      { name: 'a', cores: 1 },
      { name: 'b', cores: 3 },
      { name: 'c', cores: 2 },
    ]);
    expect(budget.segments.map((s) => s.key)).toEqual(['b', 'c', 'a']);
    expect(budget.segments.every((s) => s.colorVar.startsWith('var(--series-'))).toBe(true);
    // Colored by each container's own stable identity hash (containerColor),
    // not by its rank position in the ribbon -- a container's segment must
    // match the SAME color the Metrics hero chart/Compare would assign it,
    // and must not repaint if its cores rank shifts relative to its
    // neighbors on a later tick.
    expect(budget.segments.map((s) => s.colorVar)).toEqual(['b', 'c', 'a'].map((name) => `var(${containerColor(name)})`));
  });

  it('a container keeps the same color regardless of which rank position it sorts into', () => {
    const first = buildCoreBudget(8, 0, [
      { name: 'a', cores: 1 },
      { name: 'b', cores: 3 },
    ]);
    // 'a' now outranks 'b' -- under the old position-based rule this
    // would have swapped their colors; identity-based coloring must not.
    const second = buildCoreBudget(8, 0, [
      { name: 'a', cores: 5 },
      { name: 'b', cores: 3 },
    ]);
    const colorFor = (budget: ReturnType<typeof buildCoreBudget>, key: string) =>
      budget.segments.find((s) => s.key === key)?.colorVar;
    expect(colorFor(first, 'a')).toBe(colorFor(second, 'a'));
    expect(colorFor(first, 'b')).toBe(colorFor(second, 'b'));
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

  it('buckets everything past the top 10 into one "Others" segment', () => {
    const containers = Array.from({ length: 13 }, (_, i) => ({ name: `c${i}`, cores: 20 - i }));
    const budget = buildCoreBudget(64, 0, containers);
    expect(budget.segments).toHaveLength(MAX_NAMED_SEGMENTS + 1);
    const others = budget.segments[budget.segments.length - 1];
    expect(others.key).toBe('others');
    expect(others.label).toBe('Others (3)');
    // c10 (cores=10) + c11 (cores=9) + c12 (cores=8)
    expect(others.cores).toBe(27);
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

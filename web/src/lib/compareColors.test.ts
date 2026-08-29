import { describe, expect, it } from 'vitest';
import { seriesColorVar } from './compareColors';

describe('seriesColorVar', () => {
  it('assigns --series-1 through --series-8 for indices 0-7', () => {
    const expected = [1, 2, 3, 4, 5, 6, 7, 8].map((n) => `--series-${n}`);
    const actual = [0, 1, 2, 3, 4, 5, 6, 7].map(seriesColorVar);
    expect(actual).toEqual(expected);
  });

  it('wraps back to --series-1 past the 8-slot palette (defensive only)', () => {
    expect(seriesColorVar(8)).toBe('--series-1');
    expect(seriesColorVar(9)).toBe('--series-2');
  });

  it('wraps a negative index into range rather than producing a bogus var name', () => {
    expect(seriesColorVar(-1)).toBe('--series-8');
  });
});

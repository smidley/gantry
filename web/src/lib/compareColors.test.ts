import { describe, expect, it } from 'vitest';
import { seriesColorVar } from './compareColors';

describe('seriesColorVar', () => {
  it('assigns --series-1 through --series-10 for indices 0-9', () => {
    const expected = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((n) => `--series-${n}`);
    const actual = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map(seriesColorVar);
    expect(actual).toEqual(expected);
  });

  it('wraps back to --series-1 past the 10-slot palette (defensive only)', () => {
    expect(seriesColorVar(10)).toBe('--series-1');
    expect(seriesColorVar(11)).toBe('--series-2');
  });

  it('wraps a negative index into range rather than producing a bogus var name', () => {
    expect(seriesColorVar(-1)).toBe('--series-10');
  });
});

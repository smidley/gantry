import { describe, expect, it } from 'vitest';
import { extractTokenName } from './theme';

describe('extractTokenName', () => {
  it('recognizes a bare custom-property name (TimeChart/GPUEntityCard colorVar shape)', () => {
    expect(extractTokenName('--series-1')).toBe('--series-1');
    expect(extractTokenName('--status-critical')).toBe('--status-critical');
  });

  it('recognizes a full var() reference (Sparkline/StatTile color shape)', () => {
    expect(extractTokenName('var(--series-1)')).toBe('--series-1');
  });

  it('tolerates surrounding whitespace', () => {
    expect(extractTokenName('  --series-1  ')).toBe('--series-1');
    expect(extractTokenName('  var(--series-1)  ')).toBe('--series-1');
  });

  it('returns null for a literal color (passes through unchanged upstream)', () => {
    expect(extractTokenName('#ff0000')).toBeNull();
    expect(extractTokenName('red')).toBeNull();
    expect(extractTokenName('rgba(0,0,0,0.5)')).toBeNull();
  });

  it('returns null for a malformed var() reference', () => {
    expect(extractTokenName('var(series-1)')).toBeNull(); // missing --
    expect(extractTokenName('var(--series-1')).toBeNull(); // missing closing paren
  });
});

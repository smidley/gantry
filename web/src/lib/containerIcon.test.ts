import { describe, expect, it } from 'vitest';
import { fallbackLetter } from './containerIcon';

describe('fallbackLetter', () => {
  it('uppercases the first character of the name', () => {
    expect(fallbackLetter('jellyfin')).toBe('J');
  });

  it('falls back to ? for an empty name', () => {
    expect(fallbackLetter('')).toBe('?');
  });

  it('falls back to ? for a whitespace-only name', () => {
    expect(fallbackLetter('   ')).toBe('?');
  });

  it('ignores leading whitespace when picking the first real character', () => {
    expect(fallbackLetter('  radarr')).toBe('R');
  });
});

import { describe, expect, it } from 'vitest';
import { resolveReducedMotion } from './motion';

describe('resolveReducedMotion', () => {
  it('forces animations ON regardless of the OS setting', () => {
    expect(resolveReducedMotion('on', true)).toBe(false);
    expect(resolveReducedMotion('on', false)).toBe(false);
  });

  it('forces animations OFF regardless of the OS setting', () => {
    expect(resolveReducedMotion('off', true)).toBe(true);
    expect(resolveReducedMotion('off', false)).toBe(true);
  });

  it('mirrors the OS setting under "system"', () => {
    expect(resolveReducedMotion('system', true)).toBe(true);
    expect(resolveReducedMotion('system', false)).toBe(false);
  });
});

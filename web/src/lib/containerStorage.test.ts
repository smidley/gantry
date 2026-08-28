import { describe, expect, it } from 'vitest';
import { normalizeStorageKind, sortMounts } from './containerStorage';

describe('normalizeStorageKind', () => {
  it('passes through every known kind unchanged', () => {
    expect(normalizeStorageKind('share')).toBe('share');
    expect(normalizeStorageKind('pool')).toBe('pool');
    expect(normalizeStorageKind('disk')).toBe('disk');
    expect(normalizeStorageKind('flash')).toBe('flash');
    expect(normalizeStorageKind('other')).toBe('other');
  });

  it('falls back to "other" for anything unrecognized, e.g. a future server build', () => {
    expect(normalizeStorageKind('exotic')).toBe('other');
    expect(normalizeStorageKind('')).toBe('other');
  });
});

describe('sortMounts', () => {
  const mount = (destination: string, kind: string, name = '') => ({ destination, storage: { kind, name } });

  it('orders by kind in share, pool, disk, flash, other order', () => {
    const mounts = [
      mount('/flash', 'flash'),
      mount('/disk', 'disk', 'disk1'),
      mount('/other', 'other'),
      mount('/pool', 'pool', 'cache'),
      mount('/share', 'share', 'appdata'),
    ];
    expect(sortMounts(mounts).map((m) => m.destination)).toEqual(['/share', '/pool', '/disk', '/flash', '/other']);
  });

  it('clusters multiple mounts into the same storage system together, by name within a kind', () => {
    const mounts = [
      mount('/b', 'pool', 'rocket_pool'),
      mount('/z', 'pool', 'cache'),
      mount('/a', 'pool', 'rocket_pool'),
    ];
    expect(sortMounts(mounts).map((m) => m.destination)).toEqual(['/z', '/a', '/b']);
  });

  it('breaks a tie within one storage system by destination', () => {
    const mounts = [mount('/z', 'share', 'appdata'), mount('/a', 'share', 'appdata')];
    expect(sortMounts(mounts).map((m) => m.destination)).toEqual(['/a', '/z']);
  });

  it('sorts an unrecognized kind alongside "other"', () => {
    const mounts = [mount('/exotic', 'exotic'), mount('/share', 'share', 'appdata')];
    expect(sortMounts(mounts).map((m) => m.destination)).toEqual(['/share', '/exotic']);
  });

  it('does not mutate the input array', () => {
    const mounts = [mount('/z', 'flash'), mount('/a', 'share', 'appdata')];
    sortMounts(mounts);
    expect(mounts.map((m) => m.destination)).toEqual(['/z', '/a']);
  });
});

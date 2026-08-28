import { describe, expect, it } from 'vitest';
import { mountCapacitySlot, normalizeStorageKind, sortMounts } from './containerStorage';

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

describe('mountCapacitySlot', () => {
  const mount = (kind: string, name = '') => ({ destination: '/x', storage: { kind, name } });

  it('resolves a pool mount to its own pool slot name', () => {
    expect(mountCapacitySlot(mount('pool', 'rocket_pool'))).toBe('rocket_pool');
  });

  it('resolves a disk mount to its own disk slot name', () => {
    expect(mountCapacitySlot(mount('disk', 'disk1'))).toBe('disk1');
  });

  it('resolves a flash mount to the fixed "flash" slot, even though the mount itself carries no name', () => {
    expect(mountCapacitySlot(mount('flash'))).toBe('flash');
  });

  it('returns null for a share mount -- a share spans disks, no single slot to show', () => {
    expect(mountCapacitySlot(mount('share', 'appdata'))).toBeNull();
  });

  it('returns null for an unresolved ("other") mount', () => {
    expect(mountCapacitySlot(mount('other'))).toBeNull();
  });

  it('returns null for an unrecognized kind, same as "other"', () => {
    expect(mountCapacitySlot(mount('exotic'))).toBeNull();
  });
});

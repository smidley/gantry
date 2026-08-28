import { describe, expect, it } from 'vitest';
import {
  isUnraidOSLoopDevice,
  mountCapacitySlot,
  normalizeStorageKind,
  recentlyActiveDevices,
  recordDeviceActivity,
  RECENT_IO_WINDOW_MS,
  sortMounts,
} from './containerStorage';

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

describe('recordDeviceActivity / recentlyActiveDevices', () => {
  const dev = (device: string, read_bps: number, write_bps: number) => ({ device, read_bps, write_bps });

  it('a device active this poll is recorded and shows up as recently active', () => {
    const lastActiveAt = new Map<string, number>();
    recordDeviceActivity([dev('sda', 100, 0)], lastActiveAt, 1000);
    expect(recentlyActiveDevices([dev('sda', 100, 0)], lastActiveAt, 1000)).toEqual([dev('sda', 100, 0)]);
  });

  it('a device never once recorded is excluded, even if it is in the current devices list at 0/0', () => {
    const lastActiveAt = new Map<string, number>();
    expect(recentlyActiveDevices([dev('sda', 0, 0)], lastActiveAt, 1000)).toEqual([]);
  });

  it('a device that went idle stays visible within the trailing window', () => {
    const lastActiveAt = new Map<string, number>();
    recordDeviceActivity([dev('sda', 100, 0)], lastActiveAt, 0);
    // now idle, but still inside RECENT_IO_WINDOW_MS of its last active tick
    const visible = recentlyActiveDevices([dev('sda', 0, 0)], lastActiveAt, RECENT_IO_WINDOW_MS);
    expect(visible).toEqual([dev('sda', 0, 0)]);
  });

  it('a device drops out once it has been idle longer than the window', () => {
    const lastActiveAt = new Map<string, number>();
    recordDeviceActivity([dev('sda', 100, 0)], lastActiveAt, 0);
    const visible = recentlyActiveDevices([dev('sda', 0, 0)], lastActiveAt, RECENT_IO_WINDOW_MS + 1);
    expect(visible).toEqual([]);
  });

  it('write-only activity counts the same as read activity', () => {
    const lastActiveAt = new Map<string, number>();
    recordDeviceActivity([dev('nvme0n1', 0, 50)], lastActiveAt, 500);
    expect(recentlyActiveDevices([dev('nvme0n1', 0, 0)], lastActiveAt, 500)).toEqual([dev('nvme0n1', 0, 0)]);
  });

  it('each device tracks its own activity independently', () => {
    const lastActiveAt = new Map<string, number>();
    recordDeviceActivity([dev('sda', 100, 0), dev('loop2', 0, 0)], lastActiveAt, 0);
    const visible = recentlyActiveDevices([dev('sda', 0, 0), dev('loop2', 0, 0)], lastActiveAt, 0);
    expect(visible.map((d) => d.device)).toEqual(['sda']);
  });
});

describe('isUnraidOSLoopDevice', () => {
  it('recognizes Unraid boot-image labels', () => {
    expect(isUnraidOSLoopDevice('bzmodules')).toBe(true);
    expect(isUnraidOSLoopDevice('bzroot')).toBe(true);
    expect(isUnraidOSLoopDevice('bzimage')).toBe(true);
  });

  it('does not flag an ordinary label that merely contains "bz"', () => {
    expect(isUnraidOSLoopDevice('docker.img')).toBe(false);
    expect(isUnraidOSLoopDevice('rocket_pool')).toBe(false);
  });
});

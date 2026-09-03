import { describe, expect, it } from 'vitest';
import {
  isUnraidOSLoopDevice,
  shfsFrontedShares,
  shfsNote,
  mountCapacitySlot,
  normalizeStorageKind,
  recentlyActiveDevices,
  recordDeviceActivity,
  RECENT_IO_WINDOW_MS,
  sharePlacementText,
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

describe('sharePlacementText', () => {
  it('is null with no placement at all', () => {
    expect(sharePlacementText(undefined)).toBeNull();
  });

  it('"only" names the pool, with its kind when known', () => {
    expect(sharePlacementText({ mode: 'only', pool: 'scratch' }, 'nvme')).toBe('→ lives on scratch (nvme)');
  });

  it('"only" still names the pool with no kind resolved yet', () => {
    expect(sharePlacementText({ mode: 'only', pool: 'scratch' })).toBe('→ lives on scratch');
  });

  it('"yes" reads as cache then array, with no pool name needed', () => {
    expect(sharePlacementText({ mode: 'yes', pool: 'cache' })).toBe('→ cache then array');
  });

  it('"no" reads as array only', () => {
    expect(sharePlacementText({ mode: 'no' })).toBe('→ array');
  });

  it('"prefer" names the pool and notes the array overflow', () => {
    expect(sharePlacementText({ mode: 'prefer', pool: 'rocket_pool' }, 'nvme')).toBe('→ prefers rocket_pool (nvme), spills to array');
  });

  it('is null for "only"/"prefer" with no pool name (malformed input)', () => {
    expect(sharePlacementText({ mode: 'only' })).toBeNull();
    expect(sharePlacementText({ mode: 'prefer' })).toBeNull();
  });

  it('is null for an unrecognized mode', () => {
    expect(sharePlacementText({ mode: 'bogus' })).toBeNull();
  });
});

describe('shfsFrontedShares', () => {
  const mount = (kind: string, name: string, shfs?: boolean) => ({ storage: { kind, name, shfs } });

  it('names every share mount the server flagged as shfs-fronted', () => {
    expect(
      shfsFrontedShares([mount('share', 'Movies', true), mount('share', 'TV', true), mount('pool', 'rocket_pool')]),
    ).toEqual(['Movies', 'TV']);
  });

  it('skips a share whose IO is attributable (exclusive, so bind-mounted past shfs)', () => {
    expect(shfsFrontedShares([mount('share', 'data', false), mount('share', 'Movies', true)])).toEqual(['Movies']);
  });

  it('lists each share once even when several mounts point into it', () => {
    expect(shfsFrontedShares([mount('share', 'TV', true), mount('share', 'TV', true)])).toEqual(['TV']);
  });

  it('is empty when nothing is shfs-fronted', () => {
    expect(shfsFrontedShares([mount('pool', 'cache'), mount('disk', 'disk1')])).toEqual([]);
  });
});

describe('shfsNote', () => {
  it('is null when no share is shfs-fronted -- nothing to explain', () => {
    expect(shfsNote([])).toBeNull();
  });

  it('reads naturally for one share', () => {
    expect(shfsNote(['Movies'])).toBe(
      "Movies goes through Unraid's shfs layer, which does that disk IO on the container's behalf. None of it can be counted here or in the IO chart.",
    );
  });

  it('joins two shares with "and"', () => {
    expect(shfsNote(['Movies', 'TV'])).toBe(
      "Movies and TV go through Unraid's shfs layer, which does that disk IO on the container's behalf. None of it can be counted here or in the IO chart.",
    );
  });

  it('comma-separates three or more, with "and" before the last', () => {
    expect(shfsNote(['Movies', 'TV', 'Web_Media'])).toBe(
      "Movies, TV and Web_Media go through Unraid's shfs layer, which does that disk IO on the container's behalf. None of it can be counted here or in the IO chart.",
    );
  });
});

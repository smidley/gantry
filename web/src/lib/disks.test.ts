import { describe, expect, it } from 'vitest';
import { diskKind, diskMediaType, diskRole, diskTempState, diskUsagePct, sortDiskEntities } from './disks';

describe('diskRole', () => {
  it('classifies parity slots, including dual parity', () => {
    expect(diskRole('parity')).toBe('parity');
    expect(diskRole('parity2')).toBe('parity');
  });

  it('classifies numbered data disks', () => {
    expect(diskRole('disk1')).toBe('data');
    expect(diskRole('disk10')).toBe('data');
  });

  it('classifies the boot flash device', () => {
    expect(diskRole('flash')).toBe('flash');
  });

  it('buckets everything else (cache, named pools) as pool', () => {
    expect(diskRole('cache')).toBe('pool');
    expect(diskRole('rocket_pool')).toBe('pool');
    expect(diskRole('scratch')).toBe('pool');
  });
});

describe('sortDiskEntities', () => {
  it('orders parity, then numbered disks, then pools, then flash', () => {
    const names = ['cache', 'flash', 'disk2', 'parity', 'disk1'];
    expect(sortDiskEntities(names)).toEqual(['parity', 'disk1', 'disk2', 'cache', 'flash']);
  });

  it('orders numbered disks numerically, not lexicographically', () => {
    expect(sortDiskEntities(['disk10', 'disk2', 'disk1'])).toEqual(['disk1', 'disk2', 'disk10']);
  });

  it('orders dual parity before parity2', () => {
    expect(sortDiskEntities(['parity2', 'parity'])).toEqual(['parity', 'parity2']);
  });

  it('breaks ties within the pool group alphabetically', () => {
    expect(sortDiskEntities(['scratch', 'cache', 'rocket_pool'])).toEqual(['cache', 'rocket_pool', 'scratch']);
  });

  it('does not mutate the input array', () => {
    const input = ['disk2', 'disk1'];
    sortDiskEntities(input);
    expect(input).toEqual(['disk2', 'disk1']);
  });
});

describe('diskTempState', () => {
  it('reads a present temp.c as a reading, regardless of spun_up', () => {
    expect(diskTempState({ 'temp.c': 34.5, spun_up: 1 })).toEqual({ kind: 'reading', celsius: 34.5 });
  });

  it('treats a temp of exactly 0 as a real reading, not a missing sample', () => {
    expect(diskTempState({ 'temp.c': 0, spun_up: 1 })).toEqual({ kind: 'reading', celsius: 0 });
  });

  it('reports spun-down when temp.c is absent and spun_up is 0', () => {
    expect(diskTempState({ spun_up: 0 })).toEqual({ kind: 'spun-down' });
  });

  it('reports no-sensor when spun_up is 1 but temp.c is absent (e.g. the boot flash device)', () => {
    expect(diskTempState({ spun_up: 1 })).toEqual({ kind: 'no-sensor' });
  });

  it('reports no-sensor when spun_up itself is absent', () => {
    expect(diskTempState({})).toEqual({ kind: 'no-sensor' });
    expect(diskTempState(undefined)).toEqual({ kind: 'no-sensor' });
    expect(diskTempState(null)).toEqual({ kind: 'no-sensor' });
  });
});

describe('diskMediaType', () => {
  it('reads rotational=0 as solid-state', () => {
    expect(diskMediaType({ rotational: 0 })).toBe('ssd');
  });

  it('reads rotational=1 as spinning', () => {
    expect(diskMediaType({ rotational: 1 })).toBe('hdd');
  });

  it('treats any nonzero rotational reading as spinning, not just exactly 1', () => {
    expect(diskMediaType({ rotational: 2 })).toBe('hdd');
  });

  it('returns null when rotational is absent -- unknown, not a guess', () => {
    expect(diskMediaType({})).toBeNull();
    expect(diskMediaType(undefined)).toBeNull();
    expect(diskMediaType(null)).toBeNull();
  });
});

describe('diskKind', () => {
  it('reads nvme/usb straight from disk_meta -- signals rotational alone can never carry', () => {
    expect(diskKind({ kind: 'nvme' }, { rotational: 0 })).toBe('nvme');
    expect(diskKind({ kind: 'usb' }, { rotational: 1 })).toBe('usb');
  });

  it('reads hdd/ssd from disk_meta too, agreeing with the legacy rotational read', () => {
    expect(diskKind({ kind: 'hdd' }, { rotational: 1 })).toBe('hdd');
    expect(diskKind({ kind: 'ssd' }, { rotational: 0 })).toBe('ssd');
  });

  it('falls back to the legacy rotational-only read when disk_meta has no entry for this slot', () => {
    expect(diskKind(undefined, { rotational: 0 })).toBe('ssd');
    expect(diskKind(null, { rotational: 1 })).toBe('hdd');
    expect(diskKind({}, { rotational: 1 })).toBe('hdd');
  });

  it('falls back when disk_meta.kind is present but unrecognized (e.g. a future server, an older enum)', () => {
    expect(diskKind({ kind: 'exotic' }, { rotational: 0 })).toBe('ssd');
  });

  it('returns null when neither disk_meta nor rotational says anything', () => {
    expect(diskKind(undefined, {})).toBeNull();
    expect(diskKind(undefined, undefined)).toBeNull();
  });
});

describe('diskUsagePct', () => {
  it('computes a percentage from used/free bytes', () => {
    expect(diskUsagePct({ 'fs.used_bytes': 30, 'fs.free_bytes': 70 })).toBe(30);
  });

  it('returns null when either half is absent (e.g. parity, which has no filesystem)', () => {
    expect(diskUsagePct({ 'fs.used_bytes': 30 })).toBeNull();
    expect(diskUsagePct({ 'fs.free_bytes': 70 })).toBeNull();
    expect(diskUsagePct({})).toBeNull();
    expect(diskUsagePct(undefined)).toBeNull();
  });

  it('returns 0 rather than dividing by zero when total is 0', () => {
    expect(diskUsagePct({ 'fs.used_bytes': 0, 'fs.free_bytes': 0 })).toBe(0);
  });
});

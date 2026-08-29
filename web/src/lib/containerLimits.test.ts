import { describe, expect, it } from 'vitest';
import { cpusetCoreCount, limitsFactsParts } from './containerLimits';

describe('cpusetCoreCount', () => {
  it('counts a single range', () => {
    expect(cpusetCoreCount('0-5')).toBe(6);
  });

  it('counts mixed ranges and singles', () => {
    expect(cpusetCoreCount('0-5, 13-15')).toBe(9);
  });

  it('counts a lone single core', () => {
    expect(cpusetCoreCount('3')).toBe(1);
  });

  it('counts singles-only, comma-separated', () => {
    expect(cpusetCoreCount('0, 2, 4')).toBe(3);
  });
});

describe('limitsFactsParts', () => {
  it('returns an empty array when nothing is limited', () => {
    expect(limitsFactsParts({})).toEqual([]);
  });

  it('renders only the resources that have a limit, in a fixed order', () => {
    expect(
      limitsFactsParts({
        memLimitBytes: 2 * 1024 ** 3,
        cpuAllocCores: 4,
        pidsLimit: 2048,
      }),
    ).toEqual(['memory 2.0 GiB', 'CPU 4.0 cores', 'pids 2048']);
  });

  it('renders memory alone when only memory is limited', () => {
    expect(limitsFactsParts({ memLimitBytes: 1.7e9 })).toEqual([`memory ${'1.6 GiB'}`]);
  });

  it('appends the cpuset pin with its own core count, after the other parts', () => {
    expect(
      limitsFactsParts({
        cpuAllocCores: 9,
        cpuset: '0-5, 13-15',
      }),
    ).toEqual(['CPU 9.0 cores', 'pinned to 9 cores: 0-5, 13-15']);
  });

  it('renders the cpuset pin alone when nothing else is limited', () => {
    expect(limitsFactsParts({ cpuset: '0-1' })).toEqual(['pinned to 2 cores: 0-1']);
  });

  it('ignores an empty cpuset string as "no pin"', () => {
    expect(limitsFactsParts({ cpuset: '', memLimitBytes: 1e9 })).toEqual([`memory ${'953.7 MiB'}`]);
  });

  it('rounds pids to a whole number', () => {
    expect(limitsFactsParts({ pidsLimit: 2048.4 })).toEqual(['pids 2048']);
  });
});

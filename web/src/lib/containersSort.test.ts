import { describe, expect, it } from 'vitest';
import { matchesContainerFilter, sortContainerNames } from './containersSort';
import type { ContainerDTO } from './api';

const containers: Record<string, ContainerDTO> = {
  alpha: { state: 'running', health: '', image: 'img/alpha:1', metrics: { 'cpu.pct': 10, 'mem.bytes': 300 } },
  beta: { state: 'running', health: 'unhealthy', image: 'img/beta:1', metrics: { 'cpu.pct': 30, 'mem.bytes': 100 } },
  gamma: { state: 'exited', health: '', image: 'img/gamma:1', metrics: { 'cpu.pct': 20, 'mem.bytes': 200 } },
};
const names = Object.keys(containers);

describe('sortContainerNames', () => {
  it('sorts by cpu descending (the table default)', () => {
    expect(sortContainerNames(names, containers, 'cpu', 'desc', 0)).toEqual(['beta', 'gamma', 'alpha']);
  });

  it('sorts by cpu ascending', () => {
    expect(sortContainerNames(names, containers, 'cpu', 'asc', 0)).toEqual(['alpha', 'gamma', 'beta']);
  });

  it('sorts by mem', () => {
    expect(sortContainerNames(names, containers, 'mem', 'desc', 0)).toEqual(['alpha', 'gamma', 'beta']);
  });

  it('sorts by name', () => {
    expect(sortContainerNames(names, containers, 'name', 'asc', 0)).toEqual(['alpha', 'beta', 'gamma']);
    expect(sortContainerNames(names, containers, 'name', 'desc', 0)).toEqual(['gamma', 'beta', 'alpha']);
  });

  it('sorts by health severity (unhealthy=critical first on desc)', () => {
    expect(sortContainerNames(names, containers, 'health', 'desc', 0)).toEqual(['beta', 'gamma', 'alpha']);
  });

  it('missing metrics default to 0 rather than sorting unpredictably', () => {
    const withGpu: Record<string, ContainerDTO> = {
      alpha: { state: 'running', health: '', image: '', metrics: { 'gpu.video.busy_pct': 15 } },
      delta: { state: 'running', health: '', image: '', metrics: {} }, // no gpu.* metrics at all
    };
    const result = sortContainerNames(['alpha', 'delta'], withGpu, 'gpu', 'desc', 0);
    expect(result).toEqual(['alpha', 'delta']); // 0 GPU (delta) sorts last on desc, not NaN/undefined
  });

  it('breaks ties by name ascending regardless of direction', () => {
    const tied: Record<string, ContainerDTO> = {
      zeta: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 5 } },
      yankee: { state: 'running', health: '', image: '', metrics: { 'cpu.pct': 5 } },
    };
    expect(sortContainerNames(['zeta', 'yankee'], tied, 'cpu', 'desc', 0)).toEqual(['yankee', 'zeta']);
    expect(sortContainerNames(['zeta', 'yankee'], tied, 'cpu', 'asc', 0)).toEqual(['yankee', 'zeta']);
  });

  it('sorts by uptime using nowTs - meta.started_at', () => {
    const withUptime: Record<string, ContainerDTO> = {
      old: { state: 'running', health: '', image: '', metrics: { 'meta.started_at': 100 } },
      new: { state: 'running', health: '', image: '', metrics: { 'meta.started_at': 900 } },
    };
    expect(sortContainerNames(['old', 'new'], withUptime, 'uptime', 'desc', 1000)).toEqual(['old', 'new']);
  });

  it('does not mutate the input names array', () => {
    const input = [...names];
    sortContainerNames(input, containers, 'cpu', 'desc', 0);
    expect(input).toEqual(names);
  });
});

describe('matchesContainerFilter', () => {
  it('matches an empty filter against anything', () => {
    expect(matchesContainerFilter('jellyfin', 'jellyfin/jellyfin:latest', '')).toBe(true);
    expect(matchesContainerFilter('jellyfin', 'jellyfin/jellyfin:latest', '   ')).toBe(true);
  });

  it('matches a substring of the name, case-insensitively', () => {
    expect(matchesContainerFilter('jellyfin', 'x', 'JELLY')).toBe(true);
    expect(matchesContainerFilter('jellyfin', 'x', 'radarr')).toBe(false);
  });

  it('matches a substring of the image', () => {
    expect(matchesContainerFilter('web', 'linuxserver/radarr:latest', 'radarr')).toBe(true);
    expect(matchesContainerFilter('web', 'linuxserver/radarr:latest', 'sonarr')).toBe(false);
  });
});

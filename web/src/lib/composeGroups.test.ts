import { describe, expect, it } from 'vitest';
import { composeGroups } from './composeGroups';
import type { ContainerDTO } from './api';

function container(composeProject: string): ContainerDTO {
  return { state: 'running', health: 'healthy', image: 'demo:latest', icon: '', compose_project: composeProject, metrics: {} };
}

describe('composeGroups', () => {
  it('groups containers sharing a compose project, sorted by member name', () => {
    const containers: Record<string, ContainerDTO> = {
      'gridmind-worker': container('gridmind-cloud'),
      'gridmind-api': container('gridmind-cloud'),
      jellyfin: container(''),
    };

    expect(composeGroups(containers)).toEqual([
      { project: 'gridmind-cloud', names: ['gridmind-api', 'gridmind-worker'] },
    ]);
  });

  it('excludes a project with only one known member', () => {
    const containers: Record<string, ContainerDTO> = {
      solo: container('solo-project'),
      jellyfin: container(''),
    };

    expect(composeGroups(containers)).toEqual([]);
  });

  it('excludes containers with no compose project at all', () => {
    const containers: Record<string, ContainerDTO> = {
      jellyfin: container(''),
      plex: container(''),
    };

    expect(composeGroups(containers)).toEqual([]);
  });

  it('sorts multiple groups by project name', () => {
    const containers: Record<string, ContainerDTO> = {
      z1: container('zeta'),
      z2: container('zeta'),
      a1: container('alpha'),
      a2: container('alpha'),
    };

    expect(composeGroups(containers).map((g) => g.project)).toEqual(['alpha', 'zeta']);
  });

  it('returns an empty array for no containers', () => {
    expect(composeGroups({})).toEqual([]);
  });
});

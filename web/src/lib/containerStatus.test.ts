import { describe, expect, it } from 'vitest';
import {
  containerHealthStatus,
  containerRunState,
  partitionContainerNames,
  unhealthyContainerNames,
} from './containerStatus';

describe('containerHealthStatus', () => {
  it('reports a running container with unhealthy health as critical', () => {
    expect(containerHealthStatus('running', 'unhealthy')).toBe('critical');
  });

  it('reports an exited container as serious even with a stale "unhealthy" health string', () => {
    // Docker never clears health on stop -- an exited container that was
    // unhealthy while it ran keeps reporting health=unhealthy forever.
    // That must NOT read as critical (Scott: stopped containers showing
    // up red as "needs a look").
    expect(containerHealthStatus('exited', 'unhealthy')).toBe('serious');
    expect(containerHealthStatus('dead', 'unhealthy')).toBe('serious');
  });

  it('reports a running container with no/healthy healthcheck as good', () => {
    expect(containerHealthStatus('running', '')).toBe('good');
    expect(containerHealthStatus('running', 'healthy')).toBe('good');
  });

  it('reports a running container still starting its healthcheck as warning', () => {
    expect(containerHealthStatus('running', 'starting')).toBe('warning');
  });

  it('reports exited/dead as serious', () => {
    expect(containerHealthStatus('exited', '')).toBe('serious');
    expect(containerHealthStatus('dead', '')).toBe('serious');
  });

  it('falls back to warning for any other transitional state', () => {
    expect(containerHealthStatus('created', '')).toBe('warning');
    expect(containerHealthStatus('restarting', '')).toBe('warning');
    expect(containerHealthStatus('paused', '')).toBe('warning');
  });
});

describe('containerRunState', () => {
  it('reads running and created as their own states', () => {
    expect(containerRunState('running')).toBe('running');
    expect(containerRunState('created')).toBe('created');
  });

  it('buckets everything else -- exited, dead, paused, restarting, unrecognized -- as stopped', () => {
    for (const state of ['exited', 'dead', 'paused', 'restarting', 'removing', '']) {
      expect(containerRunState(state)).toBe('stopped');
    }
  });
});

describe('partitionContainerNames', () => {
  const containers = {
    alpha: { state: 'running' },
    beta: { state: 'exited' },
    gamma: { state: 'created' },
    delta: { state: 'created' },
  };

  it('splits names into running/stopped/created by their own state', () => {
    const p = partitionContainerNames(['alpha', 'beta', 'gamma', 'delta'], containers);
    expect(p.running).toEqual(['alpha']);
    expect(p.stopped).toEqual(['beta']);
    expect(p.created).toEqual(['gamma', 'delta']);
  });

  it('preserves the input order within each bucket', () => {
    const p = partitionContainerNames(['delta', 'alpha', 'gamma'], containers);
    expect(p.created).toEqual(['delta', 'gamma']);
  });

  it('treats a name missing from the containers map as stopped rather than throwing', () => {
    const p = partitionContainerNames(['ghost'], containers);
    expect(p.stopped).toEqual(['ghost']);
  });
});

describe('unhealthyContainerNames', () => {
  it('includes a running container reporting unhealthy', () => {
    const names = unhealthyContainerNames({ sonarr: { state: 'running', health: 'unhealthy' } });
    expect(names).toEqual(['sonarr']);
  });

  it('excludes an exited container even with a stale "unhealthy" health string', () => {
    const names = unhealthyContainerNames({
      sonarr: { state: 'running', health: 'unhealthy' },
      radarr: { state: 'exited', health: 'unhealthy' },
    });
    expect(names).toEqual(['sonarr']);
  });

  it('excludes a running container with healthy/no healthcheck', () => {
    const names = unhealthyContainerNames({ sonarr: { state: 'running', health: 'healthy' } });
    expect(names).toEqual([]);
  });

  it('sorts by name', () => {
    const names = unhealthyContainerNames({
      radarr: { state: 'running', health: 'unhealthy' },
      sonarr: { state: 'running', health: 'unhealthy' },
    });
    expect(names).toEqual(['radarr', 'sonarr']);
  });
});

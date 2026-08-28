import { describe, expect, it } from 'vitest';
import { containerHealthStatus, containerRunState, partitionContainerNames } from './containerStatus';

describe('containerHealthStatus', () => {
  it('reports unhealthy as critical regardless of state', () => {
    expect(containerHealthStatus('running', 'unhealthy')).toBe('critical');
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

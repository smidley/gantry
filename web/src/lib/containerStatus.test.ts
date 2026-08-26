import { describe, expect, it } from 'vitest';
import { containerHealthStatus } from './containerStatus';

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

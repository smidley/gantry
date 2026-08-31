import { describe, expect, it } from 'vitest';
import { parseHash } from './router';

describe('parseHash', () => {
  it('parses the bare/empty hash as overview', () => {
    expect(parseHash('')).toEqual({ name: 'overview', params: {} });
    expect(parseHash('#')).toEqual({ name: 'overview', params: {} });
    expect(parseHash('#/')).toEqual({ name: 'overview', params: {} });
  });

  it('parses every static route', () => {
    expect(parseHash('#/containers')).toEqual({ name: 'containers', params: {} });
    expect(parseHash('#/top')).toEqual({ name: 'top', params: {} });
    expect(parseHash('#/storage')).toEqual({ name: 'storage', params: {} });
    expect(parseHash('#/maintenance')).toEqual({ name: 'maintenance', params: {} });
    expect(parseHash('#/gpu')).toEqual({ name: 'gpu', params: {} });
    expect(parseHash('#/events')).toEqual({ name: 'events', params: {} });
    expect(parseHash('#/insights')).toEqual({ name: 'insights', params: {} });
    expect(parseHash('#/alerts')).toEqual({ name: 'alerts', params: {} });
    expect(parseHash('#/settings')).toEqual({ name: 'settings', params: {} });
  });

  it('parses a container-detail route with a name param', () => {
    expect(parseHash('#/containers/jellyfin')).toEqual({
      name: 'container-detail',
      params: { name: 'jellyfin' },
    });
  });

  it('parses a top route with a resource param, for the Overview switcher deep link', () => {
    expect(parseHash('#/top/mem')).toEqual({ name: 'top', params: { resource: 'mem' } });
  });

  it('parses the insights map deep link into a mode param, same route name', () => {
    expect(parseHash('#/insights/map')).toEqual({ name: 'insights', params: { mode: 'map' } });
  });

  it('parses a compare route, capturing the raw comma-joined names segment', () => {
    expect(parseHash('#/compare/jellyfin,plex')).toEqual({
      name: 'compare',
      params: { names: 'jellyfin,plex' },
    });
  });

  it('parses a compare route with a single name', () => {
    expect(parseHash('#/compare/jellyfin')).toEqual({ name: 'compare', params: { names: 'jellyfin' } });
  });

  it('parses a bare compare route (no names at all) rather than falling through to not-found', () => {
    expect(parseHash('#/compare')).toEqual({ name: 'compare', params: {} });
    expect(parseHash('#/compare/')).toEqual({ name: 'compare', params: {} });
  });

  it('URL-decodes a name param', () => {
    expect(parseHash('#/containers/my%20app')).toEqual({
      name: 'container-detail',
      params: { name: 'my app' },
    });
  });

  it('tolerates a trailing slash', () => {
    expect(parseHash('#/containers/')).toEqual({ name: 'containers', params: {} });
  });

  it('falls back to not-found for unknown or over-deep paths', () => {
    expect(parseHash('#/nope')).toEqual({ name: 'not-found', params: {} });
    expect(parseHash('#/containers/x/y')).toEqual({ name: 'not-found', params: {} });
  });
});

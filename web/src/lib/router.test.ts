import { describe, expect, it } from 'vitest';
import { navActiveName, parseHash } from './router';

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

  it('parses hash query parameters for filtered views', () => {
    expect(parseHash('#/containers?state=active')).toEqual({
      name: 'containers',
      params: { state: 'active' },
    });
  });

  it('parses a top route with a resource param, for the Overview switcher deep link', () => {
    expect(parseHash('#/top/mem')).toEqual({ name: 'top', params: { resource: 'mem' } });
  });

  it('parses the insights map deep link into a mode param, same route name', () => {
    expect(parseHash('#/insights/map')).toEqual({ name: 'insights', params: { mode: 'map' } });
  });

  it('parses an insight evidence page, its numeric id captured', () => {
    expect(parseHash('#/insights/123')).toEqual({ name: 'insight-detail', params: { id: '123' } });
  });

  it('routes a non-numeric insight id to the detail view, which renders its own not-found copy', () => {
    // Deliberately NOT a router-level rejection -- InsightDetail's own
    // parseInsightId decides, so a garbage id and a real-looking-but-
    // unknown one land on the same back-linked page (router.ts' own doc
    // on the insight-detail entry).
    expect(parseHash('#/insights/abc')).toEqual({ name: 'insight-detail', params: { id: 'abc' } });
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

describe('navActiveName', () => {
  it('is the route itself for every top-level page', () => {
    expect(navActiveName('insights')).toBe('insights');
    expect(navActiveName('overview')).toBe('overview');
  });

  it('lights the parent nav item for a detail page, which has no nav entry of its own', () => {
    expect(navActiveName('insight-detail')).toBe('insights');
    expect(navActiveName('container-detail')).toBe('containers');
  });
});

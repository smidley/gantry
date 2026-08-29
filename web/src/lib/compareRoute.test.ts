import { describe, expect, it } from 'vitest';
import { buildCompareHash, knownCompareNames, parseCompareNames } from './compareRoute';
import type { ContainerDTO } from './api';

describe('buildCompareHash', () => {
  it('sorts names ascending regardless of input order', () => {
    expect(buildCompareHash(['plex', 'jellyfin', 'radarr'])).toBe('#/compare/jellyfin,plex,radarr');
    expect(buildCompareHash(['radarr', 'jellyfin', 'plex'])).toBe('#/compare/jellyfin,plex,radarr');
  });

  it('dedupes repeated names', () => {
    expect(buildCompareHash(['jellyfin', 'plex', 'jellyfin'])).toBe('#/compare/jellyfin,plex');
  });

  it('URL-encodes each name individually', () => {
    expect(buildCompareHash(['my app', 'plex'])).toBe('#/compare/my%20app,plex');
  });

  it('produces the same hash for the same set regardless of selection order (order-stable)', () => {
    const a = buildCompareHash(['gridmind-db', 'gridmind-api', 'gridmind-worker']);
    const b = buildCompareHash(['gridmind-worker', 'gridmind-db', 'gridmind-api']);
    expect(a).toBe(b);
  });

  it('handles an empty list', () => {
    expect(buildCompareHash([])).toBe('#/compare/');
  });
});

describe('parseCompareNames', () => {
  it('splits a comma-joined param into names', () => {
    expect(parseCompareNames('jellyfin,plex,radarr')).toEqual(['jellyfin', 'plex', 'radarr']);
  });

  it('trims whitespace and drops empty entries from stray/doubled commas', () => {
    expect(parseCompareNames(' jellyfin , ,plex,')).toEqual(['jellyfin', 'plex']);
  });

  it('dedupes while preserving first-seen order', () => {
    expect(parseCompareNames('plex,jellyfin,plex')).toEqual(['plex', 'jellyfin']);
  });

  it('returns an empty array for undefined or empty input', () => {
    expect(parseCompareNames(undefined)).toEqual([]);
    expect(parseCompareNames('')).toEqual([]);
  });

  it('returns a single-element array for one name', () => {
    expect(parseCompareNames('jellyfin')).toEqual(['jellyfin']);
  });
});

describe('knownCompareNames', () => {
  const empty: ContainerDTO = { state: '', health: '', image: '', icon: '', compose_project: '', metrics: {} };

  it('keeps only names present in the containers map', () => {
    const containers: Record<string, ContainerDTO> = { jellyfin: empty, plex: empty };
    expect(knownCompareNames(['jellyfin', 'plex', 'gone'], containers)).toEqual(['jellyfin', 'plex']);
  });

  it('preserves input order', () => {
    const containers: Record<string, ContainerDTO> = { jellyfin: empty, plex: empty };
    expect(knownCompareNames(['plex', 'jellyfin'], containers)).toEqual(['plex', 'jellyfin']);
  });
});

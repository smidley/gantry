import { describe, expect, it } from 'vitest';
import { containerColor } from './containerColor';

describe('containerColor', () => {
  it('is deterministic: the same name always resolves to the same slot', () => {
    for (const name of ['jellyfin', 'sonarr', 'gridmind-cloud-worker-1', '']) {
      expect(containerColor(name)).toBe(containerColor(name));
    }
  });

  it('always returns one of the 10 categorical slots', () => {
    const names = [
      'jellyfin',
      'plex',
      'radarr',
      'sonarr',
      'prowlarr',
      'bazarr',
      'overseerr',
      'tautulli',
      'gridmind-api',
      'gridmind-db',
    ];
    for (const name of names) {
      expect(containerColor(name)).toMatch(/^--series-(?:[1-9]|10)$/);
    }
  });

  it('spreads a realistic fleet across more than one slot, not collapsing every name onto the same hue', () => {
    const names = [
      'jellyfin',
      'plex',
      'radarr',
      'sonarr',
      'prowlarr',
      'bazarr',
      'overseerr',
      'tautulli',
      'scrypted',
      'homeassistant',
      'pihole',
      'nginx-proxy-manager',
    ];
    const distinct = new Set(names.map(containerColor));
    expect(distinct.size).toBeGreaterThan(1);
  });

  it('is unaffected by a name colliding with -- or differing only slightly from -- another', () => {
    // Two near-identical names (a common real shape: "-1"/"-2" suffixed
    // compose replicas) must each still be internally stable even if
    // they happen to land on the same slot as each other -- collisions
    // are accepted (see this module's own doc), never disambiguated.
    const a1 = containerColor('gridmind-cloud-worker-1');
    const a2 = containerColor('gridmind-cloud-worker-1');
    const b1 = containerColor('gridmind-cloud-worker-2');
    expect(a1).toBe(a2);
    expect(a1).toMatch(/^--series-(?:[1-9]|10)$/);
    expect(b1).toMatch(/^--series-(?:[1-9]|10)$/);
  });

  it('does not depend on a caller-supplied position -- the whole point of replacing seriesColorVar(i)', () => {
    // containerColor takes no index at all, so a container's rank
    // shifting (rankStability's own rolling-average re-sort) or a
    // fixed-size slot pool being recycled for a different entity can
    // never repaint an already-tracked line's color out from under it.
    expect(containerColor.length).toBe(1);
  });
});

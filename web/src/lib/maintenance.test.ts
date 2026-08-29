import { describe, expect, it } from 'vitest';
import type { ContainerMaintenanceInfo, ImageInfo } from './api';
import {
  containerAge,
  containersMatchingPruneMode,
  hasKeepWarning,
  imagesMatchingPruneMode,
  isHttpUrl,
  managedBadge,
  removableImages,
  sortContainersByAge,
  sortImagesBySize,
  sumImageBytes,
} from './maintenance';

function image(overrides: Partial<ImageInfo> = {}): ImageInfo {
  return {
    id: 'abc123',
    full_id: `sha256:${'a'.repeat(64)}`,
    repo_tags: ['<none>'],
    size_bytes: 1000,
    created: 1_735_000_000,
    state: 'unused',
    ...overrides,
  };
}

function container(overrides: Partial<ContainerMaintenanceInfo> = {}): ContainerMaintenanceInfo {
  return {
    id: 'abc123',
    full_id: 'a'.repeat(64),
    name: 'test',
    image: 'test:latest',
    state: 'exited',
    created: 1_735_000_000,
    ...overrides,
  };
}

describe('removableImages', () => {
  it('keeps unused and dangling, drops in-use', () => {
    const images = [image({ id: '1', state: 'in-use' }), image({ id: '2', state: 'unused' }), image({ id: '3', state: 'dangling' })];
    expect(removableImages(images).map((i) => i.id)).toEqual(['2', '3']);
  });

  it('returns empty for an all-in-use fleet', () => {
    expect(removableImages([image({ state: 'in-use' })])).toEqual([]);
  });
});

describe('sumImageBytes', () => {
  it('sums size_bytes across images, 0 for an empty list', () => {
    expect(sumImageBytes([image({ size_bytes: 100 }), image({ size_bytes: 250 })])).toBe(350);
    expect(sumImageBytes([])).toBe(0);
  });
});

describe('imagesMatchingPruneMode', () => {
  it('matches state exactly, one mode at a time', () => {
    const images = [image({ id: '1', state: 'dangling' }), image({ id: '2', state: 'unused' }), image({ id: '3', state: 'dangling' })];
    expect(imagesMatchingPruneMode(images, 'dangling').map((i) => i.id)).toEqual(['1', '3']);
    expect(imagesMatchingPruneMode(images, 'unused').map((i) => i.id)).toEqual(['2']);
  });
});

describe('sortImagesBySize', () => {
  it('sorts largest first without mutating the input', () => {
    const small = image({ id: 'small', size_bytes: 10 });
    const big = image({ id: 'big', size_bytes: 1000 });
    const mid = image({ id: 'mid', size_bytes: 100 });
    const input = [small, big, mid];
    const sorted = sortImagesBySize(input);
    expect(sorted.map((i) => i.id)).toEqual(['big', 'mid', 'small']);
    expect(input.map((i) => i.id)).toEqual(['small', 'big', 'mid']); // original order untouched
  });
});

describe('managedBadge', () => {
  it('reads dockerman as "Unraid template"', () => {
    expect(managedBadge('dockerman')).toBe('Unraid template');
  });

  it('names a compose project directly, distinct from the dockerman label', () => {
    expect(managedBadge('media-stack')).toBe('Compose: media-stack');
  });

  it('returns null for empty/undefined -- no badge at all', () => {
    expect(managedBadge('')).toBeNull();
    expect(managedBadge(undefined)).toBeNull();
  });
});

describe('hasKeepWarning', () => {
  it('true when managed is set, restart_policy empty', () => {
    expect(hasKeepWarning(container({ managed: 'dockerman' }))).toBe(true);
  });

  it('true when restart_policy is set, managed empty', () => {
    expect(hasKeepWarning(container({ restart_policy: 'always' }))).toBe(true);
  });

  it('true when both are set', () => {
    expect(hasKeepWarning(container({ managed: 'dockerman', restart_policy: 'unless-stopped' }))).toBe(true);
  });

  it('false when neither is set', () => {
    expect(hasKeepWarning(container())).toBe(false);
  });
});

describe('containerAge', () => {
  it('prefers finished_at when present', () => {
    expect(containerAge(container({ created: 100, finished_at: 200 }))).toBe(200);
  });

  it('falls back to created when finished_at is absent (created-state containers)', () => {
    expect(containerAge(container({ created: 100, finished_at: undefined }))).toBe(100);
  });
});

describe('containersMatchingPruneMode', () => {
  const exited1 = container({ id: 'e1', state: 'exited' });
  const exited2 = container({ id: 'e2', state: 'exited' });
  const created1 = container({ id: 'c1', state: 'created' });
  const dead1 = container({ id: 'd1', state: 'dead' });
  const all = [exited1, exited2, created1, dead1];

  it('exited matches only exited', () => {
    expect(containersMatchingPruneMode(all, 'exited').map((c) => c.id)).toEqual(['e1', 'e2']);
  });

  it('created matches only created', () => {
    expect(containersMatchingPruneMode(all, 'created').map((c) => c.id)).toEqual(['c1']);
  });

  it('all-stopped matches exited and created, but never dead', () => {
    expect(containersMatchingPruneMode(all, 'all-stopped').map((c) => c.id)).toEqual(['e1', 'e2', 'c1']);
  });
});

describe('sortContainersByAge', () => {
  it('sorts oldest (smallest timestamp) first without mutating the input', () => {
    const old = container({ id: 'old', created: 100 });
    const recent = container({ id: 'recent', created: 300 });
    const mid = container({ id: 'mid', created: 200 });
    const input = [recent, old, mid];
    const sorted = sortContainersByAge(input);
    expect(sorted.map((c) => c.id)).toEqual(['old', 'mid', 'recent']);
    expect(input.map((c) => c.id)).toEqual(['recent', 'old', 'mid']);
  });

  it('uses finished_at over created when both are present', () => {
    const finishedEarly = container({ id: 'a', created: 500, finished_at: 100 });
    const finishedLate = container({ id: 'b', created: 100, finished_at: 500 });
    expect(sortContainersByAge([finishedLate, finishedEarly]).map((c) => c.id)).toEqual(['a', 'b']);
  });
});

describe('isHttpUrl', () => {
  it('accepts http and https', () => {
    expect(isHttpUrl('http://example.com/changelog')).toBe(true);
    expect(isHttpUrl('https://example.com/changelog')).toBe(true);
  });

  it('rejects other schemes', () => {
    expect(isHttpUrl('javascript:alert(1)')).toBe(false);
    expect(isHttpUrl('data:text/html,hi')).toBe(false);
    expect(isHttpUrl('ftp://example.com')).toBe(false);
  });

  it('rejects undefined/empty/malformed input', () => {
    expect(isHttpUrl(undefined)).toBe(false);
    expect(isHttpUrl('')).toBe(false);
    expect(isHttpUrl('not a url')).toBe(false);
  });
});

import { describe, expect, it } from 'vitest';
import { removeGroup, renameGroup, upsertGroup } from './groups';
import type { Group } from './api';

describe('upsertGroup', () => {
  it('appends a new group when no existing group shares its name', () => {
    const groups: Group[] = [{ name: 'media', members: ['jellyfin'] }];

    expect(upsertGroup(groups, 'backups', ['duplicati'])).toEqual([
      { name: 'media', members: ['jellyfin'] },
      { name: 'backups', members: ['duplicati'] },
    ]);
  });

  it('replaces the existing group\'s members in place when the name already exists', () => {
    const groups: Group[] = [
      { name: 'media', members: ['jellyfin'] },
      { name: 'backups', members: ['duplicati'] },
    ];

    expect(upsertGroup(groups, 'media', ['jellyfin', 'sonarr', 'radarr'])).toEqual([
      { name: 'media', members: ['jellyfin', 'sonarr', 'radarr'] },
      { name: 'backups', members: ['duplicati'] },
    ]);
  });

  it('trims the submitted name before comparing or storing it', () => {
    const groups: Group[] = [{ name: 'media', members: ['jellyfin'] }];

    expect(upsertGroup(groups, '  media  ', ['sonarr'])).toEqual([{ name: 'media', members: ['sonarr'] }]);
  });

  it('never mutates the input array or its member arrays', () => {
    const original: Group[] = [{ name: 'media', members: ['jellyfin'] }];
    const originalMembers = original[0].members;

    const result = upsertGroup(original, 'media', ['sonarr']);

    expect(original).toEqual([{ name: 'media', members: ['jellyfin'] }]);
    expect(original[0].members).toBe(originalMembers);
    expect(result).not.toBe(original);
  });

  it('starts from an empty list', () => {
    expect(upsertGroup([], 'media', ['jellyfin'])).toEqual([{ name: 'media', members: ['jellyfin'] }]);
  });
});

describe('renameGroup', () => {
  it('renames the matching group, leaving its members untouched', () => {
    const groups: Group[] = [
      { name: 'media', members: ['jellyfin', 'sonarr'] },
      { name: 'backups', members: ['duplicati'] },
    ];

    expect(renameGroup(groups, 'media', 'entertainment')).toEqual([
      { name: 'entertainment', members: ['jellyfin', 'sonarr'] },
      { name: 'backups', members: ['duplicati'] },
    ]);
  });

  it('trims the new name', () => {
    const groups: Group[] = [{ name: 'media', members: ['jellyfin'] }];

    expect(renameGroup(groups, 'media', '  entertainment  ')).toEqual([{ name: 'entertainment', members: ['jellyfin'] }]);
  });

  it('is a no-op copy when oldName is not found', () => {
    const groups: Group[] = [{ name: 'media', members: ['jellyfin'] }];

    const result = renameGroup(groups, 'ghost', 'entertainment');

    expect(result).toEqual(groups);
    expect(result).not.toBe(groups);
  });
});

describe('removeGroup', () => {
  it('drops the matching group and leaves the rest untouched', () => {
    const groups: Group[] = [
      { name: 'media', members: ['jellyfin'] },
      { name: 'backups', members: ['duplicati'] },
    ];

    expect(removeGroup(groups, 'media')).toEqual([{ name: 'backups', members: ['duplicati'] }]);
  });

  it('is a no-op when the name is not found', () => {
    const groups: Group[] = [{ name: 'media', members: ['jellyfin'] }];

    expect(removeGroup(groups, 'ghost')).toEqual(groups);
  });

  it('returns an empty array when removing the only group', () => {
    expect(removeGroup([{ name: 'media', members: ['jellyfin'] }], 'media')).toEqual([]);
  });
});

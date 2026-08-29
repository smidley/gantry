// Pure list-transform helpers for custom container groups -- the
// user-named, user-picked counterpart to composeGroups.ts's own
// docker-compose-derived groups. groups.svelte.ts wraps these around
// the reactive store + /api/groups round-trip; kept here, pure and
// testable, the same split livering.ts/motion.ts each have against
// their own .svelte.ts sibling.
import type { Group } from './api';

// upsertGroup returns a NEW groups array with `name`'s own entry
// replaced (member set updated in place, same position) if a group by
// that exact name already exists, or appended as a new group
// otherwise -- "Save as group" semantics: saving under an existing
// name overwrites its membership rather than erroring as a duplicate.
export function upsertGroup(groups: Group[], name: string, members: string[]): Group[] {
  const trimmed = name.trim();
  const next: Group = { name: trimmed, members: [...members] };
  const idx = groups.findIndex((g) => g.name === trimmed);
  if (idx === -1) return [...groups, next];
  const copy = groups.slice();
  copy[idx] = next;
  return copy;
}

// renameGroup returns a NEW groups array with `oldName`'s own entry
// renamed to `newName`, members untouched. A plain no-op copy (still a
// new array reference) when oldName isn't found.
export function renameGroup(groups: Group[], oldName: string, newName: string): Group[] {
  const trimmed = newName.trim();
  return groups.map((g) => (g.name === oldName ? { ...g, name: trimmed } : g));
}

// removeGroup returns a NEW groups array with `name`'s own entry
// dropped. Deleting a group never touches the containers it named --
// this is a pure list edit, nothing here (or anywhere upstream of the
// PUT it feeds) ever reaches a container.
export function removeGroup(groups: Group[], name: string): Group[] {
  return groups.filter((g) => g.name !== name);
}

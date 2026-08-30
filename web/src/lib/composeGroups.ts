// Pure compose-project grouping for the Containers view's own Groups
// chip row: which docker-compose stacks does the current fleet actually
// have MORE THAN ONE known member of, right now.
import type { ContainerDTO } from './api';

export interface ComposeGroup {
  project: string;
  names: string[]; // sorted ascending -- matches compareRoute.ts's buildCompareHash canonical order, so a chip's href is byte-identical to selecting those same members by hand and clicking "Compare N ->"
}

// composeGroups buckets every container by its own compose_project label
// ("" -- not part of any compose stack -- is excluded entirely), keeping
// only projects with 2+ currently-known members: the Groups chip row only
// exists to surface a real "these work together" team, and a project
// with exactly one member left has nothing to compare. Sorted by project
// name ascending for a stable chip order across renders.
export function composeGroups(containers: Record<string, ContainerDTO>): ComposeGroup[] {
  const byProject = new Map<string, string[]>();
  for (const [name, c] of Object.entries(containers)) {
    const project = c.compose_project;
    if (!project) continue;
    const names = byProject.get(project);
    if (names) {
      names.push(name);
    } else {
      byProject.set(project, [name]);
    }
  }

  const groups: ComposeGroup[] = [];
  for (const [project, names] of byProject) {
    if (names.length < 2) continue;
    groups.push({ project, names: [...names].sort((a, b) => a.localeCompare(b)) });
  }
  groups.sort((a, b) => a.project.localeCompare(b.project));
  return groups;
}

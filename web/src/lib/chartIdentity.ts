interface SeriesIdentity { label: string; colorVar: string; dash?: number[] }

// Keep name colors stable, and disambiguate collisions in the visible chart.
// Sorting within each color group makes dash assignment independent of rank.
export function distinctDashes(series: SeriesIdentity[]): Array<number[] | undefined> {
  const groups = new Map<string, string[]>();
  for (const item of series) {
    const names = groups.get(item.colorVar) ?? [];
    names.push(item.label);
    groups.set(item.colorVar, names);
  }
  for (const names of groups.values()) names.sort();
  return series.map((item) => {
    if (item.dash) return item.dash;
    const group = groups.get(item.colorVar)!;
    const index = group.indexOf(item.label);
    return index <= 0 ? undefined : [index * 3 + 3, 3, 2, 3];
  });
}

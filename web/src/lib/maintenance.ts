// Pure domain logic for the Maintenance view (views/Maintenance.svelte) --
// selection/classification/sorting kept out of the component so it's
// unit-testable without a DOM (this project's vitest config has no jsdom
// -- see vitest.config.ts's own doc).
//
// containersMatchingPruneMode deliberately has NO olderThanHours
// parameter, unlike the server's own selectPruneTargets: a prune button's
// confirm dialog wants to preview exactly what a click will remove, but
// the server's age cutoff is measured against ITS OWN "now" -- real
// wall-clock time.Now() in real mode, but a fixed epoch in fake mode
// (fakeContainersBaseCreated, deliberately NOT time.Now() -- see
// internal/fake/containers_maintenance.go's own doc: every fake
// container's Created/FinishedAt is authored as a fixed historical
// offset, so an age filter has to measure against that same fixed point
// to mean anything there). The frontend has no way to know which "now"
// applies -- there's no wire field for it -- so the STATE-only match
// below is exact for the common case (no age filter, the default) and
// Maintenance.svelte renders an explicit "confirmed after running"
// caveat for the one case (an active age filter) it can't preview
// precisely.
import type { ContainerMaintenanceInfo, ImageInfo } from './api';

// removableImages: the Images card's own table -- unused+dangling only,
// never in-use (an in-use image can't be removed without --force, which
// Gantry never sets on either mode).
export function removableImages(images: ImageInfo[]): ImageInfo[] {
  return images.filter((im) => im.state === 'unused' || im.state === 'dangling');
}

export function sumImageBytes(images: ImageInfo[]): number {
  return images.reduce((sum, im) => sum + im.size_bytes, 0);
}

// imagesMatchingPruneMode mirrors PruneImages' own server-side selection
// exactly (state === mode) -- previewing a prune button's target set is
// always exact for images: unlike containers, there's no age filter to
// diverge on.
export function imagesMatchingPruneMode(images: ImageInfo[], mode: 'dangling' | 'unused'): ImageInfo[] {
  return images.filter((im) => im.state === mode);
}

// sortImagesBySize: largest reclaim value first -- the most actionable
// candidates lead the table rather than whatever order the backend
// happened to report.
export function sortImagesBySize(images: ImageInfo[]): ImageInfo[] {
  return [...images].sort((a, b) => b.size_bytes - a.size_bytes);
}

// managedBadge: the KEEP-warning label for a container's own `managed`
// hint. "dockerman" (Unraid's own template manager) reads as "Unraid
// template"; any other non-empty value is a docker-compose project's own
// name -- named directly rather than reusing the dockerman label, since
// they're different managers with different implications for someone
// deciding whether to remove the container by hand.
export function managedBadge(managed: string | undefined): string | null {
  if (!managed) return null;
  if (managed === 'dockerman') return 'Unraid template';
  return `Compose: ${managed}`;
}

// hasKeepWarning: true for a row Maintenance.svelte's own selection UI
// treats as worth a second look before checking -- either KEEP-warning
// hint, managed or restart_policy.
export function hasKeepWarning(ct: ContainerMaintenanceInfo): boolean {
  return !!ct.managed || !!ct.restart_policy;
}

// containerAge: the timestamp a container's own age column reads off --
// finished_at when the backend's inspect enrichment landed one (exited
// only), created otherwise (a created-but-never-started container, or an
// exited one enrichment never reached).
export function containerAge(ct: ContainerMaintenanceInfo): number {
  return ct.finished_at ?? ct.created;
}

// containersMatchingPruneMode mirrors docker.selectPruneTargets' own
// STATE selection (minus the age filter -- see this module's own top
// doc). all-stopped excludes dead, same as the server.
export function containersMatchingPruneMode(
  containers: ContainerMaintenanceInfo[],
  mode: 'exited' | 'created' | 'all-stopped',
): ContainerMaintenanceInfo[] {
  return containers.filter((ct) => ct.state === mode || (mode === 'all-stopped' && (ct.state === 'exited' || ct.state === 'created')));
}

// sortContainersByAge: oldest first -- the longest-neglected exited/
// created containers are the most obviously safe, most valuable rows to
// clean up, so they lead the table.
export function sortContainersByAge(containers: ContainerMaintenanceInfo[]): ContainerMaintenanceInfo[] {
  return [...containers].sort((a, b) => containerAge(a) - containerAge(b));
}

// isHttpUrl gates a container's own changelog_url before it's ever
// rendered as a clickable link: both fields are backend-derived (not
// user input), but changelog_url in particular is a URL a docker registry
// somewhere chose, not Gantry -- an http(s)-only allowlist is a cheap,
// worthwhile guard against a stray scheme (javascript:, data:, ...)
// ever becoming a real link, resolving nothing else.
export function isHttpUrl(url: string | undefined): boolean {
  if (!url) return false;
  try {
    return new URL(url).protocol === 'http:' || new URL(url).protocol === 'https:';
  } catch {
    return false;
  }
}

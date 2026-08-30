// containerHealthStatus maps a container's state+health into one of the
// four HealthDot statuses -- shared by the Containers table, Container
// Detail's header, and anywhere else a single dot needs to summarize a
// container's condition. health only means anything for a container
// docker is actively running a HEALTHCHECK against, so it takes priority
// over state when it says something definite (healthy/unhealthy);
// otherwise state alone decides.
export type HealthStatus = 'good' | 'warning' | 'serious' | 'critical';

export function containerHealthStatus(state: string, health: string): HealthStatus {
  // Docker never clears health on stop, so an exited container can still
  // carry a stale "unhealthy" from when it was running -- state must
  // gate health, not the other way around, or a stopped container reads
  // as critical.
  if (state === 'running') return health === 'unhealthy' ? 'critical' : health === 'starting' ? 'warning' : 'good';
  if (state === 'exited' || state === 'dead') return 'serious';
  // created, restarting, paused, or any other transitional/unrecognized
  // state -- worth a second look, but not yet a confirmed problem.
  return 'warning';
}

// ContainerRunState is the coarser, three-way split Overview's fleet
// headline/strip and the Containers view's collapsed section both need:
// "created" (docker's own state for a container that's been provisioned
// but never started -- e.g. an ephemeral CI-runner spawn) is its own
// bucket, distinct from "stopped" (was running, isn't now), since a
// never-started container has nothing to monitor and floods both
// surfaces during a churny burst (Scott's own report: 17 created
// GitHub-runner containers showing up as "stopped"). Anything that isn't
// "running" or "created" -- exited, dead, paused, restarting, or an
// unrecognized future state -- reads as "stopped".
export type ContainerRunState = 'running' | 'created' | 'stopped';

export function containerRunState(state: string): ContainerRunState {
  if (state === 'running') return 'running';
  if (state === 'created') return 'created';
  return 'stopped';
}

// unhealthyContainerNames lists every RUNNING container reporting
// health=unhealthy, sorted by name -- Overview's own attention module
// feeds this straight into overviewStatus.ts's unhealthyNames input. A
// stopped container is excluded even when its health string still says
// "unhealthy" (docker doesn't clear it on exit, see containerHealthStatus
// above) -- it's already covered by the aggregated "stopped" anomaly,
// not a second, red, per-container one.
export function unhealthyContainerNames(containers: Record<string, { state: string; health: string }>): string[] {
  return Object.entries(containers)
    .filter(([, c]) => c.state === 'running' && c.health === 'unhealthy')
    .map(([name]) => name)
    .sort();
}

export interface ContainerNamePartition {
  running: string[];
  stopped: string[];
  created: string[];
}

// partitionContainerNames buckets `names` by containerRunState, reading
// each one's state off `containers` -- same (names, containers-map) shape
// as containersSort.ts's sortContainerNames, so both views can feed it
// straight off live.frame.containers. A name absent from `containers`
// (defensive, shouldn't happen) reads as stopped rather than throwing.
export function partitionContainerNames(
  names: string[],
  containers: Record<string, { state: string }>,
): ContainerNamePartition {
  const out: ContainerNamePartition = { running: [], stopped: [], created: [] };
  for (const name of names) {
    out[containerRunState(containers[name]?.state ?? '')].push(name);
  }
  return out;
}

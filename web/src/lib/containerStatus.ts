// containerHealthStatus maps a container's state+health into one of the
// four HealthDot statuses -- shared by the Containers table, Container
// Detail's header, and anywhere else a single dot needs to summarize a
// container's condition. health only means anything for a container
// docker is actively running a HEALTHCHECK against, so it takes priority
// over state when it says something definite (healthy/unhealthy);
// otherwise state alone decides.
export type HealthStatus = 'good' | 'warning' | 'serious' | 'critical';

export function containerHealthStatus(state: string, health: string): HealthStatus {
  if (health === 'unhealthy') return 'critical';
  if (state === 'running') return health === 'starting' ? 'warning' : 'good';
  if (state === 'exited' || state === 'dead') return 'serious';
  // created, restarting, paused, or any other transitional/unrecognized
  // state -- worth a second look, but not yet a confirmed problem.
  return 'warning';
}

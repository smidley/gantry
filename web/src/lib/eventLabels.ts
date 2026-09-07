/** User-facing names; retain wire codes for filtering and diagnostics. */
export const EVENT_LABELS: Record<string, string> = {
  'container.start': 'Container started', 'container.die': 'Container stopped',
  'container.oom': 'Container ran out of memory', 'container.health': 'Container health changed',
  'container.removed': 'Container removed', 'image.removed': 'Image removed',
  'array.state': 'Array state changed', 'parity.start': 'Parity check started',
  'parity.finish': 'Parity check finished', 'disk.errors': 'Disk errors detected',
  'alert.fired': 'Alert triggered', 'alert.resolved': 'Alert cleared',
  'insight.detected': 'Incident detected', 'insight.resolved': 'Incident resolved',
};
export function eventLabel(kind: string): string { return EVENT_LABELS[kind] ?? kind; }

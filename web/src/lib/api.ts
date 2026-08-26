// Typed fetch helpers for Gantry's /api surface -- every helper returns
// already-decoded JSON (or throws), keeping components free of
// fetch/response/JSON.parse boilerplate.

// ContainerDTO/SnapshotDTO mirror internal/server/api_snapshot.go's
// SnapshotDTO exactly -- this IS the SSE frame shape too (see
// sse.svelte.ts), not just the /api/live/snapshot response.
export interface ContainerDTO {
  state: string;
  health: string;
  image: string;
  metrics: Record<string, number>;
}

export interface SnapshotDTO {
  ts: number;
  unraid_version: string;
  host: Record<string, number>;
  containers: Record<string, ContainerDTO>;
  disks: Record<string, Record<string, number>>;
  unraid: Record<string, Record<string, number>>; // entity ("array"|"docker") -> metric -> value
  gpu: Record<string, Record<string, number>>;
  sources: Record<string, string>;
}

export interface ContainerInfo {
  name: string;
  state: string;
  health: string;
  image: string;
}

// SeriesPoint is [ts, avg, max] -- an array, not an object, matching
// the server's payload-size-motivated wire shape (see
// internal/server/api_history.go's seriesResponseDTO).
export type SeriesPoint = [number, number, number];

export interface SeriesResult {
  metric: string;
  points: SeriesPoint[];
}

export interface TopRow {
  entity: string;
  value: number;
}

// GantryEvent mirrors store.Event's wire shape exactly: that Go struct
// carries no `json:"..."` tags, so its capitalized field names ARE the
// JSON keys.
export interface GantryEvent {
  ID: number;
  TS: number;
  Kind: string;
  Entity: string;
  Severity: string;
  Detail: string;
}

export interface RetentionSettings {
  r1_hours: number;
  r2_days: number;
  r3_days: number;
  size_cap_mb: number;
}

export interface SettingsResponse {
  retention: RetentionSettings;
  env_overridden: string[];
}

export type TopResource = 'cpu' | 'mem' | 'net' | 'io' | 'gpu';
export type TopWindow = 'now' | '1h' | '24h' | '7d';
export type TopAgg = 'avg' | 'peak';

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`GET ${url}: ${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

export function fetchSeries(params: {
  kind: string;
  entity: string;
  metrics: string[];
  from?: number;
  to?: number;
}): Promise<SeriesResult[]> {
  const q = new URLSearchParams({
    kind: params.kind,
    entity: params.entity,
    metrics: params.metrics.join(','),
  });
  if (params.from !== undefined) q.set('from', String(params.from));
  if (params.to !== undefined) q.set('to', String(params.to));
  return getJSON<SeriesResult[]>(`/api/series?${q.toString()}`);
}

export function fetchTop(params: {
  resource: TopResource;
  window: TopWindow;
  agg?: TopAgg;
  limit?: number;
}): Promise<TopRow[]> {
  const q = new URLSearchParams({ resource: params.resource, window: params.window });
  if (params.agg) q.set('agg', params.agg);
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  return getJSON<TopRow[]>(`/api/top?${q.toString()}`);
}

export function fetchEvents(
  params: {
    kinds?: string[];
    entity?: string;
    from?: number;
    to?: number;
    limit?: number;
  } = {},
): Promise<GantryEvent[]> {
  const q = new URLSearchParams();
  if (params.kinds?.length) q.set('kinds', params.kinds.join(','));
  if (params.entity) q.set('entity', params.entity);
  if (params.from !== undefined) q.set('from', String(params.from));
  if (params.to !== undefined) q.set('to', String(params.to));
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  const qs = q.toString();
  return getJSON<GantryEvent[]>(`/api/events${qs ? `?${qs}` : ''}`);
}

export function fetchContainers(): Promise<ContainerInfo[]> {
  return getJSON<ContainerInfo[]>('/api/containers');
}

export function fetchSettings(): Promise<SettingsResponse> {
  return getJSON<SettingsResponse>('/api/settings');
}

// putSettings surfaces the server's structured error body (400's
// {error, fields} or 409's {error, env_overridden}) as the thrown
// Error's message when the request fails, rather than a generic
// status-code message -- the settings editor needs that detail to
// point at the right field.
export async function putSettings(retention: RetentionSettings): Promise<SettingsResponse> {
  const res = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ retention }),
  });
  const body = (await res.json()) as SettingsResponse & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `PUT /api/settings: ${res.status} ${res.statusText}`);
  }
  return body;
}

// streamLogs opens /api/containers/{name}/logs and yields decoded text
// chunks as they arrive. follow=1 streams indefinitely; follow=0 (the
// default) yields the tail then ends. Callers drive this with a
// `for await` loop; breaking out of that loop triggers the generator's
// `finally`, which cancels the underlying reader so the connection
// doesn't linger.
export async function* streamLogs(
  name: string,
  opts: { follow?: boolean; tail?: number } = {},
): AsyncGenerator<string> {
  const q = new URLSearchParams();
  if (opts.follow) q.set('follow', '1');
  if (opts.tail !== undefined) q.set('tail', String(opts.tail));
  const qs = q.toString();

  const res = await fetch(`/api/containers/${encodeURIComponent(name)}/logs${qs ? `?${qs}` : ''}`);
  if (!res.ok || !res.body) {
    throw new Error(`logs ${name}: ${res.status} ${res.statusText}`);
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      yield decoder.decode(value, { stream: true });
    }
  } finally {
    await reader.cancel().catch(() => {});
  }
}

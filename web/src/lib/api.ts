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
  icon: string;
  metrics: Record<string, number>;
}

// GPUMetaDTO mirrors server.GPUMetaDTO -- one GPU entity's vendor +
// driver, the card title's own source of truth (see GPUStrip/
// GPUEntityCard's own doc for why the raw pdev address alone isn't).
export interface GPUMetaDTO {
  vendor: string;
  driver: string;
}

export interface SnapshotDTO {
  ts: number;
  unraid_version: string;
  host: Record<string, number>;
  containers: Record<string, ContainerDTO>;
  disks: Record<string, Record<string, number>>;
  unraid: Record<string, Record<string, number>>; // entity ("array"|"docker") -> metric -> value
  gpu: Record<string, Record<string, number>>;
  gpu_meta: Record<string, GPUMetaDTO>; // pdev (or "gpu0"/"nvidia0") -> vendor+driver meta
  sources: Record<string, string>;
}

export interface ContainerInfo {
  name: string;
  state: string;
  health: string;
  image: string;
  icon: string;
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

// signal (threaded through every helper below that a view might call
// repeatedly on a fast-changing selection -- range/resource/window/agg
// tabs) lets a caller cancel a request it no longer cares about. Passing
// one through to fetch() does two things together: it actually tears
// down the in-flight network request (rather than just letting its
// result be ignored later), and it rejects the returned promise with a
// DOMException named "AbortError" -- callers distinguish that from a
// real failure via `err?.name === 'AbortError'` and simply ignore it,
// since a newer request has already superseded it.
async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal });
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
  signal?: AbortSignal;
}): Promise<SeriesResult[]> {
  const q = new URLSearchParams({
    kind: params.kind,
    entity: params.entity,
    metrics: params.metrics.join(','),
  });
  if (params.from !== undefined) q.set('from', String(params.from));
  if (params.to !== undefined) q.set('to', String(params.to));
  return getJSON<SeriesResult[]>(`/api/series?${q.toString()}`, params.signal);
}

export function fetchTop(params: {
  resource: TopResource;
  window: TopWindow;
  agg?: TopAgg;
  limit?: number;
  signal?: AbortSignal;
}): Promise<TopRow[]> {
  const q = new URLSearchParams({ resource: params.resource, window: params.window });
  if (params.agg) q.set('agg', params.agg);
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  return getJSON<TopRow[]>(`/api/top?${q.toString()}`, params.signal);
}

export function fetchEvents(
  params: {
    kinds?: string[];
    entity?: string;
    from?: number;
    to?: number;
    limit?: number;
    signal?: AbortSignal;
  } = {},
): Promise<GantryEvent[]> {
  const q = new URLSearchParams();
  if (params.kinds?.length) q.set('kinds', params.kinds.join(','));
  if (params.entity) q.set('entity', params.entity);
  if (params.from !== undefined) q.set('from', String(params.from));
  if (params.to !== undefined) q.set('to', String(params.to));
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  const qs = q.toString();
  return getJSON<GantryEvent[]>(`/api/events${qs ? `?${qs}` : ''}`, params.signal);
}

export function fetchContainers(): Promise<ContainerInfo[]> {
  return getJSON<ContainerInfo[]>('/api/containers');
}

// fetchSnapshot backs Overview's live-seed discovery step: host metrics
// carry no fixed per-device vocabulary (a real box's net/diskio keys are
// named after whatever interfaces/devices it actually has -- see
// metrics.ts's own sumMetricsByPattern doc), so seeding a sum-of-pattern
// sparkline needs to know the CURRENT exact key names before it can ask
// /api/series for them by name. This is a plain GET, answered
// synchronously from server state -- unlike waiting on live.frame off
// the SSE store, it never races that connection's own first frame.
export function fetchSnapshot(): Promise<SnapshotDTO> {
  return getJSON<SnapshotDTO>('/api/live/snapshot');
}

export function fetchSettings(): Promise<SettingsResponse> {
  return getJSON<SettingsResponse>('/api/settings');
}

export interface VersionResponse {
  version: string;
}

// fetchVersion backs the Settings view's About card. version defaults
// to the literal string "dev" server-side (main.go's `var version =
// "dev"`) when the binary wasn't built with -ldflags -X main.version=...
// -- there is no "unset" case to distinguish from a real release tag.
export function fetchVersion(): Promise<VersionResponse> {
  return getJSON<VersionResponse>('/api/version');
}

// SettingsPutError is what putSettings throws on a non-2xx response: a
// plain Error (so every existing `err.message`/`err instanceof Error`
// caller keeps working unchanged) with the server's own structured
// per-field detail attached -- a 400's {fields: {name: reason}} or a
// 409's {envOverridden: [name, ...]} (see api_settings.go's
// handleSettingsPut) -- so the retention editor can point its inline
// error at the SPECIFIC field that failed, not just show one generic
// banner message. Both are undefined for a plain 404 (Settings
// unavailable) or a malformed-body 500, which carry neither.
export interface SettingsPutError extends Error {
  fields?: Record<string, string>;
  envOverridden?: string[];
}

// putSettings surfaces the server's structured error body (400's
// {error, fields} or 409's {error, env_overridden}) as the thrown
// SettingsPutError's message + fields/envOverridden when the request
// fails, rather than a generic status-code message -- the settings
// editor needs that detail to point at the right field.
export async function putSettings(retention: RetentionSettings): Promise<SettingsResponse> {
  const res = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ retention }),
  });
  const body = (await res.json()) as SettingsResponse & {
    error?: string;
    fields?: Record<string, string>;
    env_overridden?: string[];
  };
  if (!res.ok) {
    const err: SettingsPutError = new Error(body.error ?? `PUT /api/settings: ${res.status} ${res.statusText}`);
    err.fields = body.fields;
    // On a 409 body specifically, env_overridden names the SUBMITTED
    // fields that conflicted with their env-resolved value (a narrower,
    // per-request list) -- not the general "every currently locked
    // field" list settingsGetResponse's own same-named key carries on
    // GET/a successful PUT. Both share the wire name because the server
    // reuses RetentionSettings' sibling struct shape for both bodies;
    // the caller distinguishes them by which response this is (an error
    // vs. a success), not by the key name alone.
    err.envOverridden = body.env_overridden;
    throw err;
  }
  return body;
}

// streamLogs opens /api/containers/{name}/logs and yields decoded text
// chunks as they arrive. follow=1 streams indefinitely; follow=0 (the
// default) yields the tail then ends. Callers drive this with a
// `for await` loop; breaking out of that loop triggers the generator's
// `finally`, which cancels the underlying reader so the connection
// doesn't linger.
//
// opts.signal, when given, is passed straight through to fetch(): a
// follow=1 stream otherwise sits in reader.read() forever for a quiet
// container, with nothing telling the underlying fetch (or the server's
// own follow goroutine, see api_logs.go's drain doc) to stop just
// because the caller stopped consuming -- an unmount or a container
// switch must actively abort, not merely stop reading. Aborting rejects
// the pending fetch/read with a DOMException named "AbortError", which
// propagates out of this generator (through the same `finally` below)
// for the caller's own try/catch to recognize via `err?.name ===
// 'AbortError'` and treat as an intentional stop, not a real failure.
export async function* streamLogs(
  name: string,
  opts: { follow?: boolean; tail?: number; signal?: AbortSignal } = {},
): AsyncGenerator<string> {
  const q = new URLSearchParams();
  if (opts.follow) q.set('follow', '1');
  if (opts.tail !== undefined) q.set('tail', String(opts.tail));
  const qs = q.toString();

  const res = await fetch(`/api/containers/${encodeURIComponent(name)}/logs${qs ? `?${qs}` : ''}`, {
    signal: opts.signal,
  });
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

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
  // compose_project is docker.Meta's own ComposeProject, straight through
  // (server's api_snapshot.go) -- "" for a container not created via
  // docker compose. Backs the Containers view's Groups chip row and the
  // compare view (lib/composeGroups.ts).
  compose_project: string;
  // cpuset is docker.Meta's own field of the same name, straight through
  // -- "" for no cpuset pin (or one that doesn't narrow below the host's
  // own core count). Backs Container Detail's Limits facts line
  // (lib/containerLimits.ts).
  cpuset: string;
  // exit_code is docker.Meta's own field of the same name, straight
  // through -- meaningful only once state is "exited"/"dead". Backs
  // Container Detail's anomaly banner (lib/containerAnomaly.ts).
  exit_code: number;
  // created/update_status/changelog_url/project_url/webui_url/networks/
  // ports all mirror docker.Meta's own fields of the same name straight
  // through (server's api_snapshot.go) and all carry "omitempty" on the
  // Go side -- absent, not just falsy, when unpopulated.
  created?: number;
  update_status?: string;
  changelog_url?: string;
  project_url?: string;
  webui_url?: string;
  networks?: NetworkInfoDTO[];
  ports?: PortInfoDTO[];
  metrics: Record<string, number>;
}

// NetworkInfoDTO mirrors server.NetworkInfoDTO -- one docker network a
// container is attached to. ip is absent for a network that assigns none
// to this container and for the synthetic {name: "host"} entry
// host-network containers report.
export interface NetworkInfoDTO {
  name: string;
  ip?: string;
}

// PortInfoDTO mirrors server.PortInfoDTO -- one container-port binding.
// host_ip/host_port are both absent for an exposed-but-unpublished port
// (EXPOSE with no -p) -- itself useful information, not an absence to
// filter out.
export interface PortInfoDTO {
  container_port: number;
  proto: string;
  host_ip?: string;
  host_port?: number;
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

// SharePlacementDTO mirrors api_storage.go's own SharePlacementDTO: mode
// is useCache's own wire value ("yes" | "no" | "only" | "prefer"); pool
// is absent (not "") when mode is "no" -- see its Go doc for why.
export interface SharePlacementDTO {
  mode: string;
  pool?: string;
}

// StorageMountDTO/StorageDeviceDTO/StorageDTO mirror internal/server/
// api_storage.go's MountDTO/DeviceIODTO/StorageDTO exactly.
export interface StorageMountDTO {
  source: string;
  destination: string;
  rw: boolean;
  storage: { kind: string; name: string; placement?: SharePlacementDTO };
}

export interface StorageDeviceDTO {
  device: string;
  label: string;
  kind: string;
  read_bps: number;
  write_bps: number;
}

export interface StorageDTO {
  mounts: StorageMountDTO[];
  devices: StorageDeviceDTO[];
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

// Group mirrors server.Group -- one user-defined, named set of
// container names (api_groups.go), the custom counterpart to
// composeGroups.ts's own docker-compose-derived groups.
export interface Group {
  name: string;
  members: string[];
}

export interface GroupsResponse {
  groups: Group[];
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

// fetchContainerStorage backs ContainerDetail's Storage section --
// mounts plus each backing device's current IO rate (the latter is
// ring-only, never in the SSE frame, hence a plain poll here rather
// than a read off `live.frame`). A 404 (name docker/fake-mode don't
// know) throws same as every other getJSON call; the caller treats that
// as "no storage panel for this container" rather than an error state.
export function fetchContainerStorage(name: string, signal?: AbortSignal): Promise<StorageDTO> {
  return getJSON<StorageDTO>(`/api/containers/${encodeURIComponent(name)}/storage`, signal);
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

export function fetchGroups(): Promise<GroupsResponse> {
  return getJSON<GroupsResponse>('/api/groups');
}

// putGroups replaces the entire saved groups list -- no per-group
// create/rename/delete route, matching GroupsIface.Set's own
// whole-document-replace contract server-side (api_groups.go). Throws
// a plain Error carrying the server's own message (e.g. "duplicate
// group name") on a non-2xx response -- unlike putSettings, the server
// never attaches per-field detail here (validateGroups returns one
// combined message, not a field map), so there's nothing extra to
// preserve on the thrown error.
export async function putGroups(groups: Group[]): Promise<GroupsResponse> {
  const res = await fetch('/api/groups', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ groups }),
  });
  const body = (await res.json()) as GroupsResponse & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `PUT /api/groups: ${res.status} ${res.statusText}`);
  }
  return body;
}

// ImageInfo/ImagesSummary/ImagesDTO mirror internal/server/api_images.go's
// structs of the same names exactly. repo_tags is never empty on the
// wire -- the server itself fills in a digest ref or the literal
// "<none>" (see its own digestRefsOrNone) whenever a real image has no
// tag, so this view never needs to look at repo_digests directly.
export interface ImageInfo {
  id: string;
  full_id: string;
  repo_tags: string[];
  repo_digests?: string[];
  size_bytes: number;
  created: number;
  state: string; // 'in-use' | 'unused' | 'dangling'
  containers?: string[];
}

export interface ImagesSummary {
  in_use: number;
  unused: number;
  dangling: number;
  reclaimable_bytes: number;
  note: string;
}

export interface ImagesDTO {
  images: ImageInfo[];
  summary: ImagesSummary;
}

export function fetchImages(signal?: AbortSignal): Promise<ImagesDTO> {
  return getJSON<ImagesDTO>('/api/images', signal);
}

export interface ImageRemoveResult {
  id: string;
  ok: boolean;
  error?: string;
}

export interface DeletedImage {
  id: string;
  repo_tags: string[];
  size_bytes: number;
}

export interface ImagePruneResult {
  deleted: DeletedImage[];
  reclaimed_bytes: number;
  errors: string[];
}

// postConfirmed is the shared shape of every /api/images and
// /api/containers/maintenance mutating route: POST with the resource's
// own X-Gantry-Confirm value, JSON body. Every one of those routes'
// error bodies (400/403/413/428/500) is the app-wide {"error":"..."}
// shape (writeError), so a thrown Error's message is always that string
// rather than a generic status line -- matching putSettings/putGroups'
// own "surface the server's own message" convention, just without their
// extra structured fields (none of these routes attach any).
async function postConfirmed<T>(url: string, confirmValue: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Gantry-Confirm': confirmValue },
    body: JSON.stringify(body),
  });
  const parsed = await res.json();
  if (!res.ok) {
    throw new Error((parsed as { error?: string })?.error ?? `POST ${url}: ${res.status} ${res.statusText}`);
  }
  return parsed as T;
}

export function removeImages(ids: string[]): Promise<ImageRemoveResult[]> {
  return postConfirmed('/api/images/remove', 'images', { ids });
}

export function pruneImages(mode: 'dangling' | 'unused'): Promise<ImagePruneResult> {
  return postConfirmed('/api/images/prune', 'images', { mode });
}

// ContainerMaintenanceInfo/-Summary/-DTO mirror internal/server/
// api_containers_maintenance.go's structs of the same names exactly.
export interface ContainerMaintenanceInfo {
  id: string;
  full_id: string;
  name: string;
  image: string;
  state: string; // 'exited' | 'created' | 'dead'
  exit_code?: number;
  created: number;
  finished_at?: number;
  managed?: string;
  restart_policy?: string;
}

export interface ContainerMaintenanceSummary {
  exited: number;
  created: number;
  dead: number;
}

export interface ContainerMaintenanceDTO {
  containers: ContainerMaintenanceInfo[];
  summary: ContainerMaintenanceSummary;
}

export function fetchContainersMaintenance(signal?: AbortSignal): Promise<ContainerMaintenanceDTO> {
  return getJSON<ContainerMaintenanceDTO>('/api/containers/maintenance', signal);
}

export interface ContainerRemoveResult {
  id: string;
  ok: boolean;
  error?: string;
}

export interface DeletedContainer {
  id: string;
  name: string;
  image: string;
}

export interface ContainerPruneResult {
  deleted: DeletedContainer[];
  errors: string[];
}

export function removeContainersMaintenance(ids: string[]): Promise<ContainerRemoveResult[]> {
  return postConfirmed('/api/containers/maintenance/remove', 'containers', { ids });
}

export function pruneContainersMaintenance(
  mode: 'exited' | 'created' | 'all-stopped',
  olderThanHours: number = 0,
): Promise<ContainerPruneResult> {
  return postConfirmed('/api/containers/maintenance/prune', 'containers', { mode, older_than_hours: olderThanHours });
}

// probeReadOnly detects GANTRY_READ_ONLY (never exposed on any GET
// response -- there's no config hint for it anywhere in the frame or
// /api/settings) without ever risking a real mutation. Every mutating
// /api/images and /api/containers/maintenance route checks the confirm
// header and the read-only flag (requireMutationAllowed, server-side)
// BEFORE the body is even decoded, so a deliberately-invalid mode can
// never reach an actual remove/prune call -- it 400s "mode must be..."
// when writable, or 403s "read-only mode" when not, either way before
// anything real happens. images/prune is picked arbitrarily: ReadOnly is
// one flag shared by every mutating route on both resources, never
// scoped per-resource, so one probe answers for both Maintenance cards.
export async function probeReadOnly(signal?: AbortSignal): Promise<boolean> {
  const res = await fetch('/api/images/prune', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Gantry-Confirm': 'images' },
    body: JSON.stringify({ mode: '__gantry_probe__' }),
    signal,
  });
  return res.status === 403;
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

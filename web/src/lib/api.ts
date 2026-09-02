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
  // server_name mirrors server.SnapshotDTO's own field -- var.ini's NAME,
  // the HOST_HOSTNAME env fallback, or this container's own hostname,
  // first non-empty wins; "" when every source comes up empty. Backs the
  // Sidebar brand block's server identity (Sidebar.svelte).
  server_name: string;
  host: Record<string, number>;
  containers: Record<string, ContainerDTO>;
  disks: Record<string, Record<string, number>>;
  unraid: Record<string, Record<string, number>>; // entity ("array"|"docker") -> metric -> value
  gpu: Record<string, Record<string, number>>;
  gpu_meta: Record<string, GPUMetaDTO>; // pdev (or "gpu0"/"nvidia0") -> vendor+driver meta
  sources: Record<string, string>;
  alerts: AlertsBlockDTO;
  insights: InsightsBlockDTO;
}

// FiringAlertDTO mirrors server.FiringAlertDTO -- one firing instance's
// frame-sized summary, carried live in SnapshotDTO.alerts.firing on
// every 2s tick (no polling). A narrower cousin of AlertInstanceDTO
// below: just what the Overview headline and the Alerts view's live
// section need, plus rule_name (the frame joins it against the current
// rule list once per tick so no consumer has to).
//
// summary is the instance's own stored sentence -- the only meaningful
// description for an EVENT alert, whose metric/value/threshold are all
// the zero value (there is no metric to compare against a threshold).
// Render summary instead of value/threshold whenever metric is "".
export interface FiringAlertDTO {
  rule_id: string;
  rule_name: string;
  severity: string;
  kind: string;
  entity: string;
  metric: string;
  value: number;
  threshold: number;
  summary: string;
  fired_at: number;
  silenced: boolean;
}

// AlertsBlockDTO mirrors server.AlertsBlockDTO. firing is always a real
// (if empty) array, capped at 20 entries server-side; truncated + firing_count
// mean the headline's own count and the visible row count can disagree
// only in the rare case where more than 20 things are firing at once --
// firing_count is the true total to show, truncated the amount cut.
export interface AlertsBlockDTO {
  firing: FiringAlertDTO[];
  firing_count: number;
  truncated: number;
  channels: Record<string, string>;
}

// AlertRuleDTO mirrors server.AlertRuleDTO field-for-field (identical
// names/order to internal/store.AlertRule) -- see that Go struct's own
// doc and internal/store/migrations/003_alerts.sql for what each field
// means. A rule with type "event" carries the zero value ("", 0, false)
// for every threshold-only field (metric/op/threshold/...), and vice
// versa.
export interface AlertRuleDTO {
  id: string;
  name: string;
  enabled: boolean;
  builtin: boolean;
  type: 'threshold' | 'event';
  kind: string;
  entity_glob: string;
  entity_class: string;
  metric: string;
  op: string;
  threshold: number;
  clear_threshold: number;
  warn_threshold: number;
  critical_threshold: number;
  band_family: string;
  for_seconds: number;
  clear_seconds: number;
  event_kinds: string;
  min_severity: string;
  clear_event_kinds: string;
  clear_max_severity: string;
  severity: string;
  channels: string;
  renotify_hours: number;
  updated_at: number;
}

export interface AlertRulesResponse {
  rules: AlertRuleDTO[];
}

// AlertInstanceDTO mirrors server.AlertInstanceDTO -- one alert_instances
// row plus silenced, computed fresh from the current silence list at
// response time (always false on a history row: "currently silenced"
// isn't meaningful for something already resolved).
export interface AlertInstanceDTO {
  id: number;
  rule_id: string;
  kind: string;
  entity: string;
  metric: string;
  state: string;
  severity: string;
  value: number;
  threshold: number;
  summary: string;
  started_at: number;
  fired_at: number;
  resolved_at: number;
  resolve_reason: string;
  last_notified_at: number;
  notify_count: number;
  silenced: boolean;
}

// SilenceDTO mirrors server.SilenceDTO. scope is "all" only for a
// global mute (rule_id and entity both ""), omitted otherwise.
export interface SilenceDTO {
  id: number;
  rule_id: string;
  entity: string;
  reason: string;
  until: number;
  created_at: number;
  scope?: 'all';
}

export interface AlertsGetResponse {
  active: AlertInstanceDTO[];
  silences: SilenceDTO[];
  channels: Record<string, string>;
}

// WebhookTargetDTO mirrors server.WebhookTargetDTO. header_value is
// NEVER present on the wire (header_set stands in for it) -- see
// WebhookTargetInput's own doc for how a PUT edits a secret without
// ever echoing it back first.
export interface WebhookTargetDTO {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  header_name?: string;
  header_set: boolean;
  timeout_s: number;
  env_overridden?: boolean;
}

export interface WebhooksGetResponse {
  targets: WebhookTargetDTO[];
}

// WebhookTargetInput is PUT /api/alerts/webhooks' per-target wire shape.
// header_value is optional and three-way: omitted keeps whatever secret
// is already stored for this id, "" clears it, anything else sets it --
// the only way to edit a write-only secret without ever reading it back.
export interface WebhookTargetInput {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  header_name: string;
  header_value?: string;
  timeout_s: number;
}

export function fetchAlerts(): Promise<AlertsGetResponse> {
  return getJSON<AlertsGetResponse>('/api/alerts');
}

// fetchAlertRules fetches the live configured rule set, or (defaults:
// true) the compiled-in seed list -- the exact GET /api/alerts/
// rules?defaults=1 path Task 11's "reset to default" control uses, so
// this component never hardcodes the defaults table itself.
export function fetchAlertRules(opts: { defaults?: boolean } = {}): Promise<AlertRulesResponse> {
  const qs = opts.defaults ? '?defaults=1' : '';
  return getJSON<AlertRulesResponse>(`/api/alerts/rules${qs}`);
}

// AlertRulesPutError mirrors SettingsPutError's own shape: a plain
// Error carrying the server's message, thrown on any non-2xx response
// (400 field-shaped validation failures from alert.ValidateRule, or the
// builtin-identity 400s handleAlertsRulesPut's own doc names) -- the
// rule editor surfaces message verbatim, since the server's messages
// already name the offending rule id and the exact bound violated.
export type AlertRulesPutError = Error;

// putAlertRules performs the whole-document replace: the caller submits
// its own already-edited full list (builtins included, exactly as GET
// returned them, edited numbers and all) -- see handleAlertsRulesPut's
// own doc for why a partial submission can't work (an omitted builtin
// reads as an attempted deletion and 400s).
export async function putAlertRules(rules: AlertRuleDTO[]): Promise<AlertRulesResponse> {
  const res = await apiFetch('/api/alerts/rules', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ rules }),
  });
  const body = (await res.json()) as AlertRulesResponse & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `PUT /api/alerts/rules: ${res.status} ${res.statusText}`);
  }
  return body;
}

export function fetchAlertHistory(
  params: { from?: number; to?: number; limit?: number; signal?: AbortSignal } = {},
): Promise<AlertInstanceDTO[]> {
  const q = new URLSearchParams();
  if (params.from !== undefined) q.set('from', String(params.from));
  if (params.to !== undefined) q.set('to', String(params.to));
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  const qs = q.toString();
  return getJSON<AlertInstanceDTO[]>(`/api/alerts/history${qs ? `?${qs}` : ''}`, params.signal);
}

// createSilence backs the Alerts view's snooze control: rule_id and/or
// entity "" scope it wider (rule-wide or entity-wide); leaving BOTH ""
// requires scope:"all" (handleAlertsSilencesPost's own guard against an
// accidental global mute) -- Task 10 deliberately never offers that
// gesture from the Active row itself, only Settings' own explicit
// global-silence control does.
export async function createSilence(body: {
  rule_id?: string;
  entity?: string;
  hours: number;
  reason?: string;
  scope?: 'all';
}): Promise<SilenceDTO> {
  const res = await apiFetch('/api/alerts/silences', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ rule_id: body.rule_id ?? '', entity: body.entity ?? '', hours: body.hours, reason: body.reason ?? '', scope: body.scope ?? '' }),
  });
  const parsed = (await res.json()) as SilenceDTO & { error?: string };
  if (!res.ok) {
    throw new Error(parsed.error ?? `POST /api/alerts/silences: ${res.status} ${res.statusText}`);
  }
  return parsed;
}

// deleteSilence lifts a silence early ("lift" in the Alerts view) --
// 204 whether or not the id still existed, so this never throws for an
// already-expired/already-lifted silence.
export async function deleteSilence(id: number): Promise<void> {
  const res = await apiFetch(`/api/alerts/silences/${id}`, { method: 'DELETE' });
  if (!res.ok && res.status !== 204) {
    throw new Error(`DELETE /api/alerts/silences/${id}: ${res.status} ${res.statusText}`);
  }
}

// --- overview acknowledgements ---------------------------------------------

// OverviewAckDTO mirrors server.OverviewAckDTO -- one overview_acks row:
// a concrete (kind, entity) attention concern suppressed until `until`.
export interface OverviewAckDTO {
  id: number;
  kind: string;
  entity: string;
  until: number;
  created_at: number;
}

export interface AcksGetResponse {
  acks: OverviewAckDTO[];
}

export function fetchAcks(signal?: AbortSignal): Promise<AcksGetResponse> {
  return getJSON<AcksGetResponse>('/api/acks', signal);
}

// createAck backs the Overview attention row's ack control (1h/24h/7d)
// for FRAME-DERIVED callouts only -- an alert-backed callout's ack goes
// through createSilence instead (one mechanism per system; the control
// routes by callout kind). kind and entity are both always concrete:
// the server 400s anything outside its closed kind vocabulary or with
// an empty entity -- there is deliberately no global ack shape at all
// (unlike silences' explicit scope:"all" gesture).
export async function createAck(body: { kind: string; entity: string; hours: number }): Promise<OverviewAckDTO> {
  const res = await apiFetch('/api/acks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const parsed = (await res.json()) as OverviewAckDTO & { error?: string };
  if (!res.ok) {
    throw new Error(parsed.error ?? `POST /api/acks: ${res.status} ${res.statusText}`);
  }
  return parsed;
}

// deleteAck lifts an ack early -- 204 whether or not the id still
// existed, the deleteSilence convention exactly.
export async function deleteAck(id: number): Promise<void> {
  const res = await apiFetch(`/api/acks/${id}`, { method: 'DELETE' });
  if (!res.ok && res.status !== 204) {
    throw new Error(`DELETE /api/acks/${id}: ${res.status} ${res.statusText}`);
  }
}

export function fetchWebhookTargets(): Promise<WebhooksGetResponse> {
  return getJSON<WebhooksGetResponse>('/api/alerts/webhooks');
}

// putWebhookTargets is READ_ONLY-gated server-side (a 403 there is
// exactly as real an outcome as a 400/409, surfaced the same way) --
// the one alerting write path GANTRY_READ_ONLY actually blocks (webhook
// targets configure an outbound side-effect capability; rules/silences
// don't -- see handleAlertsWebhooksPut's own doc for the asymmetry).
export async function putWebhookTargets(targets: WebhookTargetInput[]): Promise<WebhooksGetResponse> {
  const res = await apiFetch('/api/alerts/webhooks', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ targets }),
  });
  const body = (await res.json()) as WebhooksGetResponse & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `PUT /api/alerts/webhooks: ${res.status} ${res.statusText}`);
  }
  return body;
}

// --- insights (Phase 5) --------------------------------------------------

// EvidenceDTO mirrors server.EvidenceDTO -- insight.Evidence's wire
// shape. Not every field applies to every rule/shape/confidence
// combination (the Go struct's own doc names which); an unused field is
// simply its zero value here too. insights.ts's formatEvidenceNumber
// decides which numbers a given finding's evidence drawer actually
// renders, rather than this type trying to encode that itself.
export interface EvidenceDTO {
  culprit_share_pct: number;
  device_util_pct: number;
  await_ms: number;
  victim_stall_pct: number;
  window_minutes: number;
  other_users: string[];
  iowait_pct: number;
  host_cpu_pct: number;
  spin_count: number;
  spin_window_minutes: number;
  engine_busy_pct: number;
  baseline_pct: number;
}

// InsightDTO mirrors server.InsightDTO -- one insight_instances row.
// evidence is absent on the SSE frame's own insights.active items
// (server's own doc: "statements included, evidence excluded") and
// present everywhere else this type is used (GET /api/insights,
// /api/insights/history, /api/insights/{id}, and the dismiss response).
export interface InsightDTO {
  id: number;
  rule_id: string;
  victim_kind: string;
  victim: string;
  culprit: string;
  culprits: string;
  resource: string;
  state: string;
  severity: string;
  confidence: string;
  tier: string;
  statement: string;
  started_at: number;
  fired_at: number;
  resolved_at: number;
  resolve_reason: string;
  evidence?: EvidenceDTO;
}

// InsightsBlockDTO mirrors server.InsightsBlockDTO -- SnapshotDTO's own
// insights block AND GET /api/insights' response envelope share this
// exact shape (see the Go type's own doc for why one struct serves
// both: they differ only in whether each active item's evidence is
// populated).
export interface InsightsBlockDTO {
  active: InsightDTO[];
  tier: 'proxy' | 'psi';
  suppressed: number;
}

// InsightRuleDTO mirrors server.InsightRuleDTO -- one compiled-in rule's
// current tuning, for the Insights view's own Rules section. defaults
// is the compiled-in set with no overrides applied, so the UI's own
// "reset to default" control never has to hardcode the seven rules'
// thresholds itself.
export interface InsightRuleDTO {
  rule_id: string;
  title: string;
  tier: 'proxy' | 'psi';
  psi_upgrade: boolean;
  enabled: boolean;
  notify: boolean;
  thresholds: Record<string, number>;
  defaults: Record<string, number>;
  updated_at: number;
}

export interface InsightRulesResponse {
  rules: InsightRuleDTO[];
}

// InsightRuleInput is PUT /api/insights/rules' per-rule wire shape:
// enable/notify/overrides only -- see server.insightRuleInput's own doc
// for why a rule's SHAPE (its metric, its evaluator) has nowhere on
// this type to even land.
export interface InsightRuleInput {
  rule_id: string;
  enabled: boolean;
  notify: boolean;
  overrides: Record<string, number>;
}

// GraphNodeDTO/GraphEdgeDTO/InsightGraphDTO mirror server's own DTOs of
// the same names exactly (api_insights.go) -- GET /api/insights/graph's
// payload, mapLayout.ts's own input shape. See GraphEdgeDTO's Go doc
// for the hub-and-spoke edge shape: a culprit edge always runs
// container -> resource; a victim edge (only present when the finding
// names a specific victim CONTAINER) runs resource -> container. Every
// edge therefore always has exactly two endpoints.
export interface GraphNodeDTO {
  id: string;
  kind: 'container' | 'resource';
  label: string;
}

export interface GraphEdgeDTO {
  id: string;
  from: string;
  to: string;
  kind: 'culprit' | 'victim';
  insight_id: number;
  rule_id: string;
  confidence: string;
  severity: string;
  share_pct: number;
}

export interface InsightGraphDTO {
  nodes: GraphNodeDTO[];
  edges: GraphEdgeDTO[];
}

// fetchInsights backs the Insights view's own initial load (the frame's
// live insights.active block covers every later update with no polling
// needed) -- this is the one call site that actually gets evidence
// bundles for every active row up front, matching GET /api/insights'
// own contract.
export function fetchInsights(signal?: AbortSignal): Promise<InsightsBlockDTO> {
  return getJSON<InsightsBlockDTO>('/api/insights', signal);
}

// fetchInsight backs the evidence drawer: one instance, active or
// resolved, always WITH its evidence bundle.
export function fetchInsight(id: number, signal?: AbortSignal): Promise<InsightDTO> {
  return getJSON<InsightDTO>(`/api/insights/${id}`, signal);
}

export function fetchInsightHistory(
  params: { from?: number; to?: number; limit?: number; signal?: AbortSignal } = {},
): Promise<InsightDTO[]> {
  const q = new URLSearchParams();
  if (params.from !== undefined) q.set('from', String(params.from));
  if (params.to !== undefined) q.set('to', String(params.to));
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  const qs = q.toString();
  return getJSON<InsightDTO[]>(`/api/insights/history${qs ? `?${qs}` : ''}`, params.signal);
}

export function fetchInsightRules(signal?: AbortSignal): Promise<InsightRulesResponse> {
  return getJSON<InsightRulesResponse>('/api/insights/rules', signal);
}

// putInsightRules is the whole-document replace: the caller submits its
// own already-edited full list, exactly as GET returned it (see
// putAlertRules' own doc for the identical contract on the alerts
// side -- the insights side has no builtin/user split to worry about,
// every rule here is always compiled-in).
export async function putInsightRules(rules: InsightRuleInput[]): Promise<InsightRulesResponse> {
  const res = await apiFetch('/api/insights/rules', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ rules }),
  });
  const body = (await res.json()) as InsightRulesResponse & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `PUT /api/insights/rules: ${res.status} ${res.statusText}`);
  }
  return body;
}

// dismissInsight backs the Active row's own "not useful" control
// (1d/7d/30d) -- resolves the instance server-side and returns its
// fresh (now resolved) state, so the caller can move it straight into
// History without a full refetch.
export async function dismissInsight(id: number, days: number): Promise<InsightDTO> {
  const res = await apiFetch(`/api/insights/${id}/dismiss`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ days }),
  });
  const body = (await res.json()) as InsightDTO & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `POST /api/insights/${id}/dismiss: ${res.status} ${res.statusText}`);
  }
  return body;
}

// fetchInsightGraph backs the interaction map's own load (mapLayout.ts
// then lays it out client-side) -- the frame carries no graph block of
// its own (Task 9's frame contract is active-findings-only), so the map
// re-polls this on the same cadence the frame ticks rather than
// deriving it from data already in hand; the payload is small (at most
// MaxActive=10 findings' worth of nodes/edges) so this is cheap.
export function fetchInsightGraph(signal?: AbortSignal): Promise<InsightGraphDTO> {
  return getJSON<InsightGraphDTO>('/api/insights/graph', signal);
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

// OverviewLayoutDTO mirrors server.OverviewLayout -- the Overview's
// saved module arrangement, one whole document: the two lanes' ordered
// module-id lists, the ids the owner switched off, the wide lane's share
// of the band's width, and each resized module's height step. Every id
// appears at most once across the three lists. `version` is the
// document's own schema number (see api_layout.go's
// overviewLayoutVersion); the SPA echoes back whatever it was handed
// rather than hardcoding it at the call site.
//
// `ratio` 0 and an absent `sizes` are exactly what a v1 document looks
// like, and both are legal input -- the server migrates them to the
// default split with every module at normal. `sizes` only ever carries
// the NON-default steps, since normal is expressed by absence.
// lib/overviewLayout.ts owns every rule about the CONTENT of all five.
export interface OverviewLayoutDTO {
  version: number;
  wide: string[];
  narrow: string[];
  hidden: string[];
  ratio: number;
  sizes: Record<string, string>;
}

export type TopResource = 'cpu' | 'mem' | 'net' | 'io' | 'gpu';
export type TopWindow = 'now' | '1h' | '24h' | '7d';
export type TopAgg = 'avg' | 'peak';

// --- request plumbing --------------------------------------------------------
// Every mutating request carries `X-Requested-With: gantry` -- the
// server's mux-wide cross-site check (internal/server/gate.go) rejects
// any POST/PUT/DELETE without a custom header, because SameSite=Lax
// cookies are port-blind on a one-IP-many-containers Unraid box and a
// text/plain form can smuggle JSON with no preflight. GETs don't need
// it (nothing mutates), so they stay preflight-free.
//
// Every response is also watched for 401: when the password gate is on
// and the session is missing/expired, ANY api call answers 401, and the
// registered handler (lib/auth.svelte.ts) flips the app to the login
// screen -- the "401 redirects to login" contract, centralized here so
// no view ever handles it itself. postLogin and the password endpoints
// opt out: there a 401 means "wrong password", which the calling form
// surfaces inline instead of bouncing the user.
const MUTATING_METHODS = new Set(['POST', 'PUT', 'DELETE', 'PATCH']);

let unauthorizedHandler: (() => void) | null = null;
export function setUnauthorizedHandler(fn: (() => void) | null): void {
  unauthorizedHandler = fn;
}

interface ApiFetchInit extends RequestInit {
  // suppress the 401 -> login redirect for endpoints where 401 is a
  // form-level answer, not a session-level one.
  skipUnauthorizedHandler?: boolean;
}

async function apiFetch(url: string, init: ApiFetchInit = {}): Promise<Response> {
  const { skipUnauthorizedHandler, ...rest } = init;
  const method = (rest.method ?? 'GET').toUpperCase();
  const headers = new Headers(rest.headers);
  if (MUTATING_METHODS.has(method)) headers.set('X-Requested-With', 'gantry');
  const res = await fetch(url, { ...rest, headers });
  if (res.status === 401 && !skipUnauthorizedHandler) unauthorizedHandler?.();
  return res;
}

// --- auth --------------------------------------------------------------------

// AuthStatus mirrors server.authStatusResponse. `state` is the server's
// single verdict for the SPA to switch on:
//   - "setup"    -- mandatory auth, no credential yet: first-run setup
//   - "login"    -- credential set, not signed in: the login screen
//   - "authed"   -- signed in: the app
//   - "disabled" -- GANTRY_AUTH=proxy (a reverse proxy authenticates) or
//                   =none (explicitly open): the app, no gate
// `username` is present only when authed (never leaked to an
// unauthenticated caller). `env_managed` means the credential came from
// GANTRY_USERNAME/GANTRY_PASSWORD at boot -- in-app changes hold only
// until the next restart re-applies it.
export type AuthState = 'setup' | 'login' | 'authed' | 'disabled';

export interface AuthStatus {
  mode: 'auto' | 'proxy' | 'none';
  state: AuthState;
  username?: string;
  env_managed: boolean;
  authenticated: boolean;
}

export function fetchAuthStatus(): Promise<AuthStatus> {
  return getJSON<AuthStatus>('/api/auth/status');
}

// AuthActionError carries the HTTP status so the login form can tell a
// wrong password (401) from the brute-force limiter (429) and show the
// right message for each.
export class AuthActionError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'AuthActionError';
    this.status = status;
  }
}

async function postAuth(url: string, body: unknown): Promise<void> {
  const res = await apiFetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    skipUnauthorizedHandler: true,
  });
  if (res.status === 204) return;
  let message = `${res.status} ${res.statusText}`;
  try {
    const parsed = (await res.json()) as { error?: string };
    if (parsed?.error) message = parsed.error;
  } catch {
    // a bodyless/non-JSON error keeps the status-line message
  }
  if (!res.ok) throw new AuthActionError(res.status, message);
}

// postAuthSetup runs the one-shot first-run bootstrap: it creates the
// initial username + password credential and the server hands this
// browser a session cookie in the same response.
export function postAuthSetup(username: string, password: string): Promise<void> {
  return postAuth('/api/auth/setup', { username, password });
}

export function postLogin(username: string, password: string): Promise<void> {
  return postAuth('/api/auth/login', { username, password });
}

// postLogout goes through the normal (non-skipping) path deliberately:
// it can't 401 (the route is exempt), and there's nothing form-level
// about it.
export async function postLogout(): Promise<void> {
  const res = await apiFetch('/api/auth/logout', { method: 'POST' });
  if (!res.ok && res.status !== 204) {
    throw new Error(`POST /api/auth/logout: ${res.status} ${res.statusText}`);
  }
}

// postAuthCredential changes the username and/or password. The current
// password is always required; a blank newPassword is a username-only
// change. The server wipes every other session and hands this browser a
// fresh cookie in the same response.
export function postAuthCredential(
  currentPassword: string,
  newUsername: string,
  newPassword: string,
): Promise<void> {
  return postAuth('/api/auth/credential', {
    current_password: currentPassword,
    new_username: newUsername,
    new_password: newPassword,
  });
}

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
  const res = await apiFetch(url, { signal });
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
  const res = await apiFetch('/api/settings', {
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
  const res = await apiFetch('/api/groups', {
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

// fetchOverviewLayout returns the layout the server has already MERGED
// against the module set the running binary knows about (unknown ids
// dropped, missing ones placed at their defaults -- see api_layout.go's
// mergeOverviewLayout). The SPA merges again on receipt anyway, since
// the two builds can differ across an upgrade where the browser holds a
// cached bundle.
export function fetchOverviewLayout(signal?: AbortSignal): Promise<OverviewLayoutDTO> {
  return getJSON<OverviewLayoutDTO>('/api/layout/overview', signal);
}

// putOverviewLayout replaces the entire saved arrangement -- no
// per-module move/hide route, matching LayoutIface.Set's own
// whole-document-replace contract server-side, the putGroups convention
// exactly. Throws a plain Error carrying the server's own message (e.g.
// `unknown overview module "x"`) on a non-2xx; like groups, the server
// attaches no per-field detail, so there is nothing extra to preserve.
//
// NOT read-only gated server-side, and no confirm header: a saved layout
// is config-shaped preference data, the same posture /api/groups and
// /api/settings take. The cross-site header every mutating call carries
// (apiFetch) still applies.
export async function putOverviewLayout(layout: OverviewLayoutDTO): Promise<OverviewLayoutDTO> {
  const res = await apiFetch('/api/layout/overview', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(layout),
  });
  const body = (await res.json()) as OverviewLayoutDTO & { error?: string };
  if (!res.ok) {
    throw new Error(body.error ?? `PUT /api/layout/overview: ${res.status} ${res.statusText}`);
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
  const res = await apiFetch(url, {
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
  const res = await apiFetch('/api/images/prune', {
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

  const res = await apiFetch(`/api/containers/${encodeURIComponent(name)}/logs${qs ? `?${qs}` : ''}`, {
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

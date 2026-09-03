// insightDetail: the pure parts of the insight evidence page
// (views/InsightDetail.svelte) -- id parsing, the "was that a 404"
// verdict, and the evidence table's own field ordering/formatting.
// DOM-free and vitest-tested, the same split insights.ts and
// incidentChart.ts already follow; the view keeps only fetching,
// state and markup.
import { EVIDENCE_LABEL, formatEvidenceNumber } from './insights';
import type { EvidenceDTO } from './api';

// parseInsightId turns a raw ":id" route segment into the numeric id
// GET /api/insights/{id} wants, or null for anything that could never
// BE one. Insight ids are sqlite rowids -- positive integers -- so a
// negative, fractional, empty or non-numeric segment is rejected here
// rather than sent to the server to be 400'd: the page's own not-found
// copy is the honest answer for "#/insights/abc" either way, and not
// asking saves a round trip. Number() (not parseInt) deliberately:
// parseInt("12abc") is 12, which would silently open the WRONG
// insight's page for a mistyped link.
export function parseInsightId(raw: string | undefined | null): number | null {
  if (raw === undefined || raw === null || raw.trim() === '') return null;
  const n = Number(raw);
  if (!Number.isInteger(n) || n <= 0) return null;
  return n;
}

// isNotFoundError distinguishes "no such insight" from "the request
// failed" against api.ts' own getJSON error shape ("GET <url>: 404 Not
// Found") -- the two deserve different copy on the page (a dead link
// vs. a server that's having a moment), and getJSON throws a plain
// Error rather than carrying a status field to read. Anchored on the
// ": <status> " segment getJSON itself writes, so a 404 appearing
// anywhere in the URL (e.g. "/api/insights/404") never reads as one.
export function isNotFoundError(err: unknown): boolean {
  const message = err instanceof Error ? err.message : String(err);
  return /:\s404\s/.test(message);
}

// EVIDENCE_FIELD_ORDER is every EvidenceDTO field the page renders, in
// a stable declared order -- moved here verbatim from the evidence
// drawer this page replaced. A field this insight's own rule never
// populated (not every rule/shape/confidence populates every one --
// insight.Evidence's own doc) is simply absent from the rendered list
// rather than shown as a misleading "0".
export const EVIDENCE_FIELD_ORDER = [
  'culprit_share_pct',
  'device_util_pct',
  'await_ms',
  'victim_stall_pct',
  'window_minutes',
  'iowait_pct',
  'host_cpu_pct',
  'engine_busy_pct',
  'baseline_pct',
  'spin_count',
  'spin_window_minutes',
];

export interface EvidenceRow {
  key: string;
  label: string;
  text: string;
}

// evidenceRows renders one <dl> row per populated field, in
// EVIDENCE_FIELD_ORDER. A zero is treated as unpopulated for the same
// reason an absent field is: the Go side omits nothing and zeroes
// every field a rule didn't compute, so "0 %" here would be a claim
// the evidence bundle never actually made.
export function evidenceRows(evidence: EvidenceDTO | undefined): EvidenceRow[] {
  if (!evidence) return [];
  const ev = evidence as unknown as Record<string, number | undefined>;
  return EVIDENCE_FIELD_ORDER.filter((k) => ev[k] !== undefined && ev[k] !== 0).map((k) => ({
    key: k,
    label: EVIDENCE_LABEL[k] ?? k,
    text: formatEvidenceNumber(k, ev[k] as number),
  }));
}

// formatInstant renders one stored unix second as a local date+time,
// the EventFeedItem/ContainerDetail convention (toLocaleString) -- 0 is
// the store's own "never happened" sentinel (an active insight's
// resolved_at), which gets the same em dash every other absent value in
// this app gets rather than 1970.
export function formatInstant(ts: number): string {
  if (!ts) return '—';
  return new Date(ts * 1000).toLocaleString();
}

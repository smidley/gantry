// eventsToMarkers maps GantryEvents onto TimeChart's marker shape
// ({ts, severity, label}) for Container Detail's charts. The severity
// vocabulary here is TimeChart's own four-slot one (info/warning/serious/
// critical -- see its SEVERITY_VAR) -- deliberately NOT store.Event's own
// three-slot severity field (info/warning/alert; see EventFeedItem's
// doc), because the two encode different things: store.Event.Severity is
// how alarming the event itself is, while a chart marker's severity here
// is assigned per EVENT KIND, per the view's own contract (start=warning,
// oom=critical, health=serious) -- e.g. a plain container.start carries
// store severity "info" but still deserves a visible, warm marker on the
// timeline (a restart is worth noticing even though it isn't alarming).
import type { GantryEvent } from './api';

export type MarkerSeverity = 'info' | 'warning' | 'serious' | 'critical';

export interface ChartMarker {
  ts: number;
  severity: MarkerSeverity;
  label: string;
}

// KIND_MARKER covers every container-lifecycle event kind that can ever
// carry a container's own name as Entity (see docker/registry.go's
// diffEvents/translateEvent) -- container.die is included even though
// the brief's three named examples don't mention it: leaving a
// container's own "it stopped" moment off its OWN detail page's charts
// would be a conspicuous gap, not a deliberate omission.
const KIND_MARKER: Record<string, { severity: MarkerSeverity; label: string }> = {
  'container.start': { severity: 'warning', label: 'Start' },
  'container.oom': { severity: 'critical', label: 'OOM' },
  'container.health': { severity: 'serious', label: 'Health' },
  'container.die': { severity: 'warning', label: 'Stopped' },
  // alert.fired (Phase 4, internal/alert/engine.go's fire calls): a
  // rule's own severity (info/warning/alert) isn't threaded into the
  // event at all -- see store.Event's own Severity field on the
  // appended alert.fired row -- but a fired alert earns the chart's
  // most prominent marker regardless of which rule fired, the same way
  // container.oom already does. alert.resolved deliberately has NO
  // marker of its own: the fired marker plus the chart's own data
  // recovering already tells that story without a second glyph.
  'alert.fired': { severity: 'critical', label: 'Alert' },
  // insight.detected (Phase 5, insight/engine.go's upsertFinding):
  // severity 'info' per the plan's own contract (Task 11: "an
  // insight.detected marker, severity info, label Insight") --
  // deliberately the quietest marker on this chart, unlike alert.fired's
  // 'critical': an insight is a correlational claim, never as alarming
  // as a fired alert by design (Global Constraints: "insights never
  // page"), and this marker's own visual weight should say so.
  // insight.resolved gets no marker of its own, the exact alert.resolved
  // precedent just above -- the detected marker plus the chart's own
  // data clearing already tells that story.
  'insight.detected': { severity: 'info', label: 'Insight' },
};

export function eventsToMarkers(events: GantryEvent[] | null | undefined): ChartMarker[] {
  const out: ChartMarker[] = [];
  for (const e of events ?? []) {
    const known = KIND_MARKER[e.Kind];
    if (!known) continue; // an event kind this page has no marker mapping for is simply not drawn
    out.push({ ts: e.TS, severity: known.severity, label: e.Detail ? `${known.label} · ${e.Detail}` : known.label });
  }
  return out;
}

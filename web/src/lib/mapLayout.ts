// mapLayout: the container interaction map's own layout engine (Phase 5
// Task 14, the backlog's flagship "nice visualization of those
// relationships and live influence").
//
// DETERMINISTIC THREE-COLUMN LAYERED LAYOUT. Not force-directed. Four
// reasons, recorded here because they're the whole design decision:
//
// 1. A force sim never settles on a live dashboard. Nodes re-anneal on
//    every 2s frame as edge weights tick; the picture would be
//    permanently in motion -- the opposite of the calm the D2 design
//    direction establishes everywhere else in this app.
// 2. Position is identity. A user learns "jellyfin sits there." Force
//    layouts relocate everything whenever the edge set changes, which
//    is exactly when the user is trying to read it.
// 3. The graph is inherently tripartite -- culprit -> contended
//    resource -> victim. A layered layout EXPRESSES the causal
//    direction; a force layout expresses nothing in particular. The
//    columns carry meaning a physics simulation would discard.
// 4. Zero deps, fully testable. A deterministic layout is a pure
//    function vitest can assert node-for-node and Playwright can
//    snapshot -- d3-force would be a new dependency producing
//    non-deterministic output, against this app's own "no new
//    dependencies" constraint (BaySchematic.svelte's own hand-rolled
//    SVG precedent).
//
// Column assignment reads the EDGE SET, not any field the server
// attaches to a node: a container is "culprit" if it's ever the `from`
// of a culprit-kind edge, "victim" if it's ever the `to` of a
// victim-kind edge, and "both" -- placed in the resource-ADJACENT
// column, never duplicated -- when it's genuinely both at once (A slows
// B while C slows A: real, and two edges must attach to the SAME node).
// Resource nodes always sit in the middle column.
//
// Within a column, nodes are stable-sorted by id, and Y is a pure
// function of a node's own RANK in that sorted order -- y = MARGIN_Y +
// rank * GAP_Y, top-anchored, NEVER centered as a whole group. This is
// the one non-obvious design choice worth spelling out: an earlier
// version centered each column around the viewport's own vertical
// midpoint, which is the natural-looking thing to do for a SINGLE
// snapshot -- but it makes every rank's position a function of the
// column's TOTAL COUNT, so adding one new sibling anywhere in a column
// re-centers (and therefore MOVES) every node already in it, directly
// violating "position is identity." Anchoring from a fixed top margin
// instead makes each rank's position depend only on its OWN rank, which
// never changes when a HIGHER-ranked (later-sorting) sibling joins --
// the common case in practice, a newly-firing insight almost always
// introducing a node that sorts somewhere in the existing set, not
// uniformly before everything else. The residual case (a genuinely
// earlier-sorting arrival shifts everything below it down by one slot)
// is an accepted, honest trade-off of any deterministic rank-based
// layout with no memory across ticks -- there is no scheme that is both
// stateless and fully immune to it.
import type { GraphEdgeDTO, GraphNodeDTO, InsightGraphDTO } from './api';

export type Column = 'culprit' | 'resource' | 'victim';
export type Role = 'culprit' | 'victim' | 'both' | 'resource';

export interface LaidOutNode extends GraphNodeDTO {
  x: number;
  y: number;
  column: Column;
  role: Role;
}

export interface Layout {
  nodes: LaidOutNode[];
  edges: GraphEdgeDTO[];
  width: number;
  height: number;
}

export interface Viewport {
  w: number;
  h: number;
}

// COLUMN_X_FRACTION places each column at a fixed fraction of the
// viewport's width -- culprit left, resource centered, victim right --
// symmetric margins (0.14/0.86) so the outer columns' own node labels
// have room to render without clipping at the narrowest supported
// viewport (375px, this app's own mobile floor).
const COLUMN_X_FRACTION: Record<Column, number> = { culprit: 0.14, resource: 0.5, victim: 0.86 };

// MARGIN_Y/GAP_Y are both plain constants, deliberately independent of
// any column's node count -- see the module doc above for why that
// independence is the whole point. Exported so InteractionMap.svelte
// (via suggestedHeight below) and this file's own tests share the exact
// same numbers rather than a second hand-copied pair.
export const MARGIN_Y = 40;
export const GAP_Y = 64;
export const MIN_MAP_HEIGHT = 220;

function columnAndRole(node: GraphNodeDTO, culpritIDs: ReadonlySet<string>, victimIDs: ReadonlySet<string>): { column: Column; role: Role } {
  if (node.kind === 'resource') return { column: 'resource', role: 'resource' };
  const isCulprit = culpritIDs.has(node.id);
  const isVictim = victimIDs.has(node.id);
  if (isCulprit && isVictim) return { column: 'resource', role: 'both' };
  if (isVictim) return { column: 'victim', role: 'victim' };
  // Culprit-only, or (defensively) a container with no matching edge at
  // all -- every real node from buildInsightGraph always has >=1 edge,
  // but a hand-built test/fixture graph might not, and "just show it
  // somewhere sane" beats throwing.
  return { column: 'culprit', role: 'culprit' };
}

// groupByColumn is the shared first pass both layoutMap and
// suggestedHeight need: which column each node lands in, stable-sorted
// by id within it.
function groupByColumn(nodes: GraphNodeDTO[], edges: GraphEdgeDTO[]): Record<Column, GraphNodeDTO[]> {
  const culpritIDs = new Set(edges.filter((e) => e.kind === 'culprit').map((e) => e.from));
  const victimIDs = new Set(edges.filter((e) => e.kind === 'victim').map((e) => e.to));

  const byColumn: Record<Column, GraphNodeDTO[]> = { culprit: [], resource: [], victim: [] };
  for (const node of nodes) {
    byColumn[columnAndRole(node, culpritIDs, victimIDs).column].push(node);
  }
  for (const column of Object.keys(byColumn) as Column[]) {
    byColumn[column].sort((a, b) => a.id.localeCompare(b.id));
  }
  return byColumn;
}

// suggestedHeight is the canvas-sizing half of this module: how tall a
// viewport needs to be so the fixed MARGIN_Y/GAP_Y spacing above never
// has to shrink to fit (shrinking it would reintroduce exactly the
// count-dependent instability the module doc explains layoutMap avoids)
// and never wastes obvious space either -- driven by whichever column
// currently holds the most nodes. A caller (InteractionMap.svelte)
// computes this FIRST, then passes the result as layoutMap's own
// viewport.h, so sizing policy (how tall) and spacing policy (how far
// apart) stay cleanly separated.
export function suggestedHeight(graph: InsightGraphDTO): number {
  const byColumn = groupByColumn(graph?.nodes ?? [], graph?.edges ?? []);
  const maxCount = Math.max(byColumn.culprit.length, byColumn.resource.length, byColumn.victim.length, 1);
  return Math.max(MIN_MAP_HEIGHT, 2 * MARGIN_Y + (maxCount - 1) * GAP_Y);
}

// layoutMap is pure and deterministic: the SAME graph and viewport
// always produce the SAME coordinates, every time -- see the module doc
// above. graph.nodes/edges are read but never mutated.
export function layoutMap(graph: InsightGraphDTO, viewport: Viewport): Layout {
  const nodes = graph?.nodes ?? [];
  const edges = graph?.edges ?? [];
  const byColumn = groupByColumn(nodes, edges);

  const culpritIDs = new Set(edges.filter((e) => e.kind === 'culprit').map((e) => e.from));
  const victimIDs = new Set(edges.filter((e) => e.kind === 'victim').map((e) => e.to));

  const laidOut: LaidOutNode[] = [];
  for (const column of Object.keys(byColumn) as Column[]) {
    const x = viewport.w * COLUMN_X_FRACTION[column];
    byColumn[column].forEach((node, rank) => {
      laidOut.push({
        ...node,
        x,
        y: MARGIN_Y + rank * GAP_Y,
        column,
        role: columnAndRole(node, culpritIDs, victimIDs).role,
      });
    });
  }
  // Final output order is id-sorted too, independent of column
  // processing order -- so two calls over the identical node set never
  // disagree on array order even if graph.nodes itself arrived shuffled.
  laidOut.sort((a, b) => a.id.localeCompare(b.id));

  return { nodes: laidOut, edges, width: viewport.w, height: viewport.h };
}

// --- legend (owner-reported: "what do the confirmed and likely lines
// correspond to? I don't see them used anywhere even though they're
// listed in a key") --------------------------------------------------

// LegendPresence names which confidence styles a rendered edge set
// ACTUALLY contains.
export interface LegendPresence {
  confirmed: boolean;
  likely: boolean;
}

// legendPresence mirrors InteractionMap.svelte's own dash predicate
// EXACTLY (edge.confidence === 'likely' -> dashed; anything else ->
// solid/confirmed) -- one source of truth for "what does this canvas
// actually draw," shared by the component's own rendering and this
// legend-presence decision, so the two can never drift apart the way
// the owner's own report described (a key entry for a style nothing on
// screen uses).
export function legendPresence(edges: GraphEdgeDTO[]): LegendPresence {
  return {
    confirmed: edges.some((e) => e.confidence !== 'likely'),
    likely: edges.some((e) => e.confidence === 'likely'),
  };
}

// showLegend decides whether the legend renders AT ALL for a given
// presence, one function shared by both call sites (the owner's own
// "implemented once") rather than two hand-copied conditionals:
//
//   - standalone (compact=false, Insights.svelte's own Map mode): shows
//     the legend whenever at least one style is present -- "still only
//     list present styles," never both-or-nothing.
//   - drawer (compact=true): drops the legend ENTIRELY once only one
//     style is present -- the drawer's own CONFIRMED/LIKELY badge
//     directly above the map (Insights.svelte's insights-drawer__facts
//     row) already states the clicked insight's own tier, so a
//     single-entry legend here would repeat that, not add to it. A
//     two-style legend (this insight's own edge plus a differently-
//     confident concurrent one, muted but present) still earns its keep
//     even in the drawer, since THAT distinction isn't stated anywhere
//     else on the card.
export function showLegend(presence: LegendPresence, compact: boolean): boolean {
  const anyPresent = presence.confirmed || presence.likely;
  if (!anyPresent) return false;
  return compact ? presence.confirmed && presence.likely : true;
}

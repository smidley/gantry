<!--
  InteractionMap: the container interaction map (Phase 5 Task 14,
  backlog flagship) -- a deterministic three-column layered SVG picture
  of "who is leaning on what," culprit | contended resource | victim.
  Deliberately dumb (BaySchematic's own precedent): it knows nothing
  about WHY a finding fired, only how to lay out and draw the graph
  it's handed. Layout itself lives in lib/mapLayout.ts (pure, tested);
  this component is presentation + interaction only.

  D2-calm: every stroke on this canvas is either an insight or a node
  boundary -- no decorative connectors, no grid, no frame (the design
  direction's own "a line either separates two real regions or encodes
  real data, or it doesn't exist," applied to a graph view). Confidence
  is legible without reading a word (confirmed edges solid, likely
  dashed) but is never colour-alone either -- severity's colour and
  confidence's dash pattern are two independent, redundant signals.

  The legend below the canvas is CONDITIONAL on what's actually drawn
  (mapLayout.ts' own legendPresence/showLegend, one shared decision for
  both call sites -- owner-reported: "what do the confirmed and likely
  lines correspond to? I don't see them used anywhere even though
  they're listed in a key"): a style with no edge on screen never gets a
  key entry, and the drawer's own compact variant drops the legend
  entirely once only one style is present at all -- see showLegend's own
  doc for why that's safe (the drawer's own tier badge already says as
  much).

  Hover convention mirrors BaySchematic/FleetStrip exactly: a
  fixed-height label row below the canvas, always present in layout,
  opacity-toggled -- nothing shifts when a hover starts or ends -- with
  the identical text duplicated into every EDGE's own aria-label (plus a
  plain SVG <title> on every node, for a native tooltip) for anyone who
  cannot hover. Hovering an edge highlights its two endpoints and dims
  the rest; hovering a node highlights its incident edges (plus itself)
  the same way.

  Edges are tab-reachable and Enter/Space-openable -- Task 15's own
  a11y bar names EDGES specifically, not nodes: a node does nothing on
  click/Enter (it only dims/highlights on hover, a mouse-only
  convenience), so giving it a non-negative tabindex would make it a
  tab stop that announces information but performs no action -- exactly
  what svelte-a11y's own noninteractive-tabindex check flags, correctly.
  Keyboard/screen-reader users still reach every node's own identity
  through its INCIDENT EDGES, whose aria-label already names both
  endpoints.

  Resource icons intentionally skip a real ContainerIcon image and use
  the same letter-avatar fallback ContainerIcon itself falls back to
  (fallbackLetter) -- keeping this canvas fully SVG-native rather than
  reaching for <foreignObject> to host an <img>, which fake-data mode's
  own synthetic fleet never sets anyway (ContainerIcon's own doc).

  Two call sites share this one component (never a second map): the
  standalone List|Map segmented view (Insights.svelte's own `graph`,
  live-polled every 2s) and the evidence drawer's own embedded map (a
  ONE-TIME snapshot of whichever instant the clicked insight is anchored
  to -- insights.ts' own drawerMapAnchor/selectOverlappingInsights/
  buildInsightGraph doc). The drawer passes two props the standalone view
  never needs:
    - focusInsightId: the clicked insight's own id. Every edge (and its
      two endpoints) belonging to that insight stays at full opacity by
      DEFAULT, with everything else muted -- the exact same dim/opacity
      mechanism a hover already applies, just as a static base layer
      underneath it rather than a second visual language. null (every
      EXISTING call site) reproduces today's behaviour exactly: nothing
      muted until something is actually hovered.
    - compact: caps the canvas' own rendered height with a scrollbar
      rather than letting a large overlapping set blow out the drawer's
      own layout -- see suggestedHeight's own doc for why that number is
      already unbounded by design; nothing about layoutMap's spacing
      itself changes; a tall graph simply scrolls inside its own capped
      box, with the legend and hover-label row (both OUTSIDE that box)
      always staying in view.
-->
<script>
  import { layoutMap, suggestedHeight, legendPresence, showLegend } from '../lib/mapLayout';
  import { containerColor } from '../lib/containerColor';
  import { fallbackLetter } from '../lib/containerIcon';
  import { motion } from '../lib/motion.svelte';

  // graph: InsightGraphDTO ({nodes, edges}) -- the standalone Map mode
  // polls GET /api/insights/graph every 2s for it; the evidence drawer
  // instead builds one snapshot client-side (insights.ts' own
  // buildInsightGraph) and never re-polls. statementsById maps an edge's
  // own insight_id to that finding's full rendered statement -- the
  // hover label's actual text, not a synthesized fragment; sourced from
  // whichever list the caller already has in hand (live.frame.insights.
  // active for the standalone view, the drawer's own overlap set for the
  // drawer) rather than a second fetch either way. onOpenDrawer(insightId)
  // opens the SAME evidence drawer both the List section's cards AND
  // (see this component's own top-of-file doc) the drawer's own embedded
  // map use -- clicking a muted, concurrent edge inside the drawer
  // re-targets it at THAT insight instead of closing it.
  let {
    graph = { nodes: [], edges: [] },
    statementsById = {},
    tier = 'proxy',
    onOpenDrawer = () => {},
    focusInsightId = null,
    compact = false,
  } = $props();

  let containerEl = $state();
  let measuredWidth = $state(680);
  $effect(() => {
    if (!containerEl || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width;
      if (w && w > 0) measuredWidth = w;
    });
    ro.observe(containerEl);
    return () => ro.disconnect();
  });

  // viewport/layout: measuredWidth is the SVG's own real CSS pixel
  // width (viewBox width == that, 1 SVG unit == 1px, so text never
  // scales illegibly small at 375px the way a fixed abstract viewBox
  // would -- see mapLayout.ts's own doc on why suggestedHeight is a
  // SEPARATE sizing pass from layoutMap's fixed spacing).
  let viewport = $derived({ w: measuredWidth, h: suggestedHeight(graph) });
  let layout = $derived(layoutMap(graph, viewport));
  let nodeByID = $derived(new Map(layout.nodes.map((n) => [n.id, n])));

  // legend: which confidence styles this SPECIFIC rendered edge set
  // actually contains, and whether the legend renders at all for it --
  // mapLayout.ts' own doc has the full owner-reported story and the
  // standalone-vs-drawer rule.
  let presence = $derived(legendPresence(layout.edges));
  let shouldShowLegend = $derived(showLegend(presence, compact));

  const SEVERITY_STATUS_TOKEN = { info: '--status-good', warning: '--status-warning', alert: '--status-critical' };
  function severityToken(severity) {
    return SEVERITY_STATUS_TOKEN[severity] ?? '--status-warning';
  }

  const CONTAINER_RADIUS_MIN = 15;
  const CONTAINER_RADIUS_MAX = 26;
  const RESOURCE_HALF = 17;

  function clampShare(pct) {
    return Math.max(0, Math.min(100, Number.isFinite(pct) ? pct : 0));
  }

  // nodeRadius: "sized by their current share of the contended
  // resource, not by absolute CPU" (the plan's own instruction) -- the
  // largest share_pct among a container's own incident edges, whether
  // it's the culprit's attribution share or a named victim's own PSI
  // stall (GraphEdgeDTO.share_pct carries whichever is right for that
  // edge's kind, server-side).
  function nodeRadius(node, edges) {
    if (node.kind === 'resource') return RESOURCE_HALF;
    const incident = edges.filter((e) => e.from === node.id || e.to === node.id);
    const maxShare = incident.reduce((m, e) => Math.max(m, clampShare(e.share_pct)), 0);
    return CONTAINER_RADIUS_MIN + (maxShare / 100) * (CONTAINER_RADIUS_MAX - CONTAINER_RADIUS_MIN);
  }

  function edgeWidth(sharePct) {
    const share = clampShare(sharePct);
    return 1.5 + (share / 100) * 4.5;
  }

  // curvePath: a cubic bezier with horizontal-only tangents at both
  // ends -- reads as a calm S-curve when the two endpoints sit at
  // different heights (the common case, since culprit/victim ranks are
  // independent), and collapses to a straight line when they don't.
  function curvePath(a, b) {
    const midX = (a.x + b.x) / 2;
    return `M ${a.x} ${a.y} C ${midX} ${a.y}, ${midX} ${b.y}, ${b.x} ${b.y}`;
  }

  let hoveredEdgeID = $state(null);
  let hoveredNodeID = $state(null);

  // focusScope: focusInsightId's own static highlight set (this
  // component's own top-of-file doc) -- every edge whose insight_id
  // matches, plus their endpoints. No match at all (defensive: shouldn't
  // happen given the drawer always unions the clicked insight's own row
  // into the pool it builds this graph from, but a hand-fed graph might
  // not carry it) degrades to null -- draw everything at full opacity,
  // never mute the entire canvas over a lookup miss.
  let focusScope = $derived.by(() => {
    if (focusInsightId == null) return null;
    const matches = layout.edges.filter((e) => e.insight_id === focusInsightId);
    if (matches.length === 0) return null;
    const nodeIDs = new Set();
    for (const e of matches) {
      nodeIDs.add(e.from);
      nodeIDs.add(e.to);
    }
    return { nodeIDs, edgeIDs: new Set(matches.map((e) => e.id)) };
  });

  // scope: the current highlight set -- null means "nothing hovered and
  // nothing focused, draw everything at full opacity." Edge hover wins
  // over node hover if somehow both are set (shouldn't happen:
  // pointer/focus can only be on one element at a time), by checking it
  // first; either hover wins outright over focusScope, exactly like
  // hovering already overrides everything else -- a hover always means
  // "show me exactly this," focus or not. With nothing hovered, focusScope
  // is the fallback (null on every EXISTING call site, reproducing
  // today's behaviour exactly): the drawer's own static emphasis.
  let scope = $derived.by(() => {
    if (hoveredEdgeID) {
      const e = layout.edges.find((x) => x.id === hoveredEdgeID);
      return e ? { nodeIDs: new Set([e.from, e.to]), edgeIDs: new Set([e.id]) } : null;
    }
    if (hoveredNodeID) {
      const incident = layout.edges.filter((e) => e.from === hoveredNodeID || e.to === hoveredNodeID);
      const nodeIDs = new Set([hoveredNodeID]);
      for (const e of incident) {
        nodeIDs.add(e.from);
        nodeIDs.add(e.to);
      }
      return { nodeIDs, edgeIDs: new Set(incident.map((e) => e.id)) };
    }
    return focusScope;
  });
  function nodeDimmed(node) {
    return scope !== null && !scope.nodeIDs.has(node.id);
  }
  function edgeDimmed(edge) {
    return scope !== null && !scope.edgeIDs.has(edge.id);
  }

  function edgeLabel(edge) {
    return statementsById[edge.insight_id] ?? `${edge.from} → ${edge.to}`;
  }

  function nodeLabel(node, edges) {
    if (node.kind === 'resource') return `${node.label} — contended resource`;
    const asCulprit = edges.filter((e) => e.kind === 'culprit' && e.from === node.id).length;
    const asVictim = edges.filter((e) => e.kind === 'victim' && e.to === node.id).length;
    const parts = [];
    if (asCulprit > 0) parts.push(`culprit in ${asCulprit} active insight${asCulprit === 1 ? '' : 's'}`);
    if (asVictim > 0) parts.push(`victim in ${asVictim} active insight${asVictim === 1 ? '' : 's'}`);
    return parts.length > 0 ? `${node.label} — ${parts.join(', ')}` : node.label;
  }

  // hoverText: the fixed-height label row's own content -- an edge's
  // real rendered statement, or a node's role summary. Never both.
  let hoverText = $derived.by(() => {
    if (hoveredEdgeID) {
      const e = layout.edges.find((x) => x.id === hoveredEdgeID);
      return e ? edgeLabel(e) : null;
    }
    if (hoveredNodeID) {
      const n = nodeByID.get(hoveredNodeID);
      return n ? nodeLabel(n, layout.edges) : null;
    }
    return null;
  });

  function openDrawer(edge) {
    onOpenDrawer(edge.insight_id);
  }
  function onEdgeKeydown(e, edge) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      openDrawer(edge);
    }
  }

  let transitionMs = $derived(motion.reduced ? 0 : 150);
</script>

<div class="interaction-map" class:interaction-map--compact={compact}>
  {#if layout.nodes.length === 0}
    <div class="interaction-map__empty">
      <p class="interaction-map__empty-line">No container is currently contending with another.</p>
      {#if tier === 'proxy'}
        <p class="microlabel interaction-map__empty-tier">
          Running at tier 1 (proxy evidence) -- PSI is off, so a victim can't yet be named individually.
        </p>
      {/if}
    </div>
  {:else}
    <div class="interaction-map__canvas" bind:this={containerEl} style={`--map-transition-ms: ${transitionMs}ms`}>
      <svg viewBox="0 0 {layout.width} {layout.height}" width="100%" height={layout.height} role="img" aria-label="Container interaction map: culprits, contended resources, and victims">
        <g class="interaction-map__edges">
          {#each layout.edges as edge (edge.id)}
            {@const from = nodeByID.get(edge.from)}
            {@const to = nodeByID.get(edge.to)}
            {#if from && to}
              <g
                class="interaction-map__edge"
                class:interaction-map__edge--dimmed={edgeDimmed(edge)}
                data-insight-id={edge.insight_id}
                tabindex="0"
                role="button"
                aria-label={edgeLabel(edge)}
                onmouseenter={() => (hoveredEdgeID = edge.id)}
                onmouseleave={() => (hoveredEdgeID = null)}
                onfocus={() => (hoveredEdgeID = edge.id)}
                onblur={() => (hoveredEdgeID = null)}
                onclick={() => openDrawer(edge)}
                onkeydown={(e) => onEdgeKeydown(e, edge)}
              >
                <path class="interaction-map__edge-hit" d={curvePath(from, to)} />
                <path
                  class="interaction-map__edge-line"
                  d={curvePath(from, to)}
                  style={`stroke: var(${severityToken(edge.severity)}); stroke-width: ${edgeWidth(edge.share_pct)}px; stroke-dasharray: ${edge.confidence === 'likely' ? '7 6' : 'none'}`}
                />
              </g>
            {/if}
          {/each}
        </g>

        <g class="interaction-map__nodes">
          {#each layout.nodes as node (node.id)}
            <g
              class="interaction-map__node"
              class:interaction-map__node--dimmed={nodeDimmed(node)}
              transform={`translate(${node.x},${node.y})`}
              role="presentation"
              onmouseenter={() => (hoveredNodeID = node.id)}
              onmouseleave={() => (hoveredNodeID = null)}
            >
              <title>{nodeLabel(node, layout.edges)}</title>
              {#if node.kind === 'resource'}
                <rect
                  class="interaction-map__resource-shape"
                  x={-RESOURCE_HALF}
                  y={-RESOURCE_HALF}
                  width={RESOURCE_HALF * 2}
                  height={RESOURCE_HALF * 2}
                  rx="6"
                />
                <text class="interaction-map__resource-label" y={RESOURCE_HALF + 14} text-anchor="middle">{node.label}</text>
              {:else}
                {@const r = nodeRadius(node, layout.edges)}
                <circle class="interaction-map__container-shape" {r} style={`fill: var(${containerColor(node.id)})`} />
                <text class="interaction-map__container-letter" text-anchor="middle" dominant-baseline="central">{fallbackLetter(node.label)}</text>
                <text class="interaction-map__container-label" y={r + 14} text-anchor="middle">{node.label}</text>
              {/if}
            </g>
          {/each}
        </g>
      </svg>
    </div>

    {#if shouldShowLegend}
      <div class="microlabel interaction-map__legend">
        {#if presence.confirmed}
          <span class="interaction-map__legend-swatch interaction-map__legend-swatch--solid" aria-hidden="true"></span>
          confirmed
        {/if}
        {#if presence.likely}
          <span class="interaction-map__legend-swatch interaction-map__legend-swatch--dashed" aria-hidden="true"></span>
          likely
        {/if}
      </div>
    {/if}
  {/if}

  <!-- Fixed-height label row, always present in layout (opacity-toggled,
       never conditionally rendered) so nothing shifts on hover --
       BaySchematic's own convention. -->
  <div class="interaction-map__label" class:interaction-map__label--visible={!!hoverText}>
    {hoverText ?? ''}
  </div>
</div>

<style>
  .interaction-map {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  /* compact (the evidence drawer's own embedded map): cap the canvas'
     rendered height and let IT scroll rather than the drawer ballooning
     around a large overlapping set -- layoutMap's own spacing is
     untouched, so node/edge positions are identical either way; only the
     visible window changes. The legend and hover-label row sit OUTSIDE
     this box (siblings of .interaction-map__canvas below), so they never
     scroll out of view. */
  .interaction-map--compact .interaction-map__canvas {
    max-height: 17.5rem;
    overflow-y: auto;
  }
  .interaction-map--compact .interaction-map__empty {
    padding: 1.25rem 1rem;
  }
  .interaction-map__empty {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 2.5rem 1rem;
    text-align: center;
  }
  .interaction-map__empty-line {
    margin: 0;
    color: var(--ink-2);
  }
  .interaction-map__empty-tier {
    margin: 0;
  }
  .interaction-map__canvas {
    width: 100%;
  }
  .interaction-map__canvas svg {
    display: block;
    width: 100%;
  }
  .interaction-map__edge {
    cursor: pointer;
    opacity: 1;
    transition: opacity var(--map-transition-ms, 150ms) ease;
  }
  .interaction-map__edge:focus-visible .interaction-map__edge-line {
    stroke-width: 5px;
  }
  .interaction-map__edge--dimmed {
    opacity: 0.18;
  }
  .interaction-map__edge-hit {
    fill: none;
    stroke: transparent;
    stroke-width: 16px;
  }
  .interaction-map__edge-line {
    fill: none;
    stroke-linecap: round;
  }
  .interaction-map__node {
    transition: opacity var(--map-transition-ms, 150ms) ease;
  }
  .interaction-map__node--dimmed {
    opacity: 0.25;
  }
  .interaction-map__resource-shape {
    fill: color-mix(in oklab, var(--ink-2) 20%, transparent);
    stroke: var(--ink-2);
    stroke-width: 1.5px;
  }
  .interaction-map__resource-label {
    font-size: 11px;
    fill: var(--ink-2);
    font-family: var(--font-mono);
  }
  .interaction-map__container-shape {
    stroke: color-mix(in oklab, black 15%, transparent);
    stroke-width: 1px;
  }
  .interaction-map__container-letter {
    font-size: 12px;
    font-weight: 700;
    fill: white;
    pointer-events: none;
  }
  .interaction-map__container-label {
    font-size: 11px;
    fill: var(--ink);
    font-weight: 500;
  }
  .interaction-map__legend {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    color: var(--ink-2);
  }
  .interaction-map__legend-swatch {
    display: inline-block;
    width: 22px;
    height: 0;
    border-top: 2px solid var(--ink-2);
    margin-left: 0.5rem;
  }
  /* :first-child, not a --solid-scoped :first-of-type: with the legend
     now conditional per style (see mapLayout.ts' own legendPresence/
     showLegend doc), a dashed-only legend renders the DASHED swatch
     first, and it must lose the leading gap exactly the same way a
     solid-first legend already does. */
  .interaction-map__legend-swatch:first-child {
    margin-left: 0;
  }
  .interaction-map__legend-swatch--dashed {
    border-top-style: dashed;
  }
  /* Fixed-height label row -- see BaySchematic.svelte's identical
     convention and its own doc for why this must never be
     conditionally rendered. */
  .interaction-map__label {
    min-height: 1.3rem;
    font-size: 0.82rem;
    font-weight: 500;
    color: var(--ink);
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .interaction-map__label--visible {
    opacity: 1;
  }
</style>

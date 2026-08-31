import { describe, expect, it } from 'vitest';
import { GAP_Y, MARGIN_Y, MIN_MAP_HEIGHT, layoutMap, suggestedHeight } from './mapLayout';
import type { InsightGraphDTO } from './api';

const VIEWPORT = { w: 800, h: 400 };

function graph(nodes: InsightGraphDTO['nodes'], edges: InsightGraphDTO['edges']): InsightGraphDTO {
  return { nodes, edges };
}

describe('layoutMap', () => {
  it('produces an empty, valid layout for an empty graph', () => {
    const layout = layoutMap(graph([], []), VIEWPORT);
    expect(layout.nodes).toEqual([]);
    expect(layout.edges).toEqual([]);
    expect(layout.width).toBe(800);
    expect(layout.height).toBe(400);
  });

  it('places a pure culprit in the culprit column and a named victim container in the victim column', () => {
    const g = graph(
      [
        { id: 'qbittorrent', kind: 'container', label: 'qbittorrent' },
        { id: 'resource:disk3', kind: 'resource', label: 'disk3' },
        { id: 'jellyfin', kind: 'container', label: 'jellyfin' },
      ],
      [
        { id: 'e1', from: 'qbittorrent', to: 'resource:disk3', kind: 'culprit', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 78 },
        { id: 'e2', from: 'resource:disk3', to: 'jellyfin', kind: 'victim', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 38 },
      ],
    );
    const layout = layoutMap(g, VIEWPORT);
    const byID = new Map(layout.nodes.map((n) => [n.id, n]));
    expect(byID.get('qbittorrent')?.column).toBe('culprit');
    expect(byID.get('resource:disk3')?.column).toBe('resource');
    expect(byID.get('jellyfin')?.column).toBe('victim');
    // The resource column sits at the viewport's horizontal midpoint;
    // culprit left of it, victim right of it.
    expect(byID.get('qbittorrent')!.x).toBeLessThan(byID.get('resource:disk3')!.x);
    expect(byID.get('jellyfin')!.x).toBeGreaterThan(byID.get('resource:disk3')!.x);
  });

  // Task 14's own explicit test case: A slows B (A is culprit), C slows
  // A (A is victim) -- A must be placed ONCE, resource-adjacent, with
  // both its edges attached to that one node.
  it('places a dual-role node (culprit in one insight, victim in another) exactly once, resource-adjacent', () => {
    const g = graph(
      [
        { id: 'qbittorrent', kind: 'container', label: 'qbittorrent' },
        { id: 'resource:disk3', kind: 'resource', label: 'disk3' },
        { id: 'jellyfin', kind: 'container', label: 'jellyfin' },
        { id: 'resource:cpu', kind: 'resource', label: 'cpu' },
        { id: 'plex', kind: 'container', label: 'plex' },
      ],
      [
        { id: 'e1', from: 'qbittorrent', to: 'resource:disk3', kind: 'culprit', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'likely', severity: 'warning', share_pct: 78 },
        { id: 'e2', from: 'resource:disk3', to: 'jellyfin', kind: 'victim', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 38 },
        { id: 'e3', from: 'plex', to: 'resource:cpu', kind: 'culprit', insight_id: 2, rule_id: 'cpu-starvation', confidence: 'likely', severity: 'warning', share_pct: 44 },
        { id: 'e4', from: 'resource:cpu', to: 'qbittorrent', kind: 'victim', insight_id: 2, rule_id: 'cpu-starvation', confidence: 'confirmed', severity: 'warning', share_pct: 20 },
      ],
    );
    const layout = layoutMap(g, VIEWPORT);
    const qbittorrentNodes = layout.nodes.filter((n) => n.id === 'qbittorrent');
    expect(qbittorrentNodes).toHaveLength(1);
    expect(qbittorrentNodes[0].role).toBe('both');
    expect(qbittorrentNodes[0].column).toBe('resource');
    // Both edges touching qbittorrent must still be present, unaltered.
    const touching = layout.edges.filter((e) => e.from === 'qbittorrent' || e.to === 'qbittorrent');
    expect(touching).toHaveLength(2);
  });

  it('is deterministic: identical input yields identical coordinates across 100 runs', () => {
    const g = graph(
      [
        { id: 'sabnzbd', kind: 'container', label: 'sabnzbd' },
        { id: 'resource:cpu', kind: 'resource', label: 'cpu' },
      ],
      [{ id: 'e1', from: 'sabnzbd', to: 'resource:cpu', kind: 'culprit', insight_id: 1, rule_id: 'io-driven-cpu-load', confidence: 'likely', severity: 'warning', share_pct: 63 }],
    );
    const first = layoutMap(g, VIEWPORT);
    for (let i = 0; i < 100; i++) {
      expect(layoutMap(g, VIEWPORT)).toEqual(first);
    }
  });

  it('adding an edge (and its new node) does not move any EXISTING node', () => {
    const before = graph(
      [
        { id: 'qbittorrent', kind: 'container', label: 'qbittorrent' },
        { id: 'resource:disk3', kind: 'resource', label: 'disk3' },
      ],
      [{ id: 'e1', from: 'qbittorrent', to: 'resource:disk3', kind: 'culprit', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'likely', severity: 'warning', share_pct: 78 }],
    );
    const beforeLayout = layoutMap(before, VIEWPORT);
    const beforePositions = new Map(beforeLayout.nodes.map((n) => [n.id, { x: n.x, y: n.y }]));

    const after = graph(
      [...before.nodes, { id: 'sabnzbd', kind: 'container', label: 'sabnzbd' }],
      [...before.edges, { id: 'e2', from: 'sabnzbd', to: 'resource:disk3', kind: 'culprit', insight_id: 2, rule_id: 'disk-io-contention', confidence: 'likely', severity: 'warning', share_pct: 22 }],
    );
    const afterLayout = layoutMap(after, VIEWPORT);
    for (const [id, pos] of beforePositions) {
      const node = afterLayout.nodes.find((n) => n.id === id);
      expect(node?.x).toBe(pos.x);
      expect(node?.y).toBe(pos.y);
    }
  });

  it('stable-sorts nodes within a column by id, independent of input order', () => {
    const g = graph(
      [
        { id: 'sonarr', kind: 'container', label: 'sonarr' },
        { id: 'jellyfin', kind: 'container', label: 'jellyfin' },
        { id: 'plex', kind: 'container', label: 'plex' },
        { id: 'resource:disk3', kind: 'resource', label: 'disk3' },
      ],
      [
        { id: 'e1', from: 'resource:disk3', to: 'sonarr', kind: 'victim', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 10 },
        { id: 'e2', from: 'resource:disk3', to: 'jellyfin', kind: 'victim', insight_id: 2, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 10 },
        { id: 'e3', from: 'resource:disk3', to: 'plex', kind: 'victim', insight_id: 3, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 10 },
      ],
    );
    const layout = layoutMap(g, VIEWPORT);
    const victimOrder = layout.nodes.filter((n) => n.column === 'victim').map((n) => n.id);
    expect(victimOrder).toEqual(['jellyfin', 'plex', 'sonarr']);
  });

  it('scales to a 375px-wide viewport without any two columns overlapping', () => {
    const g = graph(
      [
        { id: 'qbittorrent', kind: 'container', label: 'qbittorrent' },
        { id: 'resource:disk3', kind: 'resource', label: 'disk3' },
        { id: 'jellyfin', kind: 'container', label: 'jellyfin' },
      ],
      [
        { id: 'e1', from: 'qbittorrent', to: 'resource:disk3', kind: 'culprit', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'likely', severity: 'warning', share_pct: 78 },
        { id: 'e2', from: 'resource:disk3', to: 'jellyfin', kind: 'victim', insight_id: 1, rule_id: 'disk-io-contention', confidence: 'confirmed', severity: 'warning', share_pct: 38 },
      ],
    );
    const layout = layoutMap(g, { w: 375, h: 700 });
    const xs = layout.nodes.map((n) => n.x).sort((a, b) => a - b);
    expect(xs[0]).toBeGreaterThan(0);
    expect(xs[xs.length - 1]).toBeLessThan(375);
    // The three columns must be distinctly separated, not collapsed
    // together by the narrow width.
    const distinctXs = new Set(xs);
    expect(distinctXs.size).toBe(3);
  });
});

describe('suggestedHeight', () => {
  it('returns the minimum height for an empty graph', () => {
    expect(suggestedHeight({ nodes: [], edges: [] })).toBe(MIN_MAP_HEIGHT);
  });

  it('grows with the largest column, driven by GAP_Y/MARGIN_Y', () => {
    const victims = ['jellyfin', 'sonarr', 'plex', 'radarr'];
    const g: InsightGraphDTO = {
      nodes: [{ id: 'resource:disk3', kind: 'resource', label: 'disk3' }, ...victims.map((id) => ({ id, kind: 'container' as const, label: id }))],
      edges: victims.map((victim, i) => ({
        id: `e${i}`,
        from: 'resource:disk3',
        to: victim,
        kind: 'victim' as const,
        insight_id: i,
        rule_id: 'disk-io-contention',
        confidence: 'confirmed',
        severity: 'warning',
        share_pct: 10,
      })),
    };
    // victim column has 4 nodes -> 3 gaps; comfortably past
    // MIN_MAP_HEIGHT, so this pins the actual FORMULA, not the floor.
    const expected = 2 * MARGIN_Y + 3 * GAP_Y;
    expect(expected).toBeGreaterThan(MIN_MAP_HEIGHT);
    expect(suggestedHeight(g)).toBe(expected);
  });

  it('feeding its own output back into layoutMap as viewport.h never has the last node overflow past the bottom margin', () => {
    const g: InsightGraphDTO = {
      nodes: [
        { id: 'resource:disk3', kind: 'resource', label: 'disk3' },
        { id: 'a', kind: 'container', label: 'a' },
        { id: 'b', kind: 'container', label: 'b' },
        { id: 'c', kind: 'container', label: 'c' },
        { id: 'd', kind: 'container', label: 'd' },
      ],
      edges: ['a', 'b', 'c', 'd'].map((name, i) => ({
        id: `e${i}`,
        from: name,
        to: 'resource:disk3',
        kind: 'culprit' as const,
        insight_id: i,
        rule_id: 'disk-io-contention',
        confidence: 'likely',
        severity: 'warning',
        share_pct: 10,
      })),
    };
    const h = suggestedHeight(g);
    const layout = layoutMap(g, { w: 800, h });
    const maxY = Math.max(...layout.nodes.map((n) => n.y));
    expect(maxY).toBeLessThanOrEqual(h - MARGIN_Y);
  });
});

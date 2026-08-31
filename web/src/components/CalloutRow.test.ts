// CalloutRow structural tests via svelte/server's SSR string render --
// DOM-free (vitest stays in the node environment; see vitest.config.ts).
// What's assertable this way is the render CONTRACT: the title links to
// describeAnomaly's own href, the reason rides inline, the severity dot
// carries the right --status-* token, and the ack affordance exists with
// a real accessible name. The ack control's endpoint routing (silence vs
// ack by kind) is behavior, not markup -- its two halves are each pinned
// where they live: ackKeyFor/derivation filtering in overviewStatus.
// test.ts, the /api/acks contract in internal/server/api_acks_test.go,
// and the round trip in tests/acks.spec.ts.
import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import CalloutRow from './CalloutRow.svelte';
import type { OverviewAnomaly } from '../lib/overviewStatus';

function renderRow(anomaly: OverviewAnomaly): string {
  return render(CalloutRow, { props: { anomaly } }).body;
}

describe('CalloutRow', () => {
  it('renders an unhealthy container as a link into its own detail page, reason inline', () => {
    const body = renderRow({ kind: 'unhealthy', name: 'sonarr' });
    expect(body).toContain('href="#/containers/sonarr"');
    expect(body).toContain('sonarr is unhealthy');
    expect(body).toContain('Failing its health check.');
    expect(body).toContain('var(--status-critical)');
  });

  it('renders a disk callout as a link to Storage', () => {
    const body = renderRow({ kind: 'disk-usage', slot: 'disk6', usagePct: 95 });
    expect(body).toContain('href="#/storage"');
    expect(body).toContain('disk6 is nearest to full');
    expect(body).toContain('95.0% capacity');
    expect(body).toContain('var(--status-warning)');
  });

  it('renders an alert-backed callout as a link to the Alerts view', () => {
    const body = renderRow({
      kind: 'alert',
      ruleId: 'host-cpu-high',
      ruleName: 'Host CPU high',
      entity: 'host',
      severity: 'warning',
    });
    expect(body).toContain('href="#/alerts"');
    expect(body).toContain('Host CPU high');
  });

  it('renders a non-routable callout as plain text, never a bare <a>', () => {
    // Only a non-docker critical source is unroutable today -- the
    // derivation never produces one, but the component must degrade
    // honestly if that ever changes (anomalyHref's own null convention).
    const body = renderRow({ kind: 'source-critical', source: 'nvidia', detail: 'no nvidia-smi' });
    expect(body).toContain('nvidia needs attention');
    expect(body).not.toContain('<a');
  });

  it('every row carries the ack affordance, named for its own concern', () => {
    const body = renderRow({ kind: 'array-stopped' });
    expect(body).toContain('aria-label="Acknowledge: Array is stopped"');
    expect(body).toContain('>Ack</button>');
  });
});

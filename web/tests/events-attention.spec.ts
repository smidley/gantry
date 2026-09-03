import { test, expect } from '@playwright/test';

// Events' own "Needs you" strip -- the destination half of Overview's
// alerts chip (Scott: "the user can click on the number and then be
// brought to a list of items that need attention. Alerts will go to the
// events page..."). Reuses overview-attention.spec.ts's own
// frame()/routeLiveFrame() idiom (same fake SnapshotDTO shape, routed
// the same way) so the exact anomalies the Overview chip counts are the
// exact rows this strip renders.
//
// What lives here: the strip renders the alerts bucket only (never a
// contention -- that's Insights' own row), it's absent with nothing to
// show, the chip lands with it in view, and the ack round trip. The
// last one is acks.spec.ts's own old UI half, moved here: the counts
// pass (#58) deleted Overview's per-item rows (and the Ack control that
// lived on them) without giving them a new home, so that coverage went
// with them; this view is that new home. The wire contract itself stays
// in acks.spec.ts; the bucketing/wording stays in
// src/lib/attentionCounts.test.ts; CalloutRow's own render contract
// (href, inline reason, ack affordance) stays in
// src/components/CalloutRow.test.ts -- this file is only about Events
// wiring the real anomaly data INTO that component and the strip's own
// presence/absence around it.

function frame(over: { containers?: Record<string, object>; insights?: object[]; alerts?: object[]; disks?: object } = {}) {
  return {
    ts: Math.floor(Date.now() / 1000),
    unraid_version: '7.0.0',
    host: { 'cpu.total': 12.5, 'mem.used_pct': 40, 'mem.used_bytes': 12.8e9 },
    containers: over.containers ?? {
      jellyfin: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 0.4, 'mem.bytes': 9e8 } },
    },
    disks: over.disks ?? {
      disk1: { 'fs.used_bytes': 4e12, 'fs.free_bytes': 4e12, 'temp.c': 38.2, errors: 0 },
      cache: { 'fs.used_bytes': 2e11, 'fs.free_bytes': 3e11, 'temp.c': 41.5, errors: 0 },
    },
    disk_meta: { disk1: { device: 'sdb', kind: 'hdd' }, cache: { device: 'nvme0n1', kind: 'nvme' } },
    unraid: { array: { 'array.started': 1, 'mover.running': 0 } },
    gpu: {},
    gpu_meta: {},
    sources: { docker: 'ok' },
    alerts: { firing: over.alerts ?? [], firing_count: (over.alerts ?? []).length, truncated: 0, channels: {} },
    insights: { active: over.insights ?? [], tier: 'proxy', suppressed: 0 },
  };
}

async function routeLiveFrame(page: import('@playwright/test').Page, f: object) {
  await page.unroute('**/api/live');
  await page.route('**/api/live', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: `retry: 300\nevent: frame\ndata: ${JSON.stringify(f)}\n\n`,
    }),
  );
}

const UNHEALTHY = {
  jellyfin: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 0.4, 'mem.bytes': 9e8 } },
  'mock-pager': { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.3, 'mem.bytes': 1e8 } },
  'mock-relay': { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.2, 'mem.bytes': 1e8 } },
};

const CONTENTION = {
  victim_kind: 'disk',
  victim: 'disk1',
  statement: 'mock-pager is starving jellyfin on disk1',
  severity: 'warning',
  confidence: 'likely',
  fired_at: Math.floor(Date.now() / 1000) - 120,
};

test('the alerts chip lands on Events with the strip in view, alerts only -- never a contention', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY, insights: [CONTENTION] }));
  await page.goto('#/');

  await page.locator('.overview__chip[data-chip="alerts"]').click();
  await expect(page).toHaveURL(/#\/events$/);

  const strip = page.locator('.events-view__attention');
  await expect(strip).toBeVisible();
  await expect(strip).toBeInViewport();
  await expect(strip.locator('.microlabel').first()).toHaveText('Needs you');

  const rows = strip.locator('.callout-row');
  await expect(rows).toHaveCount(2);
  await expect(strip.locator('.callout-row__title')).toContainText(['mock-pager is unhealthy', 'mock-relay is unhealthy']);

  // The contention never lands here -- attentionBucketFor sends it to
  // Insights instead, same split the Overview chip already makes.
  await expect(strip).not.toContainText('starving');
});

test('the strip is absent when nothing needs you, and the rest of the page renders normally', async ({ page }) => {
  await routeLiveFrame(page, frame());
  await page.goto('#/events');

  await expect(page.locator('h1.page-title')).toHaveText('Events');
  await expect(page.locator('.events-view__attention')).toHaveCount(0);
  await expect(page.locator('.events-view__filters')).toBeVisible();
});

test('a strip row is wired to the real anomaly -- its own container link and Ack control', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY }));
  await page.goto('#/events');

  const row = page.locator('.callout-row', { hasText: 'mock-pager is unhealthy' });
  await expect(row.locator('.callout-row__title')).toHaveAttribute('href', '#/containers/mock-pager');
  await expect(row.getByRole('button', { name: 'Acknowledge: mock-pager is unhealthy', exact: true })).toBeVisible();
});

// The ack round trip, end to end -- see this file's own top-of-file doc
// for why this specific coverage moved here. Acking through the SAME
// control CalloutRow always had removes the row from this strip AND
// drops Overview's own chip count, with no reload: both views read the
// one shared acks.svelte.ts singleton, so the SPA's own hash navigation
// alone is what proves the reactivity, not a fresh page load.
test('acking an item in the strip removes it there and drops the Overview alerts count', async ({ page, request }) => {
  const probeEntity = 'gantry-e2e-events-strip-probe';
  await routeLiveFrame(
    page,
    frame({
      containers: {
        jellyfin: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 0.4, 'mem.bytes': 9e8 } },
        [probeEntity]: { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.3, 'mem.bytes': 1e8 } },
        'mock-relay': { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.2, 'mem.bytes': 1e8 } },
      },
    }),
  );

  await page.goto('#/events');
  const strip = page.locator('.events-view__attention');
  await expect(strip.locator('.callout-row')).toHaveCount(2);

  const probeTitle = `${probeEntity} is unhealthy`;
  await page.getByRole('button', { name: `Acknowledge: ${probeTitle}`, exact: true }).click();
  await page.getByRole('button', { name: `Acknowledge for 1h: ${probeTitle}`, exact: true }).click();

  try {
    // Leaves the strip -- one row remains, the probe's own gone -- with
    // no reload.
    await expect(strip.locator('.callout-row')).toHaveCount(1);
    await expect(strip).not.toContainText(probeEntity);

    // ...and the SPA hash change alone (no reload) shows Overview
    // reading the same drop -- the exact cross-page reactivity the old
    // per-row Ack control always had, just relocated.
    await page.goto('#/');
    await expect(page.locator('.overview__chip[data-chip="alerts"] .overview__chip-count')).toHaveText('1');
  } finally {
    // reuseExistingServer means a leftover ack would sit in the store
    // for an hour of later runs -- always lift it (acks.spec.ts's own
    // cleanup convention), found by (kind, entity) since the UI path
    // never hands this test the created row's own id.
    const list = await (await request.get('/api/acks')).json();
    const created = list.acks.find((a: { kind: string; entity: string }) => a.kind === 'unhealthy' && a.entity === probeEntity);
    if (created) {
      await request.delete(`/api/acks/${created.id}`, { headers: { 'X-Requested-With': 'gantry' } });
    }
  }
});

// Design's own explicit case: when the LAST item is acked, the strip
// itself disappears rather than rendering an empty card -- a separate,
// minimal fixture (one alerts-bucket anomaly, not two) so this is
// pinned distinctly from the "count drops from 2 to 1" round trip above.
test('acking the only item in the strip makes it disappear entirely', async ({ page, request }) => {
  const probeEntity = 'gantry-e2e-events-strip-solo-probe';
  await routeLiveFrame(
    page,
    frame({
      containers: {
        jellyfin: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 0.4, 'mem.bytes': 9e8 } },
        [probeEntity]: { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.3, 'mem.bytes': 1e8 } },
      },
    }),
  );

  await page.goto('#/events');
  const strip = page.locator('.events-view__attention');
  await expect(strip.locator('.callout-row')).toHaveCount(1);

  const probeTitle = `${probeEntity} is unhealthy`;
  await page.getByRole('button', { name: `Acknowledge: ${probeTitle}`, exact: true }).click();
  await page.getByRole('button', { name: `Acknowledge for 1h: ${probeTitle}`, exact: true }).click();

  try {
    await expect(strip).toHaveCount(0);
  } finally {
    const list = await (await request.get('/api/acks')).json();
    const created = list.acks.find((a: { kind: string; entity: string }) => a.kind === 'unhealthy' && a.entity === probeEntity);
    if (created) {
      await request.delete(`/api/acks/${created.id}`, { headers: { 'X-Requested-With': 'gantry' } });
    }
  }
});

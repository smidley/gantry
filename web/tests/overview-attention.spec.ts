import { test, expect } from '@playwright/test';

// The "needs you" surface as COUNTS (Scott: "it doesn't create a list
// there, but instead has a count of items that need you. The user can
// click on the number and then be brought to a list of items that need
// attention. Alerts will go to the events page, and any container
// contentions will go to the insights page.").
//
// What lives here: that the chips render the right numbers for a known
// state, that pressing one lands on the page the owner chose, and that
// an acknowledgement takes its item out of the count -- the wire half
// of acks is in acks.spec.ts, the bucketing and wording in
// src/lib/attentionCounts.test.ts.
//
// The chip-count specs route their own /api/live frame (the smoke spec's
// own idiom): which anomalies the real fake-mode server is showing at
// any instant depends on its uptime (grafana's boot health check, the
// 5-minute disk-errors trigger, the scripted insight demo) and on
// whatever acks a parallel spec is briefly holding, none of which these
// assertions are about. The ack spec at the bottom deliberately does NOT
// route -- it is about the live derivation, end to end.

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

test('the attention section is two count chips, not a list of rows', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY, insights: [CONTENTION] }));
  await page.goto('#/');

  await expect(page.locator('.overview__headline-text')).toHaveText('3 things need you');

  const chips = page.locator('.overview__chip');
  await expect(chips).toHaveCount(2);
  await expect(chips.nth(0).locator('.overview__chip-count')).toHaveText('2');
  await expect(chips.nth(0).locator('.overview__chip-noun')).toHaveText('alerts');
  await expect(chips.nth(1).locator('.overview__chip-count')).toHaveText('1');
  await expect(chips.nth(1).locator('.overview__chip-noun')).toHaveText('contention');

  // The whole sentence, including where activating it goes, is in the
  // accessible name -- the visible chip is a bare number and a noun.
  await expect(chips.nth(0)).toHaveAttribute('aria-label', '2 alerts need you, view events');
  await expect(chips.nth(1)).toHaveAttribute('aria-label', '1 contention needs you, view insights');

  // No per-item rows anywhere on the page any more.
  await expect(page.locator('.callout-row')).toHaveCount(0);
});

test('a zero bucket renders no chip at all', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY }));
  await page.goto('#/');

  await expect(page.locator('.overview__headline-text')).toHaveText('2 things need you');
  const chips = page.locator('.overview__chip');
  await expect(chips).toHaveCount(1);
  await expect(chips.first()).toHaveAttribute('data-chip', 'alerts');
  await expect(page.locator('.overview__chip[data-chip="contentions"]')).toHaveCount(0);
});

test('all-clear renders no attention section and no chips', async ({ page }) => {
  await routeLiveFrame(page, frame());
  await page.goto('#/');

  await expect(page.locator('.overview__headline-text')).toHaveText('Nothing needs you');
  await expect(page.locator('.overview__attention')).toHaveCount(0);
  await expect(page.locator('.overview__chip')).toHaveCount(0);
});

test('pressing the alerts count goes to the events page', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY, insights: [CONTENTION] }));
  await page.goto('#/');

  await page.locator('.overview__chip[data-chip="alerts"]').click();
  await expect(page).toHaveURL(/#\/events$/);
  await expect(page.locator('h1.page-title')).toHaveText('Events');
});

test('pressing the contentions count goes to the insights page', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY, insights: [CONTENTION] }));
  await page.goto('#/');

  await page.locator('.overview__chip[data-chip="contentions"]').click();
  await expect(page).toHaveURL(/#\/insights$/);
  await expect(page.locator('h1.page-title')).toHaveText('Insights');
});

test('the chips are reachable and activatable from the keyboard alone', async ({ page }) => {
  await routeLiveFrame(page, frame({ containers: UNHEALTHY }));
  await page.goto('#/');

  const chip = page.locator('.overview__chip[data-chip="alerts"]');
  await chip.focus();
  await expect(chip).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/events$/);
});

// The ack integration, end to end: a real POST /api/acks against the
// real server, read back by the real acks store and applied by the real
// derivation, taking its item out of the COUNT exactly the way it used
// to take it out of the list. It replaces the old "acking a
// Needs-a-look row hides it" UI test -- there is no per-row Ack control
// on this page any more (the counts pass), so the gesture under test is
// the API call that control used to make.
//
// The FRAME is routed even though the ack is real, for one specific
// reason: an ack quiets a frame-derived row only, never a firing alert.
// Acking the live fake fleet's own unhealthy container would leave its
// container-unhealthy ALERT -- until then folded into that row by the
// dedup -- to surface on its own line, and the total would not move at
// all (the old spec's own doc noted exactly this). A routed frame with
// no alerts firing isolates the ack's effect, and its synthetic entity
// name can never collide with a real container or another spec.
test('acknowledging an item drops it out of the alerts count, and lifting it brings it back', async ({ page, request }) => {
  const ackEntity = 'gantry-e2e-chip-probe';
  await routeLiveFrame(
    page,
    frame({
      containers: {
        jellyfin: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 0.4, 'mem.bytes': 9e8 } },
        [ackEntity]: { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.3, 'mem.bytes': 1e8 } },
        'mock-relay': { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.2, 'mem.bytes': 1e8 } },
      },
    }),
  );

  const alertsCount = page.locator('.overview__chip[data-chip="alerts"] .overview__chip-count');
  await page.goto('#/');
  await expect(alertsCount).toHaveText('2');

  const created = await request.post('/api/acks', {
    headers: { 'X-Requested-With': 'gantry' },
    data: { kind: 'unhealthy', entity: ackEntity, hours: 1 },
  });
  expect(created.ok()).toBe(true);
  const ack = await created.json();

  try {
    // The acks store fetches once per page load, so a reload is how the
    // new ack reaches the derivation -- the same path a user gets.
    await page.reload();
    await expect(alertsCount).toHaveText('1');
    await expect(page.locator('.overview__headline-text')).toHaveText('1 thing needs you');
  } finally {
    // Always lift it: reuseExistingServer means a leftover ack would sit
    // in the store for an hour of later runs.
    const deleted = await request.delete(`/api/acks/${ack.id}`, { headers: { 'X-Requested-With': 'gantry' } });
    expect(deleted.status()).toBe(204);
  }

  await page.reload();
  await expect(alertsCount).toHaveText('2');
});

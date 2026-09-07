import { test, expect } from '@playwright/test';

// Full engine integration runs separately from deterministic UI checks.
test('demo-fire: a real finding fires through the engine and renders in Active or History with its statement', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(4 * 60_000 + 30_000);

  let activeNow: { rule_id: string; statement: string } | null = null;
  let seenInHistory: { rule_id: string; statement: string } | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && !activeNow && !seenInHistory) {
    const snap = await (await request.get(`${baseURL}/api/live/snapshot`)).json();
    const found = snap.insights?.active?.find(
      (i: { rule_id: string }) => i.rule_id === 'disk-io-contention' || i.rule_id === 'memory-squeeze',
    );
    if (found) {
      activeNow = found;
    } else {
      const hist = await (await request.get(`${baseURL}/api/insights/history?limit=200`)).json();
      seenInHistory = hist.find((h: { rule_id: string }) => h.rule_id === 'disk-io-contention' || h.rule_id === 'memory-squeeze') ?? null;
    }
    if (!activeNow && !seenInHistory) await page.waitForTimeout(3000);
  }
  expect(activeNow || seenInHistory, 'disk-io-contention or memory-squeeze must fire (or have already resolved) within 4 minutes').toBeTruthy();
  const finding = (activeNow ?? seenInHistory)!;

  await page.goto('#/insights');
  // Map is the DEFAULT mode whenever something's active (the plan's
  // own "the picture is the better first read" rule) -- List's own
  // markup isn't even in the DOM until selected, so force it before
  // looking for a List-only row.
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  if (activeNow) {
    // :not(--history): the shared .insights-view__row class also
    // marks a History row (see ActiveRowVs HistoryRow's own doc on
    // dismiss round-trip below) -- this branch means to find the
    // ACTIVE card specifically.
    const row = page.locator('.insights-view__row:not(.insights-view__row--history)', { hasText: finding.statement.slice(0, 30) });
    await expect(row).toBeVisible();
    await expect(row.locator('.insights-view__chip')).toBeVisible();
  } else {
    const historyRow = page.locator('.insights-view__row--history', { hasText: finding.statement.slice(0, 30) });
    await expect(historyRow).toBeVisible();
  }
});

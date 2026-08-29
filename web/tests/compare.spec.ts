import { test, expect } from '@playwright/test';

// Compare flow (multi detail view): select containers from the
// Containers view, land on the compare route, and see them charted
// together -- Scott's own ask ("I have multiple containers that work
// together as a team for an app, and I would want to see how they're
// all working together"). "jellyfin"/"plex"/"radarr" below are fixed
// fake-fleet archetypes (internal/fake/fake.go), always present and
// always state=running; "gridmind-*" is the fake fleet's own
// docker-compose demo family, all four sharing the "gridmind-cloud"
// compose project.

test('containers: selecting rows and clicking Compare navigates to the compare route with all of them charted', async ({
  page,
}) => {
  await page.goto('#/containers');

  const nameLinks = page.locator('table.containers-table:has(thead) tbody tr.container-row a');
  await expect.poll(() => nameLinks.count()).toBeGreaterThan(0);

  // No floating bar yet -- a single selection isn't enough to compare.
  const rowFor = (name: string) => page.locator('tr.container-row', { hasText: name });
  await rowFor('jellyfin').locator('.container-row__select').check();
  await expect(page.locator('.containers-view__compare-bar')).toHaveCount(0);

  await rowFor('plex').locator('.container-row__select').check();
  await rowFor('radarr').locator('.container-row__select').check();

  const bar = page.locator('.containers-view__compare-bar');
  await expect(bar).toBeVisible();
  await expect(bar).toContainText('3 selected');

  const compareLink = bar.locator('.containers-view__compare-btn');
  // buildCompareHash sorts ascending regardless of check order -- jellyfin
  // was checked first here, but the destination is alphabetical.
  await expect(compareLink).toHaveAttribute('href', '#/compare/jellyfin,plex,radarr');

  await compareLink.click();
  await expect(page).toHaveURL(/#\/compare\/jellyfin,plex,radarr$/);
  await expect(page.locator('h1.page-title')).toHaveText('Compare');

  // One chip per member, each carrying its own name.
  const chips = page.locator('.compare__chip');
  await expect(chips).toHaveCount(3);
  await expect(chips).toContainText(['jellyfin', 'plex', 'radarr']);

  // Group totals render as real (non-placeholder) numbers.
  await expect(page.locator('.compare__total-value').first()).toHaveText(/\d/);

  // At least the four always-present charts (CPU/Memory/Network/Disk IO)
  // render a canvas -- GPU is additionally expected here since jellyfin
  // always has GPU activity in fake mode.
  await expect.poll(() => page.locator('.compare__chart-card canvas').count()).toBeGreaterThanOrEqual(4);
  await expect(page.locator('.compare__chart-card', { hasText: 'GPU' })).toHaveCount(1);

  // The per-member detail table lists all three, each with a real state.
  const detailRows = page.locator('.compare-table tbody tr');
  await expect(detailRows).toHaveCount(3);
  await expect(detailRows).toContainText(['running', 'running', 'running']);
});

test('containers: a Groups chip pre-fills compare with that compose project\'s own members', async ({ page }) => {
  await page.goto('#/containers');

  const groupChip = page.locator('.containers-view__group-chip', { hasText: 'gridmind-cloud' });
  await expect(groupChip).toBeVisible();
  await expect(groupChip).toContainText('×4');
  await expect(groupChip).toHaveAttribute(
    'href',
    '#/compare/gridmind-api,gridmind-db,gridmind-scheduler,gridmind-worker',
  );

  await groupChip.click();
  await expect(page).toHaveURL(/#\/compare\//);
  await expect(page.locator('.compare__chip')).toHaveCount(4);
  await expect(page.locator('.compare__chip')).toContainText([
    'gridmind-api',
    'gridmind-db',
    'gridmind-scheduler',
    'gridmind-worker',
  ]);

  // Every member shares the same compose project -- none of these four
  // archetypes carries GPU metrics, so the GPU card must be absent here,
  // unlike the jellyfin/plex/radarr set above.
  await expect(page.locator('.compare__chart-card', { hasText: 'GPU' })).toHaveCount(0);
});

test('compare: removing a member updates the URL and re-renders with one fewer chip', async ({ page }) => {
  await page.goto('#/compare/jellyfin,plex,radarr');
  await expect(page.locator('.compare__chip')).toHaveCount(3);

  const removePlex = page.getByRole('link', { name: 'Remove plex from comparison' });
  await removePlex.click();

  await expect(page).toHaveURL(/#\/compare\/jellyfin,radarr$/);
  await expect(page.locator('.compare__chip')).toHaveCount(2);
  await expect(page.locator('.compare__chip')).toContainText(['jellyfin', 'radarr']);
  await expect(page.locator('.compare__chip')).not.toContainText(['plex']);

  const detailRows = page.locator('.compare-table tbody tr');
  await expect(detailRows).toHaveCount(2);
});

test('compare: totals sum the per-member detail table, spot-checked against the live frame', async ({ page }) => {
  await page.goto('#/compare/jellyfin,plex,radarr');
  await expect(page.locator('.compare__chip')).toHaveCount(3);
  // Let a couple of live ticks land so every member's chart slot (and
  // therefore the detail table, which is always live) has real values,
  // not the pre-tick zero every Tween starts from.
  await page.waitForTimeout(3_000);

  const cpuValues = await page.locator('.compare-table tbody tr .compare-member-row__num').evaluateAll((cells) =>
    cells
      .filter((c) => c.textContent?.includes('%'))
      .map((c) => parseFloat(c.textContent ?? '0')),
  );
  expect(cpuValues.length).toBe(3);
  const expectedTotal = cpuValues.reduce((a, b) => a + b, 0);

  const totalText = await page.locator('.compare__total-value').first().textContent();
  const actualTotal = parseFloat(totalText ?? '0');
  // A point of slack absorbs the live gliding tween (totals and the
  // table cells ease toward slightly different instants a tick apart)
  // and each value's own 1-decimal rounding.
  expect(Math.abs(actualTotal - expectedTotal)).toBeLessThan(1);
});

test('compare: an empty or single-name route shows a hint instead of a broken page', async ({ page }) => {
  await page.goto('#/compare');
  await expect(page.locator('h1.page-title')).toHaveText('Compare');
  await expect(page.locator('.compare--hint')).toContainText('No containers selected.');
  await expect(page.getByRole('link', { name: /Back to Containers/ })).toHaveAttribute('href', '#/containers');

  await page.goto('#/compare/jellyfin');
  await expect(page.locator('.compare--hint')).toContainText('Select at least one more container to compare.');
});

test('375px viewport: the compare page does not scroll horizontally, and its detail table scrolls in its own container', async ({
  page,
}) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('#/compare/jellyfin,plex,radarr');
  await expect(page.locator('.compare__chip')).toHaveCount(3);

  const { scrollWidth, innerWidth } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
  }));
  expect(scrollWidth).toBeLessThanOrEqual(innerWidth);

  const tableWrap = page.locator('.compare__table-wrap');
  const overflowsItsOwnBox = await tableWrap.evaluate((el) => el.scrollWidth > el.clientWidth);
  expect(overflowsItsOwnBox).toBe(true);
});

test('compare: hovering one chart scrubs a shared crosshair across the others', async ({ page }) => {
  await page.goto('#/compare/jellyfin,plex,radarr');
  await expect(page.locator('.compare__chart-card canvas').first()).toBeVisible({ timeout: 10_000 });

  const overlays = page.locator('.compare__chart-card .u-over');
  await expect.poll(() => overlays.count()).toBeGreaterThanOrEqual(2);

  await overlays.first().hover();

  // Every chart shares one uPlot syncKey (SYNC_KEY = 'compare'), so
  // hovering the first chart's crosshair must also populate every OTHER
  // chart's own tooltip -- not just the one actually under the pointer.
  await expect.poll(() => page.locator('.time-chart__tooltip').count()).toBeGreaterThanOrEqual(2);
});

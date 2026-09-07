import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test('the scrolling container table keeps its header visible without extending the page', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('#/containers');
  const table = page.locator('.containers-view__table-wrap').first();
  await expect(table).toBeVisible();
  await expect(page.locator('h1.page-title')).toBeFocused();
  await table.evaluate(el => { el.scrollTop = 300; });
  const tableBox = (await table.boundingBox())!;
  const headerBox = (await table.locator('thead').boundingBox())!;
  expect(Math.abs(headerBox.y - tableBox.y)).toBeLessThan(3);
  const heights = await page.evaluate(() => ({ page: document.documentElement.scrollHeight, content: document.querySelector('.layout')!.getBoundingClientRect().height }));
  expect(heights.page - heights.content).toBeLessThan(3);
});

test('core pages have no serious accessibility violations in either theme', async ({ page }) => {
  test.setTimeout(90_000);
  for (const theme of ['light', 'dark']) {
    await page.emulateMedia({ colorScheme: theme as 'light' | 'dark', reducedMotion: 'reduce' });
    for (const route of ['#/', '#/containers', '#/settings']) {
      await page.goto(route);
      await expect(page.locator('h1.page-title')).toBeVisible();
      const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze();
      expect(results.violations.filter((v) => ['serious', 'critical'].includes(v.impact ?? '')), `${theme} ${route}: ${JSON.stringify(results.violations)}`).toEqual([]);
    }
  }
});

test('phone overview puts metrics first and chart readouts stay on screen', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('#/');
  await expect(page.locator('h1.page-title')).toHaveText('Overview');
  const firstMetric = page.locator('.overview__metrics-rail .stat-tile').first();
  await expect(firstMetric).toBeVisible();
  const bounds = await firstMetric.boundingBox();
  expect(bounds!.y).toBeLessThan(400);
  await page.goto('#/top/cpu');
  await expect(page.locator('h1.page-title')).toBeFocused();
  const chart = page.locator('.time-chart').first();
  await expect(chart.locator('canvas')).toBeVisible();
  await expect(chart).not.toHaveAttribute('aria-valuemax', '0');
  await chart.focus();
  await expect(chart).toBeFocused();
  await page.keyboard.press('End');
  const readout = chart.locator('.time-chart__tooltip');
  await expect(readout).toBeVisible();
  const box = await readout.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('route navigation resets scroll, focuses the heading, and restores Back position', async ({ page }) => {
  await page.goto('#/settings');
  await expect(page.locator('h1.page-title')).toHaveText('Settings');
  await expect(page.locator('h1.page-title')).toBeFocused();
  await page.evaluate(() => window.scrollTo(0, 500));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(300);
  await page.locator('.sidebar a[href="#/containers"]').click();
  await expect(page.locator('h1.page-title')).toHaveText('Containers');
  await expect(page.locator('h1.page-title')).toBeFocused();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await expect(page).toHaveTitle('Containers · Gantry');
  await page.goBack();
  await expect(page.locator('h1.page-title')).toHaveText('Settings');
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(300);
});

test('command palette contains focus, blocks background controls, and restores focus', async ({ page }) => {
  await page.goto('#/containers');
  await expect(page.locator('h1.page-title')).toBeVisible();
  const trigger = page.getByRole('button', { name: /search|command/i }).first();
  await trigger.click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press('Tab');
    expect(await dialog.evaluate((el) => el.contains(document.activeElement))).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(trigger).toBeFocused();
});


test('cleanup confirmation traps focus, starts on Cancel, and restores its trigger', async ({ page }) => {
  const id = `sha256:${'a'.repeat(64)}`;
  await page.route('**/api/images', route => route.fulfill({ json: {
    images: [{ id: 'aaaaaaaaaaaa', full_id: id, repo_tags: ['focus-test:latest'], size_bytes: 1024, containers: [], state: 'unused', created: 1 }],
    summary: { in_use: 0, unused: 1, dangling: 0, reclaimable_bytes: 1024, note: 'Upper bound' },
  } }));
  await page.goto('#/maintenance');
  const card = page.locator('.maintenance-card', { hasText: 'Images' });
  const row = card.locator('tr', { hasText: 'focus-test:latest' });
  await row.locator('input[type="checkbox"]').check();
  const trigger = card.getByRole('button', { name: 'Remove selected (1)' });
  await trigger.click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
  await expect(dialog).toHaveAttribute('aria-describedby', 'confirm-dialog-description');
  for (const key of ['Shift+Tab', 'Tab', 'Tab', 'Tab', 'Tab']) {
    await page.keyboard.press(key);
    expect(await dialog.evaluate(el => el.contains(document.activeElement))).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(trigger).toBeFocused();
  await expect(row).toBeVisible();
});

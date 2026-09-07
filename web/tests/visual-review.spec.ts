import { test, expect } from './fixtures/insight';

// Captured artifacts also support visual review when desktop access is unavailable.
test('capture primary screens in both themes and phone layouts', async ({ page }, testInfo) => {
  test.setTimeout(90_000);
  const routes = ['#/', '#/containers', '#/containers/jellyfin', '#/compare/jellyfin,qbittorrent', '#/top/cpu', '#/storage', '#/gpu', '#/insights', '#/insights/91002', '#/alerts', '#/events', '#/maintenance', '#/settings'];
  for (const theme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 1440, height: 1000 });
    for (const [index, route] of routes.entries()) {
      await page.goto(route);
      await expect(page.locator('h1')).toBeVisible();
      await page.waitForLoadState('networkidle');
      await page.screenshot({ path: testInfo.outputPath(`${theme}-${index}.png`), fullPage: true, animations: 'disabled' });
    }
    await page.setViewportSize({ width: 390, height: 844 });
    for (const [index, route] of ['#/', '#/containers', '#/top/cpu', '#/storage', '#/settings'].entries()) {
      await page.goto(route);
      await expect(page.locator('h1')).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth), route).toBeLessThanOrEqual(390);
      await page.waitForLoadState('networkidle');
      await page.screenshot({ path: testInfo.outputPath(`${theme}-phone-${index}.png`), fullPage: true, animations: 'disabled' });
    }
  }
});

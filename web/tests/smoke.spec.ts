import { test, expect } from '@playwright/test';

// Playwright smoke suite: drives the real built binary (see
// playwright.config.ts's webServer) in GANTRY_FAKE_DATA=1 mode, which
// synthesizes a 20-container demo fleet (internal/fake/fake.go) --
// "jellyfin" below is one of that fleet's fixed archetypes, always
// present and always state=running.
const ROUTES: { hash: string; h1: string }[] = [
  { hash: '#/', h1: 'Overview' },
  { hash: '#/containers', h1: 'Containers' },
  { hash: '#/containers/jellyfin', h1: 'jellyfin' },
  { hash: '#/top', h1: 'Top Consumers' },
  { hash: '#/storage', h1: 'Storage' },
  { hash: '#/gpu', h1: 'GPU' },
  { hash: '#/events', h1: 'Events' },
  { hash: '#/settings', h1: 'Settings' },
];

test('every route renders its h1 landmark', async ({ page }) => {
  for (const r of ROUTES) {
    await page.goto(r.hash);
    await expect(page.locator('h1.page-title')).toHaveText(r.h1);
  }
});

test('overview: fleet count reflects the fake fleet and the CPU tile ticks', async ({ page }) => {
  await page.goto('#/');

  const runningCount = page.locator('.overview__fleet-count').first().locator('.tabular-nums');
  await expect(runningCount).toBeVisible();
  expect(Number(await runningCount.textContent())).toBeGreaterThanOrEqual(15);

  // CPU tile is the first of Overview's four top-row stat tiles. The fake
  // generator writes host cpu.total with real per-tick jitter (see
  // fake.go's Tick), so two samples a few ticks apart should differ --
  // expect.poll (not a fixed sleep) waits only as long as it actually
  // takes, up to the 6s window the brief allows.
  const cpuNumber = page.locator('.overview__tiles .stat-tile').first().locator('.stat-tile__number');
  const initial = await cpuNumber.textContent();
  await expect.poll(() => cpuNumber.textContent(), { timeout: 6_000 }).not.toBe(initial);
});

test('containers: clicking a header sorts the table', async ({ page }) => {
  await page.goto('#/containers');

  // Scoped to the table that has a <thead> -- Containers.svelte renders
  // a second, header-less .containers-table for the (here, always empty)
  // stopped section, and a parallel mobile card list that stays in the
  // DOM (just display:none) at this desktop viewport; an unscoped
  // selector would double-count names from either.
  const nameLinks = page.locator('table.containers-table:has(thead) tbody tr.container-row a');
  await expect.poll(() => nameLinks.count()).toBeGreaterThan(0);

  const before = await nameLinks.allTextContents();
  await page.getByRole('button', { name: 'Sort by Name' }).click();

  await expect.poll(() => nameLinks.allTextContents()).not.toEqual(before);
  const after = await nameLinks.allTextContents();
  expect(after).toEqual([...after].sort((a, b) => a.localeCompare(b)));
});

test('container detail: charts render and the log viewer shows its empty state', async ({ page }) => {
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.container-detail__charts canvas').first()).toBeVisible({ timeout: 10_000 });

  // "jellyfin" is a fake-fleet name, not a real docker container --
  // dc.StreamLogs errors for it, api_logs.go answers 404, and the log
  // viewer renders its graceful empty state instead of a broken pane.
  await expect(page.locator('.log-viewer__empty')).toContainText('Logs aren\'t available for "jellyfin"', {
    timeout: 10_000,
  });
});

test('top consumers: switching window from Now to 1h renders without erroring', async ({ page }) => {
  await page.goto('#/top');

  // Now is live and client-derived (topFromFrame) -- every fake container
  // reports cpu.pct, so this always has real bars, never the empty state.
  await expect(page.locator('.top-bar-list__row').first()).toBeVisible();

  await page.getByRole('button', { name: '1h', exact: true }).click();
  await expect(page.locator('.top-consumers__loading')).toHaveCount(0, { timeout: 10_000 });

  // No error banner, and the panel settled on either real bars or the
  // window-specific empty state -- both are acceptable outcomes for a
  // freshly-started store that may not have an hour of history yet.
  await expect(page.locator('.top-consumers__error')).toHaveCount(0);
  await expect(page.locator('.top-bar-list__row, .top-bar-list__empty').first()).toBeVisible();
});

test('theme toggle flips data-theme and persists across reload', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('#/');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  // Cycle order is system -> light -> dark -> system (theme.svelte.ts);
  // starting from system (resolved light, forced above), the first click
  // is a no-visible-op (explicit light == already-resolved light) and
  // the second lands on dark, which the following assertion catches.
  const toggle = page.locator('.theme-toggle');
  await toggle.click();
  await toggle.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('375px viewport: no route scrolls horizontally', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  for (const r of ROUTES) {
    await page.goto(r.hash);
    await expect(page.locator('h1.page-title')).toBeVisible();
    const { scrollWidth, innerWidth } = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }));
    expect(scrollWidth, `${r.hash}: scrollWidth ${scrollWidth} > innerWidth ${innerWidth}`).toBeLessThanOrEqual(
      innerWidth,
    );
  }
});

test('LivePulse shows live state while frames flow', async ({ page }) => {
  await page.goto('#/');
  await expect(page.locator('.live-pulse__ring')).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('.live-pulse__text')).toHaveText('live · 2s');

  // The ring node is recreated (Svelte {#key live.frameCount}) on every
  // received SSE frame -- waiting for a second, distinct node in that
  // position proves another live frame actually arrived, rather than a
  // fixed sleep that would only prove time passed.
  const firstRing = await page.locator('.live-pulse__ring').elementHandle();
  await page.waitForFunction((prev) => document.querySelector('.live-pulse__ring') !== prev, firstRing, {
    timeout: 6_000,
  });
  await expect(page.locator('.live-pulse__text')).toHaveText('live · 2s');
});

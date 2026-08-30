import { test, expect } from '@playwright/test';

// Container Detail's anomaly "why" banner (Scott: "if I click into a
// container that says it needs me, I expect to see some explanation
// about why it needs me instead of having to try and figure out what's
// alerting"). Fake mode's own demo scenarios (internal/fake/fake.go):
// "grafana" is modeled running-but-unhealthy from boot with one
// container.health event fired at the same instant, "vaultwarden"/
// "prowlarr" are modeled stopped with exit codes 137/0 respectively, and
// every other running archetype (e.g. "jellyfin") stays plain healthy.

test('an unhealthy container shows the why banner with a plain-language headline and evidence', async ({ page }) => {
  // grafana's own health-flip event fires on the fake generator's first
  // tick (~2s after the server starts -- internal/fake/fake.go's Run/
  // Tick), and ContainerDetail's own events fetch happens exactly ONCE
  // at mount, with no polling/retry of its own (see its own doc) --
  // arriving before that first tick has landed would leave the evidence
  // list empty for the rest of this page load, not just briefly. Poll
  // the API directly first so this test's own navigation always lands
  // after the event genuinely exists, regardless of how fresh the
  // webServer is.
  await expect
    .poll(
      async () => {
        const res = await page.request.get('/api/events?entity=grafana');
        return (await res.json()).length;
      },
      { timeout: 10_000 },
    )
    .toBeGreaterThan(0);

  await page.goto('#/containers/grafana');

  const banner = page.locator('.anomaly-banner');
  await expect(banner).toBeVisible({ timeout: 10_000 });
  await expect(banner).toContainText('Failing its health check');

  // Evidence: the fake generator fires exactly one container.health event
  // for grafana, at boot -- "Became unhealthy" plus a relative timestamp,
  // not the raw event Detail/Kind strings.
  const evidence = banner.locator('.anomaly-banner__evidence li');
  await expect(evidence).toHaveCount(1);
  await expect(evidence).toContainText('Became unhealthy');
  await expect(evidence).toContainText(/ago|just now/);
});

test('jump to logs scrolls the logs pane into view and focuses it without changing the route', async ({ page }) => {
  await page.goto('#/containers/grafana');

  const banner = page.locator('.anomaly-banner');
  await expect(banner).toBeVisible({ timeout: 10_000 });

  const logsSection = page.locator('.container-detail__logs');
  await expect(logsSection).not.toBeInViewport();

  await banner.getByRole('button', { name: 'Jump to logs →' }).click();

  await expect(logsSection).toBeInViewport();
  await expect(logsSection).toBeFocused();
  // A plain in-page anchor would have been read by this app's own hash
  // router as an unknown route (router.ts treats the WHOLE hash as a
  // path) and landed on Not Found -- pinning that the route survives is
  // exactly what protects against that regression.
  await expect(page).toHaveURL(/#\/containers\/grafana$/);
});

test('a stopped container with a known bad exit code explains it in plain language', async ({ page }) => {
  await page.goto('#/containers/vaultwarden');

  const banner = page.locator('.anomaly-banner');
  await expect(banner).toBeVisible({ timeout: 10_000 });
  await expect(banner).toContainText('Stopped — exit code 137 (killed, likely out of memory)');
  // vaultwarden is modeled stopped from boot with no lifecycle event
  // recorded for it -- the empty-evidence fallback must render instead
  // of an empty list.
  await expect(banner.locator('.anomaly-banner__evidence-empty')).toContainText('No recent events recorded');
});

test('a cleanly stopped container reads as a plain, non-alarming exit', async ({ page }) => {
  await page.goto('#/containers/prowlarr');

  const banner = page.locator('.anomaly-banner');
  await expect(banner).toBeVisible({ timeout: 10_000 });
  await expect(banner).toContainText('Stopped — exit code 0 (clean exit)');
});

test('a healthy running container shows no banner at all', async ({ page }) => {
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.container-detail__charts canvas').first()).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('.anomaly-banner')).toHaveCount(0);
});

test('the banner and its jump link remain usable at 375px', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('#/containers/grafana');

  const banner = page.locator('.anomaly-banner');
  await expect(banner).toBeVisible({ timeout: 10_000 });
  await expect(banner.getByRole('button', { name: 'Jump to logs →' })).toBeVisible();
  // No horizontal overflow: the banner's own row wraps rather than
  // forcing the page wider than its own viewport.
  const overflowX = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflowX).toBeLessThanOrEqual(1);
});

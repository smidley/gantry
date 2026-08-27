import { test, expect } from '@playwright/test';

// Proves the live-seed requirement end to end against the real binary:
// "when I view a page live, it shouldn't have empty charts that start to
// build -- it should have filled in metrics that show the last bit of
// history but is now flowing real time."
//
// The strongest available assertion here would read the plotted point
// count straight off TimeChart's own uPlot instance, but that instance
// is a closure-local variable inside the component -- never exposed on
// the DOM or a global -- and the binding design's own file list doesn't
// include changing TimeChart.svelte just to make it reachable from a
// test. The next-strongest option, and the one actually used below, is
// two-part: (1) the seed's own /api/series request actually fires, with
// the right kind/entity/~15m window, AND actually carries real points
// back (not just that a request happened to go out) -- (2) the cold-ring
// "no data" placeholder never appears once that response lands, and the
// real chart canvas is showing within 1s. Between them these prove both
// halves of the requirement: real history was fetched, and it reached
// the chart rather than being fetched and dropped.
test('container detail seeds its live CPU chart from server history on arrival', async ({ page }) => {
  test.setTimeout(60_000);

  await page.goto('#/');
  await expect(page.locator('.overview__tiles .stat-tile').first()).toBeVisible();

  // playwright.config.ts's webServer boots once for the whole suite and
  // fullyParallel:true means this file's own start time relative to
  // that boot isn't guaranteed (test/file ordering isn't something to
  // rely on) -- wait out an explicit floor so the fake collector (2s
  // cadence) has accumulated a real, non-trivial amount of ring history
  // before the seed fetch below asks for it, rather than assuming
  // smoke.spec.ts's tests already ran first and warmed the server up.
  await page.waitForTimeout(20_000);

  const [seedResponse] = await Promise.all([
    page.waitForResponse((res) => res.url().includes('/api/series') && res.url().includes('kind=container')),
    page.goto('#/containers/jellyfin'),
  ]);

  expect(seedResponse.status()).toBe(200);
  const url = new URL(seedResponse.url());
  expect(url.searchParams.get('kind')).toBe('container');
  expect(url.searchParams.get('entity')).toBe('jellyfin');
  const from = Number(url.searchParams.get('from'));
  const to = Number(url.searchParams.get('to'));
  // ~15 minutes -- a little slack for the seconds-granularity rounding
  // Date.now()/1000 vs. the request's own trip time can introduce.
  expect(to - from).toBeGreaterThan(895);
  expect(to - from).toBeLessThan(905);

  const body = (await seedResponse.json()) as { metric: string; points: unknown[] }[];
  const withPoints = body.filter((r) => r.points.length > 0);
  expect(withPoints.length).toBeGreaterThan(0);
  // At 20s+ of real server uptime and a 2s tick cadence, a genuine seed
  // should carry well more than a token point or two -- this is what
  // actually distinguishes "seeded" from "a request fired and came back
  // empty."
  expect(withPoints[0].points.length).toBeGreaterThan(5);

  // No empty-state flash: the CPU card must never show its "no data"
  // placeholder once the seed response above has landed.
  await expect(
    page.locator('.container-detail__chart-card', { hasText: 'CPU' }).locator('.container-detail__empty'),
  ).toHaveCount(0, { timeout: 1_000 });
  await expect(page.locator('.container-detail__charts canvas').first()).toBeVisible({ timeout: 1_000 });
});

// The test above proves the seed lands and the chart takes over, but it
// awaits the seed response BEFORE asserting -- by then, the real
// (usually well under 50ms) fetch has already resolved either way, so it
// can't actually catch a regression of the pending gate itself
// (liveSeedPending in ContainerDetail.svelte, mirroring GPUEntityCard's
// own field of the same name). This test instead delays the seed
// response ARTIFICIALLY, widening that window enough to assert against
// deterministically -- and, unlike the test above, deliberately does NOT
// wait out real server uptime first, so the ring genuinely has zero
// points when the seed request fires: while it's still in flight, the
// CPU card must show neither its chart (no data yet) nor the misleading
// "No CPU data for this range." placeholder. Regressing the gate (e.g.
// reverting to a bare hasPoints()/else) would surface that text here,
// where the first test above cannot.
test('container detail never flashes "no data" while its live seed is still in flight', async ({ page }) => {
  test.setTimeout(15_000);

  await page.goto('#/');
  await expect(page.locator('.overview__tiles .stat-tile').first()).toBeVisible();

  const seedRequest = page.waitForRequest(
    (req) => new URL(req.url()).pathname === '/api/series' && new URL(req.url()).searchParams.get('kind') === 'container',
  );
  await page.route(
    (url) => url.pathname === '/api/series' && url.searchParams.get('kind') === 'container',
    async (route) => {
      await new Promise((r) => setTimeout(r, 500));
      await route.continue();
    },
  );

  const cpuEmptyState = page
    .locator('.container-detail__chart-card', { hasText: 'CPU' })
    .locator('.container-detail__empty');

  await page.goto('#/containers/jellyfin');
  await seedRequest; // the seed request has fired; its (artificially delayed) response hasn't landed yet

  // Deliberately .count() (a plain, one-shot query) rather than
  // expect(locator).toHaveCount(0): that matcher auto-retries for up to
  // its own timeout, so it would still pass here even if the text
  // flashed and cleared again well inside that window -- exactly the
  // failure mode this test exists to catch. .count() reports what's in
  // the DOM at THIS instant, with nothing left to retry away.
  expect(await cpuEmptyState.count()).toBe(0);
  await page.waitForTimeout(400); // still short of the route handler's own 500ms delay
  expect(await cpuEmptyState.count()).toBe(0);
});

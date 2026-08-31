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
  await expect(page.locator('.overview__metrics-rail .stat-tile').first()).toBeVisible();

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
  await expect(page.locator('.overview__metrics-rail .stat-tile').first()).toBeVisible();

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

// Same live-seed contract, the Metrics page's own hero chart (Scott's own
// report, with a screenshot: "I can see system metrics historically, but
// I can't see metrics for containers start generating until I navigate
// to the page" -- the host-total dotted reference line already spanned
// the full window; every container line didn't). TimeChart has no DOM
// "empty state" marker the way ContainerDetail's chart cards do, so the
// second half of this proof reads the crosshair tooltip instead of a
// placeholder's absence: uPlot clamps an out-of-range cursor to the
// shared x-axis's own oldest index (buildAlignedData unions every
// series' timestamps together), so hovering right at the chart's left
// edge lands on the oldest timestamp ANY line has -- which, pre-fix, only
// the host-total line actually had a value at (every container cell
// there was a real gap, rendered as "—").
test('metrics hero chart seeds its container lines from server history on arrival', async ({ page }) => {
  test.setTimeout(60_000);

  await page.goto('#/');
  await expect(page.locator('.overview__metrics-rail .stat-tile').first()).toBeVisible();
  await page.waitForTimeout(20_000); // see the first test's own doc for why this floor is needed regardless of file/test ordering

  const [seedResponse] = await Promise.all([
    page.waitForResponse((res) => res.url().includes('/api/series') && res.url().includes('kind=container')),
    page.goto('#/top/cpu'),
  ]);

  expect(seedResponse.status()).toBe(200);
  const url = new URL(seedResponse.url());
  expect(url.searchParams.get('kind')).toBe('container');
  // Every hero slot's own seed fetch asks for exactly the CPU tab's own
  // metric (resourceMetricKeys('cpu') === ['cpu.pct']), regardless of
  // which of the (up to 10) concurrent per-container requests this
  // caught -- unlike Compare's own combined-metrics fetch below.
  expect(url.searchParams.get('metrics')).toBe('cpu.pct');
  const from = Number(url.searchParams.get('from'));
  const to = Number(url.searchParams.get('to'));
  expect(to - from).toBeGreaterThan(895);
  expect(to - from).toBeLessThan(905);

  const body = (await seedResponse.json()) as { metric: string; points: unknown[] }[];
  const withPoints = body.filter((r) => r.points.length > 0);
  expect(withPoints.length).toBeGreaterThan(0);
  expect(withPoints[0].points.length).toBeGreaterThan(5);

  const chart = page.locator('.top-consumers__header .u-over').first();
  await expect(chart).toBeVisible({ timeout: 2_000 });
  await chart.hover({ position: { x: 2, y: 10 } });

  const rows = page.locator('.top-consumers__header .time-chart__tooltip-row');
  await expect.poll(() => rows.count()).toBeGreaterThan(1); // the host-total line plus at least one container
  const texts = await rows.allTextContents();
  const hostRow = texts.find((t) => t.includes('Host total'));
  expect(hostRow, 'sanity check: the host reference line must already show real history at the left edge').toBeDefined();
  expect(hostRow).not.toContain('—');

  const containerRows = texts.filter((t) => !t.includes('Host total'));
  expect(containerRows.length).toBeGreaterThan(0);
  expect(containerRows.some((t) => !t.includes('—'))).toBe(true);
});

// Same contract, Compare's own per-member "Now" charts (makeCompareSlot,
// Compare.svelte) -- built as a direct copy of the hero chart's slot
// pool, and seeded the same way as part of this same fix. No host-total
// line here to sanity-check against (Compare has no such concept), so
// this just confirms at least one member's own row has a real value at
// the chart's oldest edge.
test('compare view seeds its live per-member charts from server history on arrival', async ({ page }) => {
  test.setTimeout(60_000);

  await page.goto('#/');
  await expect(page.locator('.overview__metrics-rail .stat-tile').first()).toBeVisible();
  await page.waitForTimeout(20_000);

  const [seedResponse] = await Promise.all([
    page.waitForResponse((res) => res.url().includes('/api/series') && res.url().includes('kind=container')),
    page.goto('#/compare/jellyfin,plex,radarr'),
  ]);

  expect(seedResponse.status()).toBe(200);
  const url = new URL(seedResponse.url());
  expect(url.searchParams.get('kind')).toBe('container');
  const from = Number(url.searchParams.get('from'));
  const to = Number(url.searchParams.get('to'));
  expect(to - from).toBeGreaterThan(895);
  expect(to - from).toBeLessThan(905);

  const body = (await seedResponse.json()) as { metric: string; points: unknown[] }[];
  const cpuEntry = body.find((r) => r.metric === 'cpu.pct');
  expect(cpuEntry?.points.length ?? 0).toBeGreaterThan(5);

  const cpuChart = page.locator('.compare__chart-card', { hasText: 'CPU' }).locator('.u-over').first();
  await expect(cpuChart).toBeVisible({ timeout: 2_000 });
  await cpuChart.hover({ position: { x: 2, y: 10 } });

  const rows = page.locator('.compare__chart-card', { hasText: 'CPU' }).locator('.time-chart__tooltip-row');
  await expect.poll(() => rows.count()).toBeGreaterThan(0);
  const texts = await rows.allTextContents();
  expect(texts.some((t) => !t.includes('—'))).toBe(true);
});

// Same contract, the Storage view's own per-drive header chart (Scott's
// own follow-up ask: "storage page needs the same historical treatment")
// -- makeDiskSlot was built the same shape as heroSlot (this file's own
// two tests above) once seed()/resetAssignment() were added to it, so
// this proves the identical fix landed there too. The default chart
// metric is "io" (CHART_METRICS' own first entry), which -- unlike
// cpu/mem's one fixed key -- is a per-DEVICE host-scoped pair
// (diskio.<device>.read_bps/.write_bps, joined off disk_meta), so the
// seed request this catches looks like the header rings' own net/io
// seeding rather than the hero chart's per-container one; asserting the
// metrics param carries both halves proves the device join resolved
// correctly, not just that SOME request fired.
test('storage chart seeds its per-drive lines from server history on arrival', async ({ page }) => {
  test.setTimeout(60_000);

  await page.goto('#/');
  await expect(page.locator('.overview__metrics-rail .stat-tile').first()).toBeVisible();
  await page.waitForTimeout(20_000); // see the first test's own doc for why this floor is needed regardless of file/test ordering

  const [seedResponse] = await Promise.all([
    page.waitForResponse((res) => res.url().includes('/api/series') && res.url().includes('kind=host') && res.url().includes('diskio')),
    page.goto('#/storage'),
  ]);

  expect(seedResponse.status()).toBe(200);
  const url = new URL(seedResponse.url());
  expect(url.searchParams.get('kind')).toBe('host');
  const metrics = url.searchParams.get('metrics') ?? '';
  expect(metrics).toContain('.read_bps');
  expect(metrics).toContain('.write_bps');
  const from = Number(url.searchParams.get('from'));
  const to = Number(url.searchParams.get('to'));
  expect(to - from).toBeGreaterThan(895);
  expect(to - from).toBeLessThan(905);

  const body = (await seedResponse.json()) as { metric: string; points: unknown[] }[];
  const withPoints = body.filter((r) => r.points.length > 0);
  expect(withPoints.length).toBeGreaterThan(0);
  expect(withPoints[0].points.length).toBeGreaterThan(5);

  const chart = page.locator('.storage-chart .u-over').first();
  await expect(chart).toBeVisible({ timeout: 2_000 });
  await chart.hover({ position: { x: 2, y: 10 } });

  const rows = page.locator('.storage-chart .time-chart__tooltip-row');
  await expect.poll(() => rows.count()).toBeGreaterThan(0);
  const texts = await rows.allTextContents();
  expect(texts.some((t) => !t.includes('—'))).toBe(true);
});

// Cold-start variant of the seeding test above: landing directly on
// #/storage as the very FIRST navigation (no prior Overview visit to
// warm up live.frame first). The seeding effect's own trigger reads
// diskNames TRACKED specifically so a mount that beats the first SSE
// frame to arrival still seeds once real disk data actually shows up,
// rather than running its one-shot loop against an empty list and never
// getting a reason to run again -- this is the one scenario the test
// above can't catch, since it always visits Overview (and lets live.frame
// warm up for 20s) before ever navigating to Storage.
test('storage chart still seeds even when it is the very first page visited (live.frame not yet warm)', async ({ page }) => {
  test.setTimeout(60_000);

  const seedResponsePromise = page.waitForResponse(
    (res) => res.url().includes('/api/series') && res.url().includes('kind=host') && res.url().includes('diskio'),
  );
  await page.goto('#/storage');

  const seedResponse = await seedResponsePromise;
  expect(seedResponse.status()).toBe(200);
  const body = (await seedResponse.json()) as { metric: string; points: unknown[] }[];
  const withPoints = body.filter((r) => r.points.length > 0);
  expect(withPoints.length).toBeGreaterThan(0);

  const chart = page.locator('.storage-chart .u-over').first();
  await expect(chart).toBeVisible({ timeout: 2_000 });
});

// Every drive defaults to VISIBLE now (Scott's own follow-up ask dropped
// the old pools/parity/active-only default-hidden set entirely) -- this
// is the one assertion that would catch a regression back to that old
// default, which the seeding test above wouldn't: it only proves ONE
// drive's history reached the chart, not that every fixture disk's own
// legend chip actually starts unhidden.
test('storage chart legend starts with every drive visible, none hidden by default', async ({ page }) => {
  await page.goto('#/storage');
  await expect(page.locator('.storage-chart__legend .storage-chart__chip').first()).toBeVisible();

  const chips = page.locator('.storage-chart__legend .storage-chart__chip');
  await expect.poll(() => chips.count()).toBeGreaterThan(1); // fake mode's fixture array has 8 disks
  const offChips = page.locator('.storage-chart__legend .storage-chart__chip--off');
  expect(await offChips.count()).toBe(0);
});

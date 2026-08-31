import { test, expect, type Page } from '@playwright/test';

// A fetched (non-"now") window's /api/top and /api/series both go
// through the store's 1-minute rollup tier end to end (query.go's own
// TopEntities doc: "SQL-only... never touches the Live ring") -- and
// FlushMinutes (store/flush.go) writes NOTHING on its first call (it
// only records a baseline), so the very first real minute of data isn't
// flushed into that tier until the SECOND 60s tick, i.e. server uptime
// >= ~120s. Below that, /api/top returns [] and this whole fetched-window
// chain (fetchedRows -> fetchedHeroSeries) never fires a single
// /api/series request at all -- confirmed against the real binary while
// building this test (a bare 20s wait, this file's own first draft,
// timed out outright; curling /api/top?resource=gpu&window=1h directly
// returned [] before the 2-minute mark and real rows just after it).
// waitForServerUptime polls the ACTUAL server uptime (not a fixed
// page-relative sleep) so this test costs no more than it has to
// depending on how much of that 2 minutes other specs already spent
// while this one was queued.
async function waitForServerUptime(page: Page, minSeconds: number) {
  await expect
    .poll(
      async () => {
        const res = await page.request.get('/api/healthz');
        const body = (await res.json()) as { uptime_s: number };
        return body.uptime_s;
      },
      { timeout: 180_000, intervals: [2_000] },
    )
    .toBeGreaterThanOrEqual(minSeconds);
}

// Proves the GPU hero chart's fetched-window (1h/24h/7d) composition
// bug is fixed: TopConsumers.svelte's heroSeries used to read only
// resourceMetricKeys('gpu')[0] ("gpu.render.busy_pct") out of a fetched
// container's own /api/series results instead of summing all four
// engines the way topFromFrame already sums them for Now mode (and
// seedHeroSlot already sums them for Now's own ring-tier seed) --
// invisible for cpu/mem (one key each), but for gpu specifically: fake
// mode's own generator (fake.go's emitGPU) only ever populates
// "gpu.video.busy_pct" per container, so the old code's bare first-key
// read was asking for a metric ("gpu.render.busy_pct") that's NEVER
// present on any container in fake mode -- every hero line in a fetched
// GPU window was entirely empty, not merely gapped.
//
// This mirrors live-seed.spec.ts's own crosshair-tooltip technique
// (TimeChart has no DOM "empty state" marker) for the "reached the
// chart" half, plus a direct assertion on the /api/series response
// itself for the "sums all four, not just the first" half.
test('metrics GPU hero chart sums all four engines in a fetched window, not just the first', async ({ page }) => {
  test.setTimeout(240_000);

  await page.goto('#/top/gpu');
  await expect(page.locator('.top-consumers__panel')).toBeVisible();

  await waitForServerUptime(page, 130);

  const [seriesResponse] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes('/api/series') && res.url().includes('kind=container') && res.url().includes('gpu.render.busy_pct'),
    ),
    page.getByRole('group', { name: 'Window' }).getByRole('button', { name: '1h', exact: true }).click(),
  ]);

  expect(seriesResponse.status()).toBe(200);
  const url = new URL(seriesResponse.url());
  expect(url.searchParams.get('kind')).toBe('container');
  const requestedMetrics = (url.searchParams.get('metrics') ?? '').split(',');
  // The fetch itself already asked for all four engines together, even
  // pre-fix -- this is heroSeries' own COMPOSITION of the response that
  // used to be buggy, not the request. Asserted anyway as a sanity check
  // that resourceMetricKeys('gpu') reached the request unchanged.
  expect(requestedMetrics.sort()).toEqual(
    ['gpu.render.busy_pct', 'gpu.video.busy_pct', 'gpu.video-enhance.busy_pct', 'gpu.copy.busy_pct'].sort(),
  );

  const body = (await seriesResponse.json()) as { metric: string; points: unknown[] }[];
  const videoEntry = body.find((r) => r.metric === 'gpu.video.busy_pct');
  // Fake mode's own only-ever-populated engine -- if this has no real
  // points either, the assertion below can't distinguish the fix from a
  // server/fixture problem unrelated to the composition bug.
  expect(videoEntry?.points.length ?? 0).toBeGreaterThan(0);

  const chart = page.locator('.top-consumers__header .u-over').first();
  await expect(chart).toBeVisible({ timeout: 2_000 });
  await chart.hover({ position: { x: 2, y: 10 } });

  const rows = page.locator('.top-consumers__header .time-chart__tooltip-row');
  await expect.poll(() => rows.count()).toBeGreaterThan(0);
  const texts = await rows.allTextContents();
  // Pre-fix, every row here reads "—": summing just gpu.render.busy_pct
  // (always absent in fake mode) leaves every hero line's own points
  // array entirely empty, so the shared x-axis has nothing to hover.
  // Post-fix, the sum equals gpu.video.busy_pct's own real history (the
  // other three engines contribute nothing here, but no longer BLOCK it
  // either).
  expect(texts.some((t) => !t.includes('—'))).toBe(true);
});

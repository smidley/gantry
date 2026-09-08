import { test, expect } from '@playwright/test';
import type { SeriesResult, SnapshotDTO } from '../src/lib/api';

// The eight-disk fake host already needs 20 Overview metrics. Exercise the
// real history API and a full reload: navigation-only tests of the smaller
// container requests missed the host request being rejected in its entirety.
test('Overview restores recorded history after a full page refresh', async ({ page, request }, testInfo) => {
  test.setTimeout(60_000);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  let cpuHistory: SeriesResult[] = [];
  await expect.poll(async () => {
    const to = Math.floor(Date.now() / 1000);
    const response = await request.get(`/api/series?kind=host&metrics=cpu.total&from=${to - 900}&to=${to}`);
    expect(response.ok()).toBe(true);
    cpuHistory = await response.json();
    return new Set(cpuHistory[0].points.map(([ts]) => ts)).size;
  }, { timeout: 25_000, intervals: [1000] }).toBeGreaterThan(8);
  // Linux CI also runs the host collector. If both collectors record the
  // same second, the chart keeps that second's last reading.
  const oldestTS = Math.min(...cpuHistory[0].points.map(([ts]) => ts));
  const oldestValue = cpuHistory[0].points.filter(([ts]) => ts === oldestTS).at(-1)![1].toFixed(1) + '%';
  const snapshot = await (await request.get('/api/live/snapshot')).json() as SnapshotDTO;
  const metrics = Object.keys(snapshot.host).filter(metric =>
    ['cpu.total', 'mem.used_pct'].includes(metric)
    || (metric.startsWith('net.') && /\.(rx|tx)_bps$/.test(metric))
    || (metric.startsWith('diskio.') && /\.(read|write)_bps$/.test(metric)),
  );
  expect(metrics.length).toBeGreaterThan(16);

  const batches: { metrics: string[]; status: number }[] = [];
  page.on('response', response => {
    const url = new URL(response.url());
    if (url.pathname === '/api/series' && url.searchParams.get('kind') === 'host') {
      batches.push({ metrics: url.searchParams.get('metrics')!.split(','), status: response.status() });
    }
  });
  const cpuTile = page.locator('.overview__metrics-rail .stat-tile[href="#/top/cpu"]');
  await page.goto('#/');
  await expect(cpuTile).toBeVisible();
  await expect.poll(() => batches.flatMap(batch => batch.metrics).length).toBeGreaterThanOrEqual(metrics.length);

  batches.length = 0;
  await page.reload();
  await expect(cpuTile).toBeVisible();
  await expect.poll(() => batches.flatMap(batch => batch.metrics).length).toBeGreaterThanOrEqual(metrics.length);
  await page.screenshot({ path: testInfo.outputPath('overview-after-refresh.png'), fullPage: true });
  expect(batches.every(batch => batch.status === 200), JSON.stringify(batches)).toBe(true);
  expect(batches.every(batch => batch.metrics.length <= 16)).toBe(true);

  // Hovering the oldest point must recover the server's earlier reading,
  // not a new sample collected since reload. The shared cursor also makes
  // all four tiles show their historical timestamp.
  await cpuTile.locator('.sparkline').hover({ position: { x: 1, y: 30 } });
  await expect(cpuTile.locator('.stat-tile__number')).toHaveText(oldestValue);
  const chips = page.locator('.overview__metrics-rail .stat-tile__chip--visible');
  await expect(chips).toHaveCount(4);
  for (const chip of await chips.all()) await expect(chip).toHaveText(/\d+[sm] ago/);
});

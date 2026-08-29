import { test, expect, type Page } from '@playwright/test';

// Container Detail's allocation display + limits listing (Scott: "I want
// to know how much of the system resources it's consuming, AND how much
// of it's own allocated resources it's consuming" + "containers that
// have limits for things like cpu cores or memory should list them").
// Fake mode's own demo scenarios (internal/fake/fake.go): "postgres" is
// memory-limited, "minecraft" is cpuset-pinned (also carrying a matching
// cpu.alloc_cores ceiling), and every other running archetype (e.g.
// "jellyfin") carries no memory/CPU/cpuset ceiling at all -- only the
// universal pids.limit every fake (and real) container gets.

function chartCard(page: Page, label: string) {
  return page.locator('.container-detail__chart-card', { has: page.locator('.microlabel', { hasText: label }) });
}

test('a memory-limited container lists its limit and shows percent-of-limit alongside percent-of-host', async ({ page }) => {
  await page.goto('#/containers/postgres');

  const limits = page.locator('.container-detail__limits');
  await expect(limits).toBeVisible({ timeout: 10_000 });
  await expect(limits).toContainText('memory 1.6 GiB');
  await expect(limits).toContainText('pids 2048');
  // postgres has no cpu.alloc_cores/cpuset -- the facts line must not
  // grow a CPU or pinned segment it has no data for.
  await expect(limits).not.toContainText('CPU');
  await expect(limits).not.toContainText('pinned');

  const memoryCard = chartCard(page, 'Memory');
  await expect(memoryCard).toContainText(/of host/, { timeout: 10_000 });
  await expect(memoryCard).toContainText(/% of its limit/);
});

test('a cpuset-pinned container shows its actual pin set and CPU allocation percentage', async ({ page }) => {
  await page.goto('#/containers/minecraft');

  const limits = page.locator('.container-detail__limits');
  await expect(limits).toBeVisible({ timeout: 10_000 });
  await expect(limits).toContainText('CPU 2.0 cores');
  await expect(limits).toContainText('pinned to 2 cores: 0-1');
  // minecraft has no memory ceiling of its own.
  await expect(limits).not.toContainText('memory');

  const cpuCard = chartCard(page, 'CPU');
  await expect(cpuCard).toContainText(/% of its allocation/, { timeout: 10_000 });
  // Allocation rides as its own chart series (same pattern as the
  // existing Throttled line) -- a second legend row confirms it's
  // actually plotted, not just named in the stat text.
  await expect(cpuCard.getByText('Allocation', { exact: true })).toBeVisible();
});

test('a fully-unlimited container shows no limits chrome beyond the universal pids ceiling', async ({ page }) => {
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.container-detail__charts canvas').first()).toBeVisible({ timeout: 10_000 });

  // pids.limit is a real-box universal default (every container gets
  // one), so the Limits line is never fully absent -- but it must carry
  // nothing else for a container with no memory/CPU/cpuset ceiling of
  // its own.
  const limits = page.locator('.container-detail__limits');
  await expect(limits).toHaveText('Limits: pids 2048');

  const cpuCard = chartCard(page, 'CPU');
  const memoryCard = chartCard(page, 'Memory');
  await expect(cpuCard).not.toContainText('of its allocation');
  await expect(memoryCard).not.toContainText('of its limit');
  await expect(cpuCard.getByText('Allocation', { exact: true })).toHaveCount(0);
});

test('pids shows quietly in the metadata card', async ({ page }) => {
  await page.goto('#/containers/jellyfin');

  const pidsRow = page.locator('.container-detail__meta-list dt', { hasText: 'Pids' });
  await expect(pidsRow).toBeVisible({ timeout: 10_000 });
  const pidsValue = page.locator('.container-detail__meta-list dd').last();
  await expect(pidsValue).toHaveText(/^\d+ \/ 2048$/);
});

test('limits and dual-perspective stats remain readable at 375px', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('#/containers/minecraft');

  const limits = page.locator('.container-detail__limits');
  await expect(limits).toBeVisible({ timeout: 10_000 });
  await expect(limits).toContainText('pinned to 2 cores: 0-1');

  const overflowX = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflowX).toBeLessThanOrEqual(1);
});

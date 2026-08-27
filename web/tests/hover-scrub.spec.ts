import { test, expect } from '@playwright/test';

// Hover-scrub (this branch's own feature): mousing over a stat-tile's
// sparkline should scrub its hero number to the hovered point's value
// and show a relative-time chip; leaving should clear both and let the
// number resume live ticking. Real pointer events via page.mouse.move
// (not dispatchEvent) so this exercises the actual pointermove/
// pointerleave handlers Sparkline.svelte binds, not a synthetic
// shortcut around them.
test('overview: hovering a stat-tile sparkline scrubs the hero number, leaving restores live ticking', async ({
  page,
}) => {
  await page.goto('#/');

  const tile = page.locator('.overview__tiles .stat-tile').first();
  await expect(tile).toBeVisible();

  const number = tile.locator('.stat-tile__number');
  const chip = tile.locator('.stat-tile__chip--visible');
  const sparkline = tile.locator('.sparkline');
  await expect(sparkline).toBeVisible();

  // A few real ticks of history (2s cadence, plus Overview's own
  // on-mount history seed) so a hover away from the sparkline's
  // rightmost edge below resolves to a genuinely real past sample
  // rather than racing an empty, not-yet-seeded ring.
  await page.waitForTimeout(4_000);

  const before = await number.textContent();
  await expect(chip).toHaveCount(0);

  const box = await sparkline.boundingBox();
  if (!box) throw new Error('sparkline has no bounding box');
  // 15% across the sparkline's own width, not its rightmost edge: the
  // live window spans 15 minutes but only the last few real ticks exist
  // yet, so anywhere left of "now" clamps to that earliest real sample --
  // near-certainly different from the ever-jittering live number.
  await page.mouse.move(box.x + box.width * 0.15, box.y + box.height / 2);

  await expect(chip).toHaveCount(1);
  await expect.poll(() => number.textContent()).not.toBe(before);
  const scrubbed = await number.textContent();

  // Off the sparkline entirely (well above its 28px strip, still over
  // the tile's own label/value area) -- pointerleave clears scrub state.
  await page.mouse.move(box.x, box.y - 40);

  await expect(chip).toHaveCount(0);
  // Same shape as the existing "CPU tile ticks" test: not a fixed sleep,
  // just polling for the number to have actually changed again -- first
  // via StatTile's own release ease back to live, then ordinary ticking.
  await expect.poll(() => number.textContent(), { timeout: 6_000 }).not.toBe(scrubbed);
});

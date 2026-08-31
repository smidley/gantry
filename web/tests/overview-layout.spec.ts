import { test, expect } from '@playwright/test';

// Computed-style verification for the region-sizing pass on the status
// band's two visual components (FleetStrip's fixed-pitch grid,
// BaySchematic's content-sized footprint) -- the half their
// svelte/server structural tests can't see, asserted in a real browser
// against the fake-mode binary. Kept intentionally component-scoped:
// the Overview-level re-balance of the band itself lands with the
// Overview integration patch, so nothing here depends on Overview's own
// class names beyond navigating to the page the components render on.

test('fleet strip lays units on a fixed-pitch grid -- whole columns, aligned across rows', async ({ page }) => {
  await page.goto('#/');
  const strip = page.locator('.fleet-strip').first();
  await expect(strip).toBeVisible();
  await expect.poll(() => page.locator('.fleet-strip .fleet-unit').count()).toBeGreaterThan(0);

  expect(await strip.evaluate((el) => getComputedStyle(el).display)).toBe('grid');

  // auto-fill resolves to N identical explicit 8px tracks -- the
  // "deliberate grid, not ragged lines" contract: every row breaks on
  // the same whole-unit boundaries, columns align vertically by
  // construction, and no unit is ever clipped at the edge.
  const columns = await strip.evaluate((el) => getComputedStyle(el).gridTemplateColumns);
  expect(columns).toMatch(/^8px( 8px)*$/);
});

test('storage array fills its module and keeps its device grid stable on hover', async ({ page }) => {
  await page.goto('#/');
  const schematic = page.locator('.bay-schematic');
  // Waits on the first live frame's disks; generous for a cold CI boot.
  await expect(schematic).toBeVisible({ timeout: 20_000 });

  const schematicBox = await schematic.boundingBox();
  const parentWidth = await schematic.evaluate((el) => el.parentElement!.getBoundingClientRect().width);
  expect(schematicBox).not.toBeNull();

  // Only meaningful when the surrounding region is actually wider than
  // the fake array's handful of bars -- at the default desktop viewport
  // it always is. If a future layout narrows the parent below that,
  // this assertion has nothing to prove and the guard keeps it honest
  // rather than flaky.
  test.skip(parentWidth < 400, `schematic parent is only ${parentWidth}px wide -- nothing to measure against`);

  expect(schematicBox!.width).toBeGreaterThan(parentWidth - 4);

  // Hovering a device reveals richer detail without changing the
  // module's width or reflowing the surrounding overview.
  await schematic.locator('.bay-schematic__bar').first().hover();
  await expect(schematic.locator('.bay-schematic__label--visible')).toBeVisible();
  const hoveredBox = await schematic.boundingBox();
  expect(hoveredBox!.width).toBeCloseTo(schematicBox!.width, 0);
});

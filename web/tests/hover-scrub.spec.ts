import { test, expect } from '@playwright/test';

// Hover-scrub (this branch's own feature, now synced across surfaces):
// mousing over a stat-tile's sparkline should scrub its hero number to
// the hovered point's value and show a relative-time chip, hold rock
// steady while genuinely stationary even as live frames keep arriving,
// auto-scrub every OTHER related metric tile on the page to the same
// instant (Scott's own requirement), and release all of them back to
// live ticking on leave. Real pointer events via page.mouse.move (not
// dispatchEvent) so this exercises the actual pointermove/pointerleave
// handlers Sparkline.svelte binds, not a synthetic shortcut around them.
test('overview: hovering one stat-tile sparkline scrubs and pins every tile in sync, leaving releases all of them', async ({
  page,
}) => {
  // The 20s pre-hover wait below plus the 5s hold loop and two release
  // polls comfortably exceed Playwright's own 30s default per-test
  // timeout -- same reasoning, and the same figure, as live-seed.spec's
  // own test.setTimeout call.
  test.setTimeout(60_000);

  await page.goto('#/');

  const tiles = page.locator('.overview__tiles .stat-tile');
  const tile1 = tiles.nth(0);
  const tile2 = tiles.nth(1);
  await expect(tile1).toBeVisible();
  await expect(tile2).toBeVisible();

  const number1 = tile1.locator('.stat-tile__number');
  const number2 = tile2.locator('.stat-tile__number');
  const chip1 = tile1.locator('.stat-tile__chip--visible');
  const chip2 = tile2.locator('.stat-tile__chip--visible');
  const sparkline1 = tile1.locator('.sparkline');
  await expect(sparkline1).toBeVisible();

  // Real ticks of history (2s cadence) so a hover away from the
  // sparkline's rightmost edge below resolves to a genuinely real past
  // sample rather than racing an empty, barely-seeded ring -- verified
  // live while building this: at only ~4s of ring depth, "the earliest
  // available point" and "the current live tick" are close enough in
  // both time AND value that they can round to the identical displayed
  // string, making the assertions below spuriously fail on a fresh
  // server with nothing wrong. 20s (matching live-seed.spec's own
  // precedent for "let the fake collector accumulate real ticks")
  // gives the metric enough real ticks -- and, per the fake generator's
  // own per-tick spike chance, a good likelihood of at least one -- to
  // reliably differ from "right now" at the 1-decimal precision these
  // tiles render.
  await page.waitForTimeout(20_000);

  const before1 = await number1.textContent();
  const before2 = await number2.textContent();
  await expect(chip1).toHaveCount(0);
  await expect(chip2).toHaveCount(0);

  const box = await sparkline1.boundingBox();
  if (!box) throw new Error('sparkline has no bounding box');
  // 15% across the sparkline's own width, not its rightmost edge: the
  // live window spans 15 minutes but only the last few real ticks exist
  // yet, so anywhere left of "now" clamps to that earliest real sample --
  // near-certainly different from the ever-jittering live number.
  await page.mouse.move(box.x + box.width * 0.15, box.y + box.height / 2);

  await expect(chip1).toHaveCount(1);
  await expect.poll(() => number1.textContent()).not.toBe(before1);

  // Sync assertion: tile 2's own sparkline was never touched, but it
  // shares the same page-global scrub bus -- its chip must appear and
  // its own number must pin to ITS metric's value at the same shared
  // instant, purely because tile 1 is being scrubbed.
  await expect(chip2).toHaveCount(1);
  await expect.poll(() => number2.textContent()).not.toBe(before2);

  // The two polls above resolve as soon as each number ticks AWAY from
  // its pre-hover text -- which can be the instant the fast (120ms)
  // scrub-follow tween starts easing, not once it's actually settled.
  // Let that tween fully finish before snapshotting the "pinned" values
  // the hold loop below compares against, or a snapshot taken mid-ease
  // would spuriously differ from the later, fully-settled reading.
  await page.waitForTimeout(300);
  const scrubbed1 = await number1.textContent();
  const scrubbed2 = await number2.textContent();

  // Hold across >=2 live frame arrivals (2s cadence) with the pointer
  // genuinely stationary. This is the regression test for the
  // pixel-anchored bug the review caught (recomputing "now" on every
  // pointermove let a jittery-but-stationary pointer creep the
  // published ts forward through history): both pinned numbers must
  // stay byte-identical for the WHOLE hold, not just "eventually
  // settle" -- neither a live frame arriving nor any internal
  // re-derivation may perturb an already-established scrub target.
  for (let i = 0; i < 5; i++) {
    await page.waitForTimeout(1_000);
    expect(await number1.textContent()).toBe(scrubbed1);
    expect(await number2.textContent()).toBe(scrubbed2);
  }

  // Off the sparkline entirely (well above its 28px strip, still over
  // the tile's own label/value area) -- pointerleave clears the bus,
  // releasing every synced tile back to live, not just tile 1.
  await page.mouse.move(box.x, box.y - 40);

  await expect(chip1).toHaveCount(0);
  await expect(chip2).toHaveCount(0);
  // Same shape as the original "CPU tile ticks" smoke test: not a fixed
  // sleep, just polling for each number to have actually changed again --
  // first via StatTile's own release ease back to live, then ordinary
  // ticking.
  await expect.poll(() => number1.textContent(), { timeout: 6_000 }).not.toBe(scrubbed1);
  await expect.poll(() => number2.textContent(), { timeout: 6_000 }).not.toBe(scrubbed2);
});

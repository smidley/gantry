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

  const tiles = page.locator('.overview__metrics-rail .stat-tile');
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

  // page.mouse.move below is a raw viewport-coordinate API (unlike a
  // locator action) and does NOT auto-scroll -- this sandbox's own
  // SourcesBanner routinely stacks several real degraded-source cards
  // above the rail (gpu/host/nvidia/pressure/unraid all non-"ok" here),
  // and the legibility corrective pass's own larger microlabels/numbers
  // grew the page just enough that tile1 can now sit below the fold too
  // (reproduced live: this test started failing the moment those sizes
  // grew, purely from the scroll position shifting under it).
  await sparkline1.scrollIntoViewIfNeeded();
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

// Scrub-parity corrective pass: on a two-value tile (Network's down+up
// rates, Disk IO's read+write), the hero number used to pin correctly
// while scrubbing but the SECOND value kept live-ticking -- it had no
// ring of its own to look a past instant up in (Scott: "the smaller
// item keeps changing"). Network (the third rail row) is this test's
// target since it's the first two-value tile in DOM order.
test('overview: scrubbing a two-value tile pins its secondary value too, not just the hero number', async ({ page }) => {
  test.setTimeout(60_000);

  await page.goto('#/');

  const networkTile = page.locator('.overview__metrics-rail .stat-tile').nth(2);
  await expect(networkTile).toBeVisible();
  // Not `.microlabel` alone: in bare mode the tile's OWN scrub chip is
  // also a (normally empty) `.microlabel` span that comes first in DOM
  // order, so an unscoped `.first()` would match it instead of the row's
  // actual label -- scope to the row-label wrapper specifically.
  await expect(networkTile.locator('.stat-tile__row-label .microlabel')).toHaveText('NETWORK', { ignoreCase: true });

  const heroNumber = networkTile.locator('.stat-tile__number');
  const secondaryValue = networkTile.locator('.stat-tile__value2');
  const chip = networkTile.locator('.stat-tile__chip--visible');
  const sparkline = networkTile.locator('.sparkline');

  // Same real-history rationale as the sync test above: enough accumulated
  // ticks that "a past instant" reliably differs from "right now".
  await page.waitForTimeout(20_000);

  const beforeHero = await heroNumber.textContent();
  const beforeSecondary = await secondaryValue.textContent();
  await expect(chip).toHaveCount(0);

  // Network is the rail's third row -- on a fresh page load this sandbox's
  // own SourcesBanner routinely has 5 real degraded-source cards stacked
  // above it (reproduced live: gpu/host/nvidia/pressure/unraid all
  // non-"ok" here), which alone can push it below the fold. page.mouse.move
  // (a raw viewport-coordinate API, unlike a locator action) does NOT
  // auto-scroll, so without this the move below silently lands nowhere
  // near the sparkline and the chip never appears -- not a product bug,
  // a test setup one.
  await sparkline.scrollIntoViewIfNeeded();
  const box = await sparkline.boundingBox();
  if (!box) throw new Error('sparkline has no bounding box');
  await page.mouse.move(box.x + box.width * 0.15, box.y + box.height / 2);

  await expect(chip).toHaveCount(1);
  await expect.poll(() => heroNumber.textContent()).not.toBe(beforeHero);
  // The bug this test guards: the secondary value must ALSO move away
  // from its pre-hover reading, not keep showing whatever's currently live.
  await expect.poll(() => secondaryValue.textContent()).not.toBe(beforeSecondary);

  await page.waitForTimeout(300); // let the fast scrub-follow tween settle, same as the sync test above
  const pinnedHero = await heroNumber.textContent();
  const pinnedSecondary = await secondaryValue.textContent();

  // Hold across >=2 live frame arrivals: BOTH values must stay
  // byte-identical, not just the hero number.
  for (let i = 0; i < 3; i++) {
    await page.waitForTimeout(1_000);
    expect(await heroNumber.textContent()).toBe(pinnedHero);
    expect(await secondaryValue.textContent()).toBe(pinnedSecondary);
  }

  await page.mouse.move(box.x, box.y - 40);
  await expect(chip).toHaveCount(0);
  await expect.poll(() => heroNumber.textContent(), { timeout: 6_000 }).not.toBe(pinnedHero);
  await expect.poll(() => secondaryValue.textContent(), { timeout: 6_000 }).not.toBe(pinnedSecondary);
});

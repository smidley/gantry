import { test, expect } from '@playwright/test';

// Browser-side verification for the fleet field and the bay schematic --
// the half their svelte/server structural tests can't see. The fleet's
// sizing RULE is pure and unit-tested across counts and areas
// (src/lib/fleetGrid.test.ts); what needs a real browser is that the
// rule is actually wired to a real measurement: that the grid renders
// the cell size it computed, that the size genuinely falls as the fleet
// grows in one fixed viewport, that the field claims the space beneath
// it, and that a glowing block says what is driving it.
//
// Container COUNT is the independent variable in most of this, so those
// specs route their own /api/live frame (the smoke/customize specs' own
// idiom) rather than depending on however many containers the box
// happens to be running. EventSource re-connects when the fulfilled
// body ends (retry: 300) and re-receives the same frame every ~300ms.

function frame(containers: Record<string, object>) {
  return {
    ts: Math.floor(Date.now() / 1000),
    unraid_version: '7.0.0',
    host: { 'cpu.total': 12.5, 'mem.used_pct': 40, 'mem.used_bytes': 12.8e9 },
    containers,
    disks: {
      disk1: { 'fs.used_bytes': 4e12, 'fs.free_bytes': 4e12, 'temp.c': 38.2, errors: 0 },
      cache: { 'fs.used_bytes': 2e11, 'fs.free_bytes': 3e11, 'temp.c': 41.5, errors: 0 },
    },
    disk_meta: { disk1: { device: 'sdb', kind: 'hdd' }, cache: { device: 'nvme0n1', kind: 'nvme' } },
    unraid: { array: { 'array.started': 1, 'mover.running': 0 } },
    gpu: {},
    gpu_meta: {},
    sources: { docker: 'ok' },
    alerts: { firing: [], firing_count: 0, truncated: 0, channels: {} },
    insights: { active: [], tier: 'proxy', suppressed: 0 },
  };
}

// quietFleet: n running containers (plus `stopped` exited ones), all
// well under every activity floor, so nothing glows and the only
// variable is the count.
function quietFleet(n: number, extra: Record<string, object> = {}, stopped = 0) {
  const containers: Record<string, object> = {};
  for (let i = 0; i < n; i++) {
    containers[`svc-${String(i + 1).padStart(2, '0')}`] = {
      state: 'running',
      health: 'healthy',
      icon: '',
      metrics: { 'cpu.pct': 0.2, 'mem.bytes': 2e8 },
    };
  }
  for (let i = 0; i < stopped; i++) {
    containers[`old-${String(i + 1).padStart(2, '0')}`] = { state: 'exited', health: '', icon: '', metrics: {} };
  }
  return frame({ ...containers, ...extra });
}

async function routeLiveFrame(page: import('@playwright/test').Page, f: object) {
  await page.unroute('**/api/live');
  await page.route('**/api/live', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: `retry: 300\nevent: frame\ndata: ${JSON.stringify(f)}\n\n`,
    }),
  );
}

// showFleet routes a frame, loads Overview and waits for the stack to
// hold exactly the expected number of blocks AND to have been measured.
// data-cell is written from the ResizeObserver-driven fit, so it is the
// honest "the sizing pass has run" signal -- and it lives on the STACK,
// which is the one element per fleet whatever its group split.
async function showFleet(page: import('@playwright/test').Page, f: object, expectedUnits: number) {
  await routeLiveFrame(page, f);
  await page.goto('#/');
  const stack = page.locator('.fleet-strip__stack');
  await expect(stack).toBeVisible();
  await expect.poll(() => page.locator('.fleet-strip .fleet-unit').count()).toBe(expectedUnits);
  let cell = 0;
  await expect(async () => {
    cell = Number(await stack.getAttribute('data-cell'));
    expect(cell).toBeGreaterThan(0);
  }).toPass({ timeout: 10_000 });
  return cell;
}

test('fleet blocks render at the computed cell size, on whole aligned columns', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const cell = await showFleet(page, quietFleet(24), 24);

  const strip = page.locator('.fleet-strip').first();
  expect(await strip.evaluate((el) => getComputedStyle(el).display)).toBe('grid');

  // Every explicit track is the SAME computed cell -- the "deliberate
  // grid, not ragged lines" contract: rows break on the same whole-unit
  // boundaries, columns align vertically by construction, and no unit is
  // ever clipped at the edge. (The literal number is measurement-driven
  // now, so the assertion is that the tracks agree with the fit, not
  // that they are any particular width.)
  const columns = (await strip.evaluate((el) => getComputedStyle(el).gridTemplateColumns)).split(' ');
  expect(columns.length).toBeGreaterThan(1);
  for (const track of columns) {
    expect(Math.abs(parseFloat(track) - cell), `track ${track} vs computed cell ${cell}px`).toBeLessThan(1.5);
  }

  // And the blocks themselves are that size, and square.
  const unitBox = await page.locator('.fleet-strip .fleet-unit').first().boundingBox();
  expect(Math.abs(unitBox!.width - cell)).toBeLessThan(1.5);
  expect(Math.abs(unitBox!.height - unitBox!.width)).toBeLessThan(1.5);

  // The grid never forces the page sideways.
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
});

// The ask itself: "if there's only 3 containers, they will be larger,
// and if there are 30 containers, the blocks will be smaller." Same
// viewport, same field, only the count changes.
//
// The assertion is the RELATIONSHIP, never a pixel: the cell is derived
// from a live measurement of a real layout, so its exact value moves
// with the viewport, the font metrics and whatever else shares the page
// -- and this test flaked once on a loaded CI runner for exactly that
// reason. What must hold is that the fit is monotonic in the count
// (more blocks never buys a bigger one) and that across the full range
// the difference is real rather than everything sitting on the ceiling.
test('fleet blocks shrink as the fleet grows, in one fixed viewport', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const three = await showFleet(page, quietFleet(3), 3);
  const thirty = await showFleet(page, quietFleet(30), 30);
  const sixty = await showFleet(page, quietFleet(60), 60);
  const hundred = await showFleet(page, quietFleet(100), 100);

  // Monotonic: a bigger fleet never renders a bigger block. Below a
  // dozen or so blocks in a full-width band everything legitimately
  // sits on the ceiling, so adjacent steps may tie.
  expect(three, '30 containers must not render larger than 3').toBeGreaterThanOrEqual(thirty);
  expect(thirty, '60 containers must not render larger than 30').toBeGreaterThanOrEqual(sixty);
  expect(sixty, '100 containers must not render larger than 60').toBeGreaterThanOrEqual(hundred);

  // And across the whole range the shrink is real, not a tie -- that is
  // the behaviour the ask is actually about.
  expect(hundred, '100 containers must render meaningfully smaller than 3').toBeLessThan(three);
  // Still a real, tappable block at the big end, not a speck.
  expect(hundred).toBeGreaterThanOrEqual(12);
});

// "Keep the stopped containers in a separate section like we had
// before" -- two grids, running first, the stopped one under its own
// heading, both at ONE pitch so they read as one fleet.
test('stopped containers get their own labelled section at the same cell size', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(18, {}, 5), 23);

  const grids = page.locator('.fleet-strip');
  await expect(grids).toHaveCount(2);
  await expect(grids.nth(0)).toHaveAttribute('aria-label', 'Running containers, 18');
  await expect(grids.nth(1)).toHaveAttribute('aria-label', 'Stopped containers, 5');

  // The heading sits between them, in the legend's own vocabulary.
  const heading = page.locator('.fleet-strip__group-label');
  await expect(heading).toHaveText('5 stopped');
  const runningBox = (await grids.nth(0).boundingBox())!;
  const headingBox = (await heading.boundingBox())!;
  const stoppedBox = (await grids.nth(1).boundingBox())!;
  expect(headingBox.y).toBeGreaterThanOrEqual(runningBox.y + runningBox.height - 1);
  expect(stoppedBox.y).toBeGreaterThanOrEqual(headingBox.y + headingBox.height - 1);

  // ONE pitch across both: same track sizes, same block size.
  const tracks = async (i: number) =>
    (await grids.nth(i).evaluate((el) => getComputedStyle(el).gridTemplateColumns)).split(' ');
  const runningTracks = await tracks(0);
  const stoppedTracks = await tracks(1);
  expect(stoppedTracks[0]).toBe(runningTracks[0]);
  expect(stoppedTracks.length).toBe(runningTracks.length);

  const firstRunning = (await grids.nth(0).locator('.fleet-unit').first().boundingBox())!;
  const firstStopped = (await grids.nth(1).locator('.fleet-unit').first().boundingBox())!;
  expect(Math.abs(firstStopped.width - firstRunning.width)).toBeLessThan(1.5);
  expect(Math.abs(firstStopped.height - firstRunning.height)).toBeLessThan(1.5);
  // Stopped blocks are still real links with their own state.
  await expect(grids.nth(1).locator('.fleet-unit').first()).toHaveClass(/fleet-unit--stopped/);
  await expect(grids.nth(1).locator('.fleet-unit').first()).toHaveAttribute('href', '#/containers/old-01');
});

test('a fleet with nothing stopped renders no stopped section at all', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(9), 9);
  await expect(page.locator('.fleet-strip')).toHaveCount(1);
  await expect(page.locator('.fleet-strip__group-label')).toHaveCount(0);
});

// "Container fleet section should be sized to take up the available
// screen space beneath it" -- with a fleet big enough to want the room,
// the field reaches well down the viewport instead of sitting at a
// fixed strip height, and it grows with the fleet rather than scrolling.
test('the fleet field claims the space beneath it in the all-clear layout', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });

  await showFleet(page, quietFleet(3), 3);
  await expect(page.locator('.overview__clear-band')).toBeVisible();
  const small = (await page.locator('.fleet-strip__field').boundingBox())!;

  await showFleet(page, quietFleet(60), 60);
  const large = (await page.locator('.fleet-strip__field').boundingBox())!;

  expect(large.height, 'a big fleet must claim more of the page than a tiny one').toBeGreaterThan(small.height);
  // It reaches into the lower half of the viewport rather than stopping
  // at a fixed strip height near the top.
  expect(large.y + large.height).toBeGreaterThan(450);
  // And it stops short of the bottom, so the next section still peeks.
  expect(large.y + large.height).toBeLessThanOrEqual(900);

  // A small fleet does NOT hold that space open under three blocks.
  expect(small.height).toBeLessThan(large.height);
});

// The glow explains itself: a container elevated on something other than
// CPU still lights up, and the hover label names the metric and value.
test('a block glowing on disk IO names that metric in its hover label', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(
    page,
    quietFleet(5, {
      seeder: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 0.2, 'mem.bytes': 3e8, 'io.read_bps': 84e6 } },
    }),
    6,
  );

  const seeder = page.locator('.fleet-strip .fleet-unit[href="#/containers/seeder"]');
  await expect(seeder).toHaveClass(/fleet-unit--active/);
  await expect(seeder).toHaveAttribute('data-metric', 'io');
  // Nothing else in the fleet is doing anything, so nothing else glows.
  await expect(page.locator('.fleet-strip .fleet-unit--active')).toHaveCount(1);

  const label = page.locator('.fleet-strip__label');
  await expect(label).not.toHaveClass(/fleet-strip__label--visible/);
  await seeder.hover();
  await expect(label).toHaveClass(/fleet-strip__label--visible/);
  await expect(label).toContainText('seeder');
  await expect(label).toContainText('glowing: disk IO 84.0 MB/s');

  // Keyboard focus reveals the same thing -- the label is not mouse-only.
  await page.mouse.move(0, 0);
  await seeder.focus();
  await expect(label).toContainText('glowing: disk IO 84.0 MB/s');
});

// The bay schematic is a Customize MODULE now, not a pinned half of the
// status band -- it renders inside the modules band's narrow lane, where
// the metrics rail used to sit. Everything else about it is unchanged.
test('storage array renders as a module in the narrow lane', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');

  const module = page.locator('.overview__modules-narrow [data-module="storage"]');
  await expect(module).toBeVisible({ timeout: 20_000 });
  await expect(module.locator('.bay-schematic')).toBeVisible();

  // It is genuinely in the modules band, not in the status band above it.
  await expect(page.locator('.overview__clear-band .bay-schematic, .overview__status-band .bay-schematic')).toHaveCount(0);

  // The rail is no longer a module at all -- it is pinned above the
  // headline, which is the whole point of the swap.
  await expect(page.locator('[data-module="metrics-rail"]')).toHaveCount(0);
  const rail = page.locator('.overview__metrics-rail');
  await expect(rail).toBeVisible();
  const railBox = (await rail.boundingBox())!;
  const headlineBox = (await page.locator('.overview__headline-zone').boundingBox())!;
  expect(railBox.y + railBox.height).toBeLessThanOrEqual(headlineBox.y + 1);
});

test('storage array fills its module and keeps its device grid stable on hover', async ({ page }) => {
  // Explicitly wide: the schematic lives in the modules band's NARROW
  // lane now, which at the default 1280px viewport is under the 400px
  // this test needs to have anything to measure -- and a test that
  // silently skips on every run proves nothing at all.
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto('#/');
  const schematic = page.locator('.bay-schematic');
  // Waits on the first live frame's disks; generous for a cold CI boot.
  await expect(schematic).toBeVisible({ timeout: 20_000 });

  const schematicBox = await schematic.boundingBox();
  // The module CARD has its own padding, so the space the schematic is
  // actually offered is its parent's content box, not its border box.
  const parentWidth = await schematic.evaluate((el) => {
    const parent = el.parentElement!;
    const style = getComputedStyle(parent);
    return parent.getBoundingClientRect().width - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
  });
  expect(schematicBox).not.toBeNull();

  // Only meaningful when the surrounding region is actually wider than
  // the fake array's handful of bars. If a future layout narrows the
  // lane below that, this assertion has nothing to prove and the guard
  // keeps it honest rather than flaky.
  test.skip(parentWidth < 400, `schematic parent is only ${parentWidth}px wide -- nothing to measure against`);

  expect(schematicBox!.width).toBeGreaterThan(parentWidth - 4);

  // Hovering a device reveals richer detail without changing the
  // module's width or reflowing the surrounding overview.
  await schematic.locator('.bay-schematic__bar').first().hover();
  await expect(schematic.locator('.bay-schematic__label--visible')).toBeVisible();
  const hoveredBox = await schematic.boundingBox();
  expect(hoveredBox!.width).toBeCloseTo(schematicBox!.width, 0);
});

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

// quietFleet: n running containers, all well under every activity floor,
// so nothing glows and the only variable is the count.
function quietFleet(n: number, extra: Record<string, object> = {}) {
  const containers: Record<string, object> = {};
  for (let i = 0; i < n; i++) {
    containers[`svc-${String(i + 1).padStart(2, '0')}`] = {
      state: 'running',
      health: 'healthy',
      icon: '',
      metrics: { 'cpu.pct': 0.2, 'mem.bytes': 2e8 },
    };
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

// showFleet routes a frame, loads Overview and waits for the strip to
// hold exactly the expected number of blocks AND to have been measured
// (data-cell is written from the ResizeObserver-driven fit, so it is the
// honest "the sizing pass has run" signal).
async function showFleet(page: import('@playwright/test').Page, f: object, expectedUnits: number) {
  await routeLiveFrame(page, f);
  await page.goto('#/');
  const strip = page.locator('.fleet-strip');
  await expect(strip).toBeVisible();
  await expect.poll(() => page.locator('.fleet-strip .fleet-unit').count()).toBe(expectedUnits);
  let cell = 0;
  await expect(async () => {
    cell = Number(await strip.getAttribute('data-cell'));
    expect(cell).toBeGreaterThan(0);
  }).toPass({ timeout: 10_000 });
  return cell;
}

test('fleet blocks render at the computed cell size, on whole aligned columns', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const cell = await showFleet(page, quietFleet(24), 24);

  const strip = page.locator('.fleet-strip');
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
test('fleet blocks shrink as the fleet grows, in one fixed viewport', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const three = await showFleet(page, quietFleet(3), 3);
  const thirty = await showFleet(page, quietFleet(30), 30);
  const sixty = await showFleet(page, quietFleet(60), 60);

  expect(three, '3 containers must render larger than 30').toBeGreaterThan(thirty);
  expect(thirty, '30 containers must render larger than 60').toBeGreaterThan(sixty);
  // Still a real, tappable block at the big end, not a speck.
  expect(sixty).toBeGreaterThanOrEqual(12);
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

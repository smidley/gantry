import { test, expect } from '@playwright/test';

// Verify usable fleet targets, predictable wrapping, and metrics ahead of
// the customizable lanes. Known frames keep geometry independent of uptime.
const CELL_W = 28;
const CELL_H = 28;
const COL_PITCH = 34;

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

// routeLiveFrame installs ONE handler per page and then only ever swaps
// the frame it serves. It deliberately does not unroute-and-re-route,
// which is what it used to do and which left a window with no handler at
// all: the EventSource reconnects every ~300ms, and a reconnect landing
// in that window reaches the REAL /api/live -- a stream that never ends,
// so it never reconnects again and the mock never applies for the rest
// of the test. Seen as a spec asking for 100 containers and being handed
// the machine's own fleet, indefinitely, until the poll gave up.
const routedFrames = new WeakMap<import('@playwright/test').Page, object>();

async function routeLiveFrame(page: import('@playwright/test').Page, f: object) {
  const alreadyRouted = routedFrames.has(page);
  routedFrames.set(page, f);
  if (alreadyRouted) return;
  await page.route('**/api/live', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: `retry: 300\nevent: frame\ndata: ${JSON.stringify(routedFrames.get(page))}\n\n`,
    }),
  );
}

// showFleet routes a frame, loads Overview and waits for the strip to
// hold exactly the expected number of pills. There is nothing to wait
// for beyond that any more: the pitch is declared in CSS, so the first
// paint of a unit is already the final geometry -- no measurement pass,
// no data-cell handshake.
//
// The pill count is the ONLY settle signal left, so it carries the whole
// budget the two-step wait used to share. A routed frame reaches the
// page on the EventSource's next reconnect (retry: 300), and a spec that
// swaps the frame several times waits that out once per swap on a
// machine already running the rest of this suite in parallel -- the
// default 5s poll lost that race (observed: a 100-container frame still
// showing the previous fleet).
async function showFleet(page: import('@playwright/test').Page, f: object, expectedUnits: number) {
  await routeLiveFrame(page, f);
  await page.goto('#/');
  await expect(page.locator('.fleet-strip').first()).toBeVisible();
  await expect
    .poll(() => page.locator('.fleet-strip .fleet-unit').count(), { timeout: 15_000 })
    .toBe(expectedUnits);
}

// unitBoxes: every pill's rendered rect, in DOM order.
async function unitBoxes(page: import('@playwright/test').Page, root = '.fleet-strip') {
  return page.locator(`${root} .fleet-unit`).evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height };
    }),
  );
}

test('fleet pills render at the fixed 28x28 targets, on whole aligned columns', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(24), 24);

  const strip = page.locator('.fleet-strip').first();
  expect(await strip.evaluate((el) => getComputedStyle(el).display)).toBe('grid');

  // Every explicit track is the same literal 28px -- the fixed target-size contract. Rows break on the same whole-unit boundaries,
  // columns align vertically by construction, and no unit is ever
  // clipped at the edge, because auto-fill only ever lays down whole
  // tracks.
  const columns = (await strip.evaluate((el) => getComputedStyle(el).gridTemplateColumns)).split(' ');
  expect(columns.length).toBeGreaterThan(1);
  for (const track of columns) {
    expect(Math.abs(parseFloat(track) - CELL_W), `track ${track} vs the declared ${CELL_W}px`).toBeLessThan(0.5);
  }

  // And the pills themselves are rectangular at that pitch -- taller
  // than they are wide, which is the whole of "back to being
  // rectangular".
  const boxes = await unitBoxes(page);
  for (const b of boxes) {
    expect(Math.abs(b.w - CELL_W)).toBeLessThan(0.5);
    expect(Math.abs(b.h - CELL_H)).toBeLessThan(0.5);
  }

  // The grid never forces the page sideways.
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
});

// The exact inverse of the count-scaled fit this reverts: the pill is
// the same size at three containers and at a hundred. Same viewport,
// same field, only the count changes -- and nothing about the pill
// moves with it.
test('fleet pills keep one pitch whatever the fleet size', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const count of [3, 30, 100]) {
    await showFleet(page, quietFleet(count), count);
    const boxes = await unitBoxes(page);
    expect(boxes, `${count} containers`).toHaveLength(count);
    for (const b of boxes) {
      expect(Math.abs(b.w - CELL_W), `${count} containers: pill width`).toBeLessThan(0.5);
      expect(Math.abs(b.h - CELL_H), `${count} containers: pill height`).toBeLessThan(0.5);
    }
  }
});

// Wrapping, not scrolling and not shrinking: a fleet too wide for one
// line runs onto more lines at the same pitch, every line starting at
// the same left edge and every column on the same 34px stride.
test('a fleet too wide for one line wraps into aligned rows', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(140), 140);

  const boxes = await unitBoxes(page, '.fleet-strip');
  const rows = [...new Set(boxes.map((b) => Math.round(b.y)))].sort((a, b) => a - b);
  expect(rows.length, 'a 140-pill fleet must occupy more than one row').toBeGreaterThan(1);

  // Columns align across rows: every x sits on the same stride from the
  // same origin.
  const originX = Math.min(...boxes.map((b) => b.x));
  for (const b of boxes) {
    const offset = b.x - originX;
    expect(Math.abs(offset - Math.round(offset / COL_PITCH) * COL_PITCH), `x ${b.x} off the ${COL_PITCH}px stride`).toBeLessThan(0.6);
  }

  // Rows are on the pill's own row pitch (28px unit + 6px row gap) --
  // no row is squeezed or stretched to make the fleet fit.
  for (let i = 1; i < rows.length; i++) {
    expect(Math.abs(rows[i] - rows[i - 1] - (CELL_H + 6)), `row ${i} pitch`).toBeLessThan(1);
  }

  // Nothing scrolls sideways, and the strip itself has no scrollbar of
  // its own -- it simply got taller.
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
});

// "Keep the stopped containers in a separate section like we had
// before" -- two rows, running first, each headed with its own name and
// count at the left, both on the one shared pitch.
test('running and stopped are two labelled rows, counted and at one pitch', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(18, {}, 5), 23);

  const grids = page.locator('.fleet-strip');
  await expect(grids).toHaveCount(2);
  await expect(grids.nth(0)).toHaveAttribute('aria-label', 'Running containers, 18');
  await expect(grids.nth(1)).toHaveAttribute('aria-label', 'Stopped containers, 5');

  // Each row says what it is and how many, at its left, and links into
  // the Containers view filtered to exactly that.
  const heads = page.locator('.fleet-strip__group-head');
  await expect(heads).toHaveCount(2);
  await expect(heads.nth(0)).toContainText('Running');
  await expect(heads.nth(0).locator('.fleet-strip__group-count')).toHaveText('18');
  await expect(heads.nth(0)).toHaveAttribute('href', '#/containers?state=running');
  await expect(heads.nth(1)).toContainText('Stopped');
  await expect(heads.nth(1).locator('.fleet-strip__group-count')).toHaveText('5');
  await expect(heads.nth(1)).toHaveAttribute('href', '#/containers?state=stopped');

  // The stopped row sits below the running one, its head to its own left.
  const runningBox = (await grids.nth(0).boundingBox())!;
  const stoppedBox = (await grids.nth(1).boundingBox())!;
  expect(stoppedBox.y).toBeGreaterThanOrEqual(runningBox.y + runningBox.height - 1);
  const stoppedHead = (await heads.nth(1).boundingBox())!;
  expect(stoppedHead.x + stoppedHead.width).toBeLessThanOrEqual(stoppedBox.x + 1);

  // ONE pitch across both -- and it is the declared one, not merely a
  // shared computed one.
  const tracks = async (i: number) =>
    (await grids.nth(i).evaluate((el) => getComputedStyle(el).gridTemplateColumns)).split(' ');
  expect((await tracks(1))[0]).toBe((await tracks(0))[0]);

  const firstRunning = (await grids.nth(0).locator('.fleet-unit').first().boundingBox())!;
  const firstStopped = (await grids.nth(1).locator('.fleet-unit').first().boundingBox())!;
  expect(Math.abs(firstRunning.width - CELL_W)).toBeLessThan(0.5);
  expect(Math.abs(firstStopped.width - firstRunning.width)).toBeLessThan(0.5);
  expect(Math.abs(firstStopped.height - firstRunning.height)).toBeLessThan(0.5);
  // Stopped blocks are still real links with their own state.
  await expect(grids.nth(1).locator('.fleet-unit').first()).toHaveClass(/fleet-unit--stopped/);
  await expect(grids.nth(1).locator('.fleet-unit').first()).toHaveAttribute('href', '#/containers/old-01');
});

test('a fleet with nothing stopped renders no stopped row at all', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(9), 9);
  await expect(page.locator('.fleet-strip')).toHaveCount(1);
  await expect(page.locator('.fleet-strip__group--stopped')).toHaveCount(0);
  await expect(page.locator('.fleet-strip__group-head')).toHaveCount(1);
});

test('the fleet card grows by the space its rows need', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showFleet(page, quietFleet(3), 3);
  const small = (await page.locator('.fleet-strip-wrap').boundingBox())!;
  expect(small.height).toBeLessThan(260);
  const rows = async () => new Set((await unitBoxes(page)).map(b => Math.round(b.y))).size;
  const beforeRows = await rows();
  await showFleet(page, quietFleet(140), 140);
  const large = (await page.locator('.fleet-strip-wrap').boundingBox())!;
  expect(Math.abs((large.height - small.height) - ((await rows()) - beforeRows) * 34)).toBeLessThan(2);
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

// The bay schematic is a Customize MODULE -- it renders in the narrow
// lane, beneath the full-width metrics summary.
test('storage array renders as a module in the narrow lane', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');

  const module = page.locator('.overview__modules-narrow [data-module="storage"]');
  await expect(module).toBeVisible({ timeout: 20_000 });
  await expect(module.locator('.bay-schematic')).toBeVisible();

  // Under the rail, not above it: the pinned head always comes first.
  const railBox = (await page.locator('.overview__metrics-rail').boundingBox())!;
  const moduleBox = (await module.boundingBox())!;
  expect(moduleBox.y).toBeGreaterThanOrEqual(railBox.y + railBox.height - 1);
});

test('metrics and health lead the saved module lanes', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');
  const rail = page.locator('.overview__metrics-rail');
  await expect(rail).toBeVisible();
  await expect(page.locator('[data-module="metrics-rail"]')).toHaveCount(0);
  const box = async (sel: string) => (await page.locator(sel).first().boundingBox())!;
  const metrics = await box('.overview__metrics-rail');
  const health = await box('.overview-health');
  const lanes = await box('.overview__modules-lanes');
  expect(metrics.y + metrics.height).toBeLessThanOrEqual(health.y);
  expect(health.y + health.height).toBeLessThanOrEqual(lanes.y);
  expect(Math.abs(metrics.width - lanes.width)).toBeLessThan(2);
  const tiles = rail.locator('.stat-tile');
  await expect(tiles).toHaveCount(4);
  const boxes = await tiles.evaluateAll(els => els.map(el => el.getBoundingClientRect().toJSON()));
  for (let i = 1; i < boxes.length; i++) {
    expect(Math.abs(boxes[i].y - boxes[0].y)).toBeLessThan(1);
    expect(boxes[i].x).toBeGreaterThan(boxes[i - 1].x);
  }
  await expect.poll(() => rail.locator('canvas').count()).toBeGreaterThanOrEqual(4);
});

test('the wide lane flows directly from fleet to modules', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');
  const module = page.locator('.overview__modules-wide .overview__module').first();
  await expect(module).toBeVisible();
  const fleet = (await page.locator('.fleet-strip-wrap').boundingBox())!;
  const next = (await module.boundingBox())!;
  expect(next.y - fleet.y - fleet.height).toBeGreaterThanOrEqual(0);
  expect(next.y - fleet.y - fleet.height).toBeLessThan(40);
});

test('phone layout leads with two-by-two metrics, health, fleet, and modules', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('#/');
  await expect(page.locator('.overview__storage')).toBeVisible();
  const box = async (sel: string) => (await page.locator(sel).first().boundingBox())!;
  const ordered = ['.overview__metrics-rail', '.overview-health', '.fleet-strip-wrap', '.overview__top', '.overview__storage'];
  let bottom = 0;
  for (const sel of ordered) {
    const b = await box(sel);
    expect(b.y, sel).toBeGreaterThanOrEqual(bottom);
    bottom = b.y + b.height;
  }
  const tiles = await page.locator('.overview__metrics-rail .stat-tile').evaluateAll(els => els.map(el => el.getBoundingClientRect().toJSON()));
  expect(Math.abs(tiles[0].y - tiles[1].y)).toBeLessThan(1);
  expect(Math.abs(tiles[2].y - tiles[3].y)).toBeLessThan(1);
  expect(tiles[2].y).toBeGreaterThan(tiles[0].y);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
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

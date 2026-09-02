import { expect, test, type Page, type APIRequestContext } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

// The Overview's "Customize" edit mode, end to end against the real
// binary: enter edit mode, drag a module to a new position with a REAL
// mouse, hide one, reset, and prove each of those survives a reload.
//
// This file boots its OWN gantry process (auth.spec.ts's harness, same
// ./gantry artifact `make release` already produced) rather than sharing
// the suite-wide instance, for one specific reason: the saved layout is
// a single GLOBAL server-side document, and half a dozen other specs
// address the Overview's modules by name -- hover-scrub and smoke read
// `.overview__metrics-rail .stat-tile`, rank-stability reads
// `.overview__top .top-bar-list`, smoke measures the wide/narrow lane
// split. Hiding or moving a module on the shared instance would break
// any of them that happened to be running at that moment
// (playwright.config.ts is fullyParallel). Its own instance makes that
// structurally impossible instead of merely unlikely.
//
// Serial within the file for the same reason one level down: these
// tests share one layout document by definition.
test.describe.configure({ mode: 'serial' });

const PORT = 8404; // the suite's own block: config PORT+3 -- see playwright.config.ts and auth.spec.ts
const URL = `http://127.0.0.1:${PORT}`;

let proc: ChildProcess;

test.beforeAll(async () => {
  proc = spawn(path.resolve(process.cwd(), '..', 'gantry'), [], {
    env: {
      ...process.env,
      GANTRY_PORT: String(PORT),
      GANTRY_DB_PATH: path.join(mkdtempSync(path.join(tmpdir(), 'gantry-layout-')), 'g.db'),
      GANTRY_FAKE_DATA: '1',
      GANTRY_AUTH: 'none',
    },
    stdio: 'ignore',
  });
  const deadline = Date.now() + 15_000;
  for (;;) {
    try {
      if ((await fetch(`${URL}/api/healthz`)).status === 200) break;
    } catch {
      // not up yet
    }
    if (Date.now() > deadline) throw new Error(`gantry on :${PORT} never became healthy`);
    await new Promise((r) => setTimeout(r, 100));
  }
});

test.afterAll(() => {
  proc?.kill('SIGTERM');
});

// resetLayout wipes the saved document back to the default straight
// through the API. An all-empty document is the shortest way to say
// "defaults": the server's own merge places every known module at its
// default home (api_layout.go's mergeOverviewLayout), which is exactly
// what the Reset control produces too.
async function resetLayout(request: APIRequestContext): Promise<void> {
  const res = await request.put(`${URL}/api/layout/overview`, {
    headers: { 'X-Requested-With': 'gantry', 'Content-Type': 'application/json' },
    data: { version: 1, wide: [], narrow: [], hidden: [] },
  });
  expect(res.ok()).toBeTruthy();
}

test.beforeEach(async ({ request }) => {
  await resetLayout(request);
});

// laneOrder reads the rendered truth -- the module ids actually in the
// DOM, in the order they actually appear -- rather than anything the
// page reports about itself.
function laneOrder(page: Page, lane: 'wide' | 'narrow'): Promise<string[]> {
  return page
    .locator(`.overview__modules-${lane} > .overview__module`)
    .evaluateAll((els) => els.map((el) => (el as HTMLElement).dataset.module ?? ''));
}

// savedLayout goes through Playwright's OWN http client (request.get),
// never a sniffed page response -- Chromium evicts sniffed bodies under
// load on the 2-core runners, which is what #46/#48 were about. This
// client buffers its own bodies, so reading json() off it is safe.
async function savedLayout(request: APIRequestContext): Promise<{ wide: string[]; narrow: string[]; hidden: string[] }> {
  const res = await request.get(`${URL}/api/layout/overview`);
  expect(res.ok()).toBeTruthy();
  return res.json();
}

// DESKTOP is deliberately taller than a real laptop: the whole modules
// band has to fit on screen at once, because a drag is measured in
// VIEWPORT coordinates. If any part of it needed scrolling, hover()
// would scroll the page to reach the handle and every coordinate read
// before that would be stale by exactly the scroll distance.
const DESKTOP = { width: 1440, height: 1500 };

// settleOverview waits out everything that lands AFTER first paint and
// changes this page's height: the leaderboard's first rows, the events
// feed's own fetch (it is the one module not carried on the SSE frame),
// and the rail's sparkline canvases. A drag measured before those
// arrive is measured against a page that then moves out from under it --
// observed as a pointerdown landing next to the handle instead of on it.
async function settleOverview(page: Page): Promise<void> {
  await expect(page.locator('.overview__top .top-bar-list li').first()).toBeVisible({ timeout: 20_000 });
  await expect(page.locator('.overview__events-list, .overview__events-empty')).toBeVisible({ timeout: 20_000 });
  await expect.poll(() => page.locator('.overview__metrics-rail canvas').count(), { timeout: 20_000 }).toBeGreaterThan(
    0,
  );
}

async function enterEditMode(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Customize' }).click();
  await expect(page.locator('.overview__module--editing').first()).toBeVisible();
}

// dragModuleAbove runs the real thing: pointer down on the module's own
// drag handle, several intermediate moves (a single jump can land before
// the drag has begun tracking), then release near the very TOP of the
// target card -- comfortably above its vertical midpoint, which is the
// boundary the drop index turns on.
//
// The press goes through locator.hover() rather than a raw
// mouse.move(box.x, box.y): hover applies Playwright's own actionability
// checks first, including STABILITY (the element's box unchanged across
// two frames), which is exactly the guarantee a coordinate read moments
// earlier does not carry on a page still filling in. The target box is
// then read AFTER that hover, so it is measured in the same viewport
// frame the pointer is actually in.
async function dragModuleAbove(page: Page, moduleId: string, targetSelector: string): Promise<void> {
  const grip = page.locator(`.overview__module[data-module="${moduleId}"] .overview__module-grip`);
  await grip.hover();

  const target = await page.locator(targetSelector).boundingBox();
  expect(target, 'the drop target must be on screen -- see DESKTOP').not.toBeNull();
  expect(target!.y).toBeGreaterThan(0);

  await page.mouse.down();
  // The lifted state proves the gesture actually started, so a failure
  // further down is never ambiguous about whether the drag ever began.
  await expect(page.locator(`.overview__module--dragging[data-module="${moduleId}"]`)).toBeVisible();
  await page.mouse.move(target!.x + target!.width / 2, target!.y + 8, { steps: 14 });
  await expect(page.locator('.overview__drop-indicator')).toBeVisible();
  await page.mouse.up();
  await expect(page.locator('.overview__module--dragging')).toHaveCount(0);
}

// railCanvasSignature fingerprints what the rail's first sparkline is
// actually DRAWING, so "still live" can be asserted as "the picture
// changed" rather than merely "the element still exists".
function railCanvasSignature(page: Page): Promise<string> {
  return page
    .locator('.overview__metrics-rail canvas')
    .first()
    .evaluate((el) => {
      const c = el as HTMLCanvasElement;
      const data = c.toDataURL();
      let h = 0;
      for (let i = 0; i < data.length; i++) h = (h * 31 + data.charCodeAt(i)) | 0;
      return `${c.width}x${c.height}:${h}`;
    });
}

async function markRailCanvas(page: Page): Promise<void> {
  await page
    .locator('.overview__metrics-rail canvas')
    .first()
    .evaluate((c) => {
      (c as HTMLCanvasElement & { __gantryMark?: string }).__gantryMark = 'pre-drag';
    });
}

function railCanvasMark(page: Page): Promise<string | null> {
  return page
    .locator('.overview__metrics-rail canvas')
    .first()
    .evaluate((c) => (c as HTMLCanvasElement & { __gantryMark?: string }).__gantryMark ?? null);
}

test('customize: dragging a module to a new position reorders it and the order survives a reload', async ({
  page,
  request,
}) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  expect(await laneOrder(page, 'wide')).toEqual(['top-consumers', 'events']);
  await enterEditMode(page);

  await dragModuleAbove(page, 'events', '.overview__top');

  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['events', 'top-consumers']);

  // Persisted server-side, not just held in this tab.
  await expect.poll(async () => (await savedLayout(request)).wide, { timeout: 10_000 }).toEqual([
    'events',
    'top-consumers',
  ]);

  await page.reload();
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['events', 'top-consumers']);
  // And it comes back in NORMAL mode -- a saved arrangement is the page,
  // not a state of the editor.
  await expect(page.locator('.overview__module--editing')).toHaveCount(0);
});

// The keyed-each contract this whole feature rests on: reordering WITHIN
// a lane relocates the existing DOM subtree -- canvas and all -- rather
// than tearing it down and building a new one. The rail is the module
// that makes this matter: four live uPlot instances, each with its own
// cursor/sync state and its own animation frame.
test('customize: reordering within a lane relocates a module without rebuilding its live charts', async ({
  page,
  request,
}) => {
  // Both charts-carrying modules in ONE lane, so the drag below is
  // unambiguously a within-lane reorder.
  const seeded = await request.put(`${URL}/api/layout/overview`, {
    headers: { 'X-Requested-With': 'gantry', 'Content-Type': 'application/json' },
    data: { version: 1, wide: ['top-consumers', 'events', 'metrics-rail'], narrow: [], hidden: [] },
  });
  expect(seeded.ok()).toBeTruthy();

  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  // Stamp the rail's first sparkline canvas: a relocated node keeps the
  // mark, a recreated one cannot.
  await markRailCanvas(page);
  const before = await railCanvasSignature(page);

  await enterEditMode(page);
  await dragModuleAbove(page, 'metrics-rail', '.overview__top');
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['metrics-rail', 'top-consumers', 'events']);

  expect(
    await railCanvasMark(page),
    'a within-lane reorder must RELOCATE the sparkline canvas, never recreate it',
  ).toBe('pre-drag');

  // ...and it is still drawing. Fake mode ticks every ~2s, so a generous
  // window gives this several chances while never passing for a chart
  // that has genuinely stopped.
  await expect.poll(() => railCanvasSignature(page), { timeout: 25_000 }).not.toBe(before);
});

// Moving a module to the OTHER lane is a different mechanism: the two
// lanes are two separate keyed each blocks (they have to be -- each is
// its own independent-height flex column, which no single grid or flex
// parent can produce from one child list), so Svelte necessarily unmounts
// the module from one and mounts it in the other. Nothing is LOST when it
// does: every live ring the rail charts (cpuRing, memRing, net/io) lives
// in the view above the component, so a remounted Sparkline is handed the
// full history immediately -- the same transient rebuild a theme switch
// already performs. This test pins that outcome: charts alive and drawing
// on the far side of a cross-lane move.
test('customize: a module dragged into the other lane changes lanes with its charts still live', async ({
  page,
  request,
}) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  await enterEditMode(page);
  await dragModuleAbove(page, 'metrics-rail', '.overview__top');

  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['metrics-rail', 'top-consumers', 'events']);
  await expect.poll(() => laneOrder(page, 'narrow')).toEqual([]);
  await expect.poll(async () => (await savedLayout(request)).narrow, { timeout: 10_000 }).toEqual([]);

  await expect.poll(() => page.locator('.overview__metrics-rail canvas').count(), { timeout: 20_000 }).toBeGreaterThan(
    0,
  );
  const after = await railCanvasSignature(page);
  await expect.poll(() => railCanvasSignature(page), { timeout: 25_000 }).not.toBe(after);
});

test('customize: hiding a module drops it from normal mode and leaves a ghost that brings it back', async ({
  page,
  request,
}) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  await page.getByRole('button', { name: 'Hide Recent events' }).click();

  // Gone from the lane, present in the tray as a ghost.
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['top-consumers']);
  await expect(page.locator('.overview__ghost[data-hidden-module="events"]')).toBeVisible();
  await expect.poll(async () => (await savedLayout(request)).hidden, { timeout: 10_000 }).toEqual(['events']);

  // Normal mode renders nothing for it at all -- not a ghost, not an
  // empty card.
  await page.getByRole('button', { name: 'Done' }).click();
  await expect(page.locator('.overview__events')).toHaveCount(0);
  await expect(page.locator('.overview__ghost')).toHaveCount(0);
  await expect(page.locator('.overview__top')).toBeVisible();

  // Survives a reload as hidden...
  await page.reload();
  await expect(page.locator('.overview__top')).toBeVisible();
  await expect(page.locator('.overview__events')).toHaveCount(0);

  // ...and the ghost's own eye brings it back, at the end of its
  // default lane.
  await enterEditMode(page);
  await page.getByRole('button', { name: 'Show Recent events' }).click();
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['top-consumers', 'events']);
  await expect(page.locator('.overview__events')).toBeVisible();
});

test('customize: an emptied lane disappears in normal mode and the survivor takes the whole band', async ({ page }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  // Both lanes always render while editing -- an emptied one still has
  // to be a drop target.
  await page.getByRole('button', { name: 'Hide Metrics rail' }).click();
  await expect(page.locator('.overview__modules-narrow')).toBeVisible();
  await expect(page.locator('.overview__lane-empty')).toBeVisible();

  await page.getByRole('button', { name: 'Done' }).click();
  await expect(page.locator('.overview__modules-narrow')).toHaveCount(0);

  // The adaptive-expansion rule keys off VISIBILITY: with nothing left
  // in the narrow lane, the wide one spans the band.
  const lanesBox = await page.locator('.overview__modules-lanes').boundingBox();
  const wideBox = await page.locator('.overview__modules-wide').boundingBox();
  expect(Math.abs(wideBox!.width - lanesBox!.width)).toBeLessThan(2);
});

test('customize: reset restores the default arrangement', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  // Reset has nothing to do until something has actually changed.
  const reset = page.getByRole('button', { name: 'Reset layout' });
  await expect(reset).toBeDisabled();

  await page.getByRole('button', { name: 'Hide Recent events' }).click();
  await dragModuleAbove(page, 'metrics-rail', '.overview__top');
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['metrics-rail', 'top-consumers']);

  await expect(reset).toBeEnabled();
  await reset.click();

  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['top-consumers', 'events']);
  await expect.poll(() => laneOrder(page, 'narrow')).toEqual(['metrics-rail']);
  await expect(page.locator('.overview__ghost')).toHaveCount(0);
  await expect(reset).toBeDisabled();

  await expect
    .poll(async () => savedLayout(request), { timeout: 10_000 })
    .toEqual({ version: 1, wide: ['top-consumers', 'events'], narrow: ['metrics-rail'], hidden: [] });
});

test('customize: Escape cancels a drag in flight, committing nothing', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  const grip = page.locator('.overview__module[data-module="events"] .overview__module-grip');
  await grip.hover();
  const topBox = await page.locator('.overview__top').boundingBox();

  await page.mouse.down();
  await page.mouse.move(topBox!.x + topBox!.width / 2, topBox!.y + 8, { steps: 14 });
  await expect(page.locator('.overview__drop-indicator')).toBeVisible();

  await page.keyboard.press('Escape');
  await expect(page.locator('.overview__module--dragging')).toHaveCount(0);
  await expect(page.locator('.overview__drop-indicator')).toHaveCount(0);
  await page.mouse.up();

  expect(await laneOrder(page, 'wide')).toEqual(['top-consumers', 'events']);
  expect((await savedLayout(request)).wide).toEqual(['top-consumers', 'events']);
});

test('customize: editing is desktop-only, but a saved arrangement still applies on mobile', async ({
  page,
  request,
}) => {
  // Saved on a desktop, read on a phone.
  const res = await request.put(`${URL}/api/layout/overview`, {
    headers: { 'X-Requested-With': 'gantry', 'Content-Type': 'application/json' },
    data: { version: 1, wide: ['events', 'top-consumers'], narrow: ['metrics-rail'], hidden: [] },
  });
  expect(res.ok()).toBeTruthy();

  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto(`${URL}/#/`);

  // No way in below the md breakpoint...
  await expect(page.getByRole('button', { name: 'Customize' })).toHaveCount(0);

  // ...but the arrangement is honoured: the lanes stack (wide first,
  // then narrow) and each keeps its own saved order.
  await expect(page.locator('.overview__events')).toBeVisible();
  const eventsBox = await page.locator('.overview__events').boundingBox();
  const topBox = await page.locator('.overview__top').boundingBox();
  const railBox = await page.locator('.overview__metrics-rail').boundingBox();
  expect(eventsBox!.y, 'the saved order put Recent events first').toBeLessThan(topBox!.y);
  expect(railBox!.y, 'the narrow lane stacks below the wide one').toBeGreaterThan(topBox!.y);

  // And it stays honoured coming back to desktop width.
  await page.setViewportSize(DESKTOP);
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['events', 'top-consumers']);
  await expect(page.getByRole('button', { name: 'Customize' })).toBeVisible();
});

import { expect, test, type Page, type APIRequestContext } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

// The Overview's "Customize" edit mode, end to end against the real
// binary: enter edit mode, drag a module to a new position with a REAL
// mouse, drag the column divider with one too, set a card's height step,
// hide a module, reset, and prove each of those survives a reload.
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

// The document's own schema number and the two v2 constants these tests
// assert against -- kept as literals rather than imported from
// src/lib/overviewLayout.ts on purpose: a spec that read the same
// constant the app renders from could never catch it changing.
const LAYOUT_VERSION = 2;
const RATIO_DEFAULT = 0.615;
const RATIO_MAX = 0.75;

interface SavedLayout {
  version: number;
  wide: string[];
  narrow: string[];
  hidden: string[];
  ratio: number;
  sizes: Record<string, string>;
}

// putLayout writes a whole document straight through the API -- the
// seeding path for a test that needs the page to START somewhere.
async function putLayout(request: APIRequestContext, data: Record<string, unknown>): Promise<void> {
  const res = await request.put(`${URL}/api/layout/overview`, {
    headers: { 'X-Requested-With': 'gantry', 'Content-Type': 'application/json' },
    data,
  });
  expect(res.ok()).toBeTruthy();
}

// resetLayout wipes the saved document back to the default straight
// through the API. An all-empty document is the shortest way to say
// "defaults": the server's own merge places every known module at its
// default home, restores the default column split and clears every
// height step (api_layout.go's mergeOverviewLayout), which is exactly
// what the Reset control produces too.
//
// Deliberately still declared `version: 1`: this runs before every test
// in the file, so the v1 -> v2 migration is exercised on the real binary
// dozens of times a run rather than in one lonely case. A v1 document is
// exactly what a browser holding a cached pre-resize bundle sends.
async function resetLayout(request: APIRequestContext): Promise<void> {
  await putLayout(request, { version: 1, wide: [], narrow: [], hidden: [] });
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
async function savedLayout(request: APIRequestContext): Promise<SavedLayout> {
  const res = await request.get(`${URL}/api/layout/overview`);
  expect(res.ok()).toBeTruthy();
  return res.json();
}

// laneSplit reads the split the page is ACTUALLY rendering -- the wide
// lane's share of the two lanes' own combined width, which is exactly
// what the saved ratio means (the flex gap between them belongs to
// neither, so it is excluded from both sides of the fraction).
async function laneSplit(page: Page): Promise<number> {
  const wide = await page.locator('.overview__modules-wide').boundingBox();
  const narrow = await page.locator('.overview__modules-narrow').boundingBox();
  if (!wide || !narrow) return NaN;
  return wide.width / (wide.width + narrow.width);
}

// One deterministic all-clear SSE frame, the same route-mock the smoke
// suite's own layout specs use: fake mode always boots one unhealthy
// container (grafana), so the all-clear band -- the adaptive expansion
// the height steps have to coexist with -- is not otherwise reachable
// here. Ten running containers so the leaderboard has more rows than
// even the tall step asks for.
function allClearFrame() {
  const containers: Record<string, object> = {};
  for (let i = 0; i < 10; i++) {
    containers[`demo-${i}`] = {
      state: 'running',
      health: 'healthy',
      icon: '',
      metrics: { 'cpu.pct': 20 - i, 'mem.bytes': (10 - i) * 1e8 },
    };
  }
  return {
    ts: Math.floor(Date.now() / 1000),
    unraid_version: '7.0.0',
    host: { 'cpu.total': 12.5, 'mem.used_pct': 42 },
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

async function routeAllClear(page: Page): Promise<void> {
  await page.route('**/api/live', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: `retry: 300\nevent: frame\ndata: ${JSON.stringify(allClearFrame())}\n\n`,
    }),
  );
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

// The rail -- four live uPlot instances -- used to be the module that
// made the keyed-each contract matter, and these helpers fingerprinted
// its canvases. It is pinned at the top of the page now and is not a
// module at all, which leaves NO canvas anywhere in the band. The
// contract is unchanged and still worth pinning, so it is pinned the
// way it can be: a stamp on a real DOM node inside the module proves
// RELOCATION (a recreated subtree cannot carry it), and the module's own
// rendered content proves it is still live on the far side.
async function markModuleNode(page: Page, moduleId: string): Promise<void> {
  await page
    .locator(`.overview__module[data-module="${moduleId}"] .card`)
    .evaluate((el) => {
      (el as HTMLElement & { __gantryMark?: string }).__gantryMark = 'pre-drag';
    });
}

function moduleNodeMark(page: Page, moduleId: string): Promise<string | null> {
  return page
    .locator(`.overview__module[data-module="${moduleId}"] .card`)
    .evaluate((el) => (el as HTMLElement & { __gantryMark?: string }).__gantryMark ?? null);
}

// topSignature is what the leaderboard is actually SHOWING -- its rows
// and their live values -- so "still live" reads as "the numbers moved"
// rather than "the element still exists". Fake mode re-ranks and
// re-values every ~2s tick.
function topSignature(page: Page): Promise<string> {
  return page.locator('.overview__top .top-bar-list').innerText();
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
// a lane relocates the existing DOM subtree rather than tearing it down
// and building a new one. Top Consumers is the module that makes this
// matter now -- a live leaderboard whose rank-stability state and row
// identities are held per-instance, so a silent recreate would reset
// the very hysteresis that stops it flickering.
test('customize: reordering within a lane relocates a module without rebuilding it', async ({ page, request }) => {
  // All three modules in ONE lane, so the drag below is unambiguously a
  // within-lane reorder.
  const seeded = await request.put(`${URL}/api/layout/overview`, {
    headers: { 'X-Requested-With': 'gantry', 'Content-Type': 'application/json' },
    data: { version: 2, wide: ['top-consumers', 'events', 'storage'], narrow: [], hidden: [] },
  });
  expect(seeded.ok()).toBeTruthy();

  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  // Stamp the leaderboard's own card: a relocated node keeps the mark,
  // a recreated one cannot.
  await markModuleNode(page, 'top-consumers');
  const before = await topSignature(page);

  await enterEditMode(page);
  await dragModuleAbove(page, 'storage', '.overview__top');
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['storage', 'top-consumers', 'events']);

  expect(
    await moduleNodeMark(page, 'top-consumers'),
    'a within-lane reorder must RELOCATE the surviving modules, never recreate them',
  ).toBe('pre-drag');

  // ...and it is still live. Fake mode ticks every ~2s, so a generous
  // window gives this several chances while never passing for a module
  // that has genuinely stopped updating.
  await expect.poll(() => topSignature(page), { timeout: 25_000 }).not.toBe(before);
});

// Moving a module to the OTHER lane is a different mechanism: the two
// lanes are two separate keyed each blocks (they have to be -- each is
// its own independent-height flex column, which no single grid or flex
// parent can produce from one child list), so Svelte necessarily unmounts
// the module from one and mounts it in the other. Nothing is LOST when it
// does: everything a module renders comes off the live frame held in the
// view above it, so a remounted module is handed the current state
// immediately -- the same transient rebuild a theme switch already
// performs. This test pins that outcome: the module arrives on the far
// side with its real content, and the page keeps updating.
test('customize: a module dragged into the other lane arrives with its content intact', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  await enterEditMode(page);
  await dragModuleAbove(page, 'storage', '.overview__top');

  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['storage', 'top-consumers', 'events']);
  await expect.poll(() => laneOrder(page, 'narrow')).toEqual([]);
  await expect.poll(async () => (await savedLayout(request)).narrow, { timeout: 10_000 }).toEqual([]);

  // Remounted, with the array it was drawing before the move.
  const moved = page.locator('.overview__modules-wide [data-module="storage"]');
  await expect(moved.locator('.bay-schematic')).toBeVisible();
  await expect.poll(() => moved.locator('.bay-schematic__bar').count(), { timeout: 20_000 }).toBeGreaterThan(0);

  // And the page as a whole is still live on the far side of the move.
  const after = await topSignature(page);
  await expect.poll(() => topSignature(page), { timeout: 25_000 }).not.toBe(after);
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

// A lane can no longer empty: each one leads with a PINNED head (the
// headline + fleet on the wide side, the metrics rail on the narrow
// one). So the rule that used to hide a moduleless lane and hand the
// whole band to the survivor is gone, and hiding the narrow lane's last
// module leaves the column exactly where the owner's split put it.
test('customize: a lane with no modules left keeps its pinned head and its share of the split', async ({ page }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  const lanes = page.locator('.overview__modules-lanes');
  const narrow = page.locator('.overview__modules-narrow');
  const beforeWidth = (await narrow.boundingBox())!.width;

  await enterEditMode(page);
  await page.getByRole('button', { name: 'Hide Storage array' }).click();
  await expect(narrow.locator('.overview__module')).toHaveCount(0);
  // Still a real drop target while editing, and it says so.
  await expect(page.locator('.overview__lane-empty')).toBeVisible();

  await page.getByRole('button', { name: 'Done' }).click();
  // And still a real COLUMN out of edit mode -- the rail is in it.
  await expect(narrow).toBeVisible();
  await expect(narrow.locator('.overview__metrics-rail')).toBeVisible();

  const lanesBox = (await lanes.boundingBox())!;
  const wideBox = (await page.locator('.overview__modules-wide').boundingBox())!;
  const narrowBox = (await narrow.boundingBox())!;
  expect(Math.abs(narrowBox.width - beforeWidth), 'the split is unchanged by an emptied lane').toBeLessThan(2);
  expect(wideBox.width, 'the wide lane must NOT swallow the band').toBeLessThan(lanesBox.width - 100);
});

// The pinned heads are not drop targets and cannot be displaced: a drag
// into the narrow lane lands UNDER the rail however high in the lane it
// is released, and the rail stays the lane's first child.
test('customize: a drop into the narrow lane lands below the pinned rail, never above it', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  // dragModuleAbove releases 8px into the target, so aiming it at the
  // RAIL is the most direct attempt to land above the rail there is.
  const rail = page.locator('.overview__metrics-rail');
  await dragModuleAbove(page, 'events', '.overview__metrics-rail');

  await expect.poll(() => laneOrder(page, 'narrow')).toEqual(['events', 'storage']);
  const railBox = (await rail.boundingBox())!;
  const moved = (await page.locator('.overview__modules-narrow [data-module="events"]').boundingBox())!;
  expect(moved.y, 'the dropped card sits below the pinned rail').toBeGreaterThanOrEqual(
    railBox.y + railBox.height - 1,
  );

  // The rail is still the lane's own first child, and still not a module.
  const firstChild = await page
    .locator('.overview__modules-narrow')
    .evaluate((el) => (el.firstElementChild as HTMLElement).dataset.pinned ?? null);
  expect(firstChild).toBe('metrics-rail');
  await expect(page.locator('[data-module="metrics-rail"]')).toHaveCount(0);

  // The saved document holds modules only -- no pinned id leaked in.
  // Polled, like every other saved-document assertion here: the store's
  // PUT is debounced, so the render lands before the write does.
  await expect.poll(async () => (await savedLayout(request)).narrow, { timeout: 10_000 }).toEqual([
    'events',
    'storage',
  ]);
  const saved = JSON.stringify(await savedLayout(request));
  expect(saved).not.toContain('metrics-rail');
  expect(saved).not.toContain('fleet');
  expect(saved).not.toContain('headline');
});

// --- Column split ----------------------------------------------------------

// dragDivider runs the real thing, the same shape dragModuleAbove does:
// hover (so Playwright's own actionability/stability checks land first),
// read the box in THAT viewport frame, then press, move in several steps
// and release. A single jump can land before the drag has begun tracking.
async function dragDivider(page: Page, dx: number): Promise<void> {
  const divider = page.locator('.overview__lane-divider');
  await divider.hover();
  const box = await divider.boundingBox();
  expect(box, 'the divider must be on screen -- see DESKTOP').not.toBeNull();

  const y = box!.y + Math.min(box!.height / 2, 240);
  const x = box!.x + box!.width / 2;
  await page.mouse.down();
  await page.mouse.move(x + dx, y, { steps: 14 });
  await page.mouse.up();
}

test('customize: dragging the divider re-splits the columns and the split survives a reload', async ({
  page,
  request,
}) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  // Outside edit mode there is no affordance at all -- the split applies,
  // the handle doesn't exist.
  await expect(page.locator('.overview__lane-divider')).toHaveCount(0);
  const before = await laneSplit(page);
  expect(before).toBeCloseTo(RATIO_DEFAULT, 2);

  await enterEditMode(page);
  await expect(page.locator('.overview__lane-divider')).toBeVisible();

  await dragDivider(page, 90);

  // The rendered split moved, and the page agrees with what it saved.
  await expect.poll(() => laneSplit(page)).toBeGreaterThan(before + 0.02);
  await expect.poll(async () => (await savedLayout(request)).ratio, { timeout: 10_000 }).toBeGreaterThan(before + 0.02);

  const saved = (await savedLayout(request)).ratio;
  expect(saved, 'the drag stays inside the designed range').toBeLessThanOrEqual(RATIO_MAX);
  expect(await laneSplit(page), 'the rendered split IS the saved number').toBeCloseTo(saved, 2);

  await page.reload();
  await settleOverview(page);
  await expect.poll(() => laneSplit(page)).toBeCloseTo(saved, 2);
  // And it comes back in NORMAL mode with no handle showing -- a saved
  // split is the page, not a state of the editor.
  await expect(page.locator('.overview__lane-divider')).toHaveCount(0);
});

// A drag past the end of the range must stop at the clamp rather than
// running off -- and the clamp is a number, so this can assert the exact
// one instead of "somewhere over there".
test('customize: the divider clamps rather than letting a lane collapse', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  await dragDivider(page, 2000);

  await expect.poll(async () => (await savedLayout(request)).ratio, { timeout: 10_000 }).toBe(RATIO_MAX);
  expect(await laneSplit(page)).toBeCloseTo(RATIO_MAX, 2);
  // The narrow lane is squeezed, never gone.
  const narrow = await page.locator('.overview__modules-narrow').boundingBox();
  expect(narrow!.width).toBeGreaterThan(100);
});

// A ratio change is a WIDTH change: the lanes must be RESIZED, never
// torn down and rebuilt, or a divider drag would churn every module in
// the band once per frame. The DOM stamp proves it -- a recreated
// subtree cannot carry the mark -- and the widths prove the drag
// actually reached the lanes rather than being swallowed.
//
// This used to be asserted against the rail's four uPlot canvases,
// which were the loudest thing a rebuild would have cost. The rail is
// pinned outside the band now and no module left in it draws to a
// canvas, so the same contract is pinned on the surviving evidence.
test('customize: dragging the divider resizes the lanes without rebuilding them', async ({ page }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  await markModuleNode(page, 'top-consumers');
  await markModuleNode(page, 'storage');
  const beforeNarrow = (await page.locator('.overview__modules-narrow').boundingBox())!.width;
  const before = await topSignature(page);

  await enterEditMode(page);
  await dragDivider(page, 120);

  // Widening the wide lane really did narrow the other one...
  await expect
    .poll(async () => (await page.locator('.overview__modules-narrow').boundingBox())!.width)
    .toBeLessThan(beforeNarrow - 20);
  // ...without recreating either lane's modules.
  expect(
    await moduleNodeMark(page, 'top-consumers'),
    'a divider drag must RESIZE the wide lane, never recreate its modules',
  ).toBe('pre-drag');
  expect(
    await moduleNodeMark(page, 'storage'),
    'a divider drag must RESIZE the narrow lane, never recreate its modules',
  ).toBe('pre-drag');
  // ...and the page is still live on the far side of the gesture.
  await expect.poll(() => topSignature(page), { timeout: 25_000 }).not.toBe(before);
});

// The divider would otherwise be this page's first pointer-only control.
test('customize: the divider is focusable and adjusts with the arrow keys', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  const divider = page.locator('.overview__lane-divider');
  await expect(divider).toHaveAttribute('role', 'separator');
  await expect(divider).toHaveAttribute('aria-valuenow', String(Math.round(RATIO_DEFAULT * 100)));

  await divider.focus();
  await expect(divider).toBeFocused();

  for (let i = 0; i < 4; i++) await page.keyboard.press('ArrowRight');
  await expect(divider).toHaveAttribute('aria-valuenow', String(Math.round(RATIO_DEFAULT * 100) + 4));

  await page.keyboard.press('ArrowLeft');
  await expect(divider).toHaveAttribute('aria-valuenow', String(Math.round(RATIO_DEFAULT * 100) + 3));

  // End goes straight to the far clamp, the window-splitter convention.
  await page.keyboard.press('End');
  await expect(divider).toHaveAttribute('aria-valuenow', String(Math.round(RATIO_MAX * 100)));
  await expect.poll(async () => (await savedLayout(request)).ratio, { timeout: 10_000 }).toBe(RATIO_MAX);
  expect(await laneSplit(page)).toBeCloseTo(RATIO_MAX, 2);
});

// A drag that ends in Escape commits nothing AND leaves nothing showing:
// the preview has to snap back to the saved split, not sit there looking
// like it saved.
test('customize: Escape cancels a divider drag, committing nothing', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  const divider = page.locator('.overview__lane-divider');
  await divider.hover();
  const box = await divider.boundingBox();
  await page.mouse.down();
  await page.mouse.move(box!.x + box!.width / 2 + 120, box!.y + Math.min(box!.height / 2, 240), { steps: 14 });
  await expect(divider).toHaveClass(/overview__lane-divider--active/);

  await page.keyboard.press('Escape');
  await expect(divider).not.toHaveClass(/overview__lane-divider--active/);
  await page.mouse.up();

  await expect.poll(() => laneSplit(page)).toBeCloseTo(RATIO_DEFAULT, 2);
  expect((await savedLayout(request)).ratio).toBe(RATIO_DEFAULT);
});

// --- Height steps -----------------------------------------------------------

function topRowCount(page: Page): Promise<number> {
  return page.locator('.overview__top .top-bar-list li').count();
}

// Top Consumers is the module that can be asserted to the row: the fake
// fleet is 20 containers, so every step's budget (3/5/8) is genuinely
// available. The events feed's own budget (4/8/14) depends on how many
// events the generator has actually emitted, which is why the test below
// pins its CAP rather than an exact count.
test('customize: a height step resizes a card by whole rows and survives a reload', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);

  // Default is the budget the page shipped with.
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(5);
  const normalBox = await page.locator('.overview__top').boundingBox();

  await enterEditMode(page);
  await page.getByRole('button', { name: 'Set Top consumers to tall' }).click();

  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(8);
  const tallBox = await page.locator('.overview__top').boundingBox();
  expect(tallBox!.height, 'tall is genuinely taller, not just relabelled').toBeGreaterThan(normalBox!.height);
  await expect(page.locator('.overview__module[data-module="top-consumers"]')).toHaveAttribute('data-size', 'tall');

  await expect.poll(async () => (await savedLayout(request)).sizes, { timeout: 10_000 }).toEqual({
    'top-consumers': 'tall',
  });

  await page.reload();
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(8);
  expect((await page.locator('.overview__top').boundingBox())!.height).toBeGreaterThan(normalBox!.height);

  // ...and compact goes the other way, immediately -- the leaderboard's
  // own rank hysteresis must not make a shrink wait out its re-sort gate.
  await enterEditMode(page);
  await page.getByRole('button', { name: 'Set Top consumers to compact' }).click();
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(3);
  expect((await page.locator('.overview__top').boundingBox())!.height).toBeLessThan(normalBox!.height);
});

test('customize: the events feed takes a step too, and storage is offered none', async ({ page, request }) => {
  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await enterEditMode(page);

  // The bay schematic's height is decided by how many array members
  // exist, not by any row budget, so storage gets a grip and an eye and
  // nothing else.
  await expect(page.getByRole('button', { name: 'Set Storage array to tall' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Set Recent events to tall' })).toBeVisible();

  await page.getByRole('button', { name: 'Set Recent events to compact' }).click();
  await expect(page.locator('.overview__module[data-module="events"]')).toHaveAttribute('data-size', 'compact');
  await expect.poll(async () => (await savedLayout(request)).sizes, { timeout: 10_000 }).toEqual({
    events: 'compact',
  });

  // The compact budget is a cap the feed cannot exceed. (An exact count
  // would depend on how many events fake mode has emitted by now, which
  // is a function of server uptime -- see fake.go's time-gated events.)
  await expect.poll(() => page.locator('.overview__events-list > div').count()).toBeLessThanOrEqual(4);

  await page.reload();
  await expect(page.locator('.overview__module[data-module="events"]')).toHaveAttribute('data-size', 'compact');
});

// --- Interplay with the all-clear state -------------------------------------
//
// The rule: a user-set size WINS. All-clear is this page's own adaptive
// change -- with nothing needing attention the headline card collapses
// to a strip and everything under it in that lane starts higher. It is
// free to grow a module still at 'normal'; it may not overrule one the
// owner has sized, and it happens identically either way.
//
// (It used to collapse a whole status BAND. Since the unification there
// is no band to collapse: the headline card is a pinned lane head, and
// its own height is the only thing that changes.)
test('customize: the all-clear state fills around a card the owner has sized', async ({ page, request }) => {
  await routeAllClear(page);
  await putLayout(request, {
    version: LAYOUT_VERSION,
    wide: ['top-consumers', 'events'],
    narrow: ['storage'],
    hidden: [],
    sizes: { 'top-consumers': 'compact' },
  });

  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);

  // The all-clear state really is active: the headline card is in its
  // collapsed form, at the head of the wide lane.
  await expect(page.locator('.overview__headline-text')).toHaveText('Nothing needs you');
  await expect(page.locator('.overview__modules-wide .overview__headline-zone')).toHaveClass(
    /overview__headline-zone--clear/,
  );

  // The sized card holds ITS budget, not one the expansion picked.
  const sized = page.locator('.overview__module[data-module="top-consumers"]');
  await expect(sized).toHaveAttribute('data-size', 'compact');
  await expect(sized).toHaveAttribute('data-adaptive', 'false');
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(3);

  // Its neighbour, untouched, is still the thing the layout may grow.
  await expect(page.locator('.overview__module[data-module="events"]')).toHaveAttribute('data-adaptive', 'true');

  // The sized card's own TOP is what must not move: it is the second
  // thing in the wide lane, so anything the all-clear state does above
  // it would show up here. (This used to be measured on the modules
  // band's own top edge, back when the band started below the status
  // band; the lane's head is that boundary now.)
  const sizedTop = (await sized.boundingBox())!.y;
  const compactHeight = (await page.locator('.overview__top').boundingBox())!.height;

  // Hand the same card back to the adaptive default: it grows to the
  // shipped budget, while the expansion above it does not move an inch --
  // the two are independent, which is the whole claim.
  await putLayout(request, {
    version: LAYOUT_VERSION,
    wide: ['top-consumers', 'events'],
    narrow: ['storage'],
    hidden: [],
  });
  await page.reload();

  await expect(page.locator('.overview__modules-wide .overview__headline-zone')).toHaveClass(
    /overview__headline-zone--clear/,
  );
  await expect(sized).toHaveAttribute('data-adaptive', 'true');
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(5);
  expect((await page.locator('.overview__top').boundingBox())!.height).toBeGreaterThan(compactHeight);
  expect(
    (await sized.boundingBox())!.y,
    'everything above the sized card is unchanged -- only the card itself was',
  ).toBeCloseTo(sizedTop, 0);
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
  await page.getByRole('button', { name: 'Set Top consumers to tall' }).click();
  await dragDivider(page, 80);
  await dragModuleAbove(page, 'storage', '.overview__top');
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['storage', 'top-consumers']);
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(8);

  await expect(reset).toBeEnabled();
  await reset.click();

  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['top-consumers', 'events']);
  await expect.poll(() => laneOrder(page, 'narrow')).toEqual(['storage']);
  await expect(page.locator('.overview__ghost')).toHaveCount(0);
  // Reset puts back the split and the height steps too, not just the
  // arrangement.
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(5);
  await expect.poll(() => laneSplit(page)).toBeCloseTo(RATIO_DEFAULT, 2);
  await expect(reset).toBeDisabled();

  await expect.poll(async () => savedLayout(request), { timeout: 10_000 }).toEqual({
    version: LAYOUT_VERSION,
    wide: ['top-consumers', 'events'],
    narrow: ['storage'],
    hidden: [],
    ratio: RATIO_DEFAULT,
    sizes: {},
  });
});

// A cached pre-resize bundle PUTs a v1 document at this binary. It has to
// be accepted and migrated, not 400'd -- a 400 would leave that tab
// unable to save anything at all until it reloaded. (resetLayout above
// already sends one before every test in this file; this pins the
// migrated RESULT, and that a v1 arrangement survives it.)
test('customize: a v1 document from a cached bundle is accepted and migrated', async ({ page, request }) => {
  await putLayout(request, { version: 1, wide: ['events', 'top-consumers'], narrow: ['storage'], hidden: [] });

  expect(await savedLayout(request)).toEqual({
    version: LAYOUT_VERSION,
    wide: ['events', 'top-consumers'],
    narrow: ['storage'],
    hidden: [],
    ratio: RATIO_DEFAULT,
    sizes: {},
  });

  await page.setViewportSize(DESKTOP);
  await page.goto(`${URL}/#/`);
  await settleOverview(page);
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['events', 'top-consumers']);
  await expect.poll(() => laneSplit(page)).toBeCloseTo(RATIO_DEFAULT, 2);
  await expect.poll(() => topRowCount(page), { timeout: 20_000 }).toBe(5);
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
    data: { version: 1, wide: ['events', 'top-consumers'], narrow: ['storage'], hidden: [] },
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
  const storageBox = await page.locator('.overview__storage').boundingBox();
  expect(eventsBox!.y, 'the saved order put Recent events first').toBeLessThan(topBox!.y);
  expect(storageBox!.y, 'the narrow lane stacks below the wide one').toBeGreaterThan(topBox!.y);

  // And it stays honoured coming back to desktop width.
  await page.setViewportSize(DESKTOP);
  await expect.poll(() => laneOrder(page, 'wide')).toEqual(['events', 'top-consumers']);
  await expect(page.getByRole('button', { name: 'Customize' })).toBeVisible();
});

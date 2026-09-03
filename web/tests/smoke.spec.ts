import { test, expect } from '@playwright/test';

// Playwright smoke suite: drives the real built binary (see
// playwright.config.ts's webServer) in GANTRY_FAKE_DATA=1 mode, which
// synthesizes a 20-container demo fleet (internal/fake/fake.go) --
// "jellyfin" below is one of that fleet's fixed archetypes, always
// present and always state=running.
const ROUTES: { hash: string; h1: string }[] = [
  { hash: '#/', h1: 'Overview' },
  { hash: '#/containers', h1: 'Containers' },
  { hash: '#/containers/jellyfin', h1: 'jellyfin' },
  { hash: '#/top', h1: 'Metrics' },
  { hash: '#/storage', h1: 'Storage' },
  { hash: '#/maintenance', h1: 'Maintenance' },
  { hash: '#/gpu', h1: 'GPU' },
  { hash: '#/events', h1: 'Events' },
  { hash: '#/insights', h1: 'Insights' },
  { hash: '#/alerts', h1: 'Alerts' },
  { hash: '#/settings', h1: 'Settings' },
];

test('every route renders its h1 landmark', async ({ page }) => {
  for (const r of ROUTES) {
    await page.goto(r.hash);
    await expect(page.locator('h1.page-title')).toHaveText(r.h1);
  }
});

test('overview: status headline reflects fleet/array/disk state, the fleet strip is fully countable, and the CPU tile ticks', async ({
  page,
}) => {
  await page.goto('#/');

  // D2's headline replaces the old fleet-count card: a plain sentence,
  // either the all-clear or a counted "N things need you" -- the fake
  // fleet boots 100% running/healthy (fake.go's Metas()) and its one
  // disk.errors trigger is 5 real minutes into server uptime, well
  // outside this test's own window, so "Nothing needs you" is the
  // expected reading fresh off a CI-built server; a long-lived reused
  // local dev server (reuseExistingServer, non-CI only) could
  // legitimately have crossed that mark, so both readings are accepted
  // and checked for internal consistency instead of asserting one only.
  const headline = page.locator('.overview__headline-text');
  await expect(headline).toBeVisible();
  const headlineText = await headline.textContent();
  const isAllClear = headlineText === 'Nothing needs you';
  expect(isAllClear || /^\d+ things? need(s)? you$/.test(headlineText ?? '')).toBe(true);
  await expect(page.locator('.overview__attention')).toHaveCount(isAllClear ? 0 : 1);

  // Fleet strip is D2's own "countable, literal" evidence -- one unit
  // per container, so its count must equal its own summary's stated
  // counts. (The shell redesign folded the old standalone fleet
  // SENTENCE into the strip's summary row of links -- "N running",
  // "M stopped", the stopped link only rendering when anything is
  // stopped at all.) Deliberately NOT a hardcoded 20: this box's
  // docker collector runs for real alongside fake.go's 20 synthetic
  // archetypes (reproduced live while building this -- a handful of
  // the sandbox's own real containers showed up in the fleet total
  // too), so the true size varies by environment. This instead checks
  // that the strip's units and its summary -- two client-side views of
  // the exact same live container set -- agree with each other.
  const summary = page.locator('.fleet-strip__summary');
  await expect(summary).toBeVisible();
  const runningText = (await summary.getByText(/^\d+ running$/).textContent()) ?? '';
  const runningStated = Number(runningText.match(/^(\d+)/)?.[1] ?? NaN);
  const stoppedLink = summary.getByText(/^\d+ stopped$/);
  const stoppedStated =
    (await stoppedLink.count()) > 0 ? Number(((await stoppedLink.textContent()) ?? '').match(/^(\d+)/)?.[1] ?? NaN) : 0;
  const statedTotal = runningStated + stoppedStated;
  expect(statedTotal, `unrecognized fleet summary counts: ${await summary.textContent()}`).toBeGreaterThan(0);

  const fleetUnits = page.locator('.fleet-strip .fleet-unit');
  await expect.poll(() => fleetUnits.count()).toBe(statedTotal);

  // CPU tile is the first row of Overview's instrument rail. The fake
  // generator writes host cpu.total with real per-tick jitter (see
  // fake.go's Tick), so two samples a few ticks apart should differ --
  // expect.poll (not a fixed sleep) waits only as long as it actually
  // takes, up to the 6s window the brief allows.
  const cpuNumber = page.locator('.overview__metrics-rail .stat-tile').first().locator('.stat-tile__number');
  const initial = await cpuNumber.textContent();
  await expect.poll(() => cpuNumber.textContent(), { timeout: 6_000 }).not.toBe(initial);
});

test('overview: the Top Consumers module metric switcher changes the module and deep-links "View all"', async ({
  page,
}) => {
  await page.goto('#/');

  const topModule = page.locator('.overview__top');
  await expect(topModule).toBeVisible();

  // Fresh context -> no stored preference yet -> CPU is the default.
  await expect(topModule.getByRole('tab', { name: 'CPU', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(topModule.locator('.overview__top-link')).toHaveAttribute('href', '#/top/cpu');

  const memTab = topModule.getByRole('tab', { name: 'Mem', exact: true });
  await memTab.click();
  await expect(memTab).toHaveAttribute('aria-selected', 'true');

  // "View all" deep-links to the just-selected resource, and landing on
  // the full #/top view pre-selects that same resource's own (fuller-
  // labeled) tab there too.
  await expect(topModule.locator('.overview__top-link')).toHaveAttribute('href', '#/top/mem');
  await topModule.locator('.overview__top-link').click();
  await expect(page).toHaveURL(/#\/top\/mem$/);
  await expect(page.getByRole('tab', { name: 'Memory', exact: true })).toHaveAttribute('aria-selected', 'true');
});

// One deterministic SSE frame for the layout specs below: they pin two
// MUTUALLY EXCLUSIVE Overview layouts (the attention band vs the
// all-clear band), and which one the real fake-mode server would show
// depends on its own uptime (grafana's health check, the 5-minute
// disk-errors trigger, the scripted insight demo) plus whatever acks a
// parallel spec is briefly holding. Routing /api/live -- the same
// route-mock pattern the events and container-storage specs use -- pins
// the exact state each spec is about instead of skipping on the wrong
// one. EventSource re-connects when the fulfilled body ends (retry:
// 300) and re-receives the same frame every ~300ms -- as steady a live
// feed as these geometry assertions could ask for.
function liveFrame(extraContainers: Record<string, object> = {}) {
  return {
    ts: Math.floor(Date.now() / 1000),
    unraid_version: '7.0.0',
    host: { 'cpu.total': 12.5, 'mem.used_pct': 42 },
    containers: {
      jellyfin: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 4.2, 'mem.bytes': 9e8 } },
      qbittorrent: { state: 'running', health: '', icon: '', metrics: { 'cpu.pct': 0.4, 'mem.bytes': 5e8 } },
      postgres: { state: 'running', health: 'healthy', icon: '', metrics: { 'cpu.pct': 1.6, 'mem.bytes': 1.2e9 } },
      vaultwarden: { state: 'exited', health: '', icon: '', metrics: {} },
      ...extraContainers,
    },
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

async function routeLiveFrame(page: import('@playwright/test').Page, frame: object) {
  await page.route('**/api/live', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: `retry: 300\nevent: frame\ndata: ${JSON.stringify(frame)}\n\n`,
    }),
  );
}

// Counts-and-fleet pass: the status band's own two-column split is
// gone. "Needs a look" is two count chips now, short enough to sit
// inline in the headline card, and the fleet strip + array schematic
// get the same full-width row in this state that all-clear already gave
// them. The frame carries one unhealthy container so the attention
// layout is guaranteed, not dependent on the server's own mood.
test('overview: with something needing you, the chips sit in the headline card and the visuals take the full width', async ({
  page,
}) => {
  await routeLiveFrame(
    page,
    liveFrame({ 'mock-pager': { state: 'running', health: 'unhealthy', icon: '', metrics: { 'cpu.pct': 0.3, 'mem.bytes': 1e8 } } }),
  );
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');

  await expect(page.locator('.overview__headline-text')).toHaveText('1 thing needs you');

  // The attention section is inside the headline card, not a column of
  // its own beside the visuals.
  await expect(page.locator('.overview__headline-zone .overview__attention')).toHaveCount(1);
  await expect(page.locator('.overview__status-facts')).toHaveCount(0);
  await expect(page.locator('.overview__status-visuals')).toHaveCount(0);

  // The visuals band spans the same content width as the headline card
  // above it, exactly as the all-clear band does.
  const band = page.locator('.overview__status-band');
  await expect(band).toBeVisible();
  const zoneBox = await page.locator('.overview__headline-zone').boundingBox();
  const bandBox = await band.boundingBox();
  expect(Math.abs(bandBox.width - zoneBox.width)).toBeLessThan(2);
  expect(bandBox.y).toBeGreaterThanOrEqual(zoneBox.y + zoneBox.height - 4);

  // The band is the fleet alone now -- the bay schematic became a
  // Customize module and moved down into the modules band -- so the
  // fleet spans the whole width rather than half of it.
  const fleetBox = await band.locator('.fleet-strip-wrap').boundingBox();
  expect(Math.abs(fleetBox.width - bandBox.width)).toBeLessThan(2);
  await expect(band.locator('.bay-schematic')).toHaveCount(0);
  await expect(page.locator('.overview__modules-band .bay-schematic')).toBeVisible();

  // And the metrics rail is pinned ABOVE the headline, which is the
  // page order this pass exists to produce.
  const railBox = await page.locator('.overview__metrics-rail').boundingBox();
  expect(railBox.y + railBox.height).toBeLessThanOrEqual(zoneBox.y + 1);
  expect(Math.abs(railBox.width - zoneBox.width)).toBeLessThan(2);
});

// Adaptive all-clear (Scott: "When there is nothing that needs
// attention... the other sections should be expanded to use the
// available space and then we won't need to scroll down so far"): zero
// callouts collapses the headline card to a compact strip (no status
// band at all) and gives the fleet strip a full-width band of its own.
test('overview: all-clear collapses the headline to a strip and the fleet takes the full width', async ({ page }) => {
  await routeLiveFrame(page, liveFrame());
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');

  await expect(page.locator('.overview__headline-text')).toHaveText('Nothing needs you');
  await expect(page.locator('.overview__attention')).toHaveCount(0);
  await expect(page.locator('.overview__status-band')).toHaveCount(0);
  await expect(page.locator('.overview__headline-zone')).toHaveClass(/overview__headline-zone--clear/);

  // The relocated array facts render inside the storage card, not as
  // orphaned sub-lines (the facts-relocation pass).
  const schematic = page.locator('.bay-schematic');
  await expect(schematic).toContainText('Array started · mover idle');
  await expect(schematic).toContainText('cache warmest at 41.5°C');

  // Full width: the band spans the same content width as the headline
  // card above it, and the fleet -- now the band's only occupant, the
  // schematic having become a module -- spans the band.
  const band = page.locator('.overview__clear-band');
  await expect(band).toBeVisible();
  const zoneBox = await page.locator('.overview__headline-zone').boundingBox();
  const bandBox = await band.boundingBox();
  expect(Math.abs(bandBox.width - zoneBox.width)).toBeLessThan(2);

  const fleet = band.locator('.fleet-strip-wrap');
  await expect(fleet).toBeVisible();
  const fleetBox = await fleet.boundingBox();
  expect(Math.abs(fleetBox.width - bandBox.width)).toBeLessThan(2);
  await expect(band.locator('.bay-schematic')).toHaveCount(0);

  // The all-clear expansion still does its job -- everything below the
  // headline sits higher than it does with callouts present. It is no
  // longer measured against a fixed pixel: the rail is pinned above the
  // headline now (deliberately, and it costs real height), so "Top
  // Consumers in the first viewport" is a promise this page order does
  // not make. What it does promise is that the schematic's own module,
  // at the head of the narrow lane, is reachable without a long scroll.
  const storageBox = await page.locator('.overview__storage').boundingBox();
  expect(storageBox.y).toBeLessThan(1100);
  const topBox = await page.locator('.overview__top').boundingBox();
  expect(topBox.y, 'the modules band starts right below the fleet band').toBeLessThan(
    bandBox.y + bandBox.height + 80,
  );

  // Mobile: the modules band stacks its lanes, wide first.
  await page.setViewportSize({ width: 375, height: 800 });
  const topMobile = await page.locator('.overview__top').boundingBox();
  const storageMobile = await page.locator('.overview__storage').boundingBox();
  expect(storageMobile.y).toBeGreaterThanOrEqual(topMobile.y + topMobile.height - 4);
});

// Counts pass: "Needs a look" is one short row inside the headline
// card -- the label plus at most two chips -- not a column of rows. It
// must stay a single line at desktop width; the moment it grows past
// one the two-column band it replaced would have been the better shape.
test('overview: needs-a-look is one short inline row inside the headline card', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');

  const attention = page.locator('.overview__attention');
  if ((await attention.count()) === 0) {
    test.skip(true, 'fake fleet booted all-clear for this run -- nothing to check');
  }
  await expect(attention).toBeVisible();

  const zoneBox = await page.locator('.overview__headline-zone').boundingBox();
  const attentionBox = await attention.boundingBox();
  expect(zoneBox).not.toBeNull();
  expect(attentionBox).not.toBeNull();

  // Inside the headline card, spanning it.
  expect(attentionBox.x).toBeGreaterThanOrEqual(zoneBox.x - 1);
  expect(attentionBox.y).toBeGreaterThan(zoneBox.y);
  expect(attentionBox.y + attentionBox.height).toBeLessThanOrEqual(zoneBox.y + zoneBox.height + 1);
  // One row: the chips share their label's own line.
  expect(attentionBox.height, 'the attention row must not stack into a column').toBeLessThan(70);

  const chips = attention.locator('.overview__chip');
  const chipCount = await chips.count();
  expect(chipCount).toBeGreaterThan(0);
  expect(chipCount).toBeLessThanOrEqual(2);
  for (let i = 0; i < chipCount; i++) {
    const chipBox = await chips.nth(i).boundingBox();
    expect(Math.abs(chipBox.y + chipBox.height / 2 - (attentionBox.y + attentionBox.height / 2))).toBeLessThan(10);
  }
});

// Balance pass, second half: the old shared two-column BODY put Top
// Consumers and the events feed in unrelated columns for no reason
// tied to either one's own content -- Top Consumers ended up
// width-starved while the rail's column ran nearly double the other
// column's height (confirmed live: 1287px vs 659px). Top Consumers and
// Recent events now share one wide lane, STACKED (never side by side,
// so they can never fight each other for width), while the narrow lane
// -- the storage module's, since the rail was pinned out of the band --
// gets its own, narrower one.
test('overview: Top Consumers and Recent events share one wide column, wider than the narrow lane', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/');

  const top = page.locator('.overview__top');
  const events = page.locator('.overview__events');
  const storage = page.locator('.overview__storage');
  await expect(top).toBeVisible();
  await expect(events).toBeVisible();
  await expect(storage).toBeVisible();

  const topBox = await top.boundingBox();
  const eventsBox = await events.boundingBox();
  const storageBox = await storage.boundingBox();

  // Stacked in the same column: same left edge and width, events below top.
  expect(Math.abs(topBox.x - eventsBox.x)).toBeLessThan(2);
  expect(Math.abs(topBox.width - eventsBox.width)).toBeLessThan(2);
  expect(eventsBox.y).toBeGreaterThanOrEqual(topBox.y + topBox.height - 4);

  // The storage module sits in its own, narrower lane to the right.
  expect(storageBox.x).toBeGreaterThan(topBox.x + topBox.width - 8);
  expect(storageBox.width).toBeLessThan(topBox.width);
});

// The chips ARE the headline count, split two ways -- so whatever the
// server's mood is on any given run, the numbers on them must add up to
// the sentence right above them. (What each bucket contains, and that
// an acked concern leaves both, is unit-tested in
// src/lib/attentionCounts.test.ts; the navigation is in
// tests/overview-attention.spec.ts.)
test('overview: the attention chips always sum to the headline count', async ({ page }) => {
  await page.goto('#/');

  const headline = page.locator('.overview__headline-text');
  await expect(headline).toBeVisible();
  if ((await headline.textContent()) === 'Nothing needs you') {
    await expect(page.locator('.overview__attention')).toHaveCount(0);
    test.skip(true, 'fake fleet booted all-clear for this run -- nothing to count');
  }

  await expect(async () => {
    const text = (await headline.textContent()) ?? '';
    const expected = Number(text.match(/^(\d+) things? needs? you$/)?.[1]);
    expect(Number.isFinite(expected)).toBe(true);
    const counts = await page.locator('.overview__chip-count').allTextContents();
    expect(counts.length).toBeGreaterThan(0);
    expect(counts.reduce((n, c) => n + Number(c), 0)).toBe(expected);
  }).toPass({ timeout: 20_000 });
});

// Bay schematic: now always part of the status band's right column
// (previously only shown during a disk anomaly), bigger, with a hover/
// focus label (slot, device, temp, used/total) and a real click-through
// into #/storage -- the header-compaction + array-visualization passes.
test('overview: the bay schematic is always visible, shows a hover/focus detail label, and links to storage', async ({
  page,
}) => {
  await page.goto('#/');

  const bars = page.locator('.bay-schematic__bar');
  await expect(bars.first()).toBeVisible();
  await expect(bars.first()).toHaveAttribute('href', '#/storage');

  const label = page.locator('.bay-schematic__label');
  await expect(label).not.toHaveClass(/bay-schematic__label--visible/);
  await bars.first().hover();
  await expect(label).toHaveClass(/bay-schematic__label--visible/);
  // slot name, device, and a used/total byte pair -- the richer detail
  // a hover now carries that the bar's own aria-label already had in
  // short form.
  await expect(label).toContainText('/');

  await bars.first().focus();
  await expect(label).toHaveClass(/bay-schematic__label--visible/);

  await bars.first().click();
  await expect(page).toHaveURL(/#\/storage$/);
});

// Fleet heat + tooltip (Scott: "make it earn its space" + "should say
// the container name and show its icon as you mouse over it"): a
// hover or keyboard focus on any unit reveals name/CPU/mem, previously
// only present as an aria-label with nothing for a sighted user.
test('overview: hovering or focusing a fleet unit reveals its name, CPU, and memory', async ({ page }) => {
  await page.goto('#/');

  const firstUnit = page.locator('.fleet-strip .fleet-unit').first();
  await expect(firstUnit).toBeVisible();
  const label = page.locator('.fleet-strip__label');
  await expect(label).not.toHaveClass(/fleet-strip__label--visible/);

  await firstUnit.hover();
  await expect(label).toHaveClass(/fleet-strip__label--visible/);
  await expect(label).toHaveText(/\d+\.\d%/); // a live CPU percentage

  await firstUnit.focus();
  await expect(label).toHaveClass(/fleet-strip__label--visible/);
});

test('containers: clicking a header sorts the table', async ({ page }) => {
  await page.goto('#/containers');

  // Scoped to the table that has a <thead> -- Containers.svelte renders
  // a second, header-less .containers-table for the (here, always empty)
  // stopped section, and a parallel mobile card list that stays in the
  // DOM (just display:none) at this desktop viewport; an unscoped
  // selector would double-count names from either.
  const nameLinks = page.locator('table.containers-table:has(thead) tbody tr.container-row a');
  await expect.poll(() => nameLinks.count()).toBeGreaterThan(0);

  const before = await nameLinks.allTextContents();
  await page.getByRole('button', { name: 'Sort by Name' }).click();

  await expect.poll(() => nameLinks.allTextContents()).not.toEqual(before);
  const after = await nameLinks.allTextContents();
  expect(after).toEqual([...after].sort((a, b) => a.localeCompare(b)));
});

// Never-started ("created") containers: fake.go's fleet always includes
// two (duplicati, watchtower) precisely to exercise this -- Scott's own
// live example was a burst of ephemeral CI-runner spawns entering the
// frame once stopped containers started showing up there too. They must
// stay out of the Overview fleet/headline entirely (nothing to monitor,
// high churn) but still be findable in the Containers view.
test('containers: never-started containers are excluded from the Overview fleet but listed here with their own state', async ({
  page,
}) => {
  await page.goto('#/containers');

  // Not among the running table's rows.
  await expect(page.locator('table.containers-table:has(thead) tr.container-row', { hasText: 'duplicati' })).toHaveCount(
    0,
  );

  const notRunningToggle = page.getByRole('button', { name: /Not running/ });
  await expect(notRunningToggle).toBeVisible();
  await notRunningToggle.click();

  const duplicatiRow = page.locator('tr.container-row', { hasText: 'duplicati' });
  await expect(duplicatiRow).toBeVisible();
  await expect(duplicatiRow).toContainText('created'); // its own state, not just a health-dot color

  const watchtowerRow = page.locator('tr.container-row', { hasText: 'watchtower' });
  await expect(watchtowerRow).toContainText('created');

  // Overview: excluded from the fleet strip's own units and (since the
  // fake fleet always has a real stopped archetype too) the strip
  // summary's own "M stopped" count, which counts exited only -- the
  // shell redesign folded the old fleet sentence into that summary row.
  await page.goto('#/');
  const stripHrefs = await page
    .locator('.fleet-strip .fleet-unit')
    .evaluateAll((els) => els.map((el) => el.getAttribute('href')));
  expect(stripHrefs).not.toContain('#/containers/duplicati');
  expect(stripHrefs).not.toContain('#/containers/watchtower');

  const summary = page.locator('.fleet-strip__summary');
  const runningText = (await summary.getByText(/^\d+ running$/).textContent()) ?? '';
  const stoppedLink = summary.getByText(/^\d+ stopped$/);
  await expect(stoppedLink, 'the fake fleet always has a stopped archetype').toBeVisible();
  const statedTotal =
    Number(runningText.match(/^(\d+)/)?.[1] ?? NaN) +
    Number(((await stoppedLink.textContent()) ?? '').match(/^(\d+)/)?.[1] ?? NaN);
  await expect.poll(() => page.locator('.fleet-strip .fleet-unit').count()).toBe(statedTotal);
});

// Regression coverage for Scott's own report: "when values change, the
// width of the columns change size. this happens constantly and is not
// good looking." table-layout:fixed + the <colgroup> (Containers.svelte)
// mean every column's width comes from ITS OWN fixed spec, never
// recomputed from a row's live content -- so column x-positions must
// stay byte-identical across two real live ticks (2s cadence), even
// though the cell TEXT underneath them keeps changing length
// ("17.2 KB/s" vs "947.6 B/s").
test('containers: column widths do not jitter as live values tick', async ({ page }) => {
  test.setTimeout(30_000);
  await page.goto('#/containers');

  const headers = page.locator('table.containers-table:has(thead) thead th');
  await expect.poll(() => headers.count()).toBeGreaterThan(0);

  async function headerRects() {
    return headers.evaluateAll((ths) => ths.map((th) => ({ left: th.getBoundingClientRect().left, width: th.getBoundingClientRect().width })));
  }

  const before = await headerRects();
  // Comfortably more than one 2s tick, so at least one visible value's
  // rendered string length has actually changed underneath these cells.
  await page.waitForTimeout(6_000);
  const after = await headerRects();

  expect(after).toEqual(before);
});

test('container detail: charts render and the log viewer shows its empty state', async ({ page }) => {
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.container-detail__charts canvas').first()).toBeVisible({ timeout: 10_000 });

  // "jellyfin" is a fake-fleet name, not a real docker container --
  // dc.StreamLogs errors for it, api_logs.go answers 404, and the log
  // viewer renders its graceful empty state instead of a broken pane.
  await expect(page.locator('.log-viewer__empty')).toContainText('Logs aren\'t available for "jellyfin"', {
    timeout: 10_000,
  });
});

// "jellyfin" is the only fake-fleet archetype whose mounts cover three
// different storage kinds at once (fakeContainerMounts: a share, an
// array disk, and the flash boot device -- "pool" only resolves on a
// real box, since it needs a real disks.ini). Devices/capacity are
// ring-only/live-frame samples that take the fake generator a couple of
// ticks to populate after the server starts, hence the generous
// timeouts on the first reads of each.
test('container detail: storage panel renders mounts with kind badges, capacity, and labeled live device IO', async ({
  page,
}) => {
  // Wide enough that the mount list's 2-column layout (>=1200px) is
  // active, so the column-alignment checks below have two real groups
  // to compare against each other, not just against themselves.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('#/containers/jellyfin');

  const mountRow = (destination: string) =>
    page
      .locator('.storage-mount')
      .filter({ has: page.locator('.storage-mount__dest', { hasText: new RegExp(`^${destination}$`) }) });
  const CAPACITY_TEXT = /^\d+\.\d% full · \d+\.\d (B|KiB|MiB|GiB|TiB|PiB) free$/;

  await expect(mountRow('/config').locator('.storage-mount__badge')).toContainText('Share · appdata');
  await expect(mountRow('/config').locator('.storage-mount__ro')).toHaveCount(0); // rw is the default -- not labeled
  // A share spans however many disks it happens to land on -- Unraid
  // tracks no true per-share usage, so there's no single slot to show
  // capacity for (mountCapacitySlot's own "don't fake it" rule) -- the
  // cell itself still renders (it's a fixed grid column), just empty.
  await expect(mountRow('/config').locator('.storage-mount__capacity-cell')).toBeEmpty();

  await expect(mountRow('/media').locator('.storage-mount__badge')).toContainText('Disk · disk1');
  await expect(mountRow('/media').locator('.storage-mount__ro')).toBeVisible();
  await expect(mountRow('/media').locator('.storage-mount__capacity-cell')).toHaveText(CAPACITY_TEXT, {
    timeout: 10_000,
  });

  await expect(mountRow('/flash').locator('.storage-mount__badge')).toContainText('Flash');
  await expect(mountRow('/flash').locator('.storage-mount__ro')).toBeVisible();
  await expect(mountRow('/flash').locator('.storage-mount__capacity-cell')).toHaveText(CAPACITY_TEXT, {
    timeout: 10_000,
  });

  // Column-alignment regression coverage for Scott's own report ("try to
  // line things up a little better here"): every mount's dest/badge cell
  // must start at the same x as every other mount's in the SAME CSS
  // column group (/config and /flash both land in the left group at
  // this width), and /media's own group (the right one) must start at a
  // consistent offset from it -- one shared grid template guarantees
  // this by construction (see ContainerDetail.svelte's own doc), unlike
  // the masonry `columns: 2` layout this replaced, which could only
  // ever align a mount with itself.
  const destConfig = await mountRow('/config').locator('.storage-mount__dest').boundingBox();
  const destFlash = await mountRow('/flash').locator('.storage-mount__dest').boundingBox();
  const badgeConfig = await mountRow('/config').locator('.storage-mount__badge-cell').boundingBox();
  const badgeFlash = await mountRow('/flash').locator('.storage-mount__badge-cell').boundingBox();
  expect(destConfig).not.toBeNull();
  expect(destFlash).not.toBeNull();
  expect(destConfig.x).toBeCloseTo(destFlash.x, 0);
  expect(badgeConfig.x).toBeCloseTo(badgeFlash.x, 0);

  const destMedia = await mountRow('/media').locator('.storage-mount__dest').boundingBox();
  expect(destMedia.x).toBeGreaterThan(destConfig.x + destConfig.width); // the right-hand CSS column, not stacked under the left

  // Devices sort by raw device name (deviceIOFromSamples) -- loop2,
  // nvme0n1, sda, sdc -- exercising all three of unraid.
  // ResolveDeviceLabel's own paths at once: a loop device's backing_file
  // (docker.img, via fake mode's own override -- fake.go's DeviceLabels,
  // since fake mode has no real /sys to read), a DiskMeta slot join
  // (nvme0n1 -> rocket_pool, kind nvme; sdc -> disk1, kind hdd), and raw
  // passthrough (sda isn't any of the fake fleet's own disk devices).
  // sdc is the Phase 5 insight demo's contended device: jellyfin holds a
  // small CONSTANT witness share of it from boot (fake.go's
  // insightDemoWitnessBps -- disk-io-contention's co-tenancy
  // requirement), so it's a permanent fourth row here, not a scheduled
  // flicker. jellyfin's own devices always carry real (nonzero) IO in
  // fake mode, so the noise rule (its own mocked tests below) never
  // hides any of these four.
  await expect(page.locator('.storage-device').first()).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('.storage-device')).toHaveCount(4);

  const loopRow = page.locator('.storage-device').nth(0);
  await expect(loopRow.locator('.storage-device__label')).toContainText('docker.img');
  await expect(loopRow.locator('.storage-device__raw')).toHaveText('loop2');
  await expect(loopRow.locator('.storage-device__kind')).toHaveCount(0); // fake's override names a label but no kind

  const poolRow = page.locator('.storage-device').nth(1);
  await expect(poolRow.locator('.storage-device__label')).toContainText('rocket_pool');
  await expect(poolRow.locator('.storage-device__raw')).toHaveText('nvme0n1');
  await expect(poolRow.locator('.storage-device__kind')).toContainText('NVMe');
  // Read/Write are named once, in the header (checked below), not
  // repeated as text on every row -- each value still carries its own
  // identity via aria-label for anyone not reading the header visually.
  await expect(poolRow.locator('.storage-device__value').nth(0)).toHaveAttribute('aria-label', /^Read /);
  await expect(poolRow.locator('.storage-device__value').nth(1)).toHaveAttribute('aria-label', /^Write /);

  const rawRow = page.locator('.storage-device').nth(2);
  await expect(rawRow.locator('.storage-device__label')).toContainText('sda');
  await expect(rawRow.locator('.storage-device__raw')).toBeEmpty(); // sda isn't any known slot's device -- stays raw, no secondary
  await expect(rawRow.locator('.storage-device__kind')).toHaveCount(0);

  const witnessRow = page.locator('.storage-device').nth(3);
  await expect(witnessRow.locator('.storage-device__label')).toContainText('disk1');
  await expect(witnessRow.locator('.storage-device__raw')).toHaveText('sdc');
  await expect(witnessRow.locator('.storage-device__kind')).toContainText('HDD');

  await expect(page.locator('.storage-device-header', { hasText: 'Read' })).toBeVisible();
  await expect(page.locator('.storage-device-header', { hasText: 'Write' })).toBeVisible();

  // Read/Write column alignment -- "so all rows and the Total row line
  // up exactly": every .storage-device__value is a (read, write) pair in
  // DOM order, one pair per device row plus one for the Total row, so
  // every even index is a Read cell and every odd index a Write cell --
  // each group must share exactly one x position.
  await expect(page.locator('.storage-total')).toContainText('Total');
  const valueXs = await page
    .locator('.storage-device__value')
    .evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().x)));
  expect(valueXs).toHaveLength(10); // 4 devices + Total, x2 each
  const readXs = valueXs.filter((_, i) => i % 2 === 0);
  const writeXs = valueXs.filter((_, i) => i % 2 === 1);
  expect(new Set(readXs).size).toBe(1);
  expect(new Set(writeXs).size).toBe(1);
});

// Share->disk placement (Scott: "you can see that the downloads share
// is used, but you don't know that the drive it's stored on is the
// nvme cache drive... we need to connect the dots"): fake mode pins the
// appdata share (every fleet member's /config mount) to rocket_pool,
// the fake fleet's own NVMe pool (fake.Generator.SharePlacements' own
// doc), so the mount that already read "Share · appdata" now also names
// which drive that share actually lives on, tinted to match rocket_
// pool's own kind badge elsewhere on this same page.
test('container detail: a share mount shows which cache pool it lives on, tinted by that pool\'s own kind', async ({
  page,
}) => {
  await page.goto('#/containers/jellyfin');

  const configMount = page
    .locator('.storage-mount')
    .filter({ has: page.locator('.storage-mount__dest', { hasText: /^\/config$/ }) });
  const placement = configMount.locator('.storage-mount__placement');

  await expect(placement).toHaveText('→ lives on rocket_pool (nvme)');
  await expect(placement).toHaveClass(/storage-mount__placement--nvme/);

  // Tinted to match, not merely present: the SAME color rocket_pool's
  // own NVMe kind badge already renders with in the Live IO section
  // below (poolRow's own storage-device__kind, see the test above).
  const placementColor = await placement.evaluate((el) => getComputedStyle(el).color);
  const nvmeBadgeColor = await page
    .locator('.storage-device__kind', { hasText: 'NVMe' })
    .evaluate((el) => getComputedStyle(el).color);
  expect(placementColor).toBe(nvmeBadgeColor);

  // A non-share mount never gets a placement line, even one backed by
  // the very pool the share above resolved to.
  const mediaMount = page
    .locator('.storage-mount')
    .filter({ has: page.locator('.storage-mount__dest', { hasText: /^\/media$/ }) });
  await expect(mediaMount.locator('.storage-mount__placement')).toHaveCount(0);
});

// Live IO noise rule (recentlyActiveDevices/recordDeviceActivity, lib/
// containerStorage.ts): fake mode's own devices are always active
// (fake.go's Tick never zeroes them), so exercising "never had any IO"
// needs a mocked response. mounts: [] means the Mounts sub-section shows
// its own "No mounts for this container." line -- same shared
// .container-detail__storage-empty class the Live IO one below would
// use if it were showing, so that check is scoped by text, not the bare
// class, to tell the two apart.
test('container detail: storage panel hides a device with no recent IO but still counts it in Total', async ({
  page,
}) => {
  await page.route('**/api/containers/jellyfin/storage', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        mounts: [],
        devices: [
          { device: 'sda', label: 'sda', kind: '', read_bps: 120000, write_bps: 40000 },
          { device: 'loop1', label: 'bzmodules', kind: '', read_bps: 0, write_bps: 0 },
        ],
      }),
    }),
  );
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.storage-device')).toHaveCount(1, { timeout: 10_000 });
  await expect(page.locator('.storage-device__label')).toContainText('sda');
  await expect(page.locator('.container-detail__storage-empty', { hasText: 'No recent disk IO.' })).toHaveCount(0);

  // Total sums BOTH devices (120000+0 read bps = 120.0 KB/s), not just
  // the one visible row -- truthful even though bzmodules never renders.
  await expect(page.locator('.storage-total .storage-device__value').nth(0)).toHaveAttribute(
    'aria-label',
    'Read 120.0 KB/s',
  );
  await expect(page.locator('.storage-total .storage-device__value').nth(1)).toHaveAttribute(
    'aria-label',
    'Write 40.0 KB/s',
  );
});

test('container detail: an active Unraid-OS loop device gets a muted suffix', async ({ page }) => {
  await page.route('**/api/containers/jellyfin/storage', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        mounts: [],
        devices: [{ device: 'loop1', label: 'bzmodules', kind: '', read_bps: 5000, write_bps: 0 }],
      }),
    }),
  );
  await page.goto('#/containers/jellyfin');

  const row = page.locator('.storage-device').first();
  await expect(row).toBeVisible({ timeout: 10_000 });
  await expect(row.locator('.storage-device__label')).toContainText('bzmodules');
  await expect(row.locator('.storage-device__os-tag')).toHaveText('(Unraid OS)');
});

test('container detail: storage panel shows a quiet message once every device has gone idle', async ({ page }) => {
  await page.route('**/api/containers/jellyfin/storage', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ mounts: [], devices: [{ device: 'sda', label: 'sda', kind: '', read_bps: 0, write_bps: 0 }] }),
    }),
  );
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.container-detail__storage-empty', { hasText: 'No recent disk IO.' })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.locator('.storage-device')).toHaveCount(0);
  await expect(page.locator('.storage-total')).toHaveCount(0);
});

// Horizontal-overflow regression: TimeChart/Sparkline bake each chart's
// width in literal canvas pixels (build()/setSize read the host's
// clientWidth), and a grid item's default min-width:auto lets that
// baked canvas set its track's minimum -- so when the content box later
// narrows (window resize, a vertical scrollbar appearing), the 1fr
// tracks physically can't shrink, cards overrun the page sideways, and
// the ResizeObserver that's supposed to re-fit the chart can never
// fire: the element it watches is held at its stale width by the very
// canvas it would resize. Two live instances of the same trap, both
// reproduced narrowing 1920 -> 1200: Container Detail's chart cards
// (the Memory card's right edge sat ~210px past the viewport, whole
// page scrolling horizontally) and Settings' footprint card (its
// sparklines pinned the row's first track at 550px, shoving the About
// card 16px past the page).
test('chart-hosting grid cards release their tracks when the viewport narrows', async ({ page }) => {
  const ROWS: { hash: string; canvas: string }[] = [
    { hash: '#/containers/jellyfin', canvas: '.container-detail__charts canvas' },
    { hash: '#/settings', canvas: '.settings-footprint canvas' },
  ];
  for (const r of ROWS) {
    await page.setViewportSize({ width: 1920, height: 900 });
    await page.goto(r.hash);

    // The chart must exist BEFORE the resize -- the bug is a stale
    // already-built canvas holding its track open, not a fresh build at
    // the narrow width (which sizes correctly).
    await expect(page.locator(r.canvas).first()).toBeVisible({ timeout: 10_000 });

    await page.setViewportSize({ width: 1200, height: 900 });

    // expect.poll gives the ResizeObserver -> setSize chain a beat to
    // settle; the invariant is "no horizontal overflow anywhere on the
    // page" (documentElement.clientWidth already excludes any vertical
    // scrollbar, so a positive difference is a real sideways spill).
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), {
        message: `horizontal overflow on ${r.hash} after narrowing`,
      })
      .toBeLessThanOrEqual(0);
  }
});

test('top consumers: switching window from Now to 1h renders without erroring', async ({ page }) => {
  await page.goto('#/top');

  // Now is live and client-derived (topFromFrame) -- every fake container
  // reports cpu.pct, so this always has real bars, never the empty state.
  await expect(page.locator('.top-bar-list__row').first()).toBeVisible();

  await page.getByRole('button', { name: '1h', exact: true }).click();
  await expect(page.locator('.top-consumers__loading')).toHaveCount(0, { timeout: 10_000 });

  // No error banner, and the panel settled on either real bars or the
  // window-specific empty state -- both are acceptable outcomes for a
  // freshly-started store that may not have an hour of history yet.
  await expect(page.locator('.top-consumers__error')).toHaveCount(0);
  await expect(page.locator('.top-bar-list__row, .top-bar-list__empty').first()).toBeVisible();

  // If the window actually has rows (real store history), the hero
  // chart's own per-container /api/series fetches must have populated a
  // legend too, not just the ranked bars -- proves the fetched-window
  // path (as opposed to Now's live rings) actually wires up.
  const rowCount = await page.locator('.top-bar-list__row').count();
  if (rowCount > 0) {
    await expect(page.locator('.top-consumers__header canvas')).toBeVisible();
    await expect.poll(() => page.locator('.top-consumers__chip').count()).toBeGreaterThan(0);
  }
});

// Metric breakdown pages: an Overview rail tile deep-links into its own
// resource's #/top/:resource route, which grows into a real attribution
// page there -- host-total header (value + live chart), the COMPLETE
// container list (not Overview's own top-5 module), a trailing
// "unattributed" summary row, and -- for a directional resource -- every
// row (including that summary one) showing both sides of the pair in
// their own colors. GPU deliberately gets none of the header/summary
// row (see topFromFrame.ts's own doc on why there's no single honest
// whole-machine GPU number).
test('overview: a rail tile deep-links to its own metric breakdown page', async ({ page }) => {
  await page.goto('#/');

  const memTile = page.locator('.overview__metrics-rail .stat-tile', { hasText: 'Memory' });
  await expect(memTile).toBeVisible();
  await expect(memTile).toHaveAttribute('href', '#/top/mem');
  await memTile.click();
  await expect(page).toHaveURL(/#\/top\/mem$/);
  await expect(page.getByRole('tab', { name: 'Memory', exact: true })).toHaveAttribute('aria-selected', 'true');
});

test('top consumers: cpu breakdown page shows a host-total header with a live multi-line chart and a legend', async ({
  page,
}) => {
  await page.goto('#/top/cpu');

  await expect(page.locator('.top-consumers__header')).toBeVisible();
  await expect(page.locator('.top-consumers__header-value')).toHaveText(/^\d+\.\d%$/);
  await expect(page.locator('.top-consumers__header canvas')).toBeVisible();

  // The hero chart's own legend: up to 10 container chips + a trailing
  // "Host total" reference chip, in the SAME order as the ranked list
  // below (both read the same top-N ranking).
  const chips = page.locator('.top-consumers__chip');
  await expect.poll(() => chips.count()).toBeGreaterThan(1);
  await expect(chips.last()).toHaveText('Host total');
  const chipCount = await chips.count();
  expect(chipCount).toBeLessThanOrEqual(11); // top 10 containers + host total

  const firstChipName = (await chips.first().textContent())?.trim();
  const firstRowName = await page.locator('.top-bar-list__name-text').first().textContent();
  expect(firstChipName).toContain(firstRowName?.trim());

  // uPlot's own built-in legend is suppressed (showLegend={false}) --
  // the chip row above is the only legend, not a second, redundant one.
  await expect(page.locator('.top-consumers__header .u-legend')).toHaveCount(0);
});

test('top consumers: clicking a legend chip toggles it, hovering focuses it, without erroring', async ({ page }) => {
  await page.goto('#/top/cpu');
  const firstChip = page.locator('.top-consumers__chip').first();
  await expect(firstChip).toBeVisible();

  await firstChip.hover();
  await firstChip.click();
  await expect(firstChip).toHaveClass(/top-consumers__chip--off/);
  await expect(firstChip).toHaveAttribute('aria-pressed', 'false');

  await firstChip.click();
  await expect(firstChip).not.toHaveClass(/top-consumers__chip--off/);
});

test('top consumers: the ranked-list card is labeled "Top Consumers" under the renamed "Metrics" page', async ({
  page,
}) => {
  await page.goto('#/top/cpu');
  await expect(page.locator('h1.page-title')).toHaveText('Metrics');
  await expect(page.locator('.top-consumers__panel-label')).toHaveText('Top Consumers');
});

test('top consumers: the cpu breakdown list is complete (not top-5) and ends with an unattributed row', async ({
  page,
}) => {
  await page.goto('#/top/cpu');

  const rows = page.locator('.top-bar-list__row');
  await expect.poll(() => rows.count()).toBeGreaterThan(5);

  // The last row is the pinned, unlinked "Unattributed (host)" summary --
  // a plain <span> name, not a link into some container's detail page.
  const lastRow = rows.last();
  await expect(lastRow).toContainText('Unattributed (host)');
  await expect(lastRow.locator('.top-bar-list__name')).toHaveCount(1);
  const tagName = await lastRow.locator('.top-bar-list__name').evaluate((el) => el.tagName);
  expect(tagName).toBe('SPAN');
});

test('top consumers: network breakdown pairs down/up in the header and on every row, in two colors', async ({
  page,
}) => {
  await page.goto('#/top/net');

  const header = page.locator('.top-consumers__header-values .top-consumers__header-value');
  await expect(header).toHaveCount(2);
  await expect(header.first()).toContainText('↓');
  await expect(header.nth(1)).toContainText('↑');
  const [downColor, upColor] = await header.evaluateAll((els) => els.map((el) => getComputedStyle(el).color));
  expect(downColor).not.toBe(upColor);

  const firstRow = page.locator('.top-bar-list__row').first();
  await expect(firstRow.locator('.top-bar-list__value')).toHaveCount(2);
  await expect(firstRow.locator('.top-bar-list__value').first()).toContainText('↓');
  await expect(firstRow.locator('.top-bar-list__value').nth(1)).toContainText('↑');
});

test('top consumers: gpu breakdown has no host-total VALUE or unattributed row, but still gets the per-container hero chart', async ({
  page,
}) => {
  await page.goto('#/top/gpu');

  await expect(page.locator('.top-bar-list__row, .top-bar-list__empty').first()).toBeVisible();
  await expect(page.locator('.top-bar-list__row', { hasText: 'Unattributed' })).toHaveCount(0);

  // gpu gets no whole-machine number (topFromFrame.ts's own doc: a
  // busy_pct is inherently per-engine/per-device) -- the header card
  // itself still renders, chart-only, whenever at least one container
  // has GPU activity (fake mode's jellyfin always does).
  const header = page.locator('.top-consumers__header');
  await expect(header).toBeVisible();
  await expect(header.locator('.top-consumers__header-value')).toHaveCount(0);
  await expect(header).toContainText('GPU');
  await expect(header).toContainText('per container');
  await expect(header.locator('canvas')).toBeVisible();
});

// Core-budget ribbon: the CPU breakdown page's own hero, live only. Math
// smoke test (the real segment math is unit-tested directly in lib/
// coreBudget.test.ts) -- this just pins that the rendered widths actually
// sum to the bar's own full width and that switching away from CPU (or
// to a fetched window) removes it cleanly rather than leaving it stuck.
test('top consumers: the CPU core-budget ribbon renders segments that sum to the bar width', async ({ page }) => {
  await page.goto('#/top/cpu');

  const bar = page.locator('.core-ribbon__bar');
  await expect(bar).toBeVisible();
  await expect(bar).toHaveAttribute('aria-label', /^CPU core budget, \d+ cores$/);

  // toPass rather than a one-shot read: segments glide width changes
  // over ~glideMs (the live tick cadence, so ~2s in fake mode --
  // CoreBudgetRibbon.svelte). Linear easing keeps the parts summing to
  // the bar while every segment glides, but a re-rank that adds or
  // removes a segment (keyed {#each}) inserts/removes it at full width
  // INSTANTLY while its neighbors glide, so the sum over/undershoots by
  // that segment's width for up to a whole glide. Observed live: ±70px
  // windows lasting ~2s each, and a 57.9px one-shot failure under
  // full-suite load, where the slower first paint lands the read
  // mid-churn instead of on the snapped (glideMs=0) first frame.
  // Retrying re-measures -- each read stays atomic inside one evaluate
  // -- until it lands on a settled bar; a genuine segment-math mis-sum
  // never settles, so the retries just run out and this still fails.
  await expect(async () => {
    const { barWidth, partsWidth } = await bar.evaluate((el) => {
      const barWidth = el.getBoundingClientRect().width;
      const parts = [...el.querySelectorAll('.core-ribbon__segment, .core-ribbon__free')];
      const partsWidth = parts.reduce((sum, p) => sum + p.getBoundingClientRect().width, 0);
      return { barWidth, partsWidth };
    });
    // A point of slack absorbs sub-pixel rounding across however many
    // segments happen to be present.
    expect(Math.abs(barWidth - partsWidth)).toBeLessThan(2);
  }).toPass({ timeout: 10_000 });

  // Hovering a segment reveals its own name+cores label; leaving it
  // hides it again.
  const firstSegment = bar.locator('.core-ribbon__segment').first();
  const label = page.locator('.core-ribbon__label');
  await expect(label).not.toHaveClass(/core-ribbon__label--visible/);
  await firstSegment.hover();
  await expect(label).toHaveClass(/core-ribbon__label--visible/);

  // Switching to a non-CPU resource removes the ribbon entirely.
  await page.getByRole('tab', { name: 'Memory', exact: true }).click();
  await expect(page.locator('.core-ribbon__bar')).toHaveCount(0);
});

// Rate-metric scale ceilings: net/io have no fixed 0-100 ceiling, so
// their bars read against a "nice" 1-2-5 ceiling at least as large as
// the current max (niceCeiling, lib/metrics.ts) instead of the
// leaderboard's own busiest row -- labeled once per surface (module vs.
// view) rather than per row. Percent metrics (cpu/gpu) are unaffected --
// still absolute 0-100, no ceiling label at all.
test('top consumers: net/io bars scale to a nice ceiling, labeled once, unlike percent metrics', async ({ page }) => {
  await page.goto('#/top/net');

  const label = page.locator('.top-consumers__scale');
  await expect(label).toBeVisible();
  await expect(label).toHaveText(/^Bars scaled to ≤ \d+(\.\d+)? (B|KB|MB|GB)\/s$/);

  // cpu/gpu stay absolute-0-100 -- no ceiling label at all.
  await page.getByRole('tab', { name: 'CPU', exact: true }).click();
  await expect(page.locator('.top-consumers__scale')).toHaveCount(0);

  // Overview's own compact module gets the identical treatment, once
  // per module rather than per row.
  await page.goto('#/');
  const netTab = page.getByRole('tab', { name: 'Net', exact: true });
  await netTab.click();
  const moduleLabel = page.locator('.overview__top-scale');
  await expect(moduleLabel).toBeVisible();
  await expect(moduleLabel).toHaveText(/^Scale ≤ \d+(\.\d+)? (B|KB|MB|GB)\/s$/);
});

// Regression coverage for the top-consumers host-share fix: a container
// used to read docker-stats' own per-core percent (routinely near/at
// 100%) right next to the host tile's much lower whole-machine total --
// confusing, and the bug this branch exists to fix. Both surfaces (the
// full page here, and Overview's compact module below) must show every
// CPU row on the SAME host-share scale as the host CPU tile, with a
// quiet "≈N.N cores" secondary giving back the docker-stats-style number
// for anyone who wants it.
test('top consumers: CPU rows read as a host-share percentage with a quiet cores secondary', async ({ page }) => {
  await page.goto('#/top/cpu');

  const rows = page.locator('.top-bar-list__row');
  await expect(rows.first()).toBeVisible();

  // Bound only the CONTAINER rows, not the pinned "Unattributed (host)"
  // summary row. fake.go's fleet never uses more than one full core
  // (fakeHostCores' own doc), so every container reads comfortably under
  // 100% -- nothing pegged at the old per-core ceiling, the regression
  // this guards. The unattributed row is NOT a container: it renders
  // hostTotal - attributed, and hostTotal reads the host's OWN cpu.total.
  // On a Linux CI runner the REAL host collector (host.New, /proc) runs
  // alongside fake mode and writes that same cpu.total key with the
  // runner's actual, test-pegged CPU -- 74-85% observed live in CI -- so
  // the host-derived unattributed row legitimately reads far above 20
  // there while every genuine fake container stays a fraction of a
  // percent under it. Locally there is no /proc, the real host collector
  // is unavailable, and only fake writes cpu.total (~16-21%), which is
  // why the un-scoped bound only ever flaked on CI. Container rows render
  // their name as a link (<a>); the unattributed row a plain <span>
  // (TopBarRow, linkable:false) -- filter on that.
  const containerRows = rows.filter({ has: page.locator('a.top-bar-list__name') });
  const values = await containerRows.locator('.top-bar-list__value').allTextContents();
  expect(values.length).toBeGreaterThan(0);
  for (const text of values) {
    const value = Number(text.replace('%', ''));
    expect(value).toBeGreaterThanOrEqual(0);
    expect(value).toBeLessThan(20);
  }

  // At least one row's secondary renders (a container using well under
  // 0.05 cores hides it entirely -- see format.ts's fmtCores).
  await expect(page.locator('.top-bar-list__secondary').first()).toHaveText(/^≈\d+\.\d cores$/);
});

// Regression coverage for Scott's own report: a quiet 6.5%-busy container
// used to draw a nearly-full bar just because nothing else running was any
// busier -- the leaderboard scaled every bar relative to its OWN top row,
// not to the machine. cpu/gpu are both read on a fixed 0-100 scale (see
// topFromFrame's resourceScaleMax), so a row's bar width and its own
// printed percentage must always agree, on every row, not just the top
// one. Two separate page.goto calls (rather than clicking between tabs on
// one page) sidesteps a separate, already-flagged bug where a container
// present on both leaderboards keeps its prior tab's value for a while
// after a same-page tab switch -- irrelevant to the scale math this pins,
// but no reason to couple this test to it.
test('top consumers: CPU and GPU bar widths read as an absolute fraction of 100, not relative to the busiest row', async ({
  page,
}) => {
  async function assertBarMatchesItsOwnValue(hash: string) {
    await page.goto(hash);
    const rows = page.locator('.top-bar-list__row');
    await expect(rows.first()).toBeVisible();
    const count = await rows.count();
    for (let i = 0; i < count; i++) {
      const { value, actualPct } = await rows.nth(i).evaluate((el) => {
        const value = Number(el.querySelector('.top-bar-list__value')!.textContent!.replace('%', ''));
        const track = el.querySelector('.top-bar-list__track')!.getBoundingClientRect();
        const bar = el.querySelector('.top-bar-list__bar')!.getBoundingClientRect();
        return { value, actualPct: (bar.width / track.width) * 100 };
      });
      // A whole point of slack absorbs the value text's own 1-decimal
      // rounding -- a relative-to-max regression would be off by tens of
      // points on every row but the top one, well outside this.
      expect(actualPct, `${hash} row ${i}: value ${value}%, bar reads ${actualPct.toFixed(1)}%`).toBeGreaterThan(
        value - 1,
      );
      expect(actualPct).toBeLessThan(value + 1);
    }
  }

  await assertBarMatchesItsOwnValue('#/top/cpu');
  await assertBarMatchesItsOwnValue('#/top/gpu');
});

test('overview: the Top Consumers module shows the same host-share cores secondary on CPU rows', async ({ page }) => {
  await page.goto('#/');

  const topModule = page.locator('.overview__top');
  await expect(topModule.locator('.top-bar-list__row').first()).toBeVisible();
  await expect(topModule.locator('.top-bar-list__secondary').first()).toHaveText(/^≈\d+\.\d cores$/);
});

// Regression coverage for Scott's own report: a live box misread its
// boot flash device as HDD and its NVMe pools as generic SSD (rotational
// alone can't tell either apart -- see disks.go's DiskKind doc). Fake
// mode's 8-disk fleet (internal/fake/fake.go) covers all four of
// Storage's own type badges. diskRow anchors on the bare disk name
// (.storage-disk__name's own exact text) rather than matching the whole
// row's text, since two different disks can share the same ROLE label
// text ("Cache / pool", for both "cache" and "rocket_pool").
test('storage: every disk type badge is classified correctly (hdd/ssd/nvme/usb)', async ({ page }) => {
  await page.goto('#/storage');

  const diskRow = (name: string) =>
    page.locator('.storage-disk').filter({ has: page.locator('.storage-disk__name', { hasText: new RegExp(`^${name}$`) }) });

  await expect(diskRow('disk1').locator('.storage-disk__media')).toContainText('HDD');
  await expect(diskRow('cache').locator('.storage-disk__media')).toContainText('SSD');
  await expect(diskRow('rocket_pool').locator('.storage-disk__media')).toContainText('NVMe');
  await expect(diskRow('flash').locator('.storage-disk__media')).toContainText('USB');

  // The boot device's own role label is distinct from a plain pool's --
  // proves ROLE_LABEL and MEDIA_LABEL aren't accidentally conflated.
  await expect(diskRow('flash')).toContainText('Boot (flash)');
});

// Status-colored values (thresholds.ts): disk2 is fake.go's own fixture
// with baseUsed 0.71 -- just over disk.capacity's 70% warn threshold --
// so its capacity number must render banded, not plain ink; disk4
// (baseUsed 0.40) stays comfortably in-band and must stay plain ink,
// proving this isn't just "every disk gets tinted".
test('storage: a disk over the capacity threshold renders its number banded, one under it stays plain ink', async ({
  page,
}) => {
  await page.goto('#/storage');

  const diskRow = (name: string) =>
    page.locator('.storage-disk').filter({ has: page.locator('.storage-disk__name', { hasText: new RegExp(`^${name}$`) }) });

  const inkColor = await page.locator('.storage-disk__name').first().evaluate((el) => getComputedStyle(el).color);

  const overThreshold = diskRow('disk2').locator('.storage-disk__usage-pct');
  await expect(overThreshold).toBeVisible();
  await expect(overThreshold).toContainText(/^7\d\.\d%$/); // ~71%, drifts slowly upward but stays in the 70s for any test run
  const overColor = await overThreshold.evaluate((el) => getComputedStyle(el).color);
  expect(overColor).not.toBe(inkColor);

  const underThreshold = diskRow('disk4').locator('.storage-disk__usage-pct');
  await expect(underThreshold).toBeVisible();
  const underColor = await underThreshold.evaluate((el) => getComputedStyle(el).color);
  expect(underColor).toBe(inkColor);
});

// Storage header chart (Scott: "a graph that can switch between disk
// io, storage used, and temperature... each line... a separate
// drive"): a segmented IO/Used/Temp switcher over a per-drive TimeChart
// and a kind-tinted legend, reusing the Metrics page's own hero-chart
// interaction pattern. pageerror is collected for the whole test --
// this is the regression guard for a real bug this feature shipped
// with (every line missing its own `label`, so TimeChart's tooltip --
// keyed by row.label -- collapsed every row onto one shared `undefined`
// key the instant a hover first populated it: each_key_duplicate).
test('storage: the header chart switches metrics/windows and its legend toggles lines without erroring', async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on('pageerror', (err) => pageErrors.push(err.message));

  await page.goto('#/storage');

  const chart = page.locator('.storage-chart');
  await expect(chart).toBeVisible();
  await expect(chart.locator('.microlabel').first()).toHaveText('IO by drive');
  await expect(chart.locator('canvas')).toBeVisible();

  const usedTab = chart.getByRole('tab', { name: 'Used', exact: true });
  await usedTab.click();
  await expect(usedTab).toHaveAttribute('aria-selected', 'true');
  await expect(chart.locator('.microlabel').first()).toHaveText('Used by drive');

  const tempTab = chart.getByRole('tab', { name: 'Temp', exact: true });
  await tempTab.click();
  await expect(chart.locator('.microlabel').first()).toHaveText('Temp by drive');

  await chart.getByRole('button', { name: '1h', exact: true }).click();
  await expect(chart.locator('.storage-chart__error')).toHaveCount(0, { timeout: 10_000 });
  await chart.getByRole('button', { name: 'Now', exact: true }).click();

  // Legend: EVERY chip starts visible now (Scott's own follow-up ask
  // dropped the old pools/parity/active-only default-hidden set
  // entirely -- "one /api/series per drive per metric on the ring tier
  // is cheap") for the fake fleet's 8-disk array; clicking a chip flips
  // its own state.
  const chips = chart.locator('.storage-chart__chip');
  await expect.poll(() => chips.count()).toBeGreaterThan(1);
  const offChips = chart.locator('.storage-chart__chip.storage-chart__chip--off');
  const onChips = chart.locator('.storage-chart__chip:not(.storage-chart__chip--off)');
  expect(await offChips.count()).toBe(0);
  expect(await onChips.count()).toBe(await chips.count());

  // Each drive's own chip carries a distinct categorical stroke color by
  // SLOT POSITION (seriesColorVar) -- fake mode's 8-disk fleet stays
  // well under the 10-hue palette's own wrap point, so every one of
  // these must come back pairwise distinct; a repeat here would mean two
  // different drives got assigned the exact same line color.
  const chipColors = await chips.evaluateAll((els) => els.map((el) => (el as HTMLElement).style.getPropertyValue('--chip-color')));
  expect(chipColors.length).toBe(new Set(chipColors).size);

  const firstChip = chips.first();
  const wasOff = (await firstChip.getAttribute('aria-pressed')) === 'false';
  await firstChip.hover();
  await firstChip.click();
  await expect(firstChip).toHaveAttribute('aria-pressed', wasOff ? 'true' : 'false');
  await firstChip.click();
  await expect(firstChip).toHaveAttribute('aria-pressed', wasOff ? 'false' : 'true');

  // Hovering the chart itself pins a tooltip listing only the currently
  // visible lines (never a hidden one) -- expect.poll rather than a
  // single assertion since the live chart keeps re-rendering under the
  // cursor every tick, and this is the exact interaction that used to
  // throw each_key_duplicate (see the test's own doc above). uPlot
  // layers its own cursor-tracking overlay (.u-over) directly on top of
  // the canvas -- that's the real hit target, not the canvas itself.
  await chart.locator('.u-over').hover();
  const tooltipRows = page.locator('.time-chart__tooltip .time-chart__tooltip-row');
  await expect.poll(() => tooltipRows.count(), { timeout: 10_000 }).toBeGreaterThan(0);
  const [visibleChipNames, tooltipText] = await Promise.all([onChips.allTextContents(), tooltipRows.allTextContents()]);
  await expect(tooltipRows).toHaveCount(visibleChipNames.length);
  for (const name of visibleChipNames) {
    expect(tooltipText.some((row) => row.includes(name.trim()))).toBe(true);
  }

  expect(pageErrors, `uncaught page errors: ${pageErrors.join('\n')}`).toEqual([]);
});

// Clickable events (eventHref, lib/eventHref.ts): mocked rather than
// waiting on the fake generator's own real-time schedule (its first
// event fires 2 real minutes into uptime) -- deterministic and instant,
// and exercises the exact same rendering path a real event would.
test('events: container/storage events are clickable and navigate; image/unknown stay plain rows', async ({ page }) => {
  const now = Math.floor(Date.now() / 1000);
  await page.route('**/api/events*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { ID: 1, TS: now, Kind: 'container.start', Entity: 'jellyfin', Severity: 'info', Detail: '' },
        { ID: 2, TS: now - 5, Kind: 'disk.errors', Entity: 'disk2', Severity: 'alert', Detail: 'errors 0 → 1' },
        { ID: 3, TS: now - 10, Kind: 'image.pull', Entity: 'demo/paperless:latest', Severity: 'info', Detail: '' },
      ]),
    }),
  );

  await page.goto('#/events');

  const containerRow = page.locator('.event-feed-item', { hasText: 'jellyfin' });
  await expect(containerRow).toHaveAttribute('href', '#/containers/jellyfin');

  const diskRow = page.locator('.event-feed-item', { hasText: 'disk.errors' });
  await expect(diskRow).toHaveAttribute('href', '#/storage');

  const imageRow = page.locator('.event-feed-item', { hasText: 'image.pull' });
  await expect(imageRow).not.toHaveAttribute('href');
  expect(await imageRow.evaluate((el) => el.tagName)).toBe('DIV');

  await containerRow.click();
  await expect(page).toHaveURL(/#\/containers\/jellyfin$/);
  await expect(page.locator('h1.page-title')).toHaveText('jellyfin');
});

test('theme toggle flips data-theme and persists across reload', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('#/');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  // Cycle order is system -> light -> dark -> system (theme.svelte.ts);
  // starting from system (resolved light, forced above), the first click
  // is a no-visible-op (explicit light == already-resolved light) and
  // the second lands on dark, which the following assertion catches.
  const toggle = page.locator('.theme-toggle');
  await toggle.click();
  await toggle.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('375px viewport: no route scrolls horizontally', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  for (const r of ROUTES) {
    await page.goto(r.hash);
    await expect(page.locator('h1.page-title')).toBeVisible();
    const { scrollWidth, innerWidth } = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }));
    expect(scrollWidth, `${r.hash}: scrollWidth ${scrollWidth} > innerWidth ${innerWidth}`).toBeLessThanOrEqual(
      innerWidth,
    );
  }
});

// Regression coverage for a 375px-only wrap: "Disk IO" is the one
// two-word label among the resource tabs and used to break onto a
// second line ("Disk" / "IO") once the row got squeezed this narrow --
// the button's own box stays a fixed min-height either way (2 lines of
// this text still fits under it), so this checks the label's own text
// layout directly instead: getClientRects() on a text range reports one
// rect per visual line, so more than one means it wrapped.
test('top consumers: the "Disk IO" resource tab stays one line at 375px', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('#/top');

  const diskIoTab = page.getByRole('tab', { name: 'Disk IO', exact: true });
  await expect(diskIoTab).toBeVisible();

  const lineCount = await diskIoTab.evaluate((el) => {
    const range = document.createRange();
    range.selectNodeContents(el);
    return range.getClientRects().length;
  });
  expect(lineCount).toBe(1);
});

test('LivePulse shows live state while frames flow', async ({ page }) => {
  await page.goto('#/');
  await expect(page.locator('.live-pulse__ring')).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('.live-pulse__text')).toHaveText('live · 2s');

  // The ring node is recreated (Svelte {#key live.frameCount}) on every
  // received SSE frame -- waiting for a second, distinct node in that
  // position proves another live frame actually arrived, rather than a
  // fixed sleep that would only prove time passed.
  const firstRing = await page.locator('.live-pulse__ring').elementHandle();
  await page.waitForFunction((prev) => document.querySelector('.live-pulse__ring') !== prev, firstRing, {
    timeout: 6_000,
  });
  await expect(page.locator('.live-pulse__text')).toHaveText('live · 2s');
});

// Smooth-streaming (docs/superpowers/sdd/smooth-streaming) adds a shared
// rAF-driven animation loop for live charts/numbers, but the whole
// feature is a no-op under prefers-reduced-motion -- the driver never
// starts at all (see lib/streamdriver.svelte.ts), and every svelte/
// motion Tween collapses to duration 0. This context is scoped to just
// this describe block (test.use here doesn't affect the suite's other
// tests, which run under normal motion) so it can assert the ONE thing
// that must still be true regardless: data keeps flowing, just without
// any animation smoothing it.
test.describe('reduced motion', () => {
  // contextOptions, not a bare test.use({ reducedMotion }): this
  // Playwright version has no top-level `reducedMotion` test option, so
  // the bare form is silently ignored and the page runs under normal
  // motion -- caught by live-glide.spec.ts's stricter discreteness
  // assertion (see its own reduced-motion doc), which this block's
  // "still ticks" poll alone could never distinguish.
  test.use({ contextOptions: { reducedMotion: 'reduce' } });

  test('overview still renders and ticks discretely under prefers-reduced-motion', async ({ page }) => {
    await page.goto('#/');
    await expect(page.locator('h1.page-title')).toHaveText('Overview');

    // Same locator/assertion shape as the un-reduced "CPU tile ticks"
    // test above -- reduced motion must not break the underlying SSE
    // data flow, only the animation on top of it.
    const cpuNumber = page.locator('.overview__metrics-rail .stat-tile').first().locator('.stat-tile__number');
    await expect(cpuNumber).toBeVisible();
    const initial = await cpuNumber.textContent();
    await expect.poll(() => cpuNumber.textContent(), { timeout: 6_000 }).not.toBe(initial);
  });
});

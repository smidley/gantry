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
  { hash: '#/top', h1: 'Top Consumers' },
  { hash: '#/storage', h1: 'Storage' },
  { hash: '#/gpu', h1: 'GPU' },
  { hash: '#/events', h1: 'Events' },
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
  // outside this test's own window, so "Everything is running" is the
  // expected reading fresh off a CI-built server; a long-lived reused
  // local dev server (reuseExistingServer, non-CI only) could
  // legitimately have crossed that mark, so both readings are accepted
  // and checked for internal consistency instead of asserting one only.
  const headline = page.locator('.overview__headline-text');
  await expect(headline).toBeVisible();
  const headlineText = await headline.textContent();
  const isAllClear = headlineText === 'Everything is running';
  expect(isAllClear || /^\d+ things? need(s)? you$/.test(headlineText ?? '')).toBe(true);
  await expect(page.locator('.overview__attention')).toHaveCount(isAllClear ? 0 : 1);

  // Fleet strip is D2's own "countable, literal" evidence -- one unit
  // per container, so its count must equal the fleet sentence's own
  // stated total. Deliberately NOT a hardcoded 20: this box's docker
  // collector runs for real alongside fake.go's 20 synthetic
  // archetypes (reproduced live while building this -- a handful of
  // the sandbox's own real containers showed up in the fleet total
  // too), so the true size varies by environment. This instead checks
  // that the strip and the sentence -- two client-side views of the
  // exact same live container set -- agree with each other. The
  // sentence itself is one of two shapes (fleetSentence, overviewStatus.
  // ts): "N containers, all running." when nothing's stopped, or "N
  // running · M stopped." once anything is -- the fake fleet always has
  // at least one stopped archetype, but a real box's own containers
  // sharing this environment could tip either way, so both are parsed.
  const fleetSentence = page.locator('.overview__sub-line').first();
  await expect(fleetSentence).toBeVisible();
  const sentenceText = (await fleetSentence.textContent()) ?? '';
  const stoppedMatch = sentenceText.match(/^(\d+) running · (\d+) stopped\.$/);
  const allRunningMatch = sentenceText.match(/^(\d+) containers?, all running\.$/);
  const statedTotal = stoppedMatch
    ? Number(stoppedMatch[1]) + Number(stoppedMatch[2])
    : allRunningMatch
      ? Number(allRunningMatch[1])
      : NaN;
  expect(statedTotal, `unrecognized fleet sentence shape: ${sentenceText}`).toBeGreaterThan(0);

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
  // active, and that the density regression check below (badge close to
  // its own row's text, not flung to a far edge) has real edge-to-edge
  // distance to measure.
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
  // capacity for (mountCapacitySlot's own "don't fake it" rule).
  await expect(mountRow('/config').locator('.storage-mount__capacity')).toHaveCount(0);

  await expect(mountRow('/media').locator('.storage-mount__badge')).toContainText('Disk · disk1');
  await expect(mountRow('/media').locator('.storage-mount__ro')).toBeVisible();
  await expect(mountRow('/media').locator('.storage-mount__capacity')).toHaveText(CAPACITY_TEXT, { timeout: 10_000 });

  await expect(mountRow('/flash').locator('.storage-mount__badge')).toContainText('Flash');
  await expect(mountRow('/flash').locator('.storage-mount__ro')).toBeVisible();
  await expect(mountRow('/flash').locator('.storage-mount__capacity')).toHaveText(CAPACITY_TEXT, { timeout: 10_000 });

  // Density regression coverage for Scott's own report ("a lot of wasted
  // space... [the kind badge] flung to the far right edge"): the badge
  // must sit close to the mount's own path text, not justified across
  // the whole card.
  const mediaPathsBox = await mountRow('/media').locator('.storage-mount__paths').boundingBox();
  const mediaBadgeBox = await mountRow('/media').locator('.storage-mount__badge').boundingBox();
  expect(mediaPathsBox).not.toBeNull();
  expect(mediaBadgeBox).not.toBeNull();
  expect(mediaBadgeBox.x - (mediaPathsBox.x + mediaPathsBox.width)).toBeLessThan(40);

  // Devices sort by raw device name (deviceIOFromSamples) -- loop2,
  // nvme0n1, sda -- exercising all three of unraid.ResolveDeviceLabel's
  // own paths at once: a loop device's backing_file (docker.img, via
  // fake mode's own override -- fake.go's DeviceLabels, since fake mode
  // has no real /sys to read), a DiskMeta slot join (nvme0n1 ->
  // rocket_pool, kind nvme), and raw passthrough (sda isn't any of the
  // fake fleet's own disk devices).
  await expect(page.locator('.storage-device').first()).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('.storage-device')).toHaveCount(3);

  const loopRow = page.locator('.storage-device').nth(0);
  await expect(loopRow.locator('.storage-device__name')).toContainText('docker.img');
  await expect(loopRow.locator('.storage-device__raw')).toHaveText('loop2');
  await expect(loopRow.locator('.storage-device__kind')).toHaveCount(0); // fake's override names a label but no kind

  const poolRow = page.locator('.storage-device').nth(1);
  await expect(poolRow.locator('.storage-device__name')).toContainText('rocket_pool');
  await expect(poolRow.locator('.storage-device__raw')).toHaveText('nvme0n1');
  await expect(poolRow.locator('.storage-device__kind')).toContainText('NVMe');
  await expect(poolRow).toContainText('Read');
  await expect(poolRow).toContainText('Write');

  const rawRow = page.locator('.storage-device').nth(2);
  await expect(rawRow.locator('.storage-device__name')).toContainText('sda');
  await expect(rawRow.locator('.storage-device__raw')).toHaveCount(0); // sda isn't any known slot's device -- stays raw, no secondary
  await expect(rawRow.locator('.storage-device__kind')).toHaveCount(0);

  // Total: read+write summed across every device above -- "how much IO
  // is this container doing" at a glance.
  await expect(page.locator('.storage-total')).toContainText('Total');
  await expect(page.locator('.storage-total')).toContainText('Read');
  await expect(page.locator('.storage-total')).toContainText('Write');
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

test('top consumers: cpu breakdown page shows a host-total header with a live chart', async ({ page }) => {
  await page.goto('#/top/cpu');

  await expect(page.locator('.top-consumers__header')).toBeVisible();
  await expect(page.locator('.top-consumers__header-value')).toHaveText(/^\d+\.\d%$/);
  await expect(page.locator('.top-consumers__header canvas')).toBeVisible();
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

test('top consumers: gpu breakdown has no host-total header or unattributed row', async ({ page }) => {
  await page.goto('#/top/gpu');

  await expect(page.locator('.top-bar-list__row, .top-bar-list__empty').first()).toBeVisible();
  await expect(page.locator('.top-consumers__header')).toHaveCount(0);
  await expect(page.locator('.top-bar-list__row', { hasText: 'Unattributed' })).toHaveCount(0);
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

  const { barWidth, partsWidth } = await bar.evaluate((el) => {
    const barWidth = el.getBoundingClientRect().width;
    const parts = [...el.querySelectorAll('.core-ribbon__segment, .core-ribbon__free')];
    const partsWidth = parts.reduce((sum, p) => sum + p.getBoundingClientRect().width, 0);
    return { barWidth, partsWidth };
  });
  // A point of slack absorbs sub-pixel rounding across however many
  // segments happen to be present.
  expect(Math.abs(barWidth - partsWidth)).toBeLessThan(2);

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

  // fake.go's fleet never uses more than one full core (fakeHostCores'
  // own doc), so every row must read comfortably under 100% -- nothing
  // pegged at the old per-core ceiling.
  const values = await rows.locator('.top-bar-list__value').allTextContents();
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
  test.use({ reducedMotion: 'reduce' });

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
